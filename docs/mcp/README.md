# MCP Server 文档

Seaturt 内置了 4 个 MCP Server，运行在 Agent 的 Docker 容器内，为 Agent 提供各种能力。

## 架构概览

所有 MCP Server 遵循 [MCP 协议](https://modelcontextprotocol.io/)（JSON-RPC 2.0 over stdio），采用**无状态架构**：每次 tool call 通过 `docker exec` 启动一个新进程，完成一次完整的 MCP 交互后退出（browser 除外，它有 daemon 模式）。

```
Agent Loop
  → Router 解析 tool 名称前缀（如 core-shell_exec → core + shell_exec）
  → Executor 通过 docker exec 启动对应 MCP Server 进程
  → MCP Server 处理 tool call 并返回结果
  → 进程退出
```

## MCP Server 列表

| MCP Server | 语言 | 默认启用 | 说明 | 文档 |
|------------|------|---------|------|------|
| **mcp-server-core** | Go | ✅ 是 | 基础 Shell/文件操作 | [core.md](./core.md) |
| **mcp-server-desktop** | Go | ✅ 是（桌面 Agent） | X11 桌面 GUI 操作 | [desktop.md](./desktop.md) |
| **mcp-server-browser** | Go + Node.js | ✅ 是（桌面 Agent） | Chromium 浏览器自动化 | [browser.md](./browser.md) |
| **mcp-server-search** | Python | ✅ 是 | 网页搜索与内容抓取 | [search.md](./search.md) |

## 通用协议

所有 MCP Server 实现统一的 JSON-RPC 2.0 方法：

| 方法 | 说明 |
|------|------|
| `initialize` | 握手，返回 server 信息和能力 |
| `notifications/initialized` | 客户端就绪通知，无需响应 |
| `tools/list` | 返回该 server 提供的所有 tool 定义 |
| `tools/call` | 执行某个 tool，返回结果 |

## Tool 名称约定

LLM 看到的 tool 名称格式为 `{mcpname}-{toolname}`，例如：
- `core-shell_exec` — core server 的 shell_exec tool
- `desktop-screenshot` — desktop server 的 screenshot tool
- `browser-open_browser` — browser server 的 open_browser tool
- `search-web_search` — search server 的 web_search tool

Router 通过前缀将请求路由到对应的 MCP Server。

## 源码位置

```
seaturt-server/docker/sandbox/mcp-servers/
├── core/          # mcp-server-core (Go)
├── desktop/       # mcp-server-desktop (Go)
├── browser/       # mcp-server-browser (Go proxy + Node.js daemon)
└── search/        # mcp-server-search (Python, PyInstaller 打包)
```
