#!/usr/bin/env python3
"""
test_send.py — P1 阶段验证脚本

验证 P1.1-P1.4 的完整功能：
  T1: 联系人列表（缓存 + 类型解析 + 模糊搜索）
  T2: 发送文本消息（频率限制 + 随机延迟）
  T3: 发送图片
  T4: 发送文件
  T5: 验证消息确认

运行方式:
  docker exec -it <container> python3 /opt/mcp-wechat/test_send.py
  docker exec -it <container> python3 /opt/mcp-wechat/test_send.py --contact "文件传输助手"
  docker exec -it <container> python3 /opt/mcp-wechat/test_send.py --skip-file  # 跳过文件发送测试
  docker exec -it <container> python3 /opt/mcp-wechat/test_send.py --contacts-only  # 只测试联系人

退出码:
  0 = 全部通过
  1 = 有 FAIL
  2 = 环境异常（微信未运行/未登录等）
"""

import argparse
import logging
import os
import sys
import tempfile
import time
import traceback

# ---------------------------------------------------------------------------
# 0) Bootstrap environment（和 main.py 一致的引导流程）
# ---------------------------------------------------------------------------

from wechat_launcher import (
    ensure_display,
    ensure_dbus,
    ensure_atspi,
    find_wechat_bin,
    is_wechat_running,
    launch_wechat,
)

ensure_display()
ensure_dbus()

from wechat_ui import WeChatUI

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=logging.DEBUG,
    format="[test-send] %(asctime)s %(levelname)-5s %(message)s",
    datefmt="%H:%M:%S",
    stream=sys.stderr,
)
logger = logging.getLogger("test-send")

# ---------------------------------------------------------------------------
# Test runner
# ---------------------------------------------------------------------------

_results: list[tuple[str, bool, str]] = []  # (name, passed, detail)


def _record(name: str, passed: bool, detail: str = ""):
    tag = "PASS ✅" if passed else "FAIL ❌"
    logger.info(f"  [{tag}] {name}" + (f" — {detail}" if detail else ""))
    _results.append((name, passed, detail))


def _summary() -> int:
    """打印汇总并返回退出码。"""
    total = len(_results)
    passed = sum(1 for _, p, _ in _results if p)
    failed = total - passed

    print()
    print("=" * 60)
    print(f"  Test Results: {passed}/{total} passed, {failed} failed")
    print("=" * 60)
    for name, ok, detail in _results:
        icon = "✅" if ok else "❌"
        line = f"  {icon} {name}"
        if detail:
            line += f"  ({detail})"
        print(line)
    print("=" * 60)

    return 0 if failed == 0 else 1


# ---------------------------------------------------------------------------
# Environment pre-check
# ---------------------------------------------------------------------------


def check_environment() -> bool:
    """检查微信运行环境并确认已登录。"""
    logger.info("=== 环境检查 ===")

    display = os.environ.get("DISPLAY", "")
    _record("DISPLAY 已设置", bool(display), display)

    dbus = os.environ.get("DBUS_SESSION_BUS_ADDRESS", "")
    _record("DBUS 已设置", bool(dbus), dbus[:60] if dbus else "")

    ensure_atspi()
    running = is_wechat_running()
    _record("微信运行中", running)

    if not running:
        logger.warning("微信未运行，尝试启动...")
        wechat_bin = find_wechat_bin()
        if wechat_bin:
            proc = launch_wechat(foreground=False)
            if proc:
                time.sleep(8)
                running = is_wechat_running()
                _record("微信启动成功", running)
        if not running:
            return False

    ui = WeChatUI()
    found = ui.find_wechat()
    _record("AT-SPI2 发现微信", found)
    if not found:
        return False

    status = ui.get_status()
    _record("微信已登录", status.get("logged_in", False))
    if not status.get("logged_in"):
        logger.error("微信未登录，请先扫码登录")
        return False

    return True


# ---------------------------------------------------------------------------
# T1: 联系人列表
# ---------------------------------------------------------------------------


