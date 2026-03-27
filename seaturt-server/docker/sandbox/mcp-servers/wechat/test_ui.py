#!/usr/bin/env python3
"""
test_ui.py — WeChatUI 本地验证脚本

验证基本 UI 操控流程：
  1. 环境检查（DISPLAY / D-Bus / AT-SPI2 / 微信进程）
  2. find_wechat() 找到微信应用
  3. get_status() 确认登录状态
  4. click_contact("文件传输助手") 打开聊天
  5. send_text("SeaTurt test") 输入文字并发送
  6. get_messages() 验证消息已出现

运行方式:
  docker exec -it <container> python3 /opt/mcp-wechat/test_ui.py
  docker exec -it <container> python3 /opt/mcp-wechat/test_ui.py --contact "M" --text "Hello"
  docker exec -it <container> python3 /opt/mcp-wechat/test_ui.py --skip-send  # 只验证读取，不发送

退出码:
  0 = 全部通过
  1 = 有 FAIL
  2 = 环境异常（微信未运行等）
"""

import argparse
import logging
import os
import sys
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
    format="[test-ui] %(asctime)s %(levelname)-5s %(message)s",
    datefmt="%H:%M:%S",
    stream=sys.stderr,
)
logger = logging.getLogger("test-ui")

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
# Tests
# ---------------------------------------------------------------------------


def test_environment():
    """T1: 环境检查"""
    logger.info("=== T1: 环境检查 ===")

    display = os.environ.get("DISPLAY", "")
    _record("DISPLAY 已设置", bool(display), display)

    dbus = os.environ.get("DBUS_SESSION_BUS_ADDRESS", "")
    _record("DBUS_SESSION_BUS_ADDRESS 已设置", bool(dbus), dbus[:60] if dbus else "")

    atspi_ok = ensure_atspi()
    _record("AT-SPI2 registryd 运行中", atspi_ok)

    wechat_bin = find_wechat_bin()
    _record("微信二进制存在", wechat_bin is not None, wechat_bin or "NOT FOUND")

    running = is_wechat_running()
    _record("微信进程运行中", running)

    if not running:
        logger.warning("微信未运行，尝试启动...")
        if wechat_bin:
            proc = launch_wechat(foreground=False)
            if proc:
                logger.info(f"已启动微信 (PID={proc.pid})，等待 8 秒...")
                time.sleep(8)
                running = is_wechat_running()
                _record("微信启动成功", running)
            else:
                _record("微信启动成功", False, "launch_wechat 返回 None")
        else:
            _record("微信启动成功", False, "找不到微信二进制")

    return running


def test_find_wechat(ui: WeChatUI):
    """T2: 查找微信应用"""
    logger.info("=== T2: 查找微信应用 ===")

    found = ui.find_wechat()
    _record("find_wechat() 成功", found)

    if found:
        status = ui.get_status()
        _record("get_status() 返回有效数据", status.get("running", False),
                f"running={status['running']}, logged_in={status['logged_in']}, "
                f"current_chat={status.get('current_chat')}, nodes={status.get('node_count')}")
        _record("微信已登录", status.get("logged_in", False))
    else:
        _record("get_status() 返回有效数据", False, "微信应用未找到")
        _record("微信已登录", False)

    return found


def test_chat_list(ui: WeChatUI):
    """T3: 聊天列表"""
    logger.info("=== T3: 聊天列表 ===")

    chats = ui.get_chat_list()
    _record("get_chat_list() 返回数据", len(chats) > 0, f"{len(chats)} 个聊天")

    if chats:
        for i, c in enumerate(chats[:5]):
            logger.info(f"  聊天 {i}: name='{c['name']}', last='{c['last_message']}', time='{c['time']}'")


def test_click_contact(ui: WeChatUI, contact: str):
    """T4: 打开联系人"""
    logger.info(f"=== T4: 打开联系人 '{contact}' ===")

    ok = ui.click_contact(contact)
    _record(f"click_contact('{contact}') 成功", ok)

    if ok:
        time.sleep(1)
        # 验证输入框出现
        inp = ui.find_input_box()
        _record("聊天输入框已出现", inp is not None,
                f"name='{inp.get_name()}'" if inp else "")

        # 验证消息列表出现
        msgs = ui.find_messages_list()
        _record("消息列表已出现", msgs is not None)

    return ok


