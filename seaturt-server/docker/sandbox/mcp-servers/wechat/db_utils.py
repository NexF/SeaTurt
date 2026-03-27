"""
数据库操作公共模块 — 适配自 scripts/db.py

功能:
  - open_db / try_open_db: 打开 SQLCipher 加密数据库
  - list_tables: 列出数据库所有表
  - load_keys / save_keys: 读写密钥 JSON 文件

路径:
  - 密钥缓存: session/wechat_db_keys.json（运行时目录）
"""

import os
import json
import logging

logger = logging.getLogger("wechat-db")

# 密钥文件路径: 与 main.py 的 SESSION_DIR 一致
SESSION_DIR = os.environ.get("WECHAT_SESSION_DIR", "/opt/wechat-daemon/session")
DEFAULT_KEYS_FILE = os.path.join(SESSION_DIR, "wechat_db_keys.json")


def open_db(db_path, key_hex, compat=4):
    """打开 SQLCipher 加密数据库。

    Args:
        db_path: 数据库文件路径
        key_hex: 64 字符十六进制密钥
        compat: cipher_compatibility 版本，默认 4（微信 WCDB 使用 SQLCipher 4）
    """
    from pysqlcipher3 import dbapi2 as sqlite
    conn = sqlite.connect(db_path)
    conn.execute(f"PRAGMA key=\"x'{key_hex}'\";")
    conn.execute(f"PRAGMA cipher_compatibility = {compat};")
    return conn


def try_open_db(db_path, key_hex, verbose=True):
    """尝试打开加密数据库，仅使用 compat=4。

    微信 Linux 版底层使用 WCDB (SQLCipher 4)，无需尝试其他版本。

    Returns:
        成功返回 Connection，失败返回 None
    """
    try:
        conn = open_db(db_path, key_hex, compat=4)
        conn.execute("SELECT count(*) FROM sqlite_master;")
        if verbose:
            logger.info(f"数据库已打开: {os.path.basename(db_path)} (compat=4)")
        return conn
    except Exception as e:
        if verbose:
            logger.debug(f"打开数据库失败 {os.path.basename(db_path)}: {e}")
        return None


def list_tables(conn):
    """列出数据库所有表名"""
    cursor = conn.execute("SELECT name FROM sqlite_master WHERE type='table';")
    return [row[0] for row in cursor.fetchall()]


def load_keys(keys_file=None):
    """加载密钥 JSON 文件。

    Returns:
        dict: {db_name: {"key": ..., "compat": ..., "db_path": ..., ...}}
    """
    path = keys_file or DEFAULT_KEYS_FILE
    if os.path.exists(path):
        try:
            with open(path) as f:
                return json.load(f)
        except (json.JSONDecodeError, IOError) as e:
            logger.warning(f"加载密钥文件失败: {e}")
    return {}


def save_keys(keys_dict, keys_file=None):
    """保存密钥到 JSON 文件"""
    path = keys_file or DEFAULT_KEYS_FILE
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w') as f:
        json.dump(keys_dict, f, indent=2, ensure_ascii=False)
    logger.info(f"密钥已保存: {path} ({len(keys_dict)} 个)")
