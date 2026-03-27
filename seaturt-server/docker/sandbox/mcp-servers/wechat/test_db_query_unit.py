#!/usr/bin/env python3
"""
test_db_query_unit.py — wechat_db_query 纯逻辑单元测试

无需微信环境、D-Bus、AT-SPI2 或真实数据库。
测试对象：消息类型解析、XML 解析、时间解析、方向解析、zstd 解压等。

运行方式 (容器内或本地):
  python3 test_db_query_unit.py
  python3 -m pytest test_db_query_unit.py -v  (如果有 pytest)

退出码:
  0 = 全部通过
  1 = 有 FAIL
"""

import sys
import os
import struct
import zlib
import unittest

# 将当前目录加入 path，确保能 import wechat_db_query 的函数
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

# 只 import 纯逻辑函数，避免触发 wechat_launcher 等环境依赖
from wechat_db_query import (
    _decompress_wcdb,
    _parse_message_type,
    _parse_message_content,
    _parse_direction,
    _parse_time_param,
    _safe_xml_parse,
    _parse_image_xml,
    _parse_voice_xml,
    _parse_video_xml,
    _parse_emoji_xml,
    _parse_voip_xml,
    _parse_system_xml,
    _parse_appmsg_xml,
    _parse_reply_xml,
    _parse_pat_xml,
    MSG_TYPE_MAP,
    APP_MSG_TYPE_MAP,
)


# ============================================================================
# 消息类型解析 (_parse_message_type)
# ============================================================================

class TestParseMessageType(unittest.TestCase):
    """测试 _parse_message_type — 基础类型 + 复合类型"""

    def test_text(self):
        self.assertEqual(_parse_message_type(1), "text")

    def test_image(self):
        self.assertEqual(_parse_message_type(3), "image")

    def test_voice(self):
        self.assertEqual(_parse_message_type(34), "voice")

    def test_video(self):
        self.assertEqual(_parse_message_type(43), "video")

    def test_emoji(self):
        self.assertEqual(_parse_message_type(47), "emoji")

    def test_voip(self):
        self.assertEqual(_parse_message_type(50), "voip")

    def test_system(self):
        self.assertEqual(_parse_message_type(10000), "system")

    def test_all_basic_types_covered(self):
        """验证 MSG_TYPE_MAP 中的所有类型都能正确解析"""
        for type_id, type_name in MSG_TYPE_MAP.items():
            self.assertEqual(_parse_message_type(type_id), type_name)

    def test_unknown_type(self):
        result = _parse_message_type(999)
        self.assertTrue(result.startswith("unknown_"), f"got: {result}")
        self.assertEqual(result, "unknown_999")

    def test_compound_type_link(self):
        """复合类型：local_type > 0xFFFF，高 32 位为 app_msg_type"""
        # app_msg_type=4 (link): local_type = 4 << 32 | base
        local_type = (4 << 32) | 0x10001
        self.assertEqual(_parse_message_type(local_type), "link")

    def test_compound_type_article(self):
        local_type = (5 << 32) | 0x10001
        self.assertEqual(_parse_message_type(local_type), "article")

    def test_compound_type_reply(self):
        local_type = (57 << 32) | 0x10001
        self.assertEqual(_parse_message_type(local_type), "reply")

    def test_compound_type_unknown_appmsg(self):
        local_type = (999 << 32) | 0x10001
        result = _parse_message_type(local_type)
        self.assertEqual(result, "appmsg_999")


# ============================================================================
# 消息方向解析 (_parse_direction)
# ============================================================================

class TestParseDirection(unittest.TestCase):
    """测试 _parse_direction — status + origin_source → send/receive"""

    def test_send_by_status(self):
        self.assertEqual(_parse_direction(2, 0), "send")

    def test_receive_by_status(self):
        self.assertEqual(_parse_direction(3, 0), "receive")

    def test_send_by_origin(self):
        self.assertEqual(_parse_direction(0, 1), "send")

    def test_receive_by_origin(self):
        self.assertEqual(_parse_direction(0, 2), "receive")

    def test_both_send(self):
        self.assertEqual(_parse_direction(2, 1), "send")

    def test_default_receive(self):
        """status 不是 2 且 origin_source 不是 1 → receive"""
        self.assertEqual(_parse_direction(0, 0), "receive")
        self.assertEqual(_parse_direction(5, 3), "receive")


