"""
数据库操作公共模块 — 所有工具脚本共用的基础设施。

功能:
  - open_db / try_open_db: 打开 SQLCipher 加密数据库
  - list_tables: 列出数据库所有表
  - decrypt_and_save: 解密并导出为明文 SQLite
  - load_keys / save_keys: 读写密钥 JSON 文件
"""

import os
import json

# 基础设施根目录: wechat_decrypt 的上一级 (wx_reverse_base/)
BASE_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# 向后兼容
PROJECT_ROOT = BASE_ROOT

# 默认配置/数据路径
DEFAULT_KEYS_FILE = os.path.join(BASE_ROOT, "config", "wechat_db_keys.json")


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
            print(f"  [+] cipher_compatibility = 4 成功")
        return conn
    except Exception:
        return None


def list_tables(conn):
    """列出数据库所有表名"""
    cursor = conn.execute("SELECT name FROM sqlite_master WHERE type='table';")
    return [row[0] for row in cursor.fetchall()]


def decrypt_and_save(db_path, key_hex, output_path):
    """解密数据库并保存为明文 SQLite 文件。

    Returns:
        成功返回 True，失败返回 False
    """
    conn = try_open_db(db_path, key_hex)
    if not conn:
        print(f"[-] 无法解密: {db_path}")
        return False

    try:
        conn.execute(f"ATTACH DATABASE '{output_path}' AS plaintext KEY '';")
        conn.execute("SELECT sqlcipher_export('plaintext');")
        conn.execute("DETACH DATABASE plaintext;")
        conn.close()
        print(f"[+] 已解密保存: {output_path}")
        return True
    except Exception as e:
        print(f"[-] 导出失败: {e}")
        conn.close()
        return False


def load_keys(keys_file=None):
    """加载密钥 JSON 文件。

    优先从指定路径加载，其次 config/wechat_db_keys.json，
    再次兼容旧格式 data/wechat_db_key.txt。

    Returns:
        dict: {db_name: {"key": ..., "compat": ..., "db_path": ..., ...}}
    """
    path = keys_file or DEFAULT_KEYS_FILE
    if os.path.exists(path):
        with open(path) as f:
            return json.load(f)

    return {}


def save_keys(keys_dict, keys_file=None):
    """保存密钥到 JSON 文件"""
    path = keys_file or DEFAULT_KEYS_FILE
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w') as f:
        json.dump(keys_dict, f, indent=2, ensure_ascii=False)
