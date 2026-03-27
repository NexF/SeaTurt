#!/usr/bin/env python3
"""
mcp-server-wechat — WeChat MCP Server (stdio transport, JSON-RPC 2.0)

纯 Python 一层架构，和 mcp-server-search 模式一致：
  Agent → docker exec → main.py (stdin/stdout) → AT-SPI2 → Linux WeChat

每次 tool call，上层 Executor 通过 docker exec 启动本脚本，
发送 MCP initialize + tools/call 请求，读取结果后关闭 stdin 使进程退出。

AT-SPI2 通过 D-Bus 和微信进程通信，无需维护长驻 daemon。
"""

import json
import os
import subprocess
import sys
import base64
import logging
import time

# ---------------------------------------------------------------------------
# Environment bootstrap (MUST run before importing wechat_ui / gi)
# ---------------------------------------------------------------------------
# docker exec does NOT inherit DBUS_SESSION_BUS_ADDRESS from the container's
# init process. We use wechat_launcher to discover/create the D-Bus session.

from wechat_launcher import (
    ensure_display,
    ensure_dbus,
    ensure_atspi,
    ensure_environment,
    find_wechat_bin,
    is_wechat_running,
    launch_wechat,
    WECHAT_BIN_PATHS,
)

# Bootstrap: 设置 DISPLAY + D-Bus 地址（必须在 import wechat_ui 之前）
ensure_display()
ensure_dbus()

from wechat_ui import WeChatUI

# ---------------------------------------------------------------------------
# Logging (stderr, not stdout — stdout is MCP protocol)
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=logging.INFO,
    format="[mcp-wechat] %(asctime)s %(levelname)s %(message)s",
    datefmt="%H:%M:%S",
    stream=sys.stderr,
)
logger = logging.getLogger("mcp-wechat")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SESSION_DIR = "/opt/wechat-daemon/session"
SCREENSHOT_PATH = os.path.join(SESSION_DIR, "screenshot.png")


# ---------------------------------------------------------------------------
# WeChat client detection
# ---------------------------------------------------------------------------


def check_wechat_installed() -> str | None:
    """
    检测微信客户端是否已安装。

    Returns:
        None 如果已安装，否则返回错误提示文本。
    """
    if find_wechat_bin():
        return None

    # 检查是否有 dpkg 安装记录
    pkg_hint = ""
    try:
        r = subprocess.run(
            ["dpkg", "-l", "wechat"],
            capture_output=True, timeout=5,
        )
        if r.returncode == 0:
            return None  # dpkg 能查到，说明装了
        pkg_hint = (
            "  # Debian/Ubuntu:\n"
            "  sudo apt-get update && sudo apt-get install -y wechat\n"
        )
    except FileNotFoundError:
        pass  # 没有 dpkg，可能是 rpm 系

    return (
        "⚠️ 未检测到微信客户端（WeChat for Linux）。\n\n"
        "请先在容器中安装微信桌面版，例如：\n"
        f"{pkg_hint}"
        "  # 或从官方 .deb 包安装:\n"
        "  wget https://dldir1v6.qq.com/weixin/Universal/Linux/WeChatLinux_x86_64.deb\n"
        "  sudo dpkg -i WeChatLinux_x86_64.deb\n"
        "  sudo apt-get install -f -y\n\n"
        "安装后重新调用此工具即可。"
    )

# ---------------------------------------------------------------------------
# Tool definitions (MCP tools/list)
# ---------------------------------------------------------------------------