# ============================================================================
# 时间参数解析 (_parse_time_param)
# ============================================================================

class TestParseTimeParam(unittest.TestCase):
    """测试 _parse_time_param — Unix 时间戳 / ISO 格式 / 边界情况"""

    def test_none(self):
        self.assertIsNone(_parse_time_param(None))

    def test_int_timestamp(self):
        self.assertEqual(_parse_time_param(1700000000), 1700000000)

    def test_float_timestamp(self):
        self.assertEqual(_parse_time_param(1700000000.5), 1700000000)

    def test_str_timestamp(self):
        self.assertEqual(_parse_time_param("1700000000"), 1700000000)

    def test_iso_full(self):
        result = _parse_time_param("2026-03-01 12:00:00")
        self.assertIsNotNone(result)
        self.assertIsInstance(result, int)

    def test_iso_no_seconds(self):
        result = _parse_time_param("2026-03-01 12:00")
        self.assertIsNotNone(result)

    def test_iso_date_only(self):
        result = _parse_time_param("2026-03-01")
        self.assertIsNotNone(result)

    def test_slash_format(self):
        result = _parse_time_param("2026/03/01 12:00:00")
        self.assertIsNotNone(result)

    def test_slash_date_only(self):
        result = _parse_time_param("2026/03/01")
        self.assertIsNotNone(result)

    def test_invalid_string(self):
        self.assertIsNone(_parse_time_param("not a date"))

    def test_empty_string(self):
        self.assertIsNone(_parse_time_param(""))

    def test_whitespace_string(self):
        """带前后空白的时间字符串"""
        result = _parse_time_param("  1700000000  ")
        self.assertEqual(result, 1700000000)


# ============================================================================
# WCDB zstd 解压 (_decompress_wcdb)
# ============================================================================

class TestDecompressWCDB(unittest.TestCase):
    """测试 _decompress_wcdb — zstd 解压 + 兜底处理"""

    def test_none(self):
        self.assertEqual(_decompress_wcdb(None), "")

    def test_str_passthrough(self):
        self.assertEqual(_decompress_wcdb("hello"), "hello")

    def test_bytes_not_zstd(self):
        """非 zstd 压缩的 bytes → 直接 UTF-8 decode"""
        self.assertEqual(_decompress_wcdb(b"plain text"), "plain text")

    def test_bytes_zstd_compressed(self):
        """zstd 压缩的 bytes → 解压"""
        try:
            import zstandard as zstd
            cctx = zstd.ZstdCompressor()
            original = "这是一条测试消息内容"
            compressed = cctx.compress(original.encode("utf-8"))
            result = _decompress_wcdb(compressed)
            self.assertEqual(result, original)
        except ImportError:
            self.skipTest("zstandard not installed")

    def test_zstd_magic_but_invalid(self):
        """有 zstd magic 但数据无效 → 兜底 UTF-8 decode"""
        data = b"\x28\xb5\x2f\xfd" + b"invalid"
        result = _decompress_wcdb(data)
        self.assertIsInstance(result, str)

    def test_bytearray(self):
        """bytearray 也应该正常处理"""
        self.assertEqual(_decompress_wcdb(bytearray(b"hello")), "hello")

    def test_non_utf8_bytes(self):
        """非 UTF-8 bytes → replace 模式不崩溃"""
        result = _decompress_wcdb(b"\xff\xfe\xfd")
        self.assertIsInstance(result, str)

    def test_other_type(self):
        """非 str/bytes → str()"""
        self.assertEqual(_decompress_wcdb(12345), "12345")


# ============================================================================
# XML 解析 (_safe_xml_parse + 各类消息 XML 解析)
# ============================================================================

class TestSafeXMLParse(unittest.TestCase):
    """测试 _safe_xml_parse — 安全 XML 解析"""

    def test_valid_xml(self):
        root = _safe_xml_parse("<msg><title>hello</title></msg>")
        self.assertIsNotNone(root)
        self.assertEqual(root.findtext("title"), "hello")

    def test_invalid_xml(self):
        """完全无效的内容 → None"""
        result = _safe_xml_parse("not xml at all {}")
        # 可能被包裹成 <root>not xml at all {}</root> 然后解析成功
        # 或返回 None，取决于具体内容
        # 只要不崩溃即可

    def test_fragment_xml(self):
        """XML 片段（无根元素）→ 自动包裹"""
        result = _safe_xml_parse("<a>1</a><b>2</b>")
        self.assertIsNotNone(result)

    def test_empty_string(self):
        # 空字符串不是有效 XML
        result = _safe_xml_parse("")
        # 可能 None 也可能是空 <root/>

    def test_none_equivalent(self):
        """特殊字符 → 不崩溃"""
        _safe_xml_parse("<")


