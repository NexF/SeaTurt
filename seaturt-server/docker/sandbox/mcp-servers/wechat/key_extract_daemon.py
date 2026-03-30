#!/usr/bin/env python3
"""
key_extract_daemon — 长驻密钥提取守护进程

s6 longrun service，容器启动后在后台持续轮询：
  1. 等待 xwechat_files 目录出现（30s 间隔）
  2. 扫描所有 account 目录下的加密 DB（30s 间隔）
  3. 等待微信进程运行（10s 间隔）
  4. 按 account 检查缓存有效性，对需要提取的 account 执行 extract_keys()
  5. 检测活跃账号（mtime），原子写 JSON
  6. 成功后进入 300s 低频 watch 模式

与 MCP tool call (main.py) 通过 JSON 文件通信，完全解耦。
"""

import os
import sys
import json
import time
import signal
import logging
import datetime

# 确保能 import 同目录下的模块
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from db_utils import (
    load_keys, save_keys, DEFAULT_KEYS_FILE,
    extract_account_id, detect_active_account,
)
from key_extract import find_wechat_pid, find_all_dbs, extract_keys

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=logging.INFO,
    format="[keyextract-daemon] %(asctime)s %(levelname)s %(message)s",
    datefmt="%H:%M:%S",
    stream=sys.stderr,
)
logger = logging.getLogger("keyextract-daemon")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

XWECHAT_BASES = [
    os.path.expanduser("~/Documents/xwechat_files"),
    "/home/ubuntu/Documents/xwechat_files",
    "/root/Documents/xwechat_files",
]

# 轮询间隔（秒）
INTERVAL_WAIT_DIR = 30      # 等待 xwechat_files 目录
INTERVAL_WAIT_PROCESS = 10  # 等待微信进程
INTERVAL_WATCH = 300        # 成功后低频 watch

# ---------------------------------------------------------------------------
# Graceful shutdown
# ---------------------------------------------------------------------------

_shutdown = False


def _handle_signal(signum, frame):
    global _shutdown
    logger.info(f"收到信号 {signum}，准备退出...")
    _shutdown = True


signal.signal(signal.SIGTERM, _handle_signal)
signal.signal(signal.SIGINT, _handle_signal)


def _interruptible_sleep(seconds: float) -> bool:
    """可中断的 sleep，返回 True 表示被中断（需要退出）"""
    end = time.time() + seconds
    while time.time() < end:
        if _shutdown:
            return True
        time.sleep(min(1.0, end - time.time()))
    return _shutdown


# ---------------------------------------------------------------------------
# Helper functions
# ---------------------------------------------------------------------------

def find_xwechat_dir() -> str | None:
    """查找 xwechat_files 目录"""
    for base in XWECHAT_BASES:
        if os.path.isdir(base):
            return base
    return None


def _needs_extraction(cached_keys: dict, account_dbs: dict) -> list[str]:
    """返回需要重新提取密钥的 account_id 列表

    Args:
        cached_keys: 完整缓存（含 _meta）
        account_dbs: {account_id: [(db_path, rel, size, salt), ...]}
    Returns:
        需要提取的 account_id 列表
    """
    need_extract = []
    for acct_id, dbs in account_dbs.items():
        cached_acct = cached_keys.get(acct_id, {})
        current_db_names = {os.path.basename(db[0]) for db in dbs}
        cached_db_names = set(cached_acct.keys())
        # 有新 DB 未被缓存覆盖 → 需要提取
        if not current_db_names.issubset(cached_db_names):
            need_extract.append(acct_id)
            continue
        # 缓存中的 db_path 不存在 → 需要提取
        for db_name, info in cached_acct.items():
            if not os.path.exists(info.get("db_path", "")):
                need_extract.append(acct_id)
                break
    return need_extract


def _build_multi_account_cache(
    extract_result: dict,
    existing_cache: dict,
    account_dbs: dict,
) -> dict:
    """将 extract_keys() 的结果合并进多账号缓存结构

    Args:
        extract_result: extract_keys() 返回值（含 accounts 字段）
        existing_cache: 现有缓存（含 _meta）
        account_dbs: {account_id: [(db_path, rel, size, salt), ...]}
    Returns:
        更新后的完整缓存
    """
    cache = dict(existing_cache)

    if extract_result.get("success") and "accounts" in extract_result:
        # 新版多账号返回
        for acct_id, keys in extract_result["accounts"].items():
            cache[acct_id] = keys
    elif extract_result.get("success") and "keys" in extract_result:
        # 兼容旧版扁平返回：按 db_path 分组到 account
        for db_name, info in extract_result["keys"].items():
            db_path = info.get("db_path", "")
            acct_id = extract_account_id(db_path)
            cache.setdefault(acct_id, {})[db_name] = info

    # 更新 active_account
    active = detect_active_account(account_dbs)
    now_str = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    cache["_meta"] = {
        "active_account": active,
        "updated_at": now_str,
    }

    return cache