TOOLS = [
    {
        "name": "wechat_login",
        "description": (
            "Get WeChat login QR code. If already logged in, returns current status. "
            "The QR code is displayed as a base64-encoded PNG image. "
            "User needs to scan it with their phone to log in."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "wechat_logout",
        "description": "Log out of the current WeChat session.",
        "inputSchema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "wechat_status",
        "description": (
            "Query current WeChat login status, including whether logged in, "
            "current chat contact, and UI node count."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "wechat_get_contacts",
        "description": (
            "Get contacts list from WeChat database. Returns full contact info including "
            "nickname, remark, WeChat ID, avatar URL. Supports filtering by type and keyword search. "
            "Data source: contact.db (7632 contacts total)."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "type": {
                    "type": "string",
                    "enum": ["contact", "group", "biz", "system", "wecom"],
                    "description": (
                        "Filter contacts by type: 'contact' for personal contacts (6071), "
                        "'group' for group chats (42), 'biz' for official accounts (1426), "
                        "'system' for system accounts, 'wecom' for enterprise contacts. "
                        "Omit to return all types."
                    ),
                },
                "keyword": {
                    "type": "string",
                    "description": (
                        "Search keyword to filter contacts by nickname, remark, WeChat ID, "
                        "or pinyin (optional)"
                    ),
                },
                "limit": {
                    "type": "integer",
                    "description": "Maximum number of contacts to return (default: 100)",
                },
            },
        },
    },
    {
        "name": "wechat_send_msg",
        "description": (
            "Send a text message to a WeChat contact. "
            "Finds the contact in chat list or by search, enters the chat, "
            "types the message, and sends it. "
            "Includes rate limiting (max 10/min) and human-like random delay."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "to": {
                    "type": "string",
                    "description": "Recipient's nickname or remark name",
                },
                "content": {
                    "type": "string",
                    "description": "Message content to send",
                },
            },
            "required": ["to", "content"],
        },
    },
    {
        "name": "wechat_send_image",
        "description": (
            "Send an image file to a WeChat contact. "
            "Supports common image formats: png, jpg, gif, webp, bmp. "
            "The image must exist at the specified path inside the container."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "to": {
                    "type": "string",
                    "description": "Recipient's nickname or remark name",
                },
                "image_path": {
                    "type": "string",
                    "description": "Absolute path to the image file inside the container",
                },
            },
            "required": ["to", "image_path"],
        },
    },
    {
        "name": "wechat_send_file",
        "description": (
            "Send a file to a WeChat contact. "
            "Supports common document formats: pdf, doc, xls, zip, etc. "
            "The file must exist at the specified path inside the container."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "to": {
                    "type": "string",
                    "description": "Recipient's nickname or remark name",
                },
                "file_path": {
                    "type": "string",
                    "description": "Absolute path to the file inside the container",
                },
            },
            "required": ["to", "file_path"],
        },
    },
    {
        "name": "wechat_read_messages",
        "description": (
            "Read chat messages from WeChat database. "
            "Requires specifying a contact name. Returns messages with sender, type, "
            "timestamp, direction, and content. "
            "Data source: message_0.db (full chat history, not limited to UI visible messages). "
            "Supports keyword filtering and time range queries."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "contact": {
                    "type": "string",
                    "description": "Contact nickname or remark name to read messages from (required)",
                },
                "keyword": {
                    "type": "string",
                    "description": "Filter messages by keyword in content (optional)",
                },
                "before": {
                    "type": "string",
                    "description": (
                        "Only return messages before this time. "
                        "Accepts Unix timestamp or ISO format (e.g. '2026-03-01 12:00:00')"
                    ),
                },
                "after": {
                    "type": "string",
                    "description": (
                        "Only return messages after this time. "
                        "Accepts Unix timestamp or ISO format (e.g. '2026-03-01 12:00:00')"
                    ),
                },
                "limit": {
                    "type": "integer",
                    "description": "Maximum number of messages to return (default: 50)",
                },
            },
            "required": ["contact"],
        },
    },
    {
        "name": "wechat_screenshot",
        "description": (
            "Take a screenshot of the WeChat window. "
            "Returns the screenshot as a base64-encoded PNG image. "
            "Useful for debugging or verifying the UI state."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {},
        },
    },
    {
        "name": "wechat_get_sessions",
        "description": (
            "Get the recent chat session list from WeChat database. "
            "Returns sessions sorted by last activity time, including contact name, "
            "unread count, last message summary, and timestamp. "
            "Much more comprehensive than UI chat list (268 sessions vs ~10 visible). "
            "Data source: session.db."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "limit": {
                    "type": "integer",
                    "description": "Maximum number of sessions to return (default: 50)",
                },
            },
        },
    },
    {
        "name": "wechat_get_unread",
        "description": (
            "Get all sessions with unread messages from WeChat database. "
            "Returns contacts/groups that have unread messages, sorted by unread count. "
            "Core capability for the agent to know 'who sent me messages'. "
            "Data source: session.db."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {},
        },
    },
]

