#!/usr/bin/env python3
"""
微信 SQLCipher 密钥提取 — 全自动非交互版

适配自 scripts/key_extract.py，去掉交互式 input()，改为全自动模式：
  - 自动扫描所有加密数据库
  - 自动选择所有 locked DB 进行密钥搜索
  - 密钥缓存到 session/wechat_db_keys.json

供 wechat_db.py 调用，不单独运行。
"""

import os
import sys
import json
import time
import struct
import subprocess
import ctypes
import tempfile
import logging
from multiprocessing import Pool, cpu_count
from collections import Counter

from db_utils import load_keys, save_keys, DEFAULT_KEYS_FILE, extract_account_id

logger = logging.getLogger("wechat-key-extract")


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
    """编译 C 扫描器为 .so 动态库"""
    tmpdir = tempfile.mkdtemp()
    c_path = os.path.join(tmpdir, "scanner.c")
    so_path = os.path.join(tmpdir, "scanner.so")
    with open(c_path, 'w') as f:
        f.write(C_SCANNER_SRC)
    r = subprocess.run(["gcc", "-O3", "-shared", "-fPIC", "-o", so_path, c_path],
                       capture_output=True, text=True)
    if r.returncode != 0:
        logger.error(f"C 编译失败: {r.stderr}")
        return None
    lib = ctypes.CDLL(so_path)
    lib.scan_candidates.restype = ctypes.c_int
    lib.scan_candidates.argtypes = [
        ctypes.c_char_p, ctypes.c_int,
        ctypes.POINTER(ctypes.c_int), ctypes.c_int,
    ]
    return lib


def scan_region_c(lib, data):
    """用 C 扫描器在内存块中搜索候选密钥偏移"""
    max_out = len(data) // 8 + 1
    out_arr = (ctypes.c_int * max_out)()
    count = lib.scan_candidates(data, len(data), out_arr, max_out)
    return [out_arr[i] for i in range(count)]


# ============ 验证 ============

_WORKER_DBS = None


def _init_worker(db_targets):
    global _WORKER_DBS
    _WORKER_DBS = db_targets


def _verify_key_multi(key_hex):
    """对多个数据库逐一验证密钥（仅 compat=4）。"""
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
    """查找微信进程 PID"""
    for cmd in [["pgrep", "-x", "wechat"], ["pgrep", "-f", "/opt/wechat/wechat"]]:
        try:
            r = subprocess.run(cmd, capture_output=True, text=True)
            pids = [p.strip() for p in r.stdout.strip().split('\n') if p.strip()]
            if pids:
                return int(pids[0])
        except Exception:
            pass
    return None


def find_all_dbs():
    """找到所有加密的微信数据库，按 account_id 分组。

    扫描路径: ~/Documents/xwechat_files/<wxid>_<hash>/db_storage/

    Returns:
        {account_id: [(db_path, rel_path, size, salt_hex), ...]}
    """
    # 支持多个可能的根路径
    search_bases = [
        os.path.expanduser("~/Documents/xwechat_files"),
        "/home/ubuntu/Documents/xwechat_files",
        "/root/Documents/xwechat_files",
    ]

    accounts = {}  # {account_id: [(db_path, rel, size, salt), ...]}
    for base in search_bases:
        if not os.path.isdir(base):
            continue
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
                        continue  # 未加密的跳过
                    acct_id = extract_account_id(full)
                    rel = full.split('db_storage/')[-1] if 'db_storage/' in full else f
                    accounts.setdefault(acct_id, []).append((full, rel, sz, h.hex()))

    return accounts


def try_key_on_db(db_path, key_hex):
    """用给定密钥尝试打开数据库，返回 compat 或 0。"""
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


def get_target_regions(pid):
    """获取微信进程的可扫描内存区域，按优先级排序"""
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


# ============ 主逻辑（全自动） ============

