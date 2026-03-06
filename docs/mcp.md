# MCP 通信

## 概述

每个 Agent 容器内可运行多个 MCP Server。v0.1.3 起采用**无状态架构**：每次 tool call 通过 `docker exec` 启动一个新进程，完成一次完整的 MCP JSON-RPC 交互（initialize → tools/call → 进程退出），不再维护长连接。

## 核心架构

### 新架构（v0.1.3）

```
Router
  → ToolRegistry (registry.go)   -- 从 YAML 读取 tools 定义，无需启动 MCP 进程
  → Executor (executor.go)
    → ephemeralTransport          -- 每次 tool call 新建 docker exec 进程
```

**关键变化**：
- **Tools 定义从 YAML 文件加载**：`workspace/.seaturt/tools/*.yaml` 声明每个 MCP Server 的 tools 列表
- **无长连接**：不再有 Pool/Client/Transport 的连接管理
- **Tool 名称使用 `mcpname-toolname` 格式**：如 `core-shell_exec`、`desktop-screenshot`
- **每次 tool call 独立进程**：通过 `docker exec` 启动新进程，走完整 MCP JSON-RPC 协议

### Agent 生命周期管理

| 操作 | 说明 | 对应容器操作 |
|------|------|-------------|
| **创建** | 创建新 Agent，分配独立容器和 workspace，写入 tools YAML | `docker create` + `docker start` |
| **运行** | Agent 在容器内执行任务 | 容器内进程执行 |
| **停止** | 暂停 Agent，保留容器和数据 | `docker stop` |
| **恢复** | 从停止状态恢复 Agent，重新加载 YAML | `docker start` |
| **删除** | 销毁 Agent，删除容器（可选保留 workspace） | `docker rm` |

### Agent 对话与任务执行

- 用户通过 API/CLI/Web UI 与 Agent 对话
- LLM 看到的是该 Agent 所有 MCP Server 汇总的完整 tools 列表（`mcpname-toolname` 格式）
- LLM 根据对话内容决定调用哪些 Tool
- Router 解析 `mcpname-toolname` → 找到对应 MCP Server → 通过 Executor 启动新进程执行
- 所有操作均在容器沙箱内完成，不影响宿主机
- Tool 执行结果自动喂回 LLM，形成 Agent Loop 直到任务完成

## Tools YAML 文件

每个 MCP Server 对应一个 YAML 文件，存放在 `workspace/.seaturt/tools/` 目录下：

```yaml
name: core
command: mcp-server-core
enabled: true
tools:
  - name: shell_exec
    description: "Execute a shell command"
    inputSchema:
      type: object
      properties:
        command:
          type: string
          description: "The shell command to execute"
      required:
        - command
  - name: file_read
    description: "Read file contents"
    inputSchema:
      type: object
      properties:
        path:
          type: string
      required:
        - path
```

内置 MCP Server（core, desktop）的 YAML 文件在 Agent 创建时自动生成（`WriteBuiltinTools()`）。

## 内置 MCP Server：`mcp-server-core`

所有 Agent 默认启用，提供基础操作能力：

| Tool（LLM 看到的名称） | 原始名称 | 说明 | 参数 | 返回类型 |
|------------------------|---------|------|------|---------|
| `core-shell_exec` | `shell_exec` | 执行 Shell 命令 | `command: string` | text |
| `core-file_read` | `file_read` | 读取文件内容 | `path: string` | text / image |
| `core-file_write` | `file_write` | 写入/创建文件 | `path: string, content: string` | text |
| `core-file_list` | `file_list` | 列出目录内容 | `path: string` | text |

## MCP Server：`mcp-server-desktop`

桌面 Agent 自动启用，提供 GUI 桌面操作能力。依赖容器内的 Xvfb + GNOME 桌面环境。

| Tool（LLM 看到的名称） | 原始名称 | 说明 | 参数 | 返回类型 |
|------------------------|---------|------|------|---------|
| `desktop-screenshot` | `screenshot` | 桌面全屏截图 | `region?: {x, y, width, height}` | image (PNG base64) |
| `desktop-mouse_click` | `mouse_click` | 模拟鼠标点击 | `x: int, y: int, button?: "left"\|"right"\|"middle"` | text |
| `desktop-mouse_move` | `mouse_move` | 移动鼠标 | `x: int, y: int` | text |
| `desktop-mouse_drag` | `mouse_drag` | 鼠标拖拽 | `from_x, from_y, to_x, to_y: int` | text |
| `desktop-keyboard_type` | `keyboard_type` | 模拟键盘输入 | `text: string` | text |
| `desktop-keyboard_key` | `keyboard_key` | 模拟按键/组合键 | `key: string` | text |
| `desktop-window_list` | `window_list` | 列出桌面窗口 | 无 | text |
| `desktop-window_focus` | `window_focus` | 聚焦指定窗口 | `window_id?: string, title?: string` | text |
| `desktop-open_app` | `open_app` | 打开应用程序 | `app: string` | text |
| `desktop-desktop_wait` | `desktop_wait` | 等待渲染稳定后截屏 | `delay_ms?: int` | image (PNG base64) |

## MCP Server 清单

| MCP Server | 二进制路径 | 默认启用 | 说明 |
|------------|----------|---------|------|
| `mcp-server-core` | `workspace/.seaturt/tools/mcp-server-core` | 是 | 基础 shell/file 操作 |
| `mcp-server-desktop` | `workspace/.seaturt/tools/mcp-server-desktop` | 是 | 桌面 GUI 操作 |

MCP Server 二进制文件和 YAML 定义统一存放在 `workspace/.seaturt/tools/` 目录下。

## 多 MCP Server 工作机制

```
Agent 创建时：
  1. WriteBuiltinTools() 将内置 MCP Server 的 YAML 写入 workspace/.seaturt/tools/
  2. ToolRegistry.LoadFromDir() 从 YAML 文件加载所有 tools 定义（不启动任何进程）
  3. 构建 Router 映射表（mcpname-toolname 格式）：
     core-shell_exec   → core server (command: mcp-server-core)
     core-file_read    → core server
     desktop-screenshot → desktop server (command: mcp-server-desktop)

Agent Loop 执行时：
  1. 将汇总的 tools 列表传给 LLM（mcpname-toolname 格式）
  2. LLM 返回 tool_call（如 core-shell_exec）
  3. Router 解析前缀：SplitToolName("core-shell_exec") → ("core", "shell_exec")
  4. Router 查找 core server 的 command → "mcp-server-core"
  5. Executor 启动 docker exec 新进程 → initialize → tools/call("shell_exec", args)
  6. 进程完成后退出，结果返回给 LLM
```

## normalizeResult 兜底

对于 MCP Server 返回不规范的 `tools/call` 响应，`normalizeResult()` 进行防御性处理：

| 情况 | 处理 |
|------|------|
| 正常响应，有 content | 保持原样 |
| content 中 type 字段缺失 | 默认补为 `"text"` |
| content 为空数组 | 返回 `"(MCP Server returned empty response)"` |
| result 为 nil | 返回 `"(MCP Server returned empty response)"` |
| IsError=true 但无 content | 返回 `"(MCP Server returned error with no details)"` |

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