# ---------------------------------------------------------------------------
# JSON-RPC helpers (same pattern as mcp-server-search)
# ---------------------------------------------------------------------------


def write_response(obj: dict) -> None:
    """Write a JSON-RPC response to stdout (one line)."""
    line = json.dumps(obj, ensure_ascii=False)
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def write_result(req_id, result) -> None:
    write_response({"jsonrpc": "2.0", "id": req_id, "result": result})


def write_error(req_id, code: int, message: str) -> None:
    write_response({
        "jsonrpc": "2.0",
        "id": req_id,
        "error": {"code": code, "message": message},
    })


# ---------------------------------------------------------------------------
# MCP protocol handlers
# ---------------------------------------------------------------------------


def handle_initialize(req_id) -> None:
    write_result(req_id, {
        "protocolVersion": "2024-11-05",
        "capabilities": {"tools": {}},
        "serverInfo": {"name": "mcp-server-wechat", "version": "1.0.0"},
    })


def handle_tools_list(req_id) -> None:
    write_result(req_id, {"tools": TOOLS})


# ---------------------------------------------------------------------------
# Tool handlers
# ---------------------------------------------------------------------------

# Lazily initialized WeChatUI instance
_ui: WeChatUI | None = None


class WeChatNotInstalled(Exception):
    """微信客户端未安装"""
    pass


def get_ui() -> WeChatUI:
    """
    Lazy init: only connect to AT-SPI2 when the first tool call arrives.

    Raises WeChatNotInstalled if the WeChat binary is not found on the system.
    """
    global _ui

    # 先检测微信客户端是否安装
    install_hint = check_wechat_installed()
    if install_hint:
        raise WeChatNotInstalled(install_hint)

    if _ui is None:
        _ui = WeChatUI()
        try:
            found = _ui.find_wechat()
            if found:
                logger.info("WeChat found via AT-SPI2")
            else:
                logger.warning("WeChat not found (will retry on next call)")
        except Exception as e:
            logger.warning(f"AT-SPI2 init: {e}")
    return _ui


def _screenshot_qr_code(ui) -> dict:
    """截取 QR 码页面并返回 MCP 结果。"""
    os.makedirs(SESSION_DIR, exist_ok=True)

    # Wait a moment for the QR code to fully render
    time.sleep(2)

    screenshot_path = ui.take_screenshot(SCREENSHOT_PATH)
    if screenshot_path and os.path.exists(screenshot_path):
        with open(screenshot_path, "rb") as f:
            img_b64 = base64.b64encode(f.read()).decode("ascii")
        return {
            "content": [
                {
                    "type": "text",
                    "text": (
                        "WeChat is waiting for QR code login.\n"
                        "Please scan the QR code below with your phone's WeChat app.\n"
                        "After scanning, confirm login on your phone."
                    ),
                },
                {"type": "image", "data": img_b64, "mimeType": "image/png"},
            ],
        }

    return {
        "content": [{"type": "text", "text": "WeChat is running but screenshot failed."}],
        "isError": True,
    }