def test_send_text(ui: WeChatUI, text: str):
    """T5: 输入文字并发送"""
    logger.info(f"=== T5: 发送文字 '{text}' ===")

    sent = ui.send_text(text)
    _record(f"send_text('{text}') 成功", sent)

    if sent:
        time.sleep(2)
        # 验证消息出现在消息列表
        messages = ui.get_messages_text_only()
        found_msg = any(text in m["content"] for m in messages)
        _record("消息出现在消息列表", found_msg,
                f"最近消息: {[m['content'] for m in messages[-3:]]}" if messages else "无消息")


def test_read_messages(ui: WeChatUI):
    """T6: 读取消息"""
    logger.info("=== T6: 读取消息 ===")

    messages = ui.get_messages()
    _record("get_messages() 返回数据", len(messages) > 0, f"{len(messages)} 条消息")

    ts_count = sum(1 for m in messages if m["type"] == "timestamp")
    msg_count = sum(1 for m in messages if m["type"] == "message")
    _record("消息类型解析正确", msg_count > 0, f"timestamps={ts_count}, messages={msg_count}")

    if messages:
        for m in messages[-5:]:
            logger.info(f"  [{m['type']}] {m['content'][:60]}")


def test_dump_tree(ui: WeChatUI):
    """T7: 控件树调试输出"""
    logger.info("=== T7: 控件树 dump ===")

    tree = ui.dump_tree(max_depth=6)
    lines = tree.strip().split("\n") if tree else []
    _record("dump_tree() 返回控件树", len(lines) > 0, f"{len(lines)} 行")

    if lines:
        for line in lines[:15]:
            logger.info(f"  {line}")
        if len(lines) > 15:
            logger.info(f"  ... (共 {len(lines)} 行)")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main():
    parser = argparse.ArgumentParser(description="WeChatUI 本地验证测试")
    parser.add_argument("--contact", default="文件传输助手",
                        help="要打开的联系人名称 (默认: 文件传输助手)")
    parser.add_argument("--text", default=None,
                        help="要发送的测试文本 (默认: 自动生成带时间戳的文本)")
    parser.add_argument("--skip-send", action="store_true",
                        help="跳过发送测试（只验证读取）")
    parser.add_argument("--dump-only", action="store_true",
                        help="只输出控件树，不执行其他测试")
    args = parser.parse_args()

    test_text = args.text or f"SeaTurt test @ {time.strftime('%H:%M:%S')}"

    print()
    print("🐢 SeaTurt — WeChatUI 本地验证")
    print(f"   联系人: {args.contact}")
    print(f"   测试文本: {test_text}")
    print(f"   跳过发送: {args.skip_send}")
    print()

    # T1: 环境检查
    env_ok = test_environment()
    if not env_ok:
        logger.error("环境检查未通过，微信未运行")
        _summary()
        sys.exit(2)

    # 创建 WeChatUI 实例
    ui = WeChatUI()

    try:
        # T2: 查找微信
        found = test_find_wechat(ui)
        if not found:
            logger.error("微信应用未找到，中止测试")
            _summary()
            sys.exit(2)

        # dump-only 模式
        if args.dump_only:
            test_dump_tree(ui)
            sys.exit(_summary())

        # T3: 聊天列表
        test_chat_list(ui)

        # T7: 控件树
        test_dump_tree(ui)

        # T4: 打开联系人
        chat_ok = test_click_contact(ui, args.contact)

        if chat_ok:
            # T6: 读取消息（发送前）
            test_read_messages(ui)

            # T5: 发送文字
            if not args.skip_send:
                test_send_text(ui, test_text)

    except Exception as e:
        logger.error(f"测试异常: {e}")
        traceback.print_exc()
        _record("未捕获异常", False, str(e))

    sys.exit(_summary())


if __name__ == "__main__":
    main()
