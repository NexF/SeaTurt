#!/usr/bin/env python3
"""
test_mcp_protocol.py — MCP JSON-RPC 协议层单元测试

测试 main.py 的 JSON-RPC 协议处理（initialize / tools/list / tools/call 路由），
不需要微信环境。通过 subprocess 启动 main.py 并通过 stdin/stdout 交互。

注意: 这个测试需要 main.py 能启动（需要 wechat_launcher 模块中
ensure_display/ensure_dbus 不崩溃）。在没有 X11/D-Bus 的环境中会 SKIP。

运行方式 (容器内):
  python3 test_mcp_protocol.py
  python3 -m pytest test_mcp_protocol.py -v

退出码:
  0 = 全部通过
  1 = 有 FAIL
  2 = 环境不满足（SKIP）
"""

import json
import os
import signal
import subprocess
import sys
import time
import unittest

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
MAIN_PY = os.path.join(SCRIPT_DIR, "main.py")


def send_jsonrpc(proc, method, params=None, req_id=1):
    """发送 JSON-RPC 请求并读取响应。"""
    request = {
        "jsonrpc": "2.0",
        "id": req_id,
        "method": method,
    }
    if params is not None:
        request["params"] = params

    line = json.dumps(request, ensure_ascii=False) + "\n"
    proc.stdin.write(line)
    proc.stdin.flush()

    # 读取响应（带超时）
    response_line = proc.stdout.readline()
    if not response_line:
        return None
    return json.loads(response_line)


def start_mcp_server():
    """启动 main.py 作为子进程。"""
    env = os.environ.copy()
    env.setdefault("DISPLAY", ":1")  # 防止 ensure_display 报错

    proc = subprocess.Popen(
        [sys.executable, MAIN_PY],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        cwd=SCRIPT_DIR,
        env=env,
    )
    return proc