def tool_wechat_login(args: dict) -> dict:
    ui = get_ui()
    status = ui.get_status()

    if status["logged_in"]:
        return {
            "content": [{
                "type": "text",
                "text": (
                    f"WeChat is already logged in.\n"
                    f"Current chat: {status.get('current_chat', 'N/A')}\n"
                    f"UI nodes: {status.get('node_count', 0)}"
                ),
            }],
        }

    if not status["running"]:
        # Try to start WeChat via s6 service or direct launch
        logger.info("WeChat not running, attempting to start...")
        started = _try_start_wechat()
        if not started:
            return {
                "content": [{
                    "type": "text",
                    "text": (
                        "WeChat is installed but not running, and could not be started automatically.\n\n"
                        "The WeChat process needs the following environment:\n"
                        "  - DISPLAY=:1 (Xvfb virtual display)\n"
                        "  - QT_ACCESSIBILITY=1\n"
                        "  - DBUS_SESSION_BUS_ADDRESS set correctly\n\n"
                        "Try running: QT_ACCESSIBILITY=1 DISPLAY=:1 /usr/bin/wechat &\n"
                        "Or restart the container to let the s6 service start WeChat."
                    ),
                }],
                "isError": True,
            }
        # Re-check status after starting
        time.sleep(3)  # Give WeChat time to initialize GUI
        ui._wechat_app = None  # Force re-discovery
        status = ui.get_status()
        if status["logged_in"]:
            return {
                "content": [{
                    "type": "text",
                    "text": "WeChat started and is already logged in (session preserved).",
                }],
            }

    # ---------------------------------------------------------------
    # "已记住账号"状态处理:
    # 微信之前登录过，打开时显示头像 + "Enter Weixin" + "Switch Account"
    # 而不是 QR 码。此时自动点击 "Switch Account" 切换到 QR 码页面。
    # ---------------------------------------------------------------
    if status.get("remembered"):
        logger.info("Detected remembered account login page, clicking 'Switch Account' to show QR code")
        if ui.click_switch_account():
            time.sleep(3)  # 等待 QR 码页面加载
            return _screenshot_qr_code(ui)
        else:
            # Switch Account 按钮没点到，截图返回当前页面
            logger.warning("Failed to click 'Switch Account', returning current screen")
            return _screenshot_qr_code(ui)

    # WeChat is running but not logged in → screenshot QR code
    return _screenshot_qr_code(ui)


def _try_start_wechat() -> bool:
    """
    Attempt to start WeChat in the background using wechat_launcher.
    Returns True if the process was successfully launched.
    """
    # 确保 D-Bus + AT-SPI2 环境就绪
    if not ensure_environment():
        logger.warning("Environment setup failed, attempting launch anyway...")

    # 启动微信（后台模式）
    proc = launch_wechat(foreground=False)
    if not proc:
        return False

    # 等待微信进程就绪
    time.sleep(2)
    if is_wechat_running():
        logger.info("WeChat started successfully via wechat_launcher")
        return True

    logger.warning("WeChat process did not appear after launch")
    return False


def tool_wechat_logout(args: dict) -> dict:
    return {
        "content": [{"type": "text", "text": "Logout is not yet implemented."}],
        "isError": True,
    }


def tool_wechat_status(args: dict) -> dict:
    ui = get_ui()
    status = ui.get_status()
    lines = [
        f"Running: {'Yes' if status['running'] else 'No'}",
        f"Logged in: {'Yes' if status['logged_in'] else 'No'}",
        f"Current chat: {status.get('current_chat') or 'N/A'}",
        f"UI nodes: {status.get('node_count', 0)}",
    ]

    # P2.5: DB 状态字段
    try:
        from wechat_db import get_wechat_db
        db = get_wechat_db()
        db_status = db.get_key_status()
        lines.append("")
        lines.append(f"DB status: {db_status['db_status']}")
        lines.append(f"DB count: {db_status['db_count']}/{db_status['db_total']} unlocked")
        lines.append(f"DB extract time: {db_status['db_extract_time']}s")
        if db_status.get('error'):
            lines.append(f"DB error: {db_status['error']}")
        core_dbs = db_status.get('core_dbs', {})
        for db_name, available in core_dbs.items():
            lines.append(f"  {db_name}: {'✅' if available else '❌'}")
    except Exception as e:
        lines.append(f"DB status: error ({e})")

    return {"content": [{"type": "text", "text": "\n".join(lines)}]}


