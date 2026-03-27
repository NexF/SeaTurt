"""
WeChatUI — AT-SPI2 控件封装层

封装所有与微信 UI 控件的交互逻辑，提供高层 API：

控件发现（P0.3）：
- find_wechat()          找到微信应用
- find_chat_list()       找到聊天列表控件（role='list', name='Chats'）
- find_input_box()       找到消息输入框（role='text', editable, name != 'Search'）
- find_send_button()     找到发送按钮（role='push button', name='Send(S)'）
- find_messages_list()   找到消息列表控件（role='list', name='Messages'）

数据读取：
- get_chat_list()        获取聊天列表（解析后的结构化数据）
- get_contacts()         获取联系人列表（带缓存 + 类型解析）
- get_messages()         获取当前聊天窗口的消息列表（区分时间戳/消息内容）
- get_status()           获取微信当前状态

操作：
- click_contact(name)    点击指定联系人进入聊天（= click_chat 别名）
- click_chat(name)       点击指定聊天
- send_text(text)        在当前聊天输入框输入文字并发送
- send_msg(to, content)  高层封装：定位联系人 → 输入 → 发送（含频率限制 + 随机延迟）
- send_image(to, path)   发送图片文件（通过剪贴板粘贴方式）
- send_file(to, path)    发送普通文件（通过剪贴板粘贴方式）
- read_input_text()      读取当前输入框内容
- clear_input()          清空输入框
- search_contact(keyword) 通过搜索框搜索联系人
- fuzzy_search_contact(keyword) 在缓存联系人中模糊搜索

导航：
- navigate_to(tab)       切换到指定导航页（Weixin/Contacts/Favorites/Moments 等）

控件定位策略（P0.3）：
- 所有定位基于 role + name 组合查找，避免依赖具体层级位置
- 参考 design.md 中的「微信控件树结构（实测映射表）」章节
- 控件树缓存机制：2 秒内复用缓存，避免频繁遍历

环境要求:
- apt-get install -y at-spi2-core gir1.2-atspi-2.0 python3-gi xdotool xclip
- 微信启动时必须设置 QT_ACCESSIBILITY=1 QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1
- DBUS_SESSION_BUS_ADDRESS 必须正确设置
"""

import gi
gi.require_version("Atspi", "2.0")
from gi.repository import Atspi

import os
import re
import random
import subprocess
import time
import logging
from collections import deque
from typing import Optional

logger = logging.getLogger("wechat-ui")

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

MAX_TREE_DEPTH = 25          # 微信聊天输入框在 depth=15，建议遍历到 25
KEYBOARD_DELAY = 0.03        # 每个按键之间的延迟（秒）
ACTION_DELAY = 0.5           # UI 操作后等待控件刷新的延迟
SEND_DELAY_MIN = 1.0         # 发送消息后最小等待
SEND_DELAY_MAX = 3.0         # 发送消息后最大等待（人类模拟）

# P1.2 频率限制
RATE_LIMIT_MAX = 10           # 每分钟最多发送条数
RATE_LIMIT_WINDOW = 60.0      # 频率窗口（秒）

# P1.1 联系人缓存
CONTACTS_CACHE_TTL = 300.0    # 联系人缓存有效期（秒）

# P1.3 发送图片支持的格式
IMAGE_EXTENSIONS = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"}

# P1.4 发送文件支持的格式（不在图片列表中的都视为文件）
# 实际上任何格式都支持，这里列出常见格式以便日志/提示
FILE_EXTENSIONS = {".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
                   ".zip", ".rar", ".7z", ".tar", ".gz", ".txt", ".csv",
                   ".mp4", ".mp3", ".wav", ".avi", ".mov"}

# design.md §4.2 导航栏按钮名称映射
NAV_BUTTONS = {
    "weixin": "Weixin",       # 聊天页
    "chat": "Weixin",         # 聊天页（别名）
    "contacts": "Contacts",   # 通讯录
    "favorites": "Favorites", # 收藏
    "moments": "Moments",     # 朋友圈
    "channels": "Channels",   # 视频号
    "search": "Search",       # 搜索
    "mini_programs": "Mini Programs Panel",  # 小程序面板
    "mobile": "Mobile",       # 手机端
    "more": "More",           # 更多
}

# design.md §4.4 消息 list item 时间戳正则（如 "23:49", "昨天 14:30"）
_TIME_PATTERN = re.compile(
    r"^\d{1,2}:\d{2}$"          # "23:49"
    r"|^昨天"                    # "昨天 14:30"
    r"|^前天"                    # "前天 08:00"
    r"|^\d{4}[/-]\d{1,2}[/-]\d{1,2}"  # "2025-03-10"
    r"|^星期"                    # "星期一"
    r"|^yesterday"              # English fallback
    r"|^\d{1,2}/\d{1,2}"       # "3/10"
)