# ---------------------------------------------------------------------------
# Main daemon loop
# ---------------------------------------------------------------------------

def daemon_loop():
    """主轮询循环"""
    logger.info("密钥提取 daemon 启动")

    while not _shutdown:
        # ---- Phase 1: 等待 xwechat_files 目录 ----
        xwechat_dir = find_xwechat_dir()
        if not xwechat_dir:
            logger.debug("xwechat_files 目录未找到，等待...")
            if _interruptible_sleep(INTERVAL_WAIT_DIR):
                break
            continue

        # ---- Phase 2: 扫描所有 account 的加密 DB ----
        account_dbs = find_all_dbs()  # {account_id: [(db_path, rel, size, salt), ...]}
        if not account_dbs:
            logger.debug("未发现加密数据库，等待...")
            if _interruptible_sleep(INTERVAL_WAIT_DIR):
                break
            continue

        total_dbs = sum(len(dbs) for dbs in account_dbs.values())
        logger.info(f"发现 {len(account_dbs)} 个账号, {total_dbs} 个加密 DB")

        # ---- Phase 3: 等待微信进程 ----
        pid = find_wechat_pid()
        if not pid:
            logger.debug("微信进程未运行，等待...")
            if _interruptible_sleep(INTERVAL_WAIT_PROCESS):
                break
            continue

        logger.info(f"微信进程 PID: {pid}")

        # ---- Phase 4: 检查哪些 account 需要提取 ----
        cached = load_keys()
        if not cached:
            cached = {"_meta": {"active_account": None, "updated_at": ""}}

        need_extract = _needs_extraction(cached, account_dbs)

        if not need_extract:
            # 所有账号都已缓存，只更新 active_account
            active = detect_active_account(account_dbs)
            if active != cached.get("_meta", {}).get("active_account"):
                cached["_meta"]["active_account"] = active
                cached["_meta"]["updated_at"] = datetime.datetime.now().strftime(
                    "%Y-%m-%d %H:%M:%S"
                )
                save_keys(cached)
                logger.info(f"活跃账号已更新: {active}")
            else:
                logger.debug("所有账号密钥已缓存，无需提取")

            if _interruptible_sleep(INTERVAL_WATCH):
                break
            continue

        # ---- Phase 5: 执行密钥提取 ----
        logger.info(f"需要提取密钥的账号: {need_extract}")

        try:
            def _progress(event, data):
                if event == "status":
                    logger.info(f"[提取] {data}")
                elif event == "found":
                    logger.info(f"[提取] 找到密钥: {data.get('db_name', '?')}")

            result = extract_keys(
                callback=_progress,
                account_filter=need_extract,
            )
        except Exception as e:
            logger.exception(f"密钥提取异常: {e}")
            if _interruptible_sleep(INTERVAL_WAIT_PROCESS):
                break
            continue

        # ---- Phase 6: 合并结果 + 原子写 ----
        if result.get("success"):
            updated_cache = _build_multi_account_cache(result, cached, account_dbs)
            save_keys(updated_cache)

            unlocked = result.get("unlocked", 0)
            total = result.get("total", 0)
            elapsed = result.get("elapsed", 0)
            active = updated_cache.get("_meta", {}).get("active_account")
            logger.info(
                f"密钥提取完成: {unlocked}/{total} 已解锁, "
                f"耗时 {elapsed:.1f}s, 活跃账号: {active}"
            )
        else:
            error = result.get("error", "未知错误")
            logger.warning(f"密钥提取未成功: {error}")
            # 即使失败也更新 active_account
            active = detect_active_account(account_dbs)
            cached["_meta"] = {
                "active_account": active,
                "updated_at": datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
            }
            save_keys(cached)

        # ---- Phase 7: 低频 watch ----
        if _interruptible_sleep(INTERVAL_WATCH):
            break

    logger.info("密钥提取 daemon 退出")


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    daemon_loop()