def tool_wechat_get_contacts(args: dict) -> dict:
    # P2.4: 基于 DB 查询
    # P2.5: 错误处理
    db_error = _check_db_ready()
    if db_error:
        return db_error

    try:
        from wechat_db_query import get_wechat_db_query
        query = get_wechat_db_query()

        contact_type = args.get("type")  # contact/group/biz/system/wecom or None
        keyword = args.get("keyword", "")
        limit = int(args.get("limit", 100))

        results = query.query_contacts(
            contact_type=contact_type,
            keyword=keyword if keyword else None,
            limit=limit,
        )

        # 格式化输出
        formatted = []
        for c in results:
            entry = {
                "nick_name": c["nick_name"],
                "remark": c["remark"],
                "alias": c["alias"],
                "type": _contact_type_name(c["local_type"]),
            }
            if c["small_head_url"]:
                entry["avatar_url"] = c["small_head_url"]
            formatted.append(entry)

        text = json.dumps(formatted, ensure_ascii=False, indent=2)
        return {"content": [{"type": "text", "text": f"Found {len(formatted)} contacts:\n{text}"}]}
    except Exception as e:
        logger.exception(f"wechat_get_contacts failed: {e}")
        return {"content": [{"type": "text", "text": f"Error querying contacts: {e}"}], "isError": True}


# ---------------------------------------------------------------------------
# P2.5: DB 状态检查 helper — DB 相关 tool 调用前统一检查
# ---------------------------------------------------------------------------


def _check_db_ready() -> dict | None:
    """检查 DB 是否就绪。

    Returns:
        None 如果就绪，否则返回 MCP 错误结果 dict。
    """
    try:
        from wechat_db import get_wechat_db
        db = get_wechat_db()

        # 首次调用时触发密钥加载
        if not db.is_ready():
            db.ensure_keys()

        status = db.get_key_status()
        if status["db_status"] == "ready":
            return None
        elif status["db_status"] == "extracting":
            return {
                "content": [{"type": "text", "text": "正在提取 DB 密钥，请稍后重试。"}],
                "isError": True,
            }
        elif status["db_status"] == "failed":
            error = status.get("error", "未知错误")
            return {
                "content": [{"type": "text", "text": f"DB 密钥提取失败，无法查询。错误: {error}"}],
                "isError": True,
            }
        else:
            # not_started: 触发提取
            db.ensure_keys()
            return {
                "content": [{"type": "text", "text": "DB 密钥提取刚启动，请稍后重试。"}],
                "isError": True,
            }
    except Exception as e:
        return {
            "content": [{"type": "text", "text": f"DB 初始化错误: {e}"}],
            "isError": True,
        }


def _contact_type_name(local_type: int) -> str:
    """将 contact.local_type 映射为可读名称。"""
    return {
        0: "system",
        1: "biz",
        2: "group",
        3: "contact",
        5: "wecom",
        6: "openim",
    }.get(local_type, f"unknown_{local_type}")


def tool_wechat_send_msg(args: dict) -> dict:
    ui = get_ui()
    to = args.get("to", "")
    content = args.get("content", "")

    if not to:
        return {"content": [{"type": "text", "text": "Missing required argument: to"}], "isError": True}
    if not content:
        return {"content": [{"type": "text", "text": "Missing required argument: content"}], "isError": True}

    # P1.2: 使用高层封装 send_msg（含频率限制 + 随机延迟）
    result = ui.send_msg(to, content)
    if not result["success"]:
        return {"content": [{"type": "text", "text": f"Failed: {result['error']}"}], "isError": True}

    return {"content": [{"type": "text", "text": f"Message sent to '{to}': {content[:100]}{'...' if len(content) > 100 else ''}"}]}


