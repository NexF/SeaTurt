#!/usr/bin/env python3
"""
微信 SQLCipher 密钥提取 — 多数据库通用版

从微信进程内存中搜索 32 字节候选密钥，用 C 扫描器过滤 + 多进程 SQLCipher 验证。

交互流程:
  1. 扫描所有加密数据库
  2. 用已知密钥测试，标记哪些已解开
  3. 列出所有数据库，让用户选择要搜索的
  4. 开始内存扫描 + 验证
  5. 结果按 JSON 格式存储，区分不同数据库

用法: sudo python3 tools/extract_key.py
"""

import os
import sys
import json
import time
import struct
import subprocess
import ctypes
import tempfile
from multiprocessing import Pool, cpu_count
from collections import Counter

from .db import load_keys, save_keys, DEFAULT_KEYS_FILE


# ============ C 扫描器 ============

C_SCANNER_SRC = r"""
#include <stdint.h>
#include <string.h>

int scan_candidates(
    const uint8_t *data, int data_len,
    int *out, int max_out
) {
    int count = 0;
    int end = data_len - 31;
    for (int off = 0; off < end && count < max_out; off += 8) {
        const uint8_t *p = data + off;
        uint64_t first8; memcpy(&first8, p, 8);
        if (first8 == 0) continue;
        uint64_t last8; memcpy(&last8, p + 24, 8);
        if (last8 == 0) continue;
        int zeros = 0;
        for (int i = 0; i < 32; i++) if (p[i] == 0) zeros++;
        if (zeros > 3) continue;
        uint8_t seen[256] = {0}; int unique = 0;
        for (int i = 0; i < 32; i++) { if (!seen[p[i]]) { seen[p[i]] = 1; unique++; } }
        if (unique < 18) continue;
        int printable = 0;
        for (int i = 0; i < 32; i++) if (p[i] >= 0x20 && p[i] <= 0x7e) printable++;
        if (printable > 22) continue;
        int ptr_like = 0;
        for (int i = 0; i < 32; i += 8) {
            uint64_t qw; memcpy(&qw, p + i, 8);
            if (qw >= 0x400000000000ULL && qw <= 0x800000000000ULL) ptr_like++;
        }
        if (ptr_like >= 2) continue;
        uint8_t freq[256] = {0};
        for (int i = 0; i < 32; i++) freq[p[i]]++;
        int singles = 0;
        for (int i = 0; i < 256; i++) if (freq[i] == 1) singles++;
        if (singles < 12) continue;
        int has_run = 0;
        for (int i = 0; i < 29; i++) {
            if (p[i] == p[i+1] && p[i] == p[i+2] && p[i] == p[i+3]) { has_run = 1; break; }
        }
        if (has_run) continue;
        out[count++] = off;
    }
    return count;
}
"""


def compile_scanner():
    tmpdir = tempfile.mkdtemp()
    c_path = os.path.join(tmpdir, "scanner.c")
    so_path = os.path.join(tmpdir, "scanner.so")
    with open(c_path, 'w') as f:
        f.write(C_SCANNER_SRC)
    r = subprocess.run(["gcc", "-O3", "-shared", "-fPIC", "-o", so_path, c_path],
                       capture_output=True, text=True)
    if r.returncode != 0:
        print(f"[-] C 编译失败: {r.stderr}")
        return None
    lib = ctypes.CDLL(so_path)
    lib.scan_candidates.restype = ctypes.c_int
    lib.scan_candidates.argtypes = [
        ctypes.c_char_p, ctypes.c_int,
        ctypes.POINTER(ctypes.c_int), ctypes.c_int,
    ]
    return lib


def scan_region_c(lib, data):
    max_out = len(data) // 8 + 1
    out_arr = (ctypes.c_int * max_out)()
    count = lib.scan_candidates(data, len(data), out_arr, max_out)
    return [out_arr[i] for i in range(count)]


# ============ 验证 ============

_WORKER_DBS = None


