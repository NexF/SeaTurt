"""
WeChatDB — 微信数据库操作封装层

纯缓存读取模式 + 多微信账号支持：
  - 密钥管理: 从 daemon 写入的 JSON 缓存读取，不再自行启动提取线程
  - 连接池: 按需打开 message_0.db / contact.db / session.db
  - 对外暴露状态查询接口

架构:
  WeChatDB (单例)
    ├── ensure_keys()         读 JSON 缓存 → 验证活跃账号核心 DB
    ├── get_key_status()      返回多账号密钥状态
    ├── get_db_conn(db_name)  获取数据库连接（默认活跃账号，可指定 account）
    └── close_all()           关闭所有连接

密钥提取由 key_extract_daemon.py（s6 longrun service）完成，
通过 wechat_db_keys.json 文件与本模块通信。
"""

import os
import threading
import logging

from db_utils import load_keys, try_open_db, DEFAULT_KEYS_FILE

logger = logging.getLogger("wechat-db")


class WeChatDB:
    """微信数据库操作封装 — 纯缓存读取 + 多账号支持"""

    # 核心数据库（P2 必需）
    CORE_DBS = {"message_0.db", "contact.db", "session.db"}

    def __init__(self):
        self._lock = threading.Lock()
        self._status = "not_started"           # not_started / ready / failed
        self._all_keys = {}                    # {account_id: {db_name: info}}
        self._active_account = None            # 当前活跃 account_id
        self._connections = {}                 # {(account_id, db_name): conn}

    # ===================================================================
    # 密钥管理
    # ===================================================================

    def ensure_keys(self) -> bool:
        """从 daemon 写入的 JSON 缓存加载密钥 → 验证活跃账号核心 DB → 标记 ready。

        Returns:
            True 如果密钥已就绪 (ready)
            False 如果缓存不存在或验证失败
        """
        with self._lock:
            if self._status == "ready":
                return True

        # 读取 daemon 写入的多账号 JSON 缓存
        cached = load_keys()
        if not cached or "_meta" not in cached:
            return False

        active = cached["_meta"].get("active_account")
        if not active or active not in cached:
            return False

        acct_keys = cached[active]

        # 验证核心 DB 可打开
        valid_count = 0
        for db_name in self.CORE_DBS:
            if db_name in acct_keys and "key" in acct_keys[db_name]:
                db_path = acct_keys[db_name].get("db_path", "")
                if os.path.exists(db_path):
                    conn = try_open_db(db_path, acct_keys[db_name]["key"], verbose=False)
                    if conn:
                        conn.close()
                        valid_count += 1

        if valid_count > 0:
            with self._lock:
                # 加载所有账号的密钥（去掉 _meta）
                self._all_keys = {k: v for k, v in cached.items() if k != "_meta"}
                self._active_account = active
                self._status = "ready"
            logger.info(f"从缓存加载密钥成功: 活跃账号={active}, {valid_count} 个核心 DB 可用")
            return True

        return False

    def get_key_status(self) -> dict:
        """返回密钥状态（含多账号信息）。

        Returns:
            dict: {
                "db_status": "not_started" / "ready" / "failed",
                "active_account": str|None,
                "accounts": list[str],
                "db_count": int,
                "db_total": int,
                "core_dbs": dict,
                "error": str|None,
            }
        """
        with self._lock:
            status = self._status
            all_keys = dict(self._all_keys)
            active = self._active_account

        # 核心 DB 可用状态（基于活跃账号）
        acct_keys = all_keys.get(active, {}) if active else {}
        core_status = {}
        for db_name in self.CORE_DBS:
            core_status[db_name] = (db_name in acct_keys and "key" in acct_keys.get(db_name, {}))

        # 统计
        total_dbs = sum(len(v) for v in all_keys.values())
        accounts = list(all_keys.keys())

        return {
            "db_status": status,
            "active_account": active,
            "accounts": accounts,
            "db_count": total_dbs,
            "db_total": total_dbs,
            "core_dbs": core_status,
            "error": None,
        }

    # ===================================================================
    # 数据库连接
    # ===================================================================

    def get_db_conn(self, db_name: str, account: str | None = None):
        """获取指定数据库的连接。

        Args:
            db_name: 数据库文件名，如 "message_0.db", "contact.db", "session.db"
            account: 可选的 account_id，默认使用活跃账号

        Returns:
            sqlite3.Connection (pysqlcipher3)

        Raises:
            RuntimeError: 密钥未就绪或数据库信息不存在
        """
        with self._lock:
            if self._status != "ready":
                raise RuntimeError(
                    f"DB 密钥未就绪 (status={self._status})，无法打开数据库。"
                    f"请等待密钥提取服务完成。"
                )
            all_keys = dict(self._all_keys)
            acct = account or self._active_account

        if not acct or acct not in all_keys:
            raise RuntimeError(f"账号 {acct} 的密钥不存在")

        acct_keys = all_keys[acct]
        if db_name not in acct_keys:
            raise RuntimeError(f"账号 {acct} 的数据库 {db_name} 密钥不存在")

        info = acct_keys[db_name]
        key_hex = info["key"]
        db_path = info["db_path"]

        if not os.path.exists(db_path):
            raise RuntimeError(f"数据库文件不存在: {db_path}")

        # 检查缓存连接是否有效（按 (account, db_name) 缓存）
        conn_key = (acct, db_name)
        if conn_key in self._connections:
            try:
                self._connections[conn_key].execute("SELECT 1")
                return self._connections[conn_key]
            except Exception:
                # 连接失效，重新打开
                try:
                    self._connections[conn_key].close()
                except Exception:
                    pass
                del self._connections[conn_key]

        # 打开新连接
        conn = try_open_db(db_path, key_hex, verbose=True)
        if not conn:
            raise RuntimeError(f"无法打开数据库 {db_name}（密钥可能已失效）")

        self._connections[conn_key] = conn
        return conn

    def is_ready(self) -> bool:
        """密钥是否已就绪"""
        with self._lock:
            return self._status == "ready"

    def get_status_text(self) -> str:
        """返回可读的状态描述"""
        with self._lock:
            status = self._status
            active = self._active_account
            acct_count = len(self._all_keys)

        if status == "not_started":
            return "DB 密钥提取服务正在后台工作中，请稍后重试"
        elif status == "ready":
            total_keys = sum(len(v) for v in self._all_keys.values())
            return f"DB 已就绪 (账号={active}, {total_keys} 个密钥, {acct_count} 个账号)"
        elif status == "failed":
            return "DB 密钥加载失败"
        return f"未知状态: {status}"

    # ===================================================================
    # 清理
    # ===================================================================

    def close_all(self):
        """关闭所有数据库连接"""
        for conn_key, conn in list(self._connections.items()):
            try:
                conn.close()
                logger.debug(f"已关闭连接: {conn_key}")
            except Exception:
                pass
        self._connections.clear()

    def __del__(self):
        self.close_all()


# ===================================================================
# 全局单例
# ===================================================================

_instance: WeChatDB | None = None
_instance_lock = threading.Lock()


def get_wechat_db() -> WeChatDB:
    """获取 WeChatDB 全局单例"""
    global _instance
    if _instance is None:
        with _instance_lock:
            if _instance is None:
                _instance = WeChatDB()
    return _instance
