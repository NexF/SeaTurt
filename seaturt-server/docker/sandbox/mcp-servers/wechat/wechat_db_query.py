"""
wechat_db_query — 微信数据库查询层

P2.1 message_0.db 消息查询
P2.2 contact.db 联系人查询
P2.3 session.db 会话列表查询

依赖:
  - wechat_db.WeChatDB (P2.0) 提供数据库连接
  - zstandard (zstd 解压 WCDB_CT=4 内容)

架构:
  WeChatDBQuery (单例)
    ├── query_messages()          P2.1 消息查询
    ├── query_all_msg_tables()    P2.1 列出消息表
    ├── query_contacts()          P2.2 联系人查询
    ├── query_chatrooms()         P2.2 群聊列表
    ├── query_chatroom_members()  P2.2 群成员查询
    ├── resolve_username_to_nickname()  P2.2 用户名→昵称
    ├── query_contact_labels()    P2.2 联系人标签
    ├── query_sessions()          P2.3 会话列表
    └── query_unread_sessions()   P2.3 未读会话
"""

import hashlib
import logging
import re
import threading
import time
import xml.etree.ElementTree as ET
from datetime import datetime
from typing import Optional

logger = logging.getLogger("wechat-db-query")

# ============================================================================
# WCDB zstd 解压
# ============================================================================

ZSTD_MAGIC = b"\x28\xb5\x2f\xfd"


def _decompress_wcdb(data) -> str:
    """解压 WCDB_CT=4 的 zstd 压缩内容。

    Args:
        data: bytes 或 str (如果已是 str 直接返回)

    Returns:
        解压后的 UTF-8 字符串
    """
    if data is None:
        return ""
    if isinstance(data, str):
        return data
    if isinstance(data, (bytes, bytearray)):
        if len(data) >= 4 and data[:4] == ZSTD_MAGIC:
            try:
                import zstandard as zstd
                dctx = zstd.ZstdDecompressor()
                decompressed = dctx.decompress(data)
                return decompressed.decode("utf-8", errors="replace")
            except Exception as e:
                logger.warning(f"zstd 解压失败: {e}")
                return data.decode("utf-8", errors="replace")
        else:
            return data.decode("utf-8", errors="replace")
    return str(data)


# ============================================================================
# 消息类型解析 (P2.1)
# ============================================================================

# 基础消息类型
MSG_TYPE_MAP = {
    1: "text",
    3: "image",
    34: "voice",
    43: "video",
    47: "emoji",
    50: "voip",
    10000: "system",
}

# 复合类型 (app_msg_type)
APP_MSG_TYPE_MAP = {
    4: "link",           # 链接分享
    5: "article",        # 公众号文章
    19: "chat_history",  # 聊天记录转发
    57: "reply",         # 引用回复
    62: "pat",           # 拍一拍
}


def _parse_message_type(local_type: int) -> str:
    """解析消息类型。

    Args:
        local_type: message_0.db 中的 local_type 字段

    Returns:
        消息类型名称字符串
    """
    # 基础类型
    if local_type in MSG_TYPE_MAP:
        return MSG_TYPE_MAP[local_type]

    # 复合类型: local_type > 65535
    if local_type > 0xFFFF:
        app_msg_type = local_type >> 32
        if app_msg_type in APP_MSG_TYPE_MAP:
            return APP_MSG_TYPE_MAP[app_msg_type]
        return f"appmsg_{app_msg_type}"

    return f"unknown_{local_type}"