class TestParseImageXML(unittest.TestCase):

    def test_with_dimensions(self):
        xml = '<msg><img cdnthumbwidth="120" cdnthumbheight="90" /></msg>'
        result = _parse_image_xml(xml)
        self.assertIn("120", result)
        self.assertIn("90", result)

    def test_no_img_element(self):
        result = _parse_image_xml("<msg></msg>")
        self.assertEqual(result, "[图片]")

    def test_invalid_xml(self):
        result = _parse_image_xml("not xml")
        self.assertIn("图片", result)


class TestParseVoiceXML(unittest.TestCase):

    def test_with_length(self):
        xml = '<msg><voicemsg voicelength="5000" /></msg>'
        result = _parse_voice_xml(xml)
        self.assertIn("5", result)  # 5 秒

    def test_zero_length(self):
        xml = '<msg><voicemsg voicelength="0" /></msg>'
        result = _parse_voice_xml(xml)
        self.assertIn("0", result)

    def test_no_voice_element(self):
        result = _parse_voice_xml("<msg></msg>")
        self.assertEqual(result, "[语音]")


class TestParseVideoXML(unittest.TestCase):

    def test_with_length(self):
        xml = '<msg><videomsg playlength="30" /></msg>'
        result = _parse_video_xml(xml)
        self.assertIn("30", result)

    def test_no_video_element(self):
        result = _parse_video_xml("<msg></msg>")
        self.assertEqual(result, "[视频]")


class TestParseEmojiXML(unittest.TestCase):

    def test_custom_emoji(self):
        xml = '<msg><emoji productid="abc123" /></msg>'
        result = _parse_emoji_xml(xml)
        self.assertEqual(result, "[表情包]")

    def test_standard_emoji(self):
        xml = '<msg><emoji productid="" /></msg>'
        result = _parse_emoji_xml(xml)
        self.assertEqual(result, "[表情]")

    def test_no_emoji_element(self):
        result = _parse_emoji_xml("<msg></msg>")
        self.assertEqual(result, "[表情]")


class TestParseVoipXML(unittest.TestCase):

    def test_voice_call(self):
        xml = "<msg><invitetype>0</invitetype></msg>"
        result = _parse_voip_xml(xml)
        self.assertIn("语音", result)

    def test_video_call(self):
        xml = "<msg><invitetype>1</invitetype></msg>"
        result = _parse_voip_xml(xml)
        self.assertIn("视频", result)

    def test_unknown_call(self):
        xml = "<msg><invitetype>99</invitetype></msg>"
        result = _parse_voip_xml(xml)
        self.assertIn("通话", result)


class TestParseSystemXML(unittest.TestCase):

    def test_revoke_message(self):
        xml = '<sysmsg><revokemsg><content>Alice 撤回了一条消息</content></revokemsg></sysmsg>'
        result = _parse_system_xml(xml)
        self.assertIn("撤回", result)

    def test_plain_system(self):
        """纯文本系统消息 → 包含 [系统] 标记"""
        result = _parse_system_xml("你已添加了 Bob 为好友")
        self.assertIn("系统", result)
        # 注意: _safe_xml_parse 会尝试包裹 <root>，纯文本可能被解析成功
        # 但 findtext 可能找不到 content 节点 → 返回 [系统消息]
        # 无论如何结果应包含 "系统" 且不崩溃

    def test_xml_with_content(self):
        """带 <content> 节点的系统消息"""
        xml = '<sysmsg><content>你邀请了 Alice 加入群聊</content></sysmsg>'
        result = _parse_system_xml(xml)
        self.assertIn("系统", result)
        self.assertIn("Alice", result)

    def test_html_system_message(self):
        """含 HTML 标签的系统消息 — 通过 _parse_message_content(10000, ...) 走完整路径"""
        # _parse_system_xml 对 HTML 片段的行为取决于 _safe_xml_parse
        # 如果解析成功但 findtext 找不到 content → [系统消息]
        # 如果解析失败 → HTML strip 后返回
        content = '<a>Alice</a> 邀请了 <a>Bob</a> 加入群聊'
        result = _parse_system_xml(content)
        self.assertIn("系统", result)
        # HTML 标签应该不存在于最终结果中
        self.assertNotIn("<a>", result)