def tool_wechat_read_messages(args: dict) -> dict:
    # P2.4: 基于 DB 查询
    # P2.5: 错误处理
    db_error = _check_db_ready()
    if db_error:
        return db_error

    contact_name = args.get("contact", "")
    if not contact_name:
        return {"content": [{"type": "text", "text": "Missing required argument: contact"}], "isError": True}

    try:
        from wechat_db_query import get_wechat_db_query
        query = get_wechat_db_query()

        limit = int(args.get("limit", 50))
        keyword = args.get("keyword")
        before_time = args.get("before")
        after_time = args.get("after")

        messages = query.query_messages(
            contact_name=contact_name,
            limit=limit,
            before_time=before_time,
            after_time=after_time,
            keyword=keyword,
        )

        if not messages:
            return {"content": [{"type": "text", "text": f"No messages found for '{contact_name}'."}]}

        # 格式化输出
        lines = []
        for msg in messages:
            direction_icon = "→" if msg["direction"] == "send" else "←"
            type_tag = f"[{msg['type']}]" if msg["type"] != "text" else ""
            line = f"[{msg['create_time_str']}] {direction_icon} {msg['sender']}{type_tag}: {msg['content']}"
            lines.append(line)

        text = "\n".join(lines)
        return {"content": [{"type": "text", "text": f"Messages with {contact_name} ({len(messages)} messages):\n\n{text}"}]}
    except ValueError as e:
        return {"content": [{"type": "text", "text": str(e)}], "isError": True}
    except Exception as e:
        logger.exception(f"wechat_read_messages failed: {e}")
        return {"content": [{"type": "text", "text": f"Error reading messages: {e}"}], "isError": True}


def tool_wechat_send_image(args: dict) -> dict:
    ui = get_ui()
    to = args.get("to", "")
    image_path = args.get("image_path", "")

    if not to:
        return {"content": [{"type": "text", "text": "Missing required argument: to"}], "isError": True}
    if not image_path:
        return {"content": [{"type": "text", "text": "Missing required argument: image_path"}], "isError": True}

    # P1.3: 发送图片
    result = ui.send_image(to, image_path)
    if not result["success"]:
        return {"content": [{"type": "text", "text": f"Failed: {result['error']}"}], "isError": True}

    return {"content": [{"type": "text", "text": f"Image sent to '{to}': {image_path}"}]}


def tool_wechat_send_file(args: dict) -> dict:
    ui = get_ui()
    to = args.get("to", "")
    file_path = args.get("file_path", "")

    if not to:
        return {"content": [{"type": "text", "text": "Missing required argument: to"}], "isError": True}
    if not file_path:
        return {"content": [{"type": "text", "text": "Missing required argument: file_path"}], "isError": True}

    # P1.4: 发送文件
    result = ui.send_file(to, file_path)
    if not result["success"]:
        return {"content": [{"type": "text", "text": f"Failed: {result['error']}"}], "isError": True}

    file_name = os.path.basename(file_path)
    return {"content": [{"type": "text", "text": f"File sent to '{to}': {file_name}"}]}


def tool_wechat_screenshot(args: dict) -> dict:
    ui = get_ui()
    os.makedirs(SESSION_DIR, exist_ok=True)
    screenshot_path = ui.take_screenshot(SCREENSHOT_PATH)
    if screenshot_path and os.path.exists(screenshot_path):
        with open(screenshot_path, "rb") as f:
            img_b64 = base64.b64encode(f.read()).decode("ascii")
        return {
            "content": [
                {"type": "text", "text": "WeChat screenshot:"},
                {"type": "image", "data": img_b64, "mimeType": "image/png"},
            ],
        }
    return {"content": [{"type": "text", "text": "Failed to take screenshot."}], "isError": True}


def tool_wechat_get_sessions(args: dict) -> dict:
    """P2.4: 会话列表 — 基于 session.db"""
    db_error = _check_db_ready()
    if db_error:
        return db_error

    try:
        from wechat_db_query import get_wechat_db_query
        query = get_wechat_db_query()

        limit = int(args.get("limit", 50))
        sessions = query.query_sessions(limit=limit)

        if not sessions:
            return {"content": [{"type": "text", "text": "No sessions found."}]}

        # 格式化输出
        lines = []
        for s in sessions:
            unread = f" ({s['unread_count']} unread)" if s['unread_count'] > 0 else ""
            draft = f" [draft: {s['draft'][:30]}]" if s['draft'] else ""
            summary = s['summary'][:60] if s['summary'] else ""
            line = f"{s['nick_name']}{unread} - {summary}{draft} [{s['last_time_str']}]"
            lines.append(line)

        text = "\n".join(lines)
        return {"content": [{"type": "text", "text": f"Recent sessions ({len(sessions)}):\n\n{text}"}]}
    except Exception as e:
        logger.exception(f"wechat_get_sessions failed: {e}")
        return {"content": [{"type": "text", "text": f"Error querying sessions: {e}"}], "isError": True}