def init_worker(db_targets):
    global _WORKER_DBS
    _WORKER_DBS = db_targets


def verify_key_multi(key_hex):
    """对多个数据库逐一验证密钥。

    只验证 cipher_compatibility=4 (SQLCipher 4.x):
    - 微信 Linux 版底层使用 WCDB，WCDB 基于 SQLCipher 4
    - 已确认所有已解锁数据库 (message_0.db, bizchat.db, biz_message_0.db) 均为 compat=4
    - 每次 SQLCipher 验证需跑 PBKDF2-SHA512 256000 轮，少试一个 compat 就少一次开销
    """
    from pysqlcipher3 import dbapi2 as sqlite
    results = {}
    for db_path in _WORKER_DBS:
        db_name = os.path.basename(db_path)
        try:
            conn = sqlite.connect(db_path)
            conn.execute(f"PRAGMA key=\"x'{key_hex}'\";")
            conn.execute("PRAGMA cipher_compatibility = 4;")
            conn.execute("SELECT count(*) FROM sqlite_master;").fetchone()
            conn.close()
            results[db_name] = 4
        except:
            try:
                conn.close()
            except:
                pass
    return (key_hex, results)


# ============ 工具函数 ============

def find_wechat_pid():
    for cmd in [["pgrep", "-x", "wechat"], ["pgrep", "-f", "/opt/wechat/wechat"]]:
        r = subprocess.run(cmd, capture_output=True, text=True)
        pids = [p.strip() for p in r.stdout.strip().split('\n') if p.strip()]
        if pids:
            return int(pids[0])
    return None


def find_all_dbs():
    """找到所有加密的微信数据库，返回 [(db_path, rel_path, size, salt_hex), ...]"""
    base = "/home/ubuntu/Documents/xwechat_files"
    dbs = []
    for root, dirs, files in os.walk(base):
        if "db_storage" not in root:
            continue
        for f in sorted(files):
            if f.endswith('.db') and '-' not in f:
                full = os.path.join(root, f)
                sz = os.path.getsize(full)
                if sz < 4096:
                    continue
                with open(full, 'rb') as fh:
                    h = fh.read(16)
                if h == b'SQLite format 3\x00':
                    continue
                rel = full.split('db_storage/')[-1] if 'db_storage/' in full else f
                dbs.append((full, rel, sz, h.hex()))
    return dbs


def progress_bar(current, total, width=35, prefix="", suffix=""):
    pct = current / max(total, 1)
    filled = int(width * pct)
    bar = '█' * filled + '░' * (width - filled)
    return f"\r  {prefix}|{bar}| {current}/{total} ({pct*100:.1f}%) {suffix}"


def get_target_regions(pid):
    regions = []
    with open(f"/proc/{pid}/maps") as f:
        for line in f:
            parts = line.strip().split()
            if len(parts) < 2:
                continue
            perms = parts[1]
            if 'r' not in perms or 'w' not in perms:
                continue
            start_s, end_s = parts[0].split('-')
            start = int(start_s, 16)
            end = int(end_s, 16)
            size = end - start
            name = parts[-1] if len(parts) >= 6 else ""
            if size > 100 * 1024 * 1024 or size < 256:
                continue
            prio = 50
            basename = os.path.basename(name) if name.startswith('/') else name
            is_anon = (name == "" or "anon" in name.lower())
            size_mb = size / 1024 / 1024
            if is_anon and start > 0x7e0000000000:
                prio = 0 if 10 <= size_mb <= 60 else 1
            elif is_anon:
                prio = 2 if 10 <= size_mb <= 60 else 3
            elif name == "[heap]":
                prio = 4
            elif basename == "wechat":
                prio = 5
            regions.append((prio, start, end, size, name))
    regions.sort(key=lambda x: (x[0], -x[3]))
    return regions