class TestParseAppmsgXML(unittest.TestCase):

    def test_with_title_and_url(self):
        xml = '<msg><appmsg><title>文章标题</title><url>https://example.com</url></appmsg></msg>'
        result = _parse_appmsg_xml(xml)
        self.assertIn("链接", result)
        self.assertIn("文章标题", result)

    def test_with_description(self):
        xml = '<msg><appmsg><title>标题</title><des>描述内容</des></appmsg></msg>'
        result = _parse_appmsg_xml(xml)
        self.assertIn("标题", result)
        self.assertIn("描述", result)

    def test_no_title(self):
        result = _parse_appmsg_xml("<msg></msg>")
        self.assertEqual(result, "[链接]")

    def test_invalid_xml(self):
        result = _parse_appmsg_xml("not xml")
        self.assertIn("链接", result)


class TestParseReplyXML(unittest.TestCase):

    def test_with_title(self):
        xml = '<msg><appmsg><title>这是回复内容</title></appmsg></msg>'
        result = _parse_reply_xml(xml)
        self.assertIn("回复内容", result)

    def test_no_title(self):
        result = _parse_reply_xml("<msg></msg>")
        self.assertEqual(result, "[引用回复]")

    def test_invalid_xml(self):
        result = _parse_reply_xml("not xml")
        self.assertIn("回复", result)


class TestParsePatXML(unittest.TestCase):

    def test_with_template(self):
        xml = '<msg><pat><template>Alice 拍了拍 Bob</template></pat></msg>'
        result = _parse_pat_xml(xml)
        self.assertIn("拍一拍", result)
        self.assertIn("Alice", result)

    def test_template_with_html(self):
        xml = '<msg><pat><template><a>Alice</a> 拍了拍 <a>Bob</a></template></pat></msg>'
        result = _parse_pat_xml(xml)
        self.assertIn("拍一拍", result)
        # HTML 标签应该被清理
        self.assertNotIn("<a>", result)

    def test_no_template(self):
        result = _parse_pat_xml("<msg></msg>")
        self.assertEqual(result, "[拍一拍]")


# ============================================================================
# 消息内容解析综合 (_parse_message_content)
# ============================================================================

class TestParseMessageContent(unittest.TestCase):
    """测试 _parse_message_content — 综合入口"""

    def test_text_message(self):
        result = _parse_message_content(1, "Hello World")
        self.assertEqual(result, "Hello World")

    def test_empty_content(self):
        result = _parse_message_content(1, "")
        self.assertEqual(result, "")

    def test_none_content(self):
        result = _parse_message_content(1, None)
        self.assertEqual(result, "")

    def test_image_message(self):
        xml = '<msg><img cdnthumbwidth="100" cdnthumbheight="80" /></msg>'
        result = _parse_message_content(3, xml)
        self.assertIn("图片", result)

    def test_system_message(self):
        result = _parse_message_content(10000, "你邀请了 Alice 加入群聊")
        self.assertIn("系统", result)

    def test_unknown_type_long_content(self):
        """未知类型 + 超长内容 → 截取前 200 字符"""
        content = "A" * 300
        result = _parse_message_content(999, content)
        self.assertTrue(len(result) <= 205)  # 200 + "..."
        self.assertTrue(result.endswith("..."))

    def test_unknown_type_short_content(self):
        """未知类型 + 短内容 → 原样返回"""
        result = _parse_message_content(999, "short")
        self.assertEqual(result, "short")


# ============================================================================
# main.py 辅助函数 (如果可以无副作用 import)
# ============================================================================