def _parse_message_content(local_type: int, content: str) -> str:
    """解析消息内容，根据类型提取有意义的信息。

    Args:
        local_type: 消息类型
        content: 解压后的消息内容

    Returns:
        人类可读的消息内容摘要
    """
    if not content:
        return ""

    type_name = _parse_message_type(local_type)

    # 文本消息直接返回
    if type_name == "text":
        return content

    # 以下类型需要解析 XML
    try:
        if type_name == "image":
            return _parse_image_xml(content)
        elif type_name == "voice":
            return _parse_voice_xml(content)
        elif type_name == "video":
            return _parse_video_xml(content)
        elif type_name == "emoji":
            return _parse_emoji_xml(content)
        elif type_name == "voip":
            return _parse_voip_xml(content)
        elif type_name == "system":
            return _parse_system_xml(content)
        elif type_name in ("link", "article"):
            return _parse_appmsg_xml(content)
        elif type_name == "chat_history":
            return _parse_appmsg_xml(content)
        elif type_name == "reply":
            return _parse_reply_xml(content)
        elif type_name == "pat":
            return _parse_pat_xml(content)
    except Exception as e:
        logger.debug(f"解析消息内容失败 (type={type_name}): {e}")

    # 兜底：截取前 200 字符
    if len(content) > 200:
        return content[:200] + "..."
    return content


def _safe_xml_parse(content: str):
    """安全解析 XML，处理各种格式异常。"""
    try:
        return ET.fromstring(content)
    except ET.ParseError:
        # 尝试包裹一层 root
        try:
            return ET.fromstring(f"<root>{content}</root>")
        except ET.ParseError:
            return None


def _parse_image_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[图片]"
    img = root.find(".//img")
    if img is not None:
        w = img.get("cdnthumbwidth", "?")
        h = img.get("cdnthumbheight", "?")
        return f"[图片 {w}x{h}]"
    return "[图片]"


def _parse_voice_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[语音]"
    voice = root.find(".//voicemsg")
    if voice is not None:
        length_ms = int(voice.get("voicelength", "0"))
        seconds = length_ms // 1000
        return f"[语音 {seconds}秒]"
    return "[语音]"


def _parse_video_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[视频]"
    video = root.find(".//videomsg")
    if video is not None:
        length = video.get("playlength", "?")
        return f"[视频 {length}秒]"
    return "[视频]"


def _parse_emoji_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[表情]"
    emoji = root.find(".//emoji")
    if emoji is not None:
        # productid 可以用来判断是否是自定义表情
        productid = emoji.get("productid", "")
        if productid:
            return "[表情包]"
        return "[表情]"
    return "[表情]"


def _parse_voip_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[通话]"
    # VOIP 消息的 XML 结构比较复杂
    invite_type = root.findtext(".//invitetype", "")
    if invite_type == "0":
        return "[语音通话]"
    elif invite_type == "1":
        return "[视频通话]"
    return "[音视频通话]"


def _parse_system_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        # 系统消息有时不是 XML 格式
        # 清理 HTML 标签
        clean = re.sub(r"<[^>]+>", "", content)
        return f"[系统] {clean.strip()}"
    revoke = root.findtext(".//revokemsg/content", "")
    if revoke:
        return f"[系统] {revoke}"
    sysmsg_content = root.findtext(".//content", "")
    if sysmsg_content:
        clean = re.sub(r"<[^>]+>", "", sysmsg_content)
        return f"[系统] {clean.strip()}"
    return "[系统消息]"


def _parse_appmsg_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[链接]"
    title = root.findtext(".//title", "")
    url = root.findtext(".//url", "")
    des = root.findtext(".//des", "")
    if title:
        result = f"[链接] {title}"
        if des:
            result += f" - {des[:50]}"
        return result
    return "[链接]"


def _parse_reply_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[引用回复]"
    title = root.findtext(".//title", "")
    if title:
        return title  # 引用回复的 title 就是回复内容
    return "[引用回复]"


def _parse_pat_xml(content: str) -> str:
    root = _safe_xml_parse(content)
    if root is None:
        return "[拍一拍]"
    template = root.findtext(".//pat/template", "")
    if template:
        # 清理 XML 标签
        clean = re.sub(r"<[^>]+>", "", template)
        return f"[拍一拍] {clean.strip()}"
    return "[拍一拍]"