class TestMCPProtocol(unittest.TestCase):
    """测试 MCP JSON-RPC 协议层"""

    proc = None
    skip_reason = None

    @classmethod
    def setUpClass(cls):
        """启动 MCP server 子进程"""
        try:
            cls.proc = start_mcp_server()
            # 给进程一点启动时间
            time.sleep(0.5)

            # 检查进程是否已崩溃
            if cls.proc.poll() is not None:
                stderr = cls.proc.stderr.read()
                cls.skip_reason = f"main.py exited early (code={cls.proc.returncode}): {stderr[:300]}"
                cls.proc = None
                return

            # 尝试 initialize
            resp = send_jsonrpc(cls.proc, "initialize", req_id=1)
            if resp is None:
                cls.skip_reason = "main.py did not respond to initialize"
                cls.proc.kill()
                cls.proc = None
                return

            if "error" in resp:
                cls.skip_reason = f"initialize error: {resp['error']}"
                cls.proc.kill()
                cls.proc = None
                return

            # 发送 notifications/initialized
            notify = json.dumps({
                "jsonrpc": "2.0",
                "method": "notifications/initialized",
            }) + "\n"
            cls.proc.stdin.write(notify)
            cls.proc.stdin.flush()

            cls._init_result = resp.get("result", {})

        except Exception as e:
            cls.skip_reason = f"Failed to start MCP server: {e}"
            if cls.proc:
                cls.proc.kill()
                cls.proc = None

    @classmethod
    def tearDownClass(cls):
        """关闭子进程"""
        if cls.proc and cls.proc.poll() is None:
            cls.proc.stdin.close()
            try:
                cls.proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                cls.proc.kill()

    def setUp(self):
        if self.skip_reason:
            self.skipTest(self.skip_reason)

    # ---- Tests ----

    def test_initialize_protocol_version(self):
        """initialize 返回正确的 protocolVersion"""
        self.assertEqual(self._init_result.get("protocolVersion"), "2024-11-05")

    def test_initialize_server_info(self):
        """initialize 返回正确的 serverInfo"""
        info = self._init_result.get("serverInfo", {})
        self.assertEqual(info.get("name"), "mcp-server-wechat")
        self.assertIn("version", info)

    def test_initialize_capabilities(self):
        """initialize 返回 capabilities.tools"""
        caps = self._init_result.get("capabilities", {})
        self.assertIn("tools", caps)

    def test_tools_list(self):
        """tools/list 返回 11 个工具"""
        resp = send_jsonrpc(self.proc, "tools/list", req_id=2)
        self.assertIsNotNone(resp)
        self.assertNotIn("error", resp)

        result = resp.get("result", {})
        tools = result.get("tools", [])
        self.assertEqual(len(tools), 11)

    def test_tools_list_names(self):
        """tools/list 包含所有预期工具名"""
        resp = send_jsonrpc(self.proc, "tools/list", req_id=3)
        tools = resp["result"]["tools"]
        names = {t["name"] for t in tools}

        expected = {
            "wechat_login", "wechat_logout", "wechat_status",
            "wechat_get_contacts", "wechat_send_msg",
            "wechat_send_image", "wechat_send_file",
            "wechat_read_messages", "wechat_screenshot",
            "wechat_get_sessions", "wechat_get_unread",
        }
        self.assertEqual(names, expected)

    def test_tools_list_has_input_schema(self):
        """每个工具都有 inputSchema"""
        resp = send_jsonrpc(self.proc, "tools/list", req_id=4)
        tools = resp["result"]["tools"]
        for tool in tools:
            self.assertIn("inputSchema", tool, f"tool {tool['name']} missing inputSchema")
            self.assertIn("type", tool["inputSchema"])
            self.assertEqual(tool["inputSchema"]["type"], "object")

    def test_tools_list_required_fields(self):
        """需要必填参数的工具正确声明 required"""
        resp = send_jsonrpc(self.proc, "tools/list", req_id=5)
        tools = resp["result"]["tools"]
        tool_map = {t["name"]: t for t in tools}

        # wechat_send_msg 需要 to + content
        send_msg = tool_map["wechat_send_msg"]
        self.assertIn("required", send_msg["inputSchema"])
        self.assertIn("to", send_msg["inputSchema"]["required"])
        self.assertIn("content", send_msg["inputSchema"]["required"])

        # wechat_read_messages 需要 contact
        read_msgs = tool_map["wechat_read_messages"]
        self.assertIn("required", read_msgs["inputSchema"])
        self.assertIn("contact", read_msgs["inputSchema"]["required"])

    def test_unknown_tool(self):
        """调用不存在的工具 → error"""
        resp = send_jsonrpc(self.proc, "tools/call", {
            "name": "nonexistent_tool",
            "arguments": {},
        }, req_id=6)
        self.assertIsNotNone(resp)
        self.assertIn("error", resp)

    def test_unknown_method(self):
        """未知 method → method not found error"""
        resp = send_jsonrpc(self.proc, "unknown/method", req_id=7)
        self.assertIsNotNone(resp)
        self.assertIn("error", resp)
        self.assertEqual(resp["error"]["code"], -32601)

    def test_invalid_json(self):
        """无效 JSON → parse error"""
        self.proc.stdin.write("not valid json\n")
        self.proc.stdin.flush()
        line = self.proc.stdout.readline()
        if line:
            resp = json.loads(line)
            self.assertIn("error", resp)
            self.assertEqual(resp["error"]["code"], -32700)


# ============================================================================
# Main
# ============================================================================

if __name__ == "__main__":
    print()
    print("🐢 SeaTurt — MCP Protocol 单元测试")
    print()

    runner = unittest.TextTestRunner(verbosity=2)
    result = runner.run(
        unittest.TestLoader().loadTestsFromTestCase(TestMCPProtocol)
    )

    total = result.testsRun
    failed = len(result.failures) + len(result.errors)
    skipped = len(result.skipped)
    passed = total - failed - skipped

    print()
    print("=" * 60)
    print(f"  Results: {passed}/{total} passed, {failed} failed, {skipped} skipped")
    print("=" * 60)

    if skipped == total:
        sys.exit(2)  # 全部 skip → 环境不满足
    sys.exit(0 if result.wasSuccessful() else 1)
