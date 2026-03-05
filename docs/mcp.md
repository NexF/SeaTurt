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

## MCP Server 清单

| MCP Server | 路径 | 默认启用 | 说明 |
|------------|------|---------|------|
| `mcp-server-core` | `/usr/local/bin/mcp-server-core` | 是 | 基础 shell/file 操作 |
| `mcp-server-browser` | `/usr/local/bin/mcp-server-browser` | 否 | 浏览器自动化 |
| `mcp-server-db` | `/usr/local/bin/mcp-server-db` | 否 | 数据库操作 |
| `mcp-server-git` | `/usr/local/bin/mcp-server-git` | 否 | Git 版本控制 |

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