# ============================================================================
# 消息方向解析
# ============================================================================

def _parse_direction(status: int, origin_source: int) -> str:
    """解析消息方向。

    Args:
        status: 2=自己发送, 3=收到
        origin_source: 1=自己, 2=对方

    Returns:
        "send" 或 "receive"
    """
    if status == 2 or origin_source == 1:
        return "send"
    return "receive"


# ============================================================================
# 时间解析
# ============================================================================

def _parse_time_param(value) -> Optional[int]:
    """解析时间参数（支持 Unix 时间戳或 ISO 格式字符串）。

    Args:
        value: int/float (Unix timestamp) 或 str (ISO format)

    Returns:
        Unix 时间戳 (int)，无效返回 None
    """
    if value is None:
        return None
    if isinstance(value, (int, float)):
        return int(value)
    if isinstance(value, str):
        value = value.strip()
        # 纯数字 → Unix 时间戳
        try:
            return int(value)
        except ValueError:
            pass
        # ISO 格式
        for fmt in ("%Y-%m-%d %H:%M:%S", "%Y-%m-%d %H:%M", "%Y-%m-%d",
                     "%Y/%m/%d %H:%M:%S", "%Y/%m/%d"):
            try:
                dt = datetime.strptime(value, fmt)
                return int(dt.timestamp())
            except ValueError:
                continue
    return None


# ============================================================================
# WeChatDBQuery 主类
# ============================================================================

