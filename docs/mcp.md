# MCP 通信

## 概述

每个 Agent 容器内可运行多个 MCP Server，Server 端通过 `docker exec` + stdio 建立连接，使用 JSON-RPC 协议通信。

## 核心功能

### Agent 生命周期管理

| 操作 | 说明 | 对应容器操作 |
|------|------|-------------|
| **创建** | 创建新 Agent，分配独立容器和 workspace | `docker create` + `docker start` |
| **运行** | Agent 在容器内执行任务 | 容器内进程执行 |
| **停止** | 暂停 Agent，保留容器和数据 | `docker stop` |
| **恢复** | 从停止状态恢复 Agent | `docker start` |
| **删除** | 销毁 Agent，删除容器（可选保留 workspace） | `docker rm` |

### Agent 对话与任务执行

- 用户通过 API/CLI/Web UI 与 Agent 对话
- LLM 看到的是该 Agent 所有 MCP Server 汇总的完整 tools 列表
- LLM 根据对话内容决定调用哪些 Tool
- Tool Router 将 tool_call 路由到对应的 MCP Server 执行
- 所有操作均在容器沙箱内完成，不影响宿主机
- Tool 执行结果自动喂回 LLM，形成 Agent Loop 直到任务完成

## 内置 MCP Server：`mcp-server-core`

所有 Agent 默认启用，提供基础操作能力：

| Tool | 说明 | 参数 | 返回类型 |
|------|------|------|---------|
| `shell_exec` | 执行 Shell 命令 | `command: string` | text |
| `file_read` | 读取文件内容 | `path: string` | text / image（二进制图片文件自动返回 image 类型） |
| `file_write` | 写入/创建文件 | `path: string, content: string` | text |
| `file_list` | 列出目录内容 | `path: string` | text |

## 可选 MCP Server

镜像内预装，按需启用：

| MCP Server | 说明 | 提供的 Tools |
|------------|------|-------------|
| `mcp-server-browser` | 浏览器操作（基于 Playwright） | `browse_url`, `screenshot`, `click` 等 |
| `mcp-server-db` | 数据库操作 | `db_query`, `db_schema` 等 |
| `mcp-server-git` | Git 操作 | `git_status`, `git_commit`, `git_diff` 等 |
| `mcp-server-desktop` | 桌面 GUI 操作（v0.0.3） | `screenshot`, `mouse_click`, `keyboard_type` 等 |

## MCP Server：`mcp-server-desktop`（v0.0.3）

桌面 Agent（`desktop: true`）自动启用，提供 GUI 桌面操作能力。依赖容器内的 Xvfb + GNOME 桌面环境。

**仅在 `seaturt/sandbox-desktop:latest` 镜像中可用。**

| Tool | 说明 | 参数 | 返回类型 |
|------|------|------|---------|
| `screenshot` | 桌面全屏截图 | `region?: {x, y, width, height}` | image (PNG base64) |
| `mouse_click` | 模拟鼠标点击 | `x: int, y: int, button?: "left"\|"right"\|"middle"` | text |
| `mouse_move` | 移动鼠标 | `x: int, y: int` | text |
| `mouse_drag` | 鼠标拖拽 | `from_x, from_y, to_x, to_y: int` | text |
| `keyboard_type` | 模拟键盘输入文字 | `text: string` | text |
| `keyboard_key` | 模拟按键/组合键 | `key: string` (如 "Return", "ctrl+c") | text |
| `window_list` | 列出桌面窗口 | 无 | text |
| `window_focus` | 聚焦指定窗口 | `window_id?: string, title?: string` | text |
| `open_app` | 打开应用程序 | `app: string` (如 "firefox", "gnome-terminal") | text |
| `desktop_wait` | 等待渲染稳定后截屏 | `delay_ms?: int` (默认 1000，上限 10000) | image (PNG base64) |

实现依赖：`xdotool`（鼠标/键盘）、`wmctrl`（窗口管理）、`import`（ImageMagick 截屏）。

### 截屏与多模态联动

`screenshot` / `desktop_wait` 返回 `image` 类型 ToolContent，通过 Agent Loop 的 `formatToolResult()` 自动转为 `llm.ContentBlock{Type:"image"}`，传给 LLM——**完全复用 v0.0.2 的多模态通道**。

```
Agent 调用 screenshot
  → mcp-server-desktop 执行截屏（scrot/import）
  → 返回 ToolContent{Type:"image", Data:"base64...", MimeType:"image/png"}
  → Agent Loop formatToolResult() → ContentBlock{Type:"image"}
  → LLM 接收图片，理解屏幕内容
  → LLM 决定下一步操作（如 mouse_click 某个按钮）
```

## MCP Server 清单

| MCP Server | 路径 | 默认启用 | 说明 |
|------------|------|---------|------|
| `mcp-server-core` | `/usr/local/bin/mcp-server-core` | 是 | 基础 shell/file 操作 |
| `mcp-server-browser` | `/usr/local/bin/mcp-server-browser` | 否 | 浏览器自动化 |
| `mcp-server-db` | `/usr/local/bin/mcp-server-db` | 否 | 数据库操作 |
| `mcp-server-git` | `/usr/local/bin/mcp-server-git` | 否 | Git 版本控制 |
| `mcp-server-desktop` | `/usr/local/bin/mcp-server-desktop` | `desktop: true` 时自动启用 | 桌面 GUI 操作（v0.0.3） |

每个 MCP Server 均为独立二进制，通过 stdio 通信。Server 端使用 `docker exec <container> <mcp-server-xxx>` 按需建立连接。

## 多 MCP Server 工作机制

```
Agent 创建时：
  1. 读取 config.mcp_servers 配置
  2. 为每个 MCP Server 建立独立连接：
     docker exec <container> mcp-server-core     → MCP Client #1
     docker exec <container> mcp-server-browser   → MCP Client #2
  3. 调用每个 MCP Server 的 tools/list，汇总所有 tools
  4. 构建 Tool Router 映射表：
     shell_exec  → MCP Client #1 (core)
     file_read   → MCP Client #1 (core)
     browse_url  → MCP Client #2 (browser)

Agent Loop 执行时：
  1. 将汇总的 tools 列表传给 LLM
  2. LLM 返回 tool_call
  3. Tool Router 根据 tool_name 找到对应 MCP Client
  4. 转发请求到对应 MCP Server 执行
  5. 返回结果给 LLM
```

## 多 Agent 并行

- 支持同时运行多个 Agent，互不干扰
- 每个 Agent 有独立的对话上下文
- 通过 Agent ID 区分和管理
- 提供全局 Agent 列表和状态总览

## MCP 多模态支持（v0.0.2）

`ToolContent` 扩展支持多种内容类型：

```go
type ToolContent struct {
    Type     string `json:"type"`               // "text" | "image" | "resource"
    Text     string `json:"text,omitempty"`      // type=text 时
    Data     string `json:"data,omitempty"`      // type=image 时，base64 编码
    MimeType string `json:"mimeType,omitempty"`  // type=image 时，如 "image/png"
}
```

- `file_read` 读取二进制图片文件（jpeg/png/gif/webp）时，自动返回 `image` 类型而非将二进制作为 text 返回
- Agent Loop 中 `formatToolResult()` 将 `ToolContent` 转为 `llm.ContentBlock`，image 类型完整传递给 LLM
- SSE 流式事件 `tool_result` 携带完整的多模态内容块