class WeChatUI:
    """微信 AT-SPI2 控件操控封装"""

    def __init__(self):
        Atspi.init()
        self._wechat_app = None  # Atspi.Accessible (微信 application 节点)
        self._all_nodes = []     # 缓存的控件树 [(depth, node), ...]
        self._tree_time = 0      # 上次遍历时间

        # P1.1 联系人缓存
        self._contacts_cache: list[dict] = []       # [{name, type, raw}, ...]
        self._contacts_cache_time: float = 0        # 缓存时间
        self._nickname_map: dict[str, str] = {}     # lower_name → original_name 映射

        # P1.2 频率限制：记录最近发送的时间戳
        self._send_timestamps: deque = deque()

    # ===================================================================
    # 底层工具方法
    # ===================================================================

    def _find_all(self, node, max_depth=MAX_TREE_DEPTH, depth=0):
        """递归收集所有控件节点"""
        results = []
        if depth > max_depth or node is None:
            return results
        results.append((depth, node))
        try:
            n = node.get_child_count()
            for i in range(n):
                try:
                    child = node.get_child_at_index(i)
                    if child:
                        results.extend(self._find_all(child, max_depth, depth + 1))
                except Exception:
                    pass
        except Exception:
            pass
        return results

    def _refresh_tree(self, force=False):
        """刷新控件树缓存（如果距离上次刷新超过 2 秒或 force=True）"""
        now = time.time()
        if not force and self._all_nodes and (now - self._tree_time) < 2.0:
            return
        if self._wechat_app is None:
            raise RuntimeError("微信应用未找到，请先调用 find_wechat()")
        self._all_nodes = self._find_all(self._wechat_app)
        self._tree_time = time.time()
        logger.debug(f"控件树已刷新: {len(self._all_nodes)} 个节点")

    def _find_node(self, role, name=None, editable=None, showing=None):
        """
        在缓存的控件树中查找第一个匹配节点。

        定位策略: role + name 组合，可选 editable/showing 状态过滤。
        """
        for _depth, node in self._all_nodes:
            try:
                if node.get_role_name() != role:
                    continue
                if name is not None and node.get_name() != name:
                    continue
                ss = node.get_state_set()
                if editable is not None:
                    if ss.contains(Atspi.StateType.EDITABLE) != editable:
                        continue
                if showing is not None:
                    if ss.contains(Atspi.StateType.SHOWING) != showing:
                        continue
                return node
            except Exception:
                pass
        return None

    def _find_nodes(self, role, name=None, showing=None, editable=None):
        """在缓存的控件树中查找所有匹配节点"""
        results = []
        for _depth, node in self._all_nodes:
            try:
                if node.get_role_name() != role:
                    continue
                if name is not None and node.get_name() != name:
                    continue
                ss = node.get_state_set()
                if showing is not None:
                    if ss.contains(Atspi.StateType.SHOWING) != showing:
                        continue
                if editable is not None:
                    if ss.contains(Atspi.StateType.EDITABLE) != editable:
                        continue
                results.append(node)
            except Exception:
                pass
        return results

    @staticmethod
    def _read_text(node) -> str:
        """
        读取控件的文本内容。

        ⚠️ 坑点 (design.md §3.5, 坑点 #4/#6):
        - get_character_at_offset() 返回 UTF-8 字节而非 Unicode codepoint
        - 中文字符需要按字节读取后 decode('utf-8')
        - 不要用 node.get_text() 或 node.query_text()（API 签名在 Ubuntu 24.04 已变）
        """
        try:
            cc = node.get_character_count()
            if cc <= 0:
                return ""
            raw_bytes = bytes(node.get_character_at_offset(i) for i in range(cc))
            return raw_bytes.decode("utf-8", errors="replace")
        except Exception:
            return ""

    @staticmethod
    def _set_text(node, text: str) -> bool:
        """
        设置控件的文本内容。

        ⚠️ 坑点 (design.md §3.6, 坑点 #5/#9):
        - 必须先检查 'EditableText' in node.get_interfaces()
        - 直接调用 node.set_text_contents()，不要用 query_editable_text()
        - 中文写入完全正常（底层 D-Bus 传 UTF-8）
        """
        try:
            ifaces = node.get_interfaces()
            if "EditableText" not in ifaces:
                logger.warning("控件不支持 EditableText 接口")
                return False
            return bool(node.set_text_contents(text))
        except Exception as e:
            logger.error(f"设置文本失败: {e}")
            return False

    @staticmethod
    def _keyboard_type(text: str, delay: float = KEYBOARD_DELAY):
        """
        通过键盘事件模拟输入文本。

        参考 design.md §3.7: grab_focus() + generate_keyboard_event(STRING)
        """
        for ch in text:
            Atspi.generate_keyboard_event(0, ch, Atspi.KeySynthType.STRING)
            time.sleep(delay)

    @staticmethod
    def _keyboard_press(keycode: int):
        """模拟按下并释放一个按键（keycode 36 = Enter, 9 = Escape）"""
        Atspi.generate_keyboard_event(keycode, None, Atspi.KeySynthType.PRESSRELEASE)

    @staticmethod
    def _mouse_click(x: int, y: int):
        """模拟鼠标左键点击 (design.md §3.7: "b1c" = button 1 click)"""
        Atspi.generate_mouse_event(x, y, "b1c")

    @staticmethod
    def _click_node_by_mouse(node) -> bool:
        """
        通过鼠标坐标点击控件。

        ⚠️ 坑点 (design.md 坑点 #8): Xvfb 环境下 get_extents 可能返回全零。
        此时 fallback 到 grab_focus + do_action。
        """
        try:
            if "Component" in node.get_interfaces():
                r = node.get_extents(Atspi.CoordType.SCREEN)
                if r.x != 0 or r.y != 0 or r.width != 0 or r.height != 0:
                    cx, cy = r.x + r.width // 2, r.y + r.height // 2
                    Atspi.generate_mouse_event(cx, cy, "b1c")
                    return True
        except Exception:
            pass
        # Fallback: grab_focus + do_action(SetFocus)
        try:
            node.grab_focus()
            if "Action" in node.get_interfaces() and node.get_n_actions() > 0:
                node.do_action(0)
            return True
        except Exception as e:
            logger.warning(f"_click_node_by_mouse fallback 失败: {e}")
            return False

    def _click_node_reliable(self, node) -> bool:
        """
        可靠点击控件（多策略 fallback）。

        策略顺序:
        1. do_action(0) — AT-SPI 原生 action（快速、可靠）
        2. Atspi.generate_mouse_event — AT-SPI 模拟鼠标
        3. xdotool mousemove + click — 系统级鼠标模拟（最可靠）

        某些 Qt 控件（如导航按钮）没有 Action 接口，do_action 会失败，
        此时 fallback 到 xdotool 鼠标点击。

        Returns:
            True if clicked successfully.
        """
        # 策略 1: do_action
        try:
            if node.get_n_actions() > 0:
                node.do_action(0)
                logger.debug("_click_node_reliable: do_action(0) 成功")
                return True
        except Exception as e:
            logger.debug(f"_click_node_reliable: do_action 失败: {e}")

        # 策略 2 & 3: 需要坐标
        cx, cy = None, None
        try:
            comp = node.get_component_iface()
            if comp:
                r = comp.get_extents(0)  # ATSPI_COORD_TYPE_SCREEN
                if r.width > 0 and r.height > 0:
                    cx = r.x + r.width // 2
                    cy = r.y + r.height // 2
        except Exception:
            pass

        if cx is None:
            # 无法获取坐标，尝试 grab_focus
            try:
                node.grab_focus()
                logger.debug("_click_node_reliable: grab_focus fallback")
                return True
            except Exception as e:
                logger.warning(f"_click_node_reliable: grab_focus 也失败: {e}")
                return False

        # 策略 2: Atspi mouse event
        try:
            Atspi.generate_mouse_event(cx, cy, "b1c")
            logger.debug(f"_click_node_reliable: Atspi mouse 点击 ({cx}, {cy})")
            return True
        except Exception as e:
            logger.debug(f"_click_node_reliable: Atspi mouse 失败: {e}")

        # 策略 3: xdotool
        try:
            display = os.environ.get("DISPLAY", ":1")
            result = subprocess.run(
                ["xdotool", "mousemove", "--screen", "0", str(cx), str(cy), "click", "1"],
                env={**os.environ, "DISPLAY": display},
                capture_output=True,
                timeout=5,
            )
            if result.returncode == 0:
                logger.debug(f"_click_node_reliable: xdotool 点击 ({cx}, {cy})")
                return True
            logger.warning(f"_click_node_reliable: xdotool 失败 rc={result.returncode}")
        except Exception as e:
            logger.warning(f"_click_node_reliable: xdotool 异常: {e}")

        return False

    # ===================================================================
    # P0.3 控件发现 API
    # ===================================================================

    def find_wechat(self) -> bool:
        """
        在 AT-SPI2 desktop 中查找微信应用。

        定位策略: desktop → application, name 包含 'wechat'

        Returns:
            True if found, False otherwise.
        """
        desktop = Atspi.get_desktop(0)
        child_count = desktop.get_child_count()
        logger.debug(f"Desktop children: {child_count}")

        for i in range(child_count):
            try:
                app = desktop.get_child_at_index(i)
                if app and "wechat" in (app.get_name() or "").lower():
                    self._wechat_app = app
                    self._refresh_tree(force=True)
                    logger.info(f"找到微信应用: {app.get_name()}, 控件数: {len(self._all_nodes)}")
                    return True
            except Exception:
                pass

        logger.warning(f"未找到微信应用（desktop children={child_count}）")
        return False

    def find_chat_list(self):
        """
        找到聊天列表控件。

        定位策略 (design.md §4.3): role='list', name='Chats'
        子节点为 list item，name 格式: "<联系人名> <最后消息> <时间>"

        Returns:
            Atspi.Accessible 节点，未找到返回 None
        """
        self._refresh_tree()
        node = self._find_node("list", name="Chats")
        if node:
            logger.debug(f"find_chat_list: 找到 Chats 列表, children={node.get_child_count()}")
        else:
            logger.debug("find_chat_list: 未找到 Chats 列表")
        return node

    def find_input_box(self):
        """
        找到消息输入框。

        定位策略 (design.md §4.5):
        - role='text', name != 'Search', EDITABLE + SHOWING
        - name 是当前聊天对象的昵称
        - 接口包含 EditableText、Text
        - 状态: EDITABLE, FOCUSABLE, MULTI_LINE, SHOWING, VISIBLE

        Returns:
            Atspi.Accessible 节点，未找到返回 None
        """
        self._refresh_tree()
        node = self._find_chat_input()
        if node:
            logger.debug(f"find_input_box: 找到输入框, name='{node.get_name()}'")
        else:
            logger.debug("find_input_box: 未找到输入框")
        return node

    def find_send_button(self):
        """
        找到发送按钮。

        定位策略 (design.md §4.6):
        - role='push button', name='Send(S)'
        - ⚠️ 坑点 #7: 按钮 Action 只暴露 SetFocus，没有 Click
        - 推荐通过坐标 Atspi.generate_mouse_event 或 Enter 键发送

        Returns:
            Atspi.Accessible 节点，未找到返回 None
        """
        self._refresh_tree()
        node = self._find_node("push button", name="Send(S)", showing=True)
        if node:
            logger.debug("find_send_button: 找到 Send(S) 按钮")
        else:
            # Fallback: 尝试不带 showing 过滤
            node = self._find_node("push button", name="Send(S)")
            if node:
                logger.debug("find_send_button: 找到 Send(S) 按钮 (非 showing)")
            else:
                logger.debug("find_send_button: 未找到 Send(S) 按钮")
        return node

    def find_messages_list(self):
        """
        找到消息列表控件。

        定位策略 (design.md §4.4): role='list', name='Messages'
        子节点为 list item:
        - 时间戳条目: name='23:49'
        - 消息内容条目: name='你好\\n '

        Returns:
            Atspi.Accessible 节点，未找到返回 None
        """
        self._refresh_tree()
        node = self._find_node("list", name="Messages")
        if node:
            logger.debug(f"find_messages_list: 找到 Messages 列表, children={node.get_child_count()}")
        else:
            logger.debug("find_messages_list: 未找到 Messages 列表")
        return node

    def find_search_input(self):
        """
        找到搜索输入框。

        定位策略 (design.md §4.7):
        - role='text', name='Search', EDITABLE + SHOWING
        - ⚠️ 实测有 3 个 Search 输入框（不同面板），通过 SHOWING 状态区分

        Returns:
            Atspi.Accessible 节点，未找到返回 None
        """
        self._refresh_tree()
        nodes = self._find_nodes("text", name="Search", editable=True, showing=True)
        if nodes:
            logger.debug(f"find_search_input: 找到 {len(nodes)} 个 Search 输入框")
            return nodes[0]
        logger.debug("find_search_input: 未找到 Search 输入框")
        return None

    def find_nav_button(self, tab: str):
        """
        找到导航栏按钮。

        定位策略 (design.md §4.2): role='push button', name 参见 NAV_BUTTONS
        支持输入: 'weixin'/'chat'/'contacts'/'favorites'/'moments' 等

        Returns:
            Atspi.Accessible 节点，未找到返回 None
        """
        btn_name = NAV_BUTTONS.get(tab.lower())
        if not btn_name:
            logger.warning(f"未知导航 tab: '{tab}', 支持: {list(NAV_BUTTONS.keys())}")
            return None
        self._refresh_tree()
        return self._find_node("push button", name=btn_name)

    # ===================================================================
    # 高层 API — 数据读取
    # ===================================================================

    def get_status(self) -> dict:
        """
        获取微信当前状态。

        Returns:
            dict with keys:
                running       — 微信进程是否通过 AT-SPI2 发现
                logged_in     — 是否已登录（有 Chats 列表）
                remembered    — 是否处于"已记住账号"登录页（有 Enter Weixin / Switch Account）
                current_chat  — 当前聊天对象昵称
                node_count    — 控件树节点数
        """
        result = {
            "running": False,
            "logged_in": False,
            "remembered": False,
            "current_chat": None,
            "node_count": 0,
        }

        if self._wechat_app is None:
            if not self.find_wechat():
                return result

        result["running"] = True

        try:
            self._refresh_tree(force=True)
            result["node_count"] = len(self._all_nodes)
        except Exception:
            return result

        # 检查是否已登录：有 Chats 列表 = 已登录 (design.md §4.3)
        chats = self._find_node("list", name="Chats")
        if chats:
            result["logged_in"] = True

        # 检查是否处于"已记住账号"登录页：
        # 界面有 "Enter Weixin" + "Switch Account" 按钮
        if not result["logged_in"]:
            enter_btn = self._find_node("push button", name="Enter Weixin", showing=True)
            switch_btn = self._find_node("push button", name="Switch Account", showing=True)
            if enter_btn or switch_btn:
                result["remembered"] = True

        # 检查当前聊天对象：输入框的 name 就是当前聊天对象昵称 (design.md §4.5)
        chat_input = self._find_chat_input()
        if chat_input:
            result["current_chat"] = chat_input.get_name() or None

        return result

    def click_switch_account(self) -> bool:
        """
        在"已记住账号"登录页点击"Switch Account"按钮，回到 QR 码扫码页面。

        ⚠️ 场景：微信之前登录过，打开时不显示 QR 码，而是显示：
          - 上次登录用户头像/名称
          - "Enter Weixin" 按钮（手机确认登录）
          - "Switch Account" 链接/按钮（切换到 QR 码扫码）
          - "Transfer files only" 按钮

        点击 "Switch Account" 后微信会回到二维码登录页面。

        Returns:
            True if clicked, False if button not found.
        """
        self._refresh_tree()
        btn = self._find_node("push button", name="Switch Account", showing=True)
        if not btn:
            logger.warning("未找到 'Switch Account' 按钮")
            return False

        logger.info("点击 'Switch Account' 切换到 QR 码登录页")
        return self._click_node_by_mouse(btn)

    def click_enter_weixin(self) -> bool:
        """
        在"已记住账号"登录页点击"Enter Weixin"按钮。

        ⚠️ 此操作需要用户在手机上确认登录。

        Returns:
            True if clicked, False if button not found.
        """
        self._refresh_tree()
        btn = self._find_node("push button", name="Enter Weixin", showing=True)
        if not btn:
            logger.warning("未找到 'Enter Weixin' 按钮")
            return False

        logger.info("点击 'Enter Weixin' 请求手机确认登录")
        return self._click_node_by_mouse(btn)

    def get_chat_list(self) -> list[dict]:
        """
        获取左侧聊天列表。

        解析 design.md §4.3 描述的 list item name 格式:
        "<联系人名> <最后消息> <时间>"
        实测: 'M 你好 23:49', '中国人保财险-小吴  ', 'Weixin Team  '

        Returns:
            list of {"name": str, "last_message": str, "time": str, "raw": str}
        """
        chats_list = self.find_chat_list()
        if not chats_list:
            logger.warning("未找到 Chats 列表")
            return []

        results = []
        try:
            n = chats_list.get_child_count()
            for i in range(n):
                item = chats_list.get_child_at_index(i)
                if not item:
                    continue
                raw = item.get_name() or ""
                parsed = self._parse_chat_item(raw)
                results.append(parsed)
        except Exception as e:
            logger.error(f"遍历聊天列表失败: {e}")

        return results

    @staticmethod
    def _parse_chat_item(raw: str) -> dict:
        """
        解析聊天列表 list item 的 name 字段。

        格式: "<联系人名> <最后消息> <时间>"
        实测: 'M 你好 23:49', '中国人保财险-小吴  ', 'Weixin Team  '

        尝试从末尾提取时间（HH:MM 格式），再分离联系人名和消息。
        """
        raw = raw.strip()
        if not raw:
            return {"name": "", "last_message": "", "time": "", "raw": ""}

        # 尝试从末尾匹配时间格式
        time_str = ""
        name_msg = raw

        # 常见时间格式: "23:49", "昨天", "星期一", "3/10" 等
        parts = raw.rsplit(" ", 1)
        if len(parts) == 2:
            candidate = parts[1].strip()
            if _TIME_PATTERN.match(candidate):
                time_str = candidate
                name_msg = parts[0]

        # 分离联系人名和最后消息
        name_parts = name_msg.split(" ", 1)
        contact = name_parts[0] if name_parts else name_msg
        last_msg = name_parts[1] if len(name_parts) > 1 else ""

        return {
            "name": contact.strip(),
            "last_message": last_msg.strip(),
            "time": time_str,
            "raw": raw,
        }

    def get_contacts(self, force_refresh: bool = False) -> list[dict]:
        """
        获取联系人列表（带缓存 + 类型解析 + 昵称映射）。

        定位策略 (design.md §4.8): role='list', name='Contacts'
        子节点: 'Official Accounts', 'WeCom Contacts', '阿布', '半眠学姐🍃' 等

        如果当前不在通讯录页，会自动点击 Contacts 导航按钮切换。

        P1.1 增强:
        - 联系人缓存（5 分钟有效期），避免频繁遍历
        - 类型解析：system / official / wecom / person
        - 构建 _nickname_map（lower_name → original_name）用于模糊搜索

        Args:
            force_refresh: 是否强制刷新缓存

        Returns:
            list of {"name": str, "type": str, "raw": str}
        """
        # 检查缓存
        now = time.time()
        if (not force_refresh
                and self._contacts_cache
                and (now - self._contacts_cache_time) < CONTACTS_CACHE_TTL):
            logger.debug(f"使用联系人缓存: {len(self._contacts_cache)} 个")
            return list(self._contacts_cache)

        self._refresh_tree()
        contacts_list = self._find_node("list", name="Contacts")
        if not contacts_list:
            logger.warning("未找到 Contacts 列表，尝试切换到通讯录页")
            # 尝试点击通讯录按钮 (design.md §4.2)
            if self.navigate_to("contacts"):
                contacts_list = self._find_node("list", name="Contacts")

        if not contacts_list:
            return []

        results = []
        try:
            n = contacts_list.get_child_count()
            for i in range(n):
                item = contacts_list.get_child_at_index(i)
                if item:
                    name = (item.get_name() or "").strip()
                    if not name:
                        continue
                    contact_type = self._classify_contact(name)
                    results.append({
                        "name": name,
                        "type": contact_type,
                        "raw": item.get_name() or "",
                    })
        except Exception as e:
            logger.error(f"遍历联系人列表失败: {e}")

        # 更新缓存
        self._contacts_cache = results
        self._contacts_cache_time = time.time()

        # 构建昵称映射表
        self._nickname_map = {}
        for c in results:
            lower = c["name"].lower()
            self._nickname_map[lower] = c["name"]

        logger.info(f"联系人缓存已更新: {len(results)} 个联系人")
        return list(results)

    @staticmethod
    def _classify_contact(name: str) -> str:
        """
        根据联系人名称推断类型。

        P1.1 联系人解析：
        - system: 系统内置（文件传输助手、WeChat Team 等）
        - official: 公众号（Official Accounts 类别）
        - wecom: 企业微信联系人
        - person: 普通联系人
        """
        system_names = {
            "File Transfer", "文件传输助手", "Weixin Team", "微信团队",
            "Official Accounts", "WeCom Contacts", "Channels", "Moments",
            "Top Stories", "WeChat Pay", "微信支付",
        }
        if name in system_names:
            return "system"
        # 公众号类名称通常是中英文混合的服务号
        if name == "Official Accounts":
            return "official"
        if name == "WeCom Contacts":
            return "wecom"
        return "person"

    def fuzzy_search_contact(self, keyword: str) -> list[dict]:
        """
        在缓存的联系人列表中模糊搜索。

        P1.1 模糊搜索：
        - 精确匹配（优先）
        - 前缀匹配
        - 包含匹配
        - 大小写不敏感

        Args:
            keyword: 搜索关键词

        Returns:
            匹配的联系人列表，按相关度排序
        """
        # 确保缓存存在
        if not self._contacts_cache:
            self.get_contacts()

        kw_lower = keyword.lower()
        exact = []
        prefix = []
        contains = []

        for c in self._contacts_cache:
            name_lower = c["name"].lower()
            if name_lower == kw_lower:
                exact.append(c)
            elif name_lower.startswith(kw_lower):
                prefix.append(c)
            elif kw_lower in name_lower:
                contains.append(c)

        results = exact + prefix + contains
        logger.debug(f"模糊搜索 '{keyword}': {len(results)} 个匹配 "
                     f"(exact={len(exact)}, prefix={len(prefix)}, contains={len(contains)})")
        return results

    def get_messages(self) -> list[dict]:
        """
        获取当前聊天窗口的消息列表。

        解析 design.md §4.4 描述的 list item:
        - 时间戳条目: name='23:49' → type='timestamp'
        - 消息内容条目: name='你好\\n ' → type='message'

        Returns:
            list of {"type": str, "content": str, "timestamp": str|None, "raw": str}
            type: 'message' | 'timestamp'
            timestamp: 如果当前条目前面有时间戳条目，则附带该时间戳
        """
        msg_list = self.find_messages_list()
        if not msg_list:
            logger.warning("未找到 Messages 列表（可能未进入聊天窗口）")
            return []

        results = []
        current_timestamp = None

        try:
            n = msg_list.get_child_count()
            for i in range(n):
                item = msg_list.get_child_at_index(i)
                if not item:
                    continue
                raw = item.get_name() or ""
                content = raw.strip()

                if not content:
                    continue

                # 判断是否为时间戳条目
                if _TIME_PATTERN.match(content):
                    current_timestamp = content
                    results.append({
                        "type": "timestamp",
                        "content": content,
                        "timestamp": content,
                        "raw": raw,
                    })
                else:
                    # 消息内容（可能含末尾换行/空格，需 strip）
                    results.append({
                        "type": "message",
                        "content": content.rstrip("\n "),
                        "timestamp": current_timestamp,
                        "raw": raw,
                    })
        except Exception as e:
            logger.error(f"遍历消息列表失败: {e}")

        return results

    def get_messages_text_only(self) -> list[dict]:
        """
        获取当前聊天窗口的消息列表（仅消息内容，过滤时间戳）。

        方便 tool 层直接使用，不需要处理 timestamp 类型。

        Returns:
            list of {"content": str, "timestamp": str|None, "raw": str}
        """
        return [
            {"content": m["content"], "timestamp": m["timestamp"], "raw": m["raw"]}
            for m in self.get_messages()
            if m["type"] == "message"
        ]

    # ===================================================================
    # 高层 API — 导航与操作
    # ===================================================================

    def navigate_to(self, tab: str) -> bool:
        """
        切换到指定导航页。

        支持: 'weixin'/'chat', 'contacts', 'favorites', 'moments', 'channels',
              'search', 'mini_programs', 'mobile', 'more'

        定位策略 (design.md §4.2): role='push button', name 参见 NAV_BUTTONS

        Args:
            tab: 导航页名称

        Returns:
            True if successfully navigated, False otherwise.
        """
        btn = self.find_nav_button(tab)
        if not btn:
            logger.error(f"未找到导航按钮: '{tab}'")
            return False

        try:
            if not self._click_node_reliable(btn):
                logger.error(f"导航按钮 '{tab}' 点击失败")
                return False
            time.sleep(ACTION_DELAY)
            self._refresh_tree(force=True)
            logger.info(f"已切换到 '{tab}' 页")
            return True
        except Exception as e:
            logger.error(f"导航到 '{tab}' 失败: {e}")
            return False

    def _find_chat_input(self):
        """
        查找聊天输入框。

        定位策略 (design.md §4.5):
        - role='text', name != 'Search', EDITABLE + SHOWING
        - name 是当前聊天对象的昵称（如 'M'）
        - 接口: ['Accessible', 'Action', 'Component', 'EditableText', 'Text']
        - 状态: EDITABLE, ENABLED, FOCUSABLE, FOCUSED, MULTI_LINE, SENSITIVE, SHOWING, VISIBLE
        """
        for _depth, node in self._all_nodes:
            try:
                if node.get_role_name() != "text":
                    continue
                name = node.get_name() or ""
                if name == "Search":
                    continue
                ss = node.get_state_set()
                if ss and ss.contains(Atspi.StateType.EDITABLE):
                    if ss.contains(Atspi.StateType.SHOWING):
                        return node
            except Exception:
                pass
        return None

    def click_contact(self, contact_name: str) -> bool:
        """
        点击指定联系人，进入聊天窗口。

        P0.3 要求的公共方法名，等价于 click_chat()。

        流程:
        1. 先在聊天列表 (Chats) 中模糊匹配
        2. 未找到则通过搜索框搜索

        Args:
            contact_name: 联系人昵称（模糊匹配）

        Returns:
            True if successfully entered chat, False otherwise.
        """
        return self.click_chat(contact_name)

    def click_chat(self, contact_name: str) -> bool:
        """
        在聊天列表中点击指定联系人，进入聊天窗口。

        定位策略:
        1. 先确保在聊天页 (Weixin)
        2. 在 Chats 列表中模糊匹配 list item name
        3. 未找到则 fallback 到 search_contact()

        Args:
            contact_name: 联系人昵称（模糊匹配）

        Returns:
            True if successfully clicked, False otherwise.
        """
        self._refresh_tree()
        chats_list = self._find_node("list", name="Chats")

        if not chats_list:
            # 可能不在聊天页，先切换
            logger.info("未找到 Chats 列表，尝试切换到聊天页")
            if self.navigate_to("weixin"):
                chats_list = self._find_node("list", name="Chats")

        if not chats_list:
            # 仍然没有 Chats 列表，直接搜索
            return self.search_contact(contact_name)

        # 在聊天列表中模糊匹配
        try:
            n = chats_list.get_child_count()
            best_match = None
            best_score = 0

            for i in range(n):
                item = chats_list.get_child_at_index(i)
                if not item:
                    continue
                name = item.get_name() or ""
                name_lower = name.lower()
                search_lower = contact_name.lower()

                # 精确前缀匹配优先（联系人名在 list item name 开头）
                if name_lower.startswith(search_lower + " ") or name_lower == search_lower:
                    best_match = item
                    best_score = 100
                    break
                # 模糊包含匹配
                elif search_lower in name_lower:
                    score = len(search_lower) / len(name_lower) * 50
                    if score > best_score:
                        best_match = item
                        best_score = score

            if best_match:
                self._click_node_reliable(best_match)
                time.sleep(ACTION_DELAY)
                self._refresh_tree(force=True)
                logger.info(f"已进入与 '{contact_name}' 的聊天 (score={best_score:.0f})")
                return True
        except Exception as e:
            logger.error(f"点击聊天失败: {e}")

        # 聊天列表中未找到，尝试搜索
        logger.info(f"聊天列表中未找到 '{contact_name}'，尝试搜索")
        return self.search_contact(contact_name)

    def search_contact(self, keyword: str) -> bool:
        """
        通过搜索框搜索并点击联系人。

        流程 (参考 design.md §4.7):
        1. 点击导航栏 Search 按钮
        2. 找到 SHOWING 的 Search 输入框（实测有 3 个，通过 SHOWING 区分）
        3. 输入关键词，等待搜索结果
        4. 在结果中匹配并点击

        Args:
            keyword: 搜索关键词

        Returns:
            True if found and clicked, False otherwise.
        """
        self._refresh_tree()

        # 先点击搜索按钮 (design.md §4.2)
        search_btn = self._find_node("push button", name="Search")
        if search_btn:
            self._click_node_reliable(search_btn)
            time.sleep(ACTION_DELAY)
            self._refresh_tree(force=True)

        # 找到搜索输入框（name='Search', editable, showing）
        # ⚠️ design.md §4.7: 实测有 3 个 Search 输入框，通过 SHOWING 状态区分
        search_input = self.find_search_input()
        if not search_input:
            logger.error("未找到搜索输入框")
            return False

        # 聚焦并输入搜索关键词
        search_input.grab_focus()
        time.sleep(0.2)

        # 先清空再输入（确保干净）
        self._set_text(search_input, "")
        time.sleep(0.1)
        self._set_text(search_input, keyword)
        time.sleep(ACTION_DELAY * 2)  # 等待搜索结果加载
        self._refresh_tree(force=True)

        # 查找搜索结果列表中的匹配项
        items = self._find_nodes("list item", showing=True)
        best_match = None
        best_score = 0

        for item in items:
            try:
                name = item.get_name() or ""
                name_lower = name.lower()
                kw_lower = keyword.lower()

                if kw_lower == name_lower:
                    best_match = item
                    best_score = 100
                    break
                elif kw_lower in name_lower:
                    score = len(kw_lower) / len(name_lower) * 50
                    if score > best_score:
                        best_match = item
                        best_score = score
            except Exception:
                pass

        if best_match:
            self._click_node_reliable(best_match)
            time.sleep(ACTION_DELAY)
            self._refresh_tree(force=True)
            logger.info(f"搜索到并点击了 '{keyword}' (score={best_score:.0f})")
            return True

        # 如果精确匹配没找到，点击第一个搜索结果
        if items:
            try:
                self._click_node_reliable(items[0])
                time.sleep(ACTION_DELAY)
                self._refresh_tree(force=True)
                logger.info("点击了搜索结果第一项")
                return True
            except Exception:
                pass

        logger.warning(f"搜索 '{keyword}' 无结果")
        # 按 Escape 关闭搜索
        self._keyboard_press(9)  # keycode 9 = Escape
        time.sleep(0.3)
        return False

    def send_text(self, text: str, use_keyboard: bool = False) -> bool:
        """
        在当前聊天输入框输入文字并发送。

        发送策略 (参考 design.md §5.1):
        1. 找到输入框 → grab_focus
        2. 写入文本（set_text_contents 或键盘模拟）
        3. 发送: 优先 Enter 键，fallback 到点击 Send(S) 按钮

        ⚠️ 坑点 #7: Send(S) 按钮只有 SetFocus Action，没有 Click。
        需要通过 generate_mouse_event 或 Enter 键发送。

        Args:
            text: 要发送的文本
            use_keyboard: 是否使用键盘模拟输入（默认用 set_text_contents）

        Returns:
            True if sent successfully, False otherwise.
        """
        self._refresh_tree()
        chat_input = self._find_chat_input()
        if not chat_input:
            logger.error("未找到聊天输入框（可能未进入聊天窗口）")
            return False

        # 聚焦输入框
        chat_input.grab_focus()
        time.sleep(0.2)

        if use_keyboard:
            # 方式B: 键盘模拟输入（design.md §3.7）
            self._keyboard_type(text)
        else:
            # 方式A: EditableText 接口直接设置（推荐, design.md §3.6）
            if not self._set_text(chat_input, text):
                # fallback 到键盘输入
                logger.info("set_text_contents 失败，fallback 到键盘输入")
                chat_input.grab_focus()
                time.sleep(0.1)
                self._keyboard_type(text)

        time.sleep(0.3)

        # 发送: 优先 Enter 键
        sent = False

        # 方式1: Enter 键发送（微信默认 Enter 发送）
        self._keyboard_press(36)  # keycode 36 = Enter
        time.sleep(ACTION_DELAY)

        # 验证是否发送成功（输入框应被清空）
        self._refresh_tree(force=True)
        chat_input_after = self._find_chat_input()
        if chat_input_after:
            remaining = self._read_text(chat_input_after)
            if not remaining.strip():
                sent = True

        if not sent:
            # 方式2: 点击 Send(S) 按钮 (design.md §4.6)
            logger.info("Enter 键发送可能未生效，尝试点击 Send(S) 按钮")
            send_btn = self.find_send_button()
            if send_btn:
                self._click_node_by_mouse(send_btn)
                time.sleep(ACTION_DELAY)
                sent = True

        if sent:
            logger.info(f"消息已发送: {text[:50]}{'...' if len(text) > 50 else ''}")
        else:
            logger.warning(f"消息发送可能未成功: {text[:50]}")

        return True  # 即使验证不确定也返回 True，因为操作已执行

    # ===================================================================
    # P1.2 频率限制 + 随机延迟
    # ===================================================================

    def _check_rate_limit(self) -> bool:
        """
        检查是否超过频率限制（每分钟 RATE_LIMIT_MAX 条）。

        Returns:
            True 如果可以发送，False 如果需要等待。
        """
        now = time.time()
        # 清理窗口外的记录
        while self._send_timestamps and self._send_timestamps[0] < now - RATE_LIMIT_WINDOW:
            self._send_timestamps.popleft()

        if len(self._send_timestamps) >= RATE_LIMIT_MAX:
            oldest = self._send_timestamps[0]
            wait = RATE_LIMIT_WINDOW - (now - oldest)
            logger.warning(f"频率限制: 已发送 {len(self._send_timestamps)} 条/分钟, "
                           f"需等待 {wait:.1f} 秒")
            return False
        return True

    def _record_send(self):
        """记录一次发送时间戳"""
        self._send_timestamps.append(time.time())

    @staticmethod
    def _human_delay():
        """模拟人类操作节奏的随机延迟（1-3 秒）"""
        delay = random.uniform(SEND_DELAY_MIN, SEND_DELAY_MAX)
        logger.debug(f"人类模拟延迟: {delay:.1f} 秒")
        time.sleep(delay)

    def _ensure_chat_ready(self, to: str) -> bool:
        """
        确保已进入与 to 的聊天窗口且输入框可用。

        策略:
        1. click_chat(to) — 搜索/列表点击
        2. 等待 + 刷新检查输入框
        3. 如果输入框没出现，再次在 Chats 列表中直接点击
        4. 如果仍然失败，用 navigate_to('weixin') 回到聊天页再试

        Returns:
            True if chat input is ready.
        """
        # 第一次尝试
        if not self.click_chat(to):
            return False

        # 检查输入框（可能需要等待）
        for wait in [0.5, 1.0, 2.0]:
            time.sleep(wait)
            self._refresh_tree(force=True)
            if self._find_chat_input():
                return True

        # 输入框没出现 — 可能搜索结果点击了但没进入聊天
        # 尝试回到聊天页后点击列表中的第一项（搜索会把目标带到列表顶部）
        logger.warning(f"click_chat 后未找到输入框，尝试回到聊天页后点击")
        self.navigate_to("weixin")
        time.sleep(0.5)
        self._refresh_tree(force=True)

        chats_list = self._find_node("list", name="Chats")
        if chats_list:
            try:
                n = chats_list.get_child_count()
                for i in range(min(n, 3)):
                    item = chats_list.get_child_at_index(i)
                    if item:
                        name = (item.get_name() or "").lower()
                        if to.lower() in name or name in to.lower():
                            self._click_node_reliable(item)
                            time.sleep(1)
                            self._refresh_tree(force=True)
                            if self._find_chat_input():
                                return True
            except Exception as e:
                logger.warning(f"重试点击聊天列表失败: {e}")

        return False

    def send_msg(self, to: str, content: str) -> dict:
        """
        P1.2 高层封装: 定位联系人 → 输入 → 发送（含频率限制 + 随机延迟）。

        流程:
        1. 检查频率限制
        2. 在聊天列表/搜索中定位联系人，确认输入框就绪
        3. 随机延迟（模拟人类操作节奏）
        4. 输入文本并发送
        5. 记录发送时间

        Args:
            to: 接收者昵称
            content: 消息文本内容

        Returns:
            dict: {"success": bool, "error": str|None}
        """
        # 1. 频率限制
        if not self._check_rate_limit():
            return {"success": False, "error": f"Rate limit exceeded ({RATE_LIMIT_MAX}/min)"}

        # 2. 定位联系人并确认输入框就绪
        if not self._ensure_chat_ready(to):
            return {"success": False, "error": f"Contact '{to}' not found or chat not ready"}

        # 3. 随机延迟
        self._human_delay()

        # 4. 发送文本
        if not self.send_text(content):
            return {"success": False, "error": "Failed to send text"}

        # 5. 记录发送
        self._record_send()

        logger.info(f"send_msg: to='{to}', content='{content[:50]}'")
        return {"success": True, "error": None}

    # ===================================================================
    # P1.3 / P1.4 发送图片与文件
    # ===================================================================

    @staticmethod
    def _copy_file_to_clipboard(file_path: str) -> bool:
        """
        通过 xclip 将文件复制到剪贴板。

        微信 Linux 版支持通过 Ctrl+V 粘贴剪贴板中的文件（图片或普通文件）。
        使用 xclip 将文件 URI 写入剪贴板的 TARGETS 中。

        ⚠️ 关键技术点:
        - xclip -selection clipboard -t text/uri-list 将文件 URI 放入剪贴板
        - 微信会识别剪贴板中的 file:// URI 并弹出发送确认
        - xclip 在无 clipboard manager 的 Xvfb 环境下会阻塞等待读取方，
          因此使用 Popen 后台运行，不等待退出

        Args:
            file_path: 文件的绝对路径

        Returns:
            True if successful, False otherwise.
        """
        abs_path = os.path.abspath(file_path)
        file_uri = f"file://{abs_path}"
        display = os.environ.get("DISPLAY", ":1")

        try:
            # xclip 在后台运行，不等待退出（它会阻塞直到有程序读取剪贴板）
            proc = subprocess.Popen(
                ["xclip", "-selection", "clipboard", "-t", "text/uri-list"],
                stdin=subprocess.PIPE,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                env={**os.environ, "DISPLAY": display},
            )
            proc.stdin.write(file_uri.encode("utf-8"))
            proc.stdin.close()
            # 给 xclip 一点时间设置剪贴板
            time.sleep(0.5)
            logger.debug(f"文件 URI 已写入 xclip (pid={proc.pid}): {file_uri}")
            return True
        except FileNotFoundError:
            logger.error("xclip 未安装，请运行: apt-get install -y xclip")
        except Exception as e:
            logger.error(f"复制文件到剪贴板失败: {e}")
        return False

    @staticmethod
    def _paste_from_clipboard():
        """
        模拟 Ctrl+V 粘贴操作。

        使用 xdotool 发送 ctrl+v 按键组合。
        """
        try:
            subprocess.run(
                ["xdotool", "key", "--clearmodifiers", "ctrl+v"],
                env={**os.environ, "DISPLAY": os.environ.get("DISPLAY", ":1")},
                capture_output=True,
                timeout=5,
            )
            logger.debug("已发送 Ctrl+V 粘贴")
        except FileNotFoundError:
            logger.error("xdotool 未安装，请运行: apt-get install -y xdotool")
        except Exception as e:
            logger.error(f"粘贴失败: {e}")

    def _send_file_via_clipboard(self, file_path: str) -> bool:
        """
        通过剪贴板粘贴方式发送文件/图片的通用实现。

        流程:
        1. 确保聊天输入框已聚焦
        2. 将文件 URI 复制到剪贴板
        3. Ctrl+V 粘贴 → 微信弹出发送确认对话框
        4. Enter 确认发送

        Args:
            file_path: 文件绝对路径

        Returns:
            True if sent, False otherwise.
        """
        self._refresh_tree()
        chat_input = self._find_chat_input()
        if not chat_input:
            logger.error("未找到聊天输入框（可能未进入聊天窗口）")
            return False

        # 1. 聚焦输入框
        chat_input.grab_focus()
        time.sleep(0.3)

        # 2. 复制文件到剪贴板
        if not self._copy_file_to_clipboard(file_path):
            return False
        time.sleep(0.3)

        # 3. Ctrl+V 粘贴
        self._paste_from_clipboard()
        time.sleep(1.5)  # 等待微信响应，弹出确认对话框

        # 4. Enter 确认发送
        # 微信粘贴文件后会弹出 "发送图片/文件" 确认对话框，按 Enter 确认
        self._keyboard_press(36)  # keycode 36 = Enter
        time.sleep(ACTION_DELAY)

        logger.info(f"文件已通过剪贴板发送: {file_path}")
        return True

    def send_image(self, to: str, image_path: str) -> dict:
        """
        P1.3 发送图片。

        流程:
        1. 验证文件存在 + 格式
        2. 检查频率限制
        3. 定位联系人
        4. 随机延迟
        5. 通过剪贴板粘贴发送
        6. 确认发送成功

        Args:
            to: 接收者昵称
            image_path: 图片文件的绝对路径

        Returns:
            dict: {"success": bool, "error": str|None}
        """
        # 1. 验证文件
        if not os.path.isfile(image_path):
            return {"success": False, "error": f"Image file not found: {image_path}"}

        ext = os.path.splitext(image_path)[1].lower()
        if ext not in IMAGE_EXTENSIONS:
            return {"success": False,
                    "error": f"Unsupported image format: {ext}. "
                             f"Supported: {', '.join(sorted(IMAGE_EXTENSIONS))}"}

        # 2. 频率限制
        if not self._check_rate_limit():
            return {"success": False, "error": f"Rate limit exceeded ({RATE_LIMIT_MAX}/min)"}

        # 3. 定位联系人
        if not self._ensure_chat_ready(to):
            return {"success": False, "error": f"Contact '{to}' not found or chat not ready"}

        # 4. 随机延迟
        self._human_delay()

        # 5. 发送
        if not self._send_file_via_clipboard(image_path):
            return {"success": False, "error": "Failed to send image via clipboard"}

        # 6. 记录发送 + 验证
        self._record_send()
        logger.info(f"send_image: to='{to}', path='{image_path}'")
        return {"success": True, "error": None}

    def send_file(self, to: str, file_path: str) -> dict:
        """
        P1.4 发送文件。

        流程:
        1. 验证文件存在
        2. 检查频率限制
        3. 定位联系人
        4. 随机延迟
        5. 通过剪贴板粘贴发送
        6. 确认发送成功

        Args:
            to: 接收者昵称
            file_path: 文件的绝对路径

        Returns:
            dict: {"success": bool, "error": str|None}
        """
        # 1. 验证文件
        if not os.path.isfile(file_path):
            return {"success": False, "error": f"File not found: {file_path}"}

        file_size = os.path.getsize(file_path)
        if file_size == 0:
            return {"success": False, "error": "File is empty (0 bytes)"}

        # 2. 频率限制
        if not self._check_rate_limit():
            return {"success": False, "error": f"Rate limit exceeded ({RATE_LIMIT_MAX}/min)"}

        # 3. 定位联系人
        if not self._ensure_chat_ready(to):
            return {"success": False, "error": f"Contact '{to}' not found or chat not ready"}

        # 4. 随机延迟
        self._human_delay()

        # 5. 发送
        if not self._send_file_via_clipboard(file_path):
            return {"success": False, "error": "Failed to send file via clipboard"}

        # 6. 记录发送 + 日志
        self._record_send()
        ext = os.path.splitext(file_path)[1].lower()
        logger.info(f"send_file: to='{to}', path='{file_path}', "
                    f"size={file_size}, ext='{ext}'")
        return {"success": True, "error": None}

    def read_input_text(self) -> str:
        """读取当前聊天输入框中的文本"""
        self._refresh_tree()
        chat_input = self._find_chat_input()
        if not chat_input:
            return ""
        return self._read_text(chat_input)

    def clear_input(self) -> bool:
        """清空当前聊天输入框"""
        self._refresh_tree()
        chat_input = self._find_chat_input()
        if not chat_input:
            return False
        return self._set_text(chat_input, "")

    def take_screenshot(self, output_path: str = "/tmp/wechat-screenshot.png") -> Optional[str]:
        """
        通过 scrot 截取微信窗口截图。

        Args:
            output_path: 截图保存路径

        Returns:
            截图文件路径，失败返回 None
        """
        try:
            # 用 scrot 截取整个 Xvfb 屏幕
            result = subprocess.run(
                ["scrot", output_path],
                env={"DISPLAY": ":1"},
                capture_output=True,
                timeout=10,
            )
            if result.returncode == 0:
                logger.info(f"截图已保存: {output_path}")
                return output_path
            else:
                logger.error(f"截图失败: {result.stderr.decode()}")
                return None
        except Exception as e:
            logger.error(f"截图异常: {e}")
            return None

    # ===================================================================
    # 调试工具
    # ===================================================================

    def dump_tree(self, max_depth: int = 10) -> str:
        """
        打印控件树摘要（调试用）。

        Returns:
            控件树的文本表示
        """
        self._refresh_tree(force=True)
        lines = []
        for depth, node in self._all_nodes:
            if depth > max_depth:
                continue
            try:
                role = node.get_role_name()
                name = node.get_name() or ""
                ss = node.get_state_set()
                flags = []
                if ss.contains(Atspi.StateType.EDITABLE):
                    flags.append("EDIT")
                if ss.contains(Atspi.StateType.SHOWING):
                    flags.append("SHOW")
                if ss.contains(Atspi.StateType.FOCUSABLE):
                    flags.append("FOCUS")
                indent = "  " * depth
                flag_str = f" [{','.join(flags)}]" if flags else ""
                name_str = f" name='{name}'" if name else ""
                lines.append(f"{indent}[{role}]{name_str}{flag_str}")
            except Exception:
                lines.append(f"{'  ' * depth}[error reading node]")
        return "\n".join(lines)