def test_contacts(ui: WeChatUI):
    """T1: 联系人列表（缓存 + 类型解析 + 模糊搜索）"""
    logger.info("=== T1: 联系人列表 ===")

    # 1. 获取通讯录联系人
    contacts = ui.get_contacts(force_refresh=True)
    _record("get_contacts() 返回数据", len(contacts) > 0, f"{len(contacts)} 个联系人")

    if contacts:
        # 2. 类型解析
        types = {}
        for c in contacts:
            t = c.get("type", "unknown")
            types[t] = types.get(t, 0) + 1
        _record("联系人类型解析", True,
                ", ".join(f"{k}={v}" for k, v in sorted(types.items())))

        # 打印前 10 个
        for i, c in enumerate(contacts[:10]):
            logger.info(f"  联系人 {i}: name='{c['name']}', type='{c.get('type')}'")

    # 3. 缓存验证：第二次调用应该走缓存
    t0 = time.time()
    contacts2 = ui.get_contacts()
    t1 = time.time()
    cache_fast = (t1 - t0) < 0.1  # 缓存应该非常快
    _record("联系人缓存生效", cache_fast and len(contacts2) == len(contacts),
            f"耗时 {(t1 - t0)*1000:.0f}ms, {len(contacts2)} 个")

    # 4. 模糊搜索
    search_results = ui.fuzzy_search_contact("文件")
    _record("模糊搜索 '文件'", len(search_results) > 0,
            f"{len(search_results)} 个匹配")
    if search_results:
        for r in search_results[:5]:
            logger.info(f"  搜索结果: '{r['name']}' ({r.get('type')})")

    # 5. 聊天列表
    chats = ui.get_chat_list()
    _record("get_chat_list() 返回数据", len(chats) > 0, f"{len(chats)} 个聊天")

    return contacts


# ---------------------------------------------------------------------------
# T2: 发送文本消息
# ---------------------------------------------------------------------------


def test_send_text(ui: WeChatUI, contact: str):
    """T2: 发送文本消息（频率限制 + 随机延迟）"""
    logger.info(f"=== T2: 发送文本消息到 '{contact}' ===")

    text = f"🐢 SeaTurt P1 test @ {time.strftime('%H:%M:%S')}"

    result = ui.send_msg(contact, text)
    _record(f"send_msg('{contact}', ...) 成功",
            result["success"],
            result.get("error") or "OK")

    if result["success"]:
        time.sleep(2)
        # 验证消息出现
        messages = ui.get_messages_text_only()
        found = any(text in m["content"] for m in messages)
        _record("消息出现在消息列表", found,
                f"最近: {[m['content'][:40] for m in messages[-3:]]}" if messages else "")


# ---------------------------------------------------------------------------
# T3: 发送图片
# ---------------------------------------------------------------------------


def test_send_image(ui: WeChatUI, contact: str):
    """T3: 发送图片"""
    logger.info(f"=== T3: 发送图片到 '{contact}' ===")

    # 创建测试图片（一个简单的 1x1 PNG）
    # PNG 最小有效文件：8 字节签名 + IHDR + IDAT + IEND
    import struct
    import zlib

    def make_minimal_png() -> bytes:
        """生成一个 2x2 红色 PNG 图片"""
        sig = b'\x89PNG\r\n\x1a\n'

        def chunk(ctype, data):
            c = ctype + data
            return struct.pack('>I', len(data)) + c + struct.pack('>I', zlib.crc32(c) & 0xFFFFFFFF)

        ihdr = chunk(b'IHDR', struct.pack('>IIBBBBB', 2, 2, 8, 2, 0, 0, 0))  # 2x2 RGB
        # 2 行，每行: filter_byte(0) + R G B R G B
        raw = b'\x00\xff\x00\x00\xff\x00\x00' * 2
        idat = chunk(b'IDAT', zlib.compress(raw))
        iend = chunk(b'IEND', b'')
        return sig + ihdr + idat + iend

    # 写入临时文件
    tmp_dir = "/tmp/seaturt-test"
    os.makedirs(tmp_dir, exist_ok=True)
    img_path = os.path.join(tmp_dir, "test_image.png")
    with open(img_path, "wb") as f:
        f.write(make_minimal_png())
    _record("测试图片创建", os.path.isfile(img_path), img_path)

    # 发送
    result = ui.send_image(contact, img_path)
    _record(f"send_image('{contact}', ...) 成功",
            result["success"],
            result.get("error") or "OK")

    # 验证格式检查
    bad_result = ui.send_image(contact, "/tmp/nonexistent.xyz")
    _record("不存在文件拒绝发送", not bad_result["success"],
            bad_result.get("error", ""))

    # 清理
    try:
        os.unlink(img_path)
    except OSError:
        pass