def tool_wechat_get_unread(args: dict) -> dict:
    """P2.4: 未读消息聚合 — 基于 session.db"""
    db_error = _check_db_ready()
    if db_error:
        return db_error

    try:
        from wechat_db_query import get_wechat_db_query
        query = get_wechat_db_query()

        unread_sessions = query.query_unread_sessions()

        if not unread_sessions:
            return {"content": [{"type": "text", "text": "No unread messages."}]}

        total_unread = sum(s["unread_count"] for s in unread_sessions)

        lines = []
        for s in unread_sessions:
            summary = s['summary'][:60] if s['summary'] else ""
            line = f"  {s['nick_name']}: {s['unread_count']} unread - {summary} [{s['last_time_str']}]"
            lines.append(line)

        text = "\n".join(lines)
        return {"content": [{"type": "text", "text": (
            f"Unread messages: {total_unread} total in {len(unread_sessions)} sessions\n\n{text}"
        )}]}
    except Exception as e:
        logger.exception(f"wechat_get_unread failed: {e}")
        return {"content": [{"type": "text", "text": f"Error querying unread: {e}"}], "isError": True}


# ---------------------------------------------------------------------------
# Tool dispatch
# ---------------------------------------------------------------------------

TOOL_HANDLERS = {
    "wechat_login": tool_wechat_login,
    "wechat_logout": tool_wechat_logout,
    "wechat_status": tool_wechat_status,
    "wechat_get_contacts": tool_wechat_get_contacts,
    "wechat_send_msg": tool_wechat_send_msg,
    "wechat_send_image": tool_wechat_send_image,
    "wechat_send_file": tool_wechat_send_file,
    "wechat_read_messages": tool_wechat_read_messages,
    "wechat_screenshot": tool_wechat_screenshot,
    "wechat_get_sessions": tool_wechat_get_sessions,
    "wechat_get_unread": tool_wechat_get_unread,
}


def handle_tools_call(req_id, params: dict) -> None:
    name = params.get("name", "")
    args = params.get("arguments", {})

    handler = TOOL_HANDLERS.get(name)
    if not handler:
        write_error(req_id, -32602, f"unknown tool: {name}")
        return

    try:
        result = handler(args)
        write_result(req_id, result)
    except WeChatNotInstalled as e:
        # 微信客户端未安装 → 返回安装引导提示
        logger.warning(f"WeChat not installed: {e}")
        write_result(req_id, {
            "content": [{"type": "text", "text": str(e)}],
            "isError": True,
        })
    except Exception as e:
        logger.exception(f"Tool {name} failed")
        write_result(req_id, {
            "content": [{"type": "text", "text": f"Error: {e}"}],
            "isError": True,
        })


# ---------------------------------------------------------------------------
# Main loop (same as mcp-server-search)
# ---------------------------------------------------------------------------


def main() -> None:
    """Read JSON-RPC requests from stdin, write responses to stdout."""
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        try:
            req = json.loads(line)
        except json.JSONDecodeError:
            write_error(None, -32700, "parse error")
            continue

        req_id = req.get("id")
        method = req.get("method", "")

        if method == "initialize":
            handle_initialize(req_id)
        elif method == "notifications/initialized":
            pass  # No response needed
        elif method == "tools/list":
            handle_tools_list(req_id)
        elif method == "tools/call":
            handle_tools_call(req_id, req.get("params", {}))
        else:
            write_error(req_id, -32601, f"method not found: {method}")


if __name__ == "__main__":
    main()
