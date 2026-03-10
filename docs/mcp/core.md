# mcp-server-core

基础 Shell 和文件操作 MCP Server，所有 Agent 默认启用。

## 基本信息

| 属性 | 值 |
|------|------|
| 名称 | `mcp-server-core` |
| 语言 | Go 1.23 |
| 协议版本 | `2024-11-05` |
| 传输方式 | stdio（JSON-RPC 2.0） |
| 默认启用 | ✅ 所有 Agent |
| 工作目录 | `/workspace` |

## 源码结构

```
mcp-servers/core/
├── go.mod     # Go module 定义
├── main.go    # MCP 协议层：JSON-RPC 路由、消息读写
└── tools.go   # Tool 实现：shell_exec, file_read, file_write, file_list
```

## Tools

### `shell_exec`

执行 Shell 命令，返回 stdout/stderr。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `command` | string | ✅ | 要执行的 Shell 命令 |
| `timeout` | number | ❌ | 超时时间（秒），默认 120，最大 1800（30 分钟） |
| `background` | boolean | ❌ | 后台运行模式，立即返回 PID |

**前台模式**（默认）：
- 在 `/workspace` 目录下通过 `sh -c` 执行命令
- 使用独立进程组（`Setpgid: true`），超时时杀掉整个进程树
- 超时后返回 `[TIMEOUT]` 提示，建议使用 `background=true`
- stdout 和 stderr 合并输出

**后台模式**（`background=true`）：
- 通过 `nohup setsid` 完全分离进程
- 立即返回 PID 和日志文件路径（`/tmp/bg_cmd.log`）
- 适用于启动服务器、浏览器等长时间运行的进程
- 可通过 `cat /tmp/bg_cmd.log` 查看输出，`kill <PID>` 停止

**示例**：
```json
{"name": "shell_exec", "arguments": {"command": "ls -la /workspace"}}
{"name": "shell_exec", "arguments": {"command": "python3 server.py", "background": true}}
{"name": "shell_exec", "arguments": {"command": "npm test", "timeout": 300}}
```

### `file_read`

读取文件内容。自动检测文件类型，图片文件返回 base64 编码的 image 内容块。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `path` | string | ✅ | 文件路径（相对路径基于 `/workspace`） |

**多模态支持**：
- 文本文件 → 返回 `type: "text"` 内容块
- 图片文件（jpeg/png/gif/webp）→ 返回 `type: "image"` 内容块，包含 base64 编码数据和 MIME 类型
- 通过 `http.DetectContentType()` 自动检测内容类型

**示例**：
```json
{"name": "file_read", "arguments": {"path": "src/main.py"}}
{"name": "file_read", "arguments": {"path": "/workspace/screenshots/result.png"}}
```

### `file_write`

写入或创建文件。自动创建父目录。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `path` | string | ✅ | 文件路径（相对路径基于 `/workspace`） |
| `content` | string | ✅ | 要写入的内容 |

- 如果文件存在则覆盖
- 自动创建不存在的父目录（`os.MkdirAll`，权限 0755）
- 文件权限 0644
- 返回写入的字节数

**示例**：
```json
{"name": "file_write", "arguments": {"path": "hello.py", "content": "print('hello')"}}
```

### `file_list`

列出目录内容，显示文件名和大小。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `path` | string | ✅ | 目录路径（相对路径基于 `/workspace`） |

- 目录名后缀 `/`
- 每行格式：`{name}\t{size} bytes`
- 空目录返回 `(empty directory)`

**示例**：
```json
{"name": "file_list", "arguments": {"path": "."}}
```

## 路径解析

所有路径参数通过 `resolvePath()` 处理：
- 绝对路径：直接使用
- 相对路径：拼接到 `/workspace` 下

## LLM 看到的 Tool 名称

通过 Router 的 `mcpname-toolname` 前缀映射：

| LLM 调用名 | 实际 Tool |
|-----------|----------|
| `core-shell_exec` | `shell_exec` |
| `core-file_read` | `file_read` |
| `core-file_write` | `file_write` |
| `core-file_list` | `file_list` |
