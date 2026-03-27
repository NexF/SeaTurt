"""
WeChatDB — 微信数据库操作封装层

P2.0 核心模块：
  - 密钥管理: 检查缓存 → 后台异步提取 → 状态查询
  - 连接池: 按需打开 message_0.db / contact.db / session.db
  - 对外暴露状态查询接口

架构:
  WeChatDB (单例)
    ├── ensure_keys()         检查缓存密钥 → 有则直接用，无则触发内存扫描
    ├── extract_keys_async()  后台线程执行密钥提取，不阻塞主线程
    ├── get_key_status()      返回密钥提取状态
    ├── get_db_conn(db_name)  获取数据库连接（自动用密钥打开）
    └── close_all()           关闭所有连接

状态机:
  not_started → extracting → ready / failed
  ready 状态下密钥已缓存到 session/wechat_db_keys.json

线程安全:
  - _status / _extract_result 通过 threading.Lock 保护
  - 数据库连接不跨线程共享（每次 get_db_conn 独立连接）
"""

import os
import time
import threading
import logging

from db_utils import load_keys, save_keys, try_open_db, DEFAULT_KEYS_FILE

logger = logging.getLogger("wechat-db")


class WeChatDB:
    """微信数据库操作封装 — 密钥管理 + 连接池 + 状态查询"""

    # 核心数据库（P2 必需）
    CORE_DBS = {"message_0.db", "contact.db", "session.db"}

    def __init__(self):
        self._lock = threading.Lock()
        self._status = "not_started"  # not_started / extracting / ready / failed
        self._extract_result = None   # extract_keys() 返回值
        self._extract_thread = None
        self._keys = {}               # {db_name: {"key": ..., "compat": ..., "db_path": ...}}
        self._connections = {}         # {db_name: connection} — 缓存的连接
        self._extract_start_time = 0
        self._extract_elapsed = 0

    # ===================================================================
    # 密钥管理
    # ===================================================================

    def ensure_keys(self) -> bool:
        """检查并确保密钥可用。

        流程:
          1. 如果已 ready，直接返回 True
          2. 尝试从缓存文件加载
          3. 如果缓存中有核心 DB 的密钥且能打开，标记 ready
          4. 否则触发后台异步提取

        Returns:
            True 如果密钥已就绪 (ready)
            False 如果正在提取 (extracting) 或失败 (failed)
        """
        with self._lock:
            if self._status == "ready":
                return True
            if self._status == "extracting":
                return False

        # 尝试从缓存加载
        cached = load_keys()
        if cached:
            # 验证缓存的密钥是否还有效（核心 DB 能打开）
            valid_count = 0
            for db_name in self.CORE_DBS:
                if db_name in cached and "key" in cached[db_name] and "db_path" in cached[db_name]:
                    db_path = cached[db_name]["db_path"]
                    key_hex = cached[db_name]["key"]
                    if os.path.exists(db_path):
                        conn = try_open_db(db_path, key_hex, verbose=False)
                        if conn:
                            conn.close()
                            valid_count += 1

            if valid_count > 0:
                with self._lock:
                    self._keys = cached
                    self._status = "ready"
                logger.info(f"从缓存加载密钥成功: {valid_count} 个核心 DB 可用")
                return True

        # 缓存无效，触发异步提取
        self.extract_keys_async()
        return False

    def extract_keys_async(self):
        """后台线程执行密钥提取，不阻塞主线程。

        如果已经在提取中，不重复启动。
        """
        with self._lock:
            if self._status == "extracting":
                logger.debug("密钥提取已在进行中")
                return
            self._status = "extracting"
            self._extract_start_time = time.time()

        self._extract_thread = threading.Thread(
            target=self._do_extract,
            name="wechat-key-extract",
            daemon=True,
        )
        self._extract_thread.start()
        logger.info("后台密钥提取已启动")

    def _do_extract(self):
        """密钥提取线程执行体"""
        try:
            from key_extract import extract_keys

            def _progress_callback(event, data):
                if event == "status":
                    logger.info(f"[提取] {data}")
                elif event == "found":
                    logger.info(f"[提取] 找到密钥: {data['db_name']}")
                elif event == "error":
                    logger.error(f"[提取] {data}")

            result = extract_keys(callback=_progress_callback)

            with self._lock:
                self._extract_result = result
                self._extract_elapsed = time.time() - self._extract_start_time
                if result["success"]:
                    self._keys = result["keys"]
                    self._status = "ready"
                    logger.info(f"密钥提取成功: {result['unlocked']}/{result['total']} 已解锁, "
                                f"耗时 {self._extract_elapsed:.1f}s")
                else:
                    self._status = "failed"
                    logger.error(f"密钥提取失败: {result.get('error', '未知错误')}")

        except Exception as e:
            with self._lock:
                self._status = "failed"
                self._extract_elapsed = time.time() - self._extract_start_time
                self._extract_result = {
                    "success": False, "keys": {}, "unlocked": 0, "total": 0,
                    "elapsed": self._extract_elapsed, "error": str(e),
                }
            logger.exception(f"密钥提取线程异常: {e}")

    def get_key_status(self) -> dict:
        """返回密钥提取状态。

        Returns:
            dict: {
                "db_status": "not_started" / "extracting" / "ready" / "failed",
                "db_count": int,          # 已解锁数据库数量
                "db_total": int,          # 总数据库数量
                "db_extract_time": float, # 密钥提取耗时（秒）
                "core_dbs": dict,         # 核心 DB 可用状态 {"message_0.db": bool, ...}
                "error": str|None,        # 错误信息
            }
        """
        with self._lock:
            status = self._status
            result = self._extract_result
            keys = dict(self._keys)
            elapsed = self._extract_elapsed

        # 核心 DB 可用状态
        core_status = {}
        for db_name in self.CORE_DBS:
            core_status[db_name] = (db_name in keys and "key" in keys.get(db_name, {}))

        total = result["total"] if result else 0
        unlocked = result["unlocked"] if result else len(keys)
        error = result.get("error") if result else None

        return {
            "db_status": status,
            "db_count": unlocked,
            "db_total": total,
            "db_extract_time": round(elapsed, 1),
            "core_dbs": core_status,
            "error": error,
        }

    # ===================================================================
    # 数据库连接
    # ===================================================================

    def get_db_conn(self, db_name: str):
        """获取指定数据库的连接。

        Args:
            db_name: 数据库文件名，如 "message_0.db", "contact.db", "session.db"

        Returns:
            sqlite3.Connection (pysqlcipher3)

        Raises:
            RuntimeError: 密钥未就绪或数据库信息不存在
        """
        with self._lock:
            if self._status != "ready":
                raise RuntimeError(
                    f"DB 密钥未就绪 (status={self._status})，无法打开数据库。"
                    f"请等待密钥提取完成。"
                )
            keys = dict(self._keys)

        if db_name not in keys:
            raise RuntimeError(f"数据库 {db_name} 的密钥不存在")

        info = keys[db_name]
        key_hex = info["key"]
        db_path = info["db_path"]

        if not os.path.exists(db_path):
            raise RuntimeError(f"数据库文件不存在: {db_path}")

        # 检查缓存连接是否有效
        if db_name in self._connections:
            try:
                self._connections[db_name].execute("SELECT 1")
                return self._connections[db_name]
            except Exception:
                # 连接失效，重新打开
                try:
                    self._connections[db_name].close()
                except Exception:
                    pass
                del self._connections[db_name]

        # 打开新连接
        conn = try_open_db(db_path, key_hex, verbose=True)
        if not conn:
            raise RuntimeError(f"无法打开数据库 {db_name}（密钥可能已失效）")

        self._connections[db_name] = conn
        return conn

    def is_ready(self) -> bool:
        """密钥是否已就绪"""
        with self._lock:
            return self._status == "ready"

    def get_status_text(self) -> str:
        """返回可读的状态描述"""
        with self._lock:
            status = self._status
        if status == "not_started":
            return "DB 密钥提取未开始"
        elif status == "extracting":
            elapsed = time.time() - self._extract_start_time
            return f"正在提取 DB 密钥 ({elapsed:.0f}s)..."
        elif status == "ready":
            return f"DB 已就绪 ({len(self._keys)} 个密钥)"
        elif status == "failed":
            error = ""
            if self._extract_result:
                error = self._extract_result.get("error", "")
            return f"DB 密钥提取失败: {error}"
        return f"未知状态: {status}"

    # ===================================================================
    # 清理
    # ===================================================================

    def close_all(self):
        """关闭所有数据库连接"""
        for db_name, conn in list(self._connections.items()):
            try:
                conn.close()
                logger.debug(f"已关闭连接: {db_name}")
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