def extract_keys(callback=None, account_filter: list[str] | None = None):
    """全自动密钥提取主函数。

    全流程：
      1. 查找微信 PID
      2. 扫描所有加密数据库（按 account 分组）
      3. 用已有密钥测试哪些已解开
      4. 对未解锁的数据库自动进行内存扫描 + 验证
      5. 保存结果

    Args:
        callback: 可选回调函数 callback(event, data)，用于上报进度：
            - ("status", str): 当前步骤描述
            - ("progress", {"scanned": int, "total": int}): 扫描进度
            - ("found", {"db_name": str, "key": str}): 找到密钥
            - ("error", str): 错误信息
        account_filter: 只提取指定 account_id 列表的密钥（None = 全部）

    Returns:
        dict: {
            "success": bool,
            "accounts": dict,      # {account_id: {db_name: {"key": ..., "compat": ..., "db_path": ...}}}
            "keys": dict,          # 扁平兼容: {db_name: {"key": ..., ...}} (所有账号合并)
            "unlocked": int,       # 已解锁数量
            "total": int,          # 总数据库数量
            "elapsed": float,      # 耗时（秒）
            "error": str|None,     # 错误信息
        }
    """
    import datetime

    def _cb(event, data):
        if callback:
            try:
                callback(event, data)
            except Exception:
                pass

    t0 = time.time()

    # 1. 查找微信 PID
    _cb("status", "查找微信进程...")
    pid = find_wechat_pid()
    if not pid:
        _cb("error", "微信未运行")
        return {
            "success": False, "accounts": {}, "keys": {}, "unlocked": 0, "total": 0,
            "elapsed": time.time() - t0, "error": "微信未运行",
        }
    logger.info(f"微信 PID: {pid}")

    # 2. 扫描数据库（按 account 分组）
    _cb("status", "扫描加密数据库...")
    account_dbs = find_all_dbs()  # {account_id: [(db_path, rel, size, salt), ...]}

    # 应用 account_filter
    if account_filter:
        account_dbs = {k: v for k, v in account_dbs.items() if k in account_filter}

    if not account_dbs:
        _cb("error", "找不到任何加密数据库")
        return {
            "success": False, "accounts": {}, "keys": {}, "unlocked": 0, "total": 0,
            "elapsed": time.time() - t0, "error": "找不到任何加密数据库",
        }

    # 展平为列表，供后续内存扫描使用
    all_dbs = []
    for acct_id, dbs in account_dbs.items():
        for db_info in dbs:
            all_dbs.append((acct_id, *db_info))  # (acct_id, db_path, rel, sz, salt)

    logger.info(f"找到 {len(account_dbs)} 个账号, {len(all_dbs)} 个加密数据库")

    # 3. 用已有密钥测试
    _cb("status", f"测试已有密钥 ({len(all_dbs)} 个数据库)...")
    known_keys = load_keys()
    # 从多账号或扁平缓存中收集已知密钥值
    all_known_values = set()
    for k, v in known_keys.items():
        if k == "_meta":
            continue
        if isinstance(v, dict) and "key" in v:
            all_known_values.add(v["key"])
        elif isinstance(v, dict):
            # 多账号格式: v = {db_name: {key: ...}}
            for db_info in v.values():
                if isinstance(db_info, dict) and "key" in db_info:
                    all_known_values.add(db_info["key"])
    all_known_values = list(all_known_values)

    db_status = []
    for acct_id, db_path, rel, sz, salt in all_dbs:
        db_name = os.path.basename(db_path)
        status = "locked"
        found_key = None
        found_compat = 0

        # 先用对应 account + db_name 的已知密钥
        acct_cache = known_keys.get(acct_id, {})
        if isinstance(acct_cache, dict) and db_name in acct_cache:
            cached_info = acct_cache[db_name]
            if isinstance(cached_info, dict) and "key" in cached_info:
                c = try_key_on_db(db_path, cached_info["key"])
                if c:
                    status = "unlocked"
                    found_key = cached_info["key"]
                    found_compat = c

        # 再用所有已知密钥交叉验证
        if status == "locked":
            for k in all_known_values:
                c = try_key_on_db(db_path, k)
                if c:
                    status = "unlocked"
                    found_key = k
                    found_compat = c
                    break

        db_status.append((acct_id, db_path, rel, sz, salt, status, found_key, found_compat))

    locked_list = []
    unlocked_list = []
    for acct_id, db_path, rel, sz, salt, status, key, compat in db_status:
        if status == "unlocked":
            unlocked_list.append((acct_id, db_path, rel, sz, key, compat))
        else:
            locked_list.append((acct_id, db_path, rel, sz, salt))

    logger.info(f"数据库状态: {len(unlocked_list)} 已解锁, {len(locked_list)} 未解锁")

    # 构建多账号结果
    now_str = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    accounts_result = {}  # {account_id: {db_name: info}}

    # 已解锁的先放入结果
    for acct_id, db_path, rel, sz, key, compat in unlocked_list:
        db_name = os.path.basename(db_path)
        accounts_result.setdefault(acct_id, {})[db_name] = {
            "key": key,
            "compat": compat,
            "db_path": db_path,
            "found_time": now_str,
        }

    # 如果全部已解锁，直接返回
    if not locked_list:
        _cb("status", "所有数据库都已解锁!")
        # 构建扁平兼容 keys
        flat_keys = {}
        for acct_id, keys in accounts_result.items():
            flat_keys.update(keys)
        save_keys(known_keys)  # 保留原有缓存
        return {
            "success": True, "accounts": accounts_result, "keys": flat_keys,
            "unlocked": len(all_dbs), "total": len(all_dbs),
            "elapsed": time.time() - t0, "error": None,
        }

    # 4. 内存扫描 + 验证
    search_targets = [db_path for acct_id, db_path, rel, sz, salt in locked_list]
    logger.info(f"将为 {len(search_targets)} 个数据库搜索密钥")

    _cb("status", "编译 C 扫描器...")
    lib = compile_scanner()
    if not lib:
        _cb("error", "C 编译失败")
        flat_keys = {}
        for acct_id, keys in accounts_result.items():
            flat_keys.update(keys)
        return {
            "success": len(unlocked_list) > 0,
            "accounts": accounts_result, "keys": flat_keys,
            "unlocked": len(unlocked_list), "total": len(all_dbs),
            "elapsed": time.time() - t0, "error": "C 扫描器编译失败（需要 gcc）",
        }

    ncpu = cpu_count()
    workers = min(ncpu, 8)
    logger.info(f"并行验证: {workers} 进程")

    regions = get_target_regions(pid)
    total_size = sum(r[3] for r in regions)
    logger.info(f"{len(regions)} 个内存区域, 共 {total_size / 1024 / 1024:.1f}MB")

    _cb("status", f"扫描内存 ({len(regions)} 个区域, {total_size / 1024 / 1024:.0f}MB)...")

    total_scanned = 0
    total_passed = 0
    verified_count = 0
    scanned_size = 0
    found_keys = {}
    BATCH_SZ = 300

    # 建立 db_name → (acct_id, db_path) 映射
    remaining_dbs = {}
    for acct_id, db_path, rel, sz, salt in locked_list:
        db_name = os.path.basename(db_path)
        remaining_dbs[db_name] = (acct_id, db_path)

    pool = Pool(processes=workers, initializer=_init_worker, initargs=(search_targets,))

    try:
        for idx, (prio, start, end, size, name) in enumerate(regions):
            if not remaining_dbs:
                break

            rname = name if name else "(anon)"
            size_mb = size / 1024 / 1024

            try:
                fd = os.open(f"/proc/{pid}/mem", os.O_RDONLY)
                os.lseek(fd, start, os.SEEK_SET)
                data = os.read(fd, size)
                os.close(fd)
            except OSError:
                scanned_size += size
                continue

            offsets = scan_region_c(lib, data)
            positions = len(data) // 8
            total_scanned += positions
            total_passed += len(offsets)
            scanned_size += size

            _cb("progress", {
                "scanned": scanned_size,
                "total": total_size,
                "region_idx": idx + 1,
                "region_count": len(regions),
                "candidates": total_passed,
            })

            if not offsets:
                continue

            batch_info = []
            for off in offsets:
                candidate = data[off:off + 32]
                key_hex = candidate.hex()
                batch_info.append((key_hex, start + off))

                if len(batch_info) >= BATCH_SZ:
                    key_hexes = [b[0] for b in batch_info]
                    results = pool.map(_verify_key_multi, key_hexes)
                    verified_count += len(results)

                    for j, (kh, db_results) in enumerate(results):
                        for db_name, compat in db_results.items():
                            if db_name in remaining_dbs:
                                addr = batch_info[j][1]
                                acct_id, db_path = remaining_dbs.pop(db_name)
                                found_keys[db_name] = (kh, addr, rname, compat, db_path, acct_id)
                                logger.info(f"★ 找到 {db_name} 的密钥! "
                                            f"key={kh[:8]}...{kh[-8:]} compat={compat} account={acct_id}")
                                _cb("found", {"db_name": db_name, "key": kh})

                    if not remaining_dbs:
                        break
                    batch_info = []

            # 处理剩余批次
            if batch_info and remaining_dbs:
                key_hexes = [b[0] for b in batch_info]
                results = pool.map(_verify_key_multi, key_hexes)
                verified_count += len(results)
                for j, (kh, db_results) in enumerate(results):
                    for db_name, compat in db_results.items():
                        if db_name in remaining_dbs:
                            addr = batch_info[j][1]
                            acct_id, db_path = remaining_dbs.pop(db_name)
                            found_keys[db_name] = (kh, addr, rname, compat, db_path, acct_id)
                            logger.info(f"★ 找到 {db_name} 的密钥! "
                                        f"key={kh[:8]}...{kh[-8:]} compat={compat} account={acct_id}")
                            _cb("found", {"db_name": db_name, "key": kh})

    except Exception as e:
        logger.error(f"内存扫描异常: {e}")
    finally:
        pool.terminate()
        pool.join()

    elapsed = time.time() - t0

    # 5. 将新找到的密钥加入多账号结果
    if found_keys:
        for db_name, (key_hex, addr, rname, compat, db_path, acct_id) in found_keys.items():
            accounts_result.setdefault(acct_id, {})[db_name] = {
                "key": key_hex,
                "compat": compat,
                "db_path": db_path,
                "found_at": f"0x{addr:x}",
                "region": rname,
                "found_time": now_str,
            }

    # 构建扁平兼容 keys
    flat_keys = {}
    for acct_id, keys in accounts_result.items():
        flat_keys.update(keys)

    total_unlocked = len(unlocked_list) + len(found_keys)
    success = total_unlocked > 0

    logger.info(f"密钥提取完成: {total_unlocked}/{len(all_dbs)} 已解锁, "
                f"耗时 {elapsed:.1f}s")

    if remaining_dbs:
        logger.warning(f"未找到密钥的数据库: {list(remaining_dbs.keys())}")

    _cb("status", f"完成: {total_unlocked}/{len(all_dbs)} 已解锁, 耗时 {elapsed:.1f}s")

    return {
        "success": success,
        "accounts": accounts_result,
        "keys": flat_keys,
        "unlocked": total_unlocked,
        "total": len(all_dbs),
        "elapsed": elapsed,
        "error": None if success else "未能提取到任何密钥",
    }