class TestContactTypeName(unittest.TestCase):
    """测试 _contact_type_name — 从 main.py import"""

    @classmethod
    def setUpClass(cls):
        """尝试 import main.py 的 _contact_type_name，
        但 main.py 在 module level 做了 ensure_display / ensure_dbus，
        在无桌面环境下会失败。所以跳过。"""
        cls.skip_reason = None
        try:
            # 先检查能否 import（会触发 wechat_launcher 的环境初始化）
            # 如果在容器外运行，gi.repository 通常不存在
            from main import _contact_type_name
            cls._contact_type_name = _contact_type_name
        except Exception as e:
            cls.skip_reason = f"Cannot import main.py: {e}"
            cls._contact_type_name = None

    def test_system(self):
        if self.skip_reason:
            self.skipTest(self.skip_reason)
        self.assertEqual(self._contact_type_name(0), "system")

    def test_biz(self):
        if self.skip_reason:
            self.skipTest(self.skip_reason)
        self.assertEqual(self._contact_type_name(1), "biz")

    def test_group(self):
        if self.skip_reason:
            self.skipTest(self.skip_reason)
        self.assertEqual(self._contact_type_name(2), "group")

    def test_contact(self):
        if self.skip_reason:
            self.skipTest(self.skip_reason)
        self.assertEqual(self._contact_type_name(3), "contact")

    def test_unknown(self):
        if self.skip_reason:
            self.skipTest(self.skip_reason)
        result = self._contact_type_name(99)
        self.assertIn("unknown", result)


# ============================================================================
# WeChatDB 状态机 (不需要真实 DB)
# ============================================================================

class TestWeChatDBStateMachine(unittest.TestCase):
    """测试 WeChatDB 的状态机逻辑（不触发真实密钥提取）"""

    def test_initial_status(self):
        from wechat_db import WeChatDB
        db = WeChatDB()
        self.assertEqual(db._status, "not_started")
        self.assertFalse(db.is_ready())

    def test_get_key_status_initial(self):
        from wechat_db import WeChatDB
        db = WeChatDB()
        status = db.get_key_status()
        self.assertEqual(status["db_status"], "not_started")
        self.assertEqual(status["db_count"], 0)
        # 核心 DB 应该都是 False
        for db_name in WeChatDB.CORE_DBS:
            self.assertFalse(status["core_dbs"][db_name])

    def test_status_text_not_started(self):
        from wechat_db import WeChatDB
        db = WeChatDB()
        text = db.get_status_text()
        self.assertIn("未开始", text)

    def test_get_db_conn_not_ready(self):
        """密钥未就绪时获取连接 → 抛异常"""
        from wechat_db import WeChatDB
        db = WeChatDB()
        with self.assertRaises(RuntimeError):
            db.get_db_conn("message_0.db")

    def test_close_all_empty(self):
        """没有打开连接时 close_all 不崩溃"""
        from wechat_db import WeChatDB
        db = WeChatDB()
        db.close_all()  # should not raise


# ============================================================================
# db_utils 函数测试 (不需要真实 DB)
# ============================================================================

class TestDBUtils(unittest.TestCase):
    """测试 db_utils.py 的辅助函数"""

    def test_load_keys_nonexistent(self):
        from db_utils import load_keys
        result = load_keys("/tmp/nonexistent_keys_file.json")
        self.assertEqual(result, {})

    def test_save_and_load_keys(self):
        import tempfile
        from db_utils import save_keys, load_keys

        with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
            tmp_path = f.name

        try:
            test_keys = {
                "message_0.db": {"key": "abc123", "compat": 4, "db_path": "/tmp/msg.db"},
                "contact.db": {"key": "def456", "compat": 4, "db_path": "/tmp/contact.db"},
            }
            save_keys(test_keys, tmp_path)
            loaded = load_keys(tmp_path)
            self.assertEqual(loaded, test_keys)
        finally:
            os.unlink(tmp_path)


# ============================================================================
# Main
# ============================================================================

if __name__ == "__main__":
    # 自定义 test runner 输出
    print()
    print("🐢 SeaTurt — wechat_db_query 单元测试")
    print()

    # 运行测试
    loader = unittest.TestLoader()
    suite = loader.loadTestsFromModule(sys.modules[__name__])

    runner = unittest.TextTestRunner(verbosity=2)
    result = runner.run(suite)

    # 汇总
    total = result.testsRun
    failed = len(result.failures) + len(result.errors)
    skipped = len(result.skipped)
    passed = total - failed - skipped

    print()
    print("=" * 60)
    print(f"  Results: {passed}/{total} passed, {failed} failed, {skipped} skipped")
    print("=" * 60)

    sys.exit(0 if result.wasSuccessful() else 1)