def try_key_on_db(db_path, key_hex):
    """用给定密钥尝试打开数据库，返回 compat 或 0。

    仅尝试 compat=4: 微信 WCDB 底层为 SQLCipher 4，无需试其他版本。
    """
    from pysqlcipher3 import dbapi2 as sqlite
    try:
        conn = sqlite.connect(db_path)
        conn.execute(f"PRAGMA key=\"x'{key_hex}'\";")
        conn.execute("PRAGMA cipher_compatibility = 4;")
        conn.execute("SELECT count(*) FROM sqlite_master;").fetchone()
        conn.close()
        return 4
    except:
        try:
            conn.close()
        except:
            pass
    return 0


# ============ 主逻辑 ============

def main():
    import datetime

    print("=" * 60)
    print("  微信 SQLCipher 密钥提取 v5")
    print("=" * 60)

    if os.geteuid() != 0:
        print("[-] 需要 root: sudo python3 tools/extract_key.py")
        sys.exit(1)

    pid = find_wechat_pid()
    if not pid:
        print("[-] 微信未运行")
        sys.exit(1)
    print(f"[+] PID: {pid}")

    # ========== 阶段 1: 扫描数据库 ==========
    print(f"\n{'='*60}")
    print(f"  阶段 1: 扫描数据库")
    print(f"{'='*60}\n")

    all_dbs = find_all_dbs()
    if not all_dbs:
        print("[-] 找不到任何加密数据库")
        sys.exit(1)

    known_keys = load_keys()
    all_known_values = list(set(v["key"] for v in known_keys.values() if "key" in v))

    db_status = []
    for i, (db_path, rel, sz, salt) in enumerate(all_dbs):
        db_name = os.path.basename(db_path)
        status = "locked"
        found_key = None
        found_compat = 0

        if db_name in known_keys and "key" in known_keys[db_name]:
            k = known_keys[db_name]["key"]
            c = try_key_on_db(db_path, k)
            if c:
                status = "unlocked"
                found_key = k
                found_compat = c

        if status == "locked":
            for k in all_known_values:
                c = try_key_on_db(db_path, k)
                if c:
                    status = "unlocked"
                    found_key = k
                    found_compat = c
                    break

        db_status.append((i, db_path, rel, sz, salt, status, found_key, found_compat))

    locked_list = []
    unlocked_list = []

    for i, db_path, rel, sz, salt, status, key, compat in db_status:
        sz_str = f"{sz/1024:.0f}KB" if sz < 1024*1024 else f"{sz/1024/1024:.1f}MB"
        if status == "unlocked":
            unlocked_list.append((i, rel, sz_str, key, compat))
            print(f"  ✅ [{i:2d}] {rel:40s} {sz_str:>8s}  已解锁 (compat={compat})")
        else:
            locked_list.append((i, db_path, rel, sz_str, salt))
            print(f"  🔒 [{i:2d}] {rel:40s} {sz_str:>8s}  未解锁 (salt:{salt[:16]}...)")

    print(f"\n  共 {len(all_dbs)} 个数据库: {len(unlocked_list)} 已解锁, {len(locked_list)} 未解锁")

    if not locked_list:
        print("\n[+] 所有数据库都已解锁!")
        return

    # ========== 阶段 2: 用户选择 ==========
    print(f"\n{'='*60}")
    print(f"  阶段 2: 选择要搜索密钥的数据库")
    print(f"{'='*60}\n")

    print("  未解锁的数据库:")
    for idx, (i, db_path, rel, sz_str, salt) in enumerate(locked_list):
        print(f"    {idx+1}. [{i}] {rel} ({sz_str})")

    print(f"\n  输入选项:")
    print(f"    数字      — 选择对应编号 (如: 1 或 1,3,5)")
    print(f"    a 或 all  — 选择全部未解锁数据库")
    print(f"    q 或 quit — 退出")

    while True:
        try:
            choice = input(f"\n  请选择 [1-{len(locked_list)}/a/q]: ").strip().lower()
        except (EOFError, KeyboardInterrupt):
            print("\n[-] 已取消")
            return

        if choice in ('q', 'quit', 'exit'):
            print("[-] 已退出")
            return

        if choice in ('a', 'all'):
            selected = list(range(len(locked_list)))
            break

        try:
            parts = [s.strip() for s in choice.replace(' ', ',').split(',') if s.strip()]
            selected = []
            for p in parts:
                if '-' in p:
                    a, b = p.split('-', 1)
                    selected.extend(range(int(a)-1, int(b)))
                else:
                    selected.append(int(p) - 1)
            if all(0 <= s < len(locked_list) for s in selected) and selected:
                break
            else:
                print(f"  [!] 请输入 1~{len(locked_list)} 之间的数字")
        except ValueError:
            print(f"  [!] 无效输入，请输入数字、a 或 q")

    # 构建搜索目标 — 仅 compat=4（微信 WCDB 固定使用 SQLCipher 4）
    search_targets = []
    for s in selected:
        i, db_path, rel, sz_str, salt = locked_list[s]
        search_targets.append(db_path)
        print(f"  → {rel}")

    print(f"\n[+] 将为 {len(search_targets)} 个数据库搜索密钥")

    # ========== 阶段 3: 内存扫描 + 验证 ==========
    print(f"\n{'='*60}")
    print(f"  阶段 3: 内存扫描 + 验证")
    print(f"{'='*60}\n")

    print("[*] 编译 C 扫描器...", end=" ", flush=True)
    lib = compile_scanner()
    if lib:
        print("成功")
    else:
        print("失败, 退出")
        sys.exit(1)

    ncpu = cpu_count()
    workers = min(ncpu, 8)
    print(f"[+] 并行验证: {workers} 进程, 目标: {len(search_targets)} 个数据库")

    regions = get_target_regions(pid)
    total_size = sum(r[3] for r in regions)
    total_mb = total_size / 1024 / 1024
    print(f"[+] {len(regions)} 个内存区域, 共 {total_mb:.1f}MB\n")

    t0 = time.time()
    total_scanned = 0
    total_passed = 0
    verified_count = 0
    scanned_size = 0
    found_keys = {}

    pool = Pool(processes=workers, initializer=init_worker, initargs=(search_targets,))
    BATCH_SZ = 300

    remaining_dbs = {}
    for db_path in search_targets:
        remaining_dbs[os.path.basename(db_path)] = db_path

    try:
        for idx, (prio, start, end, size, name) in enumerate(regions):
            if not remaining_dbs:
                break

            rname = name if name else "(anon)"
            size_mb = size / 1024 / 1024
            print(f"\n  [{idx+1}/{len(regions)}] 0x{start:x} ({size_mb:.1f}MB) {rname} [P{prio}]")

            try:
                fd = os.open(f"/proc/{pid}/mem", os.O_RDONLY)
                os.lseek(fd, start, os.SEEK_SET)
                data = os.read(fd, size)
                os.close(fd)
            except OSError:
                print("    [读取失败]")
                scanned_size += size
                continue

            t_scan = time.time()
            offsets = scan_region_c(lib, data)
            scan_time = time.time() - t_scan

            positions = len(data) // 8
            total_scanned += positions
            total_passed += len(offsets)
            scanned_size += size

            overall_pct = scanned_size / max(total_size, 1) * 100
            print(f"    扫描: {scan_time:.2f}s, {positions} 位置 → {len(offsets)} 候选 "
                  f"(总进度: {overall_pct:.0f}%)")

            if not offsets:
                continue

            batch_info = []
            for off in offsets:
                candidate = data[off:off+32]
                key_hex = candidate.hex()
                batch_info.append((key_hex, start + off))

                if len(batch_info) >= BATCH_SZ:
                    key_hexes = [b[0] for b in batch_info]
                    results = pool.map(verify_key_multi, key_hexes)
                    verified_count += len(results)

                    for j, (kh, db_results) in enumerate(results):
                        for db_name, compat in db_results.items():
                            if db_name in remaining_dbs:
                                addr = batch_info[j][1]
                                db_path = remaining_dbs.pop(db_name)
                                found_keys[db_name] = (kh, addr, rname, compat, db_path)
                                print(f"\n    ★ 找到 {db_name} 的密钥! "
                                      f"key={kh[:8]}...{kh[-8:]} compat={compat}")

                    if not remaining_dbs:
                        break

                    elapsed = time.time() - t0
                    speed = verified_count / max(elapsed, 0.01)
                    remaining_est = max(total_passed - verified_count, 0) / max(speed, 0.01)
                    eta_s = f"{remaining_est:.0f}s" if remaining_est < 120 else f"{remaining_est/60:.1f}min"
                    db_st = f"找到:{len(found_keys)}/{len(search_targets)}"
                    print(progress_bar(
                        verified_count, total_passed, width=30,
                        prefix="验证 ",
                        suffix=f"{speed:.0f}/s {db_st} ETA:{eta_s}"
                    ), end="", flush=True)

                    batch_info = []

            # 处理剩余
            if batch_info and remaining_dbs:
                key_hexes = [b[0] for b in batch_info]
                results = pool.map(verify_key_multi, key_hexes)
                verified_count += len(results)
                for j, (kh, db_results) in enumerate(results):
                    for db_name, compat in db_results.items():
                        if db_name in remaining_dbs:
                            addr = batch_info[j][1]
                            db_path = remaining_dbs.pop(db_name)
                            found_keys[db_name] = (kh, addr, rname, compat, db_path)
                            print(f"\n    ★ 找到 {db_name} 的密钥! "
                                  f"key={kh[:8]}...{kh[-8:]} compat={compat}")

    except KeyboardInterrupt:
        print("\n\n[!] 用户中断")
    finally:
        pool.terminate()
        pool.join()

    elapsed = time.time() - t0

    # ========== 阶段 4: 结果 ==========
    print(f"\n\n{'='*60}")
    print(f"  结果")
    print(f"{'='*60}")
    print(f"  耗时: {elapsed:.1f}s")
    print(f"  扫描: {total_scanned} 位置, 候选: {total_passed}, 验证: {verified_count}")

    now_str = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")

    if found_keys:
        print(f"\n  ★ 找到 {len(found_keys)} 个密钥:\n")
        for db_name, (key_hex, addr, rname, compat, db_path) in found_keys.items():
            print(f"  {'─'*54}")
            print(f"  数据库:   {db_name}")
            print(f"  路径:     {db_path}")
            print(f"  密钥:     {key_hex}")
            print(f"  地址:     0x{addr:x} ({rname})")
            print(f"  兼容模式: cipher_compatibility = {compat}")

            known_keys[db_name] = {
                "key": key_hex,
                "compat": compat,
                "db_path": db_path,
                "found_at": f"0x{addr:x}",
                "region": rname,
                "found_time": now_str,
            }

    for i, db_path, rel, sz, salt, status, key, compat in db_status:
        db_name = os.path.basename(db_path)
        if status == "unlocked" and db_name not in known_keys:
            known_keys[db_name] = {
                "key": key,
                "compat": compat,
                "db_path": db_path,
                "found_time": now_str,
            }

    save_keys(known_keys)
    print(f"\n  密钥已保存: {DEFAULT_KEYS_FILE}")

    print(f"\n  文件内容:")
    for db_name, info in known_keys.items():
        k = info.get("key", "?")
        c = info.get("compat", "?")
        print(f"    {db_name}: key={k[:8]}...{k[-8:]}, compat={c}")

    if remaining_dbs:
        print(f"\n  未找到密钥的数据库:")
        for db_name in remaining_dbs:
            print(f"    ✗ {db_name}")
        print(f"\n  [提示] 可能原因:")
        print(f"    1. 密钥不在当前进程内存中（需要微信打开过该功能）")
        print(f"    2. 密钥格式不同（非 32 字节 raw key）")
        print(f"    3. 加密参数不同（非 SQLCipher 1-4）")