# ---------------------------------------------------------------------------
# T4: 发送文件
# ---------------------------------------------------------------------------


def test_send_file(ui: WeChatUI, contact: str):
    """T4: 发送文件"""
    logger.info(f"=== T4: 发送文件到 '{contact}' ===")

    # 创建测试文件
    tmp_dir = "/tmp/seaturt-test"
    os.makedirs(tmp_dir, exist_ok=True)
    file_path = os.path.join(tmp_dir, "test_document.txt")
    with open(file_path, "w") as f:
        f.write(f"SeaTurt P1 test file\nGenerated at {time.strftime('%Y-%m-%d %H:%M:%S')}\n")
    _record("测试文件创建", os.path.isfile(file_path), file_path)

    # 发送
    result = ui.send_file(contact, file_path)
    _record(f"send_file('{contact}', ...) 成功",
            result["success"],
            result.get("error") or "OK")

    # 验证空文件拒绝
    empty_path = os.path.join(tmp_dir, "empty.txt")
    with open(empty_path, "w") as f:
        pass  # 空文件
    empty_result = ui.send_file(contact, empty_path)
    _record("空文件拒绝发送", not empty_result["success"],
            empty_result.get("error", ""))

    # 清理
    for p in [file_path, empty_path]:
        try:
            os.unlink(p)
        except OSError:
            pass


# ---------------------------------------------------------------------------
# T5: 频率限制验证
# ---------------------------------------------------------------------------


def test_rate_limit(ui: WeChatUI):
    """T5: 频率限制机制验证（不实际发送）"""
    logger.info("=== T5: 频率限制验证 ===")

    # 检查 _check_rate_limit 方法
    can_send = ui._check_rate_limit()
    _record("初始状态允许发送", can_send)

    # 模拟填满发送队列
    from collections import deque
    original = ui._send_timestamps
    ui._send_timestamps = deque(time.time() - i for i in range(10))
    blocked = not ui._check_rate_limit()
    _record("填满队列后被限制", blocked)

    # 恢复
    ui._send_timestamps = original


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main():
    parser = argparse.ArgumentParser(description="P1 阶段验证: 发消息 + 联系人 + 图片/文件")
    parser.add_argument("--contact", default="文件传输助手",
                        help="测试联系人 (默认: 文件传输助手)")
    parser.add_argument("--skip-send", action="store_true",
                        help="跳过发送测试（只验证联系人和频率限制）")
    parser.add_argument("--skip-file", action="store_true",
                        help="跳过图片/文件发送测试")
    parser.add_argument("--contacts-only", action="store_true",
                        help="只测试联系人功能")
    args = parser.parse_args()

    print()
    print("🐢 SeaTurt — P1 阶段验证")
    print(f"   联系人: {args.contact}")
    print(f"   跳过发送: {args.skip_send}")
    print(f"   跳过文件: {args.skip_file}")
    print()

    # 环境检查
    if not check_environment():
        logger.error("环境检查未通过")
        sys.exit(_summary() or 2)

    ui = WeChatUI()
    ui.find_wechat()

    try:
        # T1: 联系人列表
        test_contacts(ui)

        if args.contacts_only:
            sys.exit(_summary())

        # T5: 频率限制
        test_rate_limit(ui)

        if args.skip_send:
            sys.exit(_summary())

        # T2: 发送文本消息
        test_send_text(ui, args.contact)

        if args.skip_file:
            sys.exit(_summary())

        # T3: 发送图片
        test_send_image(ui, args.contact)

        # T4: 发送文件
        test_send_file(ui, args.contact)

    except Exception as e:
        logger.error(f"测试异常: {e}")
        traceback.print_exc()
        _record("未捕获异常", False, str(e))

    sys.exit(_summary())


if __name__ == "__main__":
    main()