class WeChatDBQuery:
    """微信数据库查询层 — P2.1/P2.2/P2.3 全部查询逻辑。

    依赖 WeChatDB (P2.0) 提供数据库连接。
    内部维护 username→nickname 缓存（线程安全）。
    """

    def __init__(self, wechat_db):
        """
        Args:
            wechat_db: WeChatDB 实例（已 ready）
        """
        self._db = wechat_db
        self._lock = threading.Lock()
        # username → nickname/remark 缓存
        self._nickname_cache: dict[str, str] = {}
        self._nickname_cache_time: float = 0
        self._NICKNAME_CACHE_TTL = 600  # 10 分钟

    # ===================================================================
    # P2.2 联系人查询 (contact.db)
    # ===================================================================

    def _ensure_nickname_cache(self):
        """确保 nickname 缓存已加载。"""
        now = time.time()
        if self._nickname_cache and (now - self._nickname_cache_time) < self._NICKNAME_CACHE_TTL:
            return

        try:
            conn = self._db.get_db_conn("contact.db")
            cursor = conn.execute(
                "SELECT username, nick_name, remark FROM contact"
            )
            cache = {}
            for username, nick_name, remark in cursor.fetchall():
                # 优先用备注名，没有则用昵称
                display = remark if remark else nick_name
                if display:
                    cache[username] = display
            with self._lock:
                self._nickname_cache = cache
                self._nickname_cache_time = now
            logger.info(f"nickname 缓存已加载: {len(cache)} 条")
        except Exception as e:
            logger.error(f"加载 nickname 缓存失败: {e}")

    def resolve_username_to_nickname(self, username: str) -> str:
        """P2.2: username → 昵称/备注名。

        优先使用缓存，缓存未命中则查 contact.db。

        Args:
            username: 微信 ID (wxid_xxx / gh_xxx / xxx@chatroom)

        Returns:
            显示名称（备注 > 昵称 > 原始 username）
        """
        if not username:
            return ""

        self._ensure_nickname_cache()

        with self._lock:
            if username in self._nickname_cache:
                return self._nickname_cache[username]

        # 缓存未命中，直接查 DB
        try:
            conn = self._db.get_db_conn("contact.db")
            cursor = conn.execute(
                "SELECT nick_name, remark FROM contact WHERE username = ? LIMIT 1",
                (username,)
            )
            row = cursor.fetchone()
            if row:
                nick_name, remark = row
                display = remark if remark else nick_name
                if display:
                    with self._lock:
                        self._nickname_cache[username] = display
                    return display
        except Exception as e:
            logger.debug(f"查询 username={username} 失败: {e}")

        return username

    def _resolve_nickname_to_username(self, nickname: str) -> Optional[str]:
        """昵称/备注 → username。

        用于消息查询时定位会话。先精确匹配，再模糊匹配。

        Args:
            nickname: 联系人昵称或备注

        Returns:
            username 或 None
        """
        try:
            conn = self._db.get_db_conn("contact.db")

            # 1. 精确匹配 remark
            cursor = conn.execute(
                "SELECT username FROM contact WHERE remark = ? LIMIT 1",
                (nickname,)
            )
            row = cursor.fetchone()
            if row:
                return row[0]

            # 2. 精确匹配 nick_name
            cursor = conn.execute(
                "SELECT username FROM contact WHERE nick_name = ? LIMIT 1",
                (nickname,)
            )
            row = cursor.fetchone()
            if row:
                return row[0]

            # 3. 模糊匹配 (nick_name / remark / alias / quan_pin / pin_yin_initial)
            cursor = conn.execute(
                """SELECT username FROM contact
                   WHERE nick_name LIKE ? OR remark LIKE ?
                         OR alias LIKE ? OR quan_pin LIKE ?
                         OR pin_yin_initial LIKE ?
                   LIMIT 1""",
                (f"%{nickname}%",) * 5
            )
            row = cursor.fetchone()
            if row:
                return row[0]

        except Exception as e:
            logger.error(f"昵称→username 查询失败 '{nickname}': {e}")

        return None

    def query_contacts(self, contact_type: Optional[str] = None,
                       keyword: Optional[str] = None,
                       limit: int = 100) -> list[dict]:
        """P2.2: 查询联系人列表。

        Args:
            contact_type: "contact"(个人) / "group"(群) / "biz"(公众号) / None(全部)
            keyword: 搜索关键词（匹配 nick_name/remark/alias/quan_pin/pin_yin_initial）
            limit: 最大返回数

        Returns:
            [{"username", "nick_name", "remark", "alias", "local_type",
              "verify_flag", "small_head_url"}, ...]
        """
        conn = self._db.get_db_conn("contact.db")

        # local_type 映射
        type_filter_map = {
            "contact": 3,    # 个人
            "group": 2,      # 群聊
            "biz": 1,        # 公众号
            "system": 0,     # 系统号
            "wecom": 5,      # 企业微信
            "openim": 6,     # OpenIM
        }

        sql = """SELECT username, nick_name, remark, alias, local_type,
                        verify_flag, small_head_url
                 FROM contact WHERE delete_flag = 0"""
        params = []

        if contact_type and contact_type in type_filter_map:
            sql += " AND local_type = ?"
            params.append(type_filter_map[contact_type])

        if keyword:
            sql += """ AND (nick_name LIKE ? OR remark LIKE ?
                        OR alias LIKE ? OR quan_pin LIKE ?
                        OR pin_yin_initial LIKE ?)"""
            kw = f"%{keyword}%"
            params.extend([kw, kw, kw, kw, kw])

        sql += " ORDER BY local_type, nick_name LIMIT ?"
        params.append(limit)

        cursor = conn.execute(sql, params)
        results = []
        for row in cursor.fetchall():
            results.append({
                "username": row[0],
                "nick_name": row[1] or "",
                "remark": row[2] or "",
                "alias": row[3] or "",
                "local_type": row[4],
                "verify_flag": row[5],
                "small_head_url": row[6] or "",
            })
        return results

    def query_chatrooms(self, limit: int = 100) -> list[dict]:
        """P2.2: 查询群聊列表。

        Returns:
            [{"username", "nick_name", "owner", "owner_nickname",
              "announcement", "member_count"}, ...]
        """
        conn_contact = self._db.get_db_conn("contact.db")

        cursor = conn_contact.execute("""
            SELECT cr.username, c.nick_name, cr.owner,
                   crid.announcement_,
                   (SELECT COUNT(*) FROM chatroom_member cm WHERE cm.room_id = cr.id) as member_count
            FROM chat_room cr
            LEFT JOIN contact c ON cr.username = c.username
            LEFT JOIN chat_room_info_detail crid ON cr.id = crid.room_id_
            ORDER BY member_count DESC
            LIMIT ?
        """, (limit,))

        results = []
        for row in cursor.fetchall():
            owner_nick = self.resolve_username_to_nickname(row[2]) if row[2] else ""
            results.append({
                "username": row[0],
                "nick_name": row[1] or row[0],
                "owner": row[2] or "",
                "owner_nickname": owner_nick,
                "announcement": row[3] or "",
                "member_count": row[4] or 0,
            })
        return results

    def query_chatroom_members(self, room_username: str) -> list[dict]:
        """P2.2: 查询群成员列表。

        Args:
            room_username: 群号 (xxx@chatroom)

        Returns:
            [{"username", "nick_name", "remark"}, ...]
        """
        conn = self._db.get_db_conn("contact.db")

        cursor = conn.execute("""
            SELECT c2.username, c2.nick_name, c2.remark
            FROM chatroom_member cm
            JOIN chat_room cr ON cm.room_id = cr.id
            JOIN contact c2 ON cm.member_id = c2.id
            WHERE cr.username = ?
            ORDER BY c2.nick_name
        """, (room_username,))

        results = []
        for row in cursor.fetchall():
            results.append({
                "username": row[0],
                "nick_name": row[1] or "",
                "remark": row[2] or "",
            })
        return results

    def query_contact_labels(self) -> list[dict]:
        """P2.2: 查询联系人标签列表。

        Returns:
            [{"label_id", "label_name", "sort_order"}, ...]
        """
        conn = self._db.get_db_conn("contact.db")
        cursor = conn.execute(
            "SELECT label_id_, label_name_, sort_order_ FROM contact_label ORDER BY sort_order_"
        )
        results = []
        for row in cursor.fetchall():
            results.append({
                "label_id": row[0],
                "label_name": row[1] or "",
                "sort_order": row[2],
            })
        return results

    # ===================================================================
    # P2.1 消息查询 (message_0.db)
    # ===================================================================

    def _get_msg_table_name(self, username: str) -> Optional[str]:
        """通过 username 计算消息表名 Msg_<MD5>。

        Args:
            username: 会话 username (wxid_xxx / xxx@chatroom)

        Returns:
            表名如 "Msg_ff44416f113e53b15c00bdbc5ffc2173"，不存在返回 None
        """
        md5_hash = hashlib.md5(username.encode("utf-8")).hexdigest()
        table_name = f"Msg_{md5_hash}"

        # 验证表是否存在
        try:
            conn = self._db.get_db_conn("message_0.db")
            cursor = conn.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name=?",
                (table_name,)
            )
            if cursor.fetchone():
                return table_name
        except Exception as e:
            logger.debug(f"验证消息表失败 {table_name}: {e}")

        return None

    def _resolve_sender(self, real_sender_id: int, msg_db_conn) -> str:
        """解析发送者: real_sender_id → Name2Id.user_name → contact.db 昵称。

        Args:
            real_sender_id: 消息的 real_sender_id 字段
            msg_db_conn: message_0.db 连接

        Returns:
            发送者显示名称
        """
        if real_sender_id is None or real_sender_id <= 0:
            return ""

        try:
            # Name2Id 的 rowid 就是 real_sender_id
            cursor = msg_db_conn.execute(
                "SELECT user_name FROM Name2Id WHERE rowid = ?",
                (real_sender_id,)
            )
            row = cursor.fetchone()
            if row and row[0]:
                username = row[0]
                return self.resolve_username_to_nickname(username)
        except Exception as e:
            logger.debug(f"解析发送者 {real_sender_id} 失败: {e}")

        return f"user_{real_sender_id}"

    def query_messages(self, contact_name: str, limit: int = 50,
                       before_time=None, after_time=None,
                       keyword: Optional[str] = None) -> list[dict]:
        """P2.1: 查询聊天消息。

        流程:
          1. contact_name → contact.db 查 username
          2. username MD5 → Msg_<hash> 表名
          3. 查询消息 + 解析类型/内容/发送者

        Args:
            contact_name: 联系人昵称/备注名
            limit: 最大返回条数 (默认 50)
            before_time: 早于此时间的消息 (Unix ts 或 ISO str)
            after_time: 晚于此时间的消息 (Unix ts 或 ISO str)
            keyword: 内容关键词过滤

        Returns:
            [{"sender", "type", "timestamp", "direction", "content",
              "create_time_str"}, ...]
            按 create_time 升序（最旧到最新）
        """
        # 1. 定位 username
        username = self._resolve_nickname_to_username(contact_name)
        if not username:
            raise ValueError(f"找不到联系人: '{contact_name}'")

        # 2. 定位消息表
        table_name = self._get_msg_table_name(username)
        if not table_name:
            raise ValueError(
                f"联系人 '{contact_name}' (username={username}) 没有消息记录"
            )

        # 3. 构建查询
        conn = self._db.get_db_conn("message_0.db")

        sql = f"""SELECT local_id, local_type, real_sender_id, create_time,
                         status, origin_source, message_content,
                         WCDB_CT_message_content
                  FROM [{table_name}] WHERE 1=1"""
        params = []

        before_ts = _parse_time_param(before_time)
        after_ts = _parse_time_param(after_time)

        if before_ts is not None:
            sql += " AND create_time < ?"
            params.append(before_ts)
        if after_ts is not None:
            sql += " AND create_time > ?"
            params.append(after_ts)

        # keyword 过滤：只对 CT=0（明文）做 LIKE，CT=4 需要解压后才能过滤
        # 这里先不在 SQL 层过滤 keyword，而是在 Python 层过滤
        sql += " ORDER BY create_time DESC LIMIT ?"
        # 如果有 keyword，多查一些以便过滤后还有足够结果
        query_limit = limit * 3 if keyword else limit
        params.append(query_limit)

        cursor = conn.execute(sql, params)
        rows = cursor.fetchall()

        # 4. 解析结果
        results = []
        for row in rows:
            local_id, local_type, real_sender_id, create_time, \
                status, origin_source, message_content, wcdb_ct = row

            # 解压内容
            if wcdb_ct == 4 and isinstance(message_content, (bytes, bytearray)):
                content_str = _decompress_wcdb(message_content)
            elif isinstance(message_content, (bytes, bytearray)):
                content_str = message_content.decode("utf-8", errors="replace")
            elif message_content is not None:
                content_str = str(message_content)
            else:
                content_str = ""

            # 解析消息内容
            parsed_content = _parse_message_content(local_type, content_str)

            # keyword 过滤
            if keyword and keyword.lower() not in parsed_content.lower():
                continue

            # 解析发送者
            sender = self._resolve_sender(real_sender_id, conn)

            # 解析方向
            direction = _parse_direction(status or 0, origin_source or 0)

            # 时间格式化
            ts_str = ""
            if create_time:
                try:
                    ts_str = datetime.fromtimestamp(create_time).strftime("%Y-%m-%d %H:%M:%S")
                except (ValueError, OSError):
                    ts_str = str(create_time)

            results.append({
                "sender": sender,
                "type": _parse_message_type(local_type),
                "timestamp": create_time or 0,
                "create_time_str": ts_str,
                "direction": direction,
                "content": parsed_content,
            })

            if len(results) >= limit:
                break

        # 返回时按时间升序（最旧到最新，方便阅读）
        results.reverse()
        return results

    def query_all_msg_tables(self) -> list[dict]:
        """P2.1: 列出所有消息表及消息数（调试用）。

        Returns:
            [{"table_name", "message_count"}, ...]
        """
        conn = self._db.get_db_conn("message_0.db")
        cursor = conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'Msg_%'"
        )
        tables = [row[0] for row in cursor.fetchall()]

        results = []
        for table in tables:
            try:
                cnt = conn.execute(f"SELECT COUNT(*) FROM [{table}]").fetchone()[0]
                results.append({"table_name": table, "message_count": cnt})
            except Exception:
                results.append({"table_name": table, "message_count": -1})

        results.sort(key=lambda x: x["message_count"], reverse=True)
        return results

    # ===================================================================
    # P2.3 会话查询 (session.db)
    # ===================================================================

    def query_sessions(self, limit: int = 50) -> list[dict]:
        """P2.3: 查询会话列表。

        按 sort_timestamp 降序（最近活跃的会话在前）。
        JOIN contact.db 获取昵称。

        Returns:
            [{"username", "nick_name", "unread_count", "summary",
              "last_timestamp", "last_time_str", "draft"}, ...]
        """
        conn = self._db.get_db_conn("session.db")

        cursor = conn.execute("""
            SELECT username, unread_count, summary, last_timestamp,
                   sort_timestamp, draft
            FROM SessionTable
            ORDER BY sort_timestamp DESC
            LIMIT ?
        """, (limit,))

        results = []
        for row in cursor.fetchall():
            username, unread_count, summary, last_ts, sort_ts, draft = row

            # 通过 contact.db 解析昵称
            nick_name = self.resolve_username_to_nickname(username)

            # 时间格式化
            ts_str = ""
            if last_ts:
                try:
                    ts_str = datetime.fromtimestamp(last_ts).strftime("%Y-%m-%d %H:%M:%S")
                except (ValueError, OSError):
                    ts_str = str(last_ts)

            results.append({
                "username": username,
                "nick_name": nick_name,
                "unread_count": unread_count or 0,
                "summary": summary or "",
                "last_timestamp": last_ts or 0,
                "last_time_str": ts_str,
                "draft": draft or "",
            })

        return results

    def query_unread_sessions(self) -> list[dict]:
        """P2.3: 查询所有有未读消息的会话。

        Agent 场景核心需求："谁给我发了未读消息"。

        Returns:
            [{"username", "nick_name", "unread_count", "summary",
              "last_timestamp", "last_time_str"}, ...]
            按 unread_count 降序。
        """
        conn = self._db.get_db_conn("session.db")

        cursor = conn.execute("""
            SELECT username, unread_count, summary, last_timestamp
            FROM SessionTable
            WHERE unread_count > 0
            ORDER BY unread_count DESC
        """)

        results = []
        for row in cursor.fetchall():
            username, unread_count, summary, last_ts = row

            nick_name = self.resolve_username_to_nickname(username)

            ts_str = ""
            if last_ts:
                try:
                    ts_str = datetime.fromtimestamp(last_ts).strftime("%Y-%m-%d %H:%M:%S")
                except (ValueError, OSError):
                    ts_str = str(last_ts)

            results.append({
                "username": username,
                "nick_name": nick_name,
                "unread_count": unread_count or 0,
                "summary": summary or "",
                "last_timestamp": last_ts or 0,
                "last_time_str": ts_str,
            })

        return results


# ============================================================================
# 全局单例（绑定到 WeChatDB 单例）
# ============================================================================

_query_instance: Optional[WeChatDBQuery] = None
_query_lock = threading.Lock()


def get_wechat_db_query() -> WeChatDBQuery:
    """获取 WeChatDBQuery 全局单例。

    依赖 WeChatDB 已 ready。
    """
    global _query_instance
    if _query_instance is None:
        with _query_lock:
            if _query_instance is None:
                from wechat_db import get_wechat_db
                db = get_wechat_db()
                _query_instance = WeChatDBQuery(db)
    return _query_instance
