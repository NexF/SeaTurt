# Agent 架构

## 概述

Agent 是 SeaTurt 的核心概念。每个 Agent 拥有独立的 Docker 容器（沙箱）、MCP 工具链、LLM 对话会话和持久化状态。Agent 接收用户消息后，通过 **Agent Loop** 自主调用工具完成任务。

## 整体架构

```
HTTP 请求 (REST API)
   │
   ▼
api/router.go
   ├── agent_handler.go  — Agent CRUD + 生命周期
   └── chat_handler.go   — 对话 (SSE 流式) + 历史
          │
          ▼
   agent/manager.go  ◄─── config/config.go (多 Provider 配置)
   ├── model.go      (数据模型)
   ├── loop.go       (Agent Loop: LLM ⟷ Tool 循环)
   │     │
   │     ├── llm/         (LLM 调用层)
   │     │   ├── client.go     — HTTP 调用 + SSE 流式解析
   │     │   ├── content.go    — 统一内容模型 (text/image/file)
   │     │   ├── provider.go   — OpenAI/Anthropic 格式化
   │     │   ├── tools.go      — MCP → OpenAI 工具转换
   │     │   └── validate.go   — 输入验证
   │     │
   │     └── mcp/         (MCP 通信层)
   │         ├── router.go     — tool_name → Client 路由
   │         ├── pool.go       — 多 MCP Server 连接池
   │         ├── client.go     — MCP JSON-RPC 客户端
   │         ├── protocol.go   — 协议类型定义
   │         └── transport.go  — Docker exec stdio 传输
   │
   └── store/sqlite.go  (持久化: Agent + Message + 图片外部化)
```

## 数据模型

### Agent

```go
type Agent struct {
    ID            string      // 时间戳生成的唯一 ID
    Name          string      // 用户指定名称
    Status        Status      // created | running | stopped | error
    ContainerID   string      // Docker 容器 ID
    Image         string      // 使用的镜像
    WorkspacePath string      // 宿主机 workspace 路径
    Config        AgentConfig // 运行配置
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

### AgentConfig

```go
type AgentConfig struct {
    Model       string            // LLM 模型标识 (如 "gpt-4o")
    MCPServers  []MCPServerConfig // MCP Server 列表
    ExtraMounts []string          // 额外挂载路径
    EnvVars     map[string]string // 自定义环境变量
    Desktop     bool              // 是否桌面模式（v0.0.3）
    SystemPrompt string           // 用户自定义的额外 system prompt（v0.0.4）
}
```

### Status 流转

```
created ──► running ──► stopped ──► (deleted)
               │            │
               │   Start    │
               │◄───────────┘
               │
               └──► error
```

### Message

```go
type Message struct {
    ID        string      // UUID
    AgentID   string
    Role      string      // "user" | "assistant" | "tool"
    Content   llm.Content // []ContentBlock — 支持 text + image 混合
    ToolCalls string      // JSON — LLM 返回的 tool_calls
    CreatedAt time.Time
}
```

### ContentBlock（统一内容模型）

```go
type ContentBlock struct {
    Type  string     // "text" | "image" | "file"
    Text  string     // Type=text 时的文本内容
    Image *ImageData // Type=image 时的图片数据
}

type ImageData struct {
    Data     string // base64 编码
    MimeType string // image/png, image/jpeg 等
    Detail   string // 可选: low/high/auto
    FilePath string // 外部化存储路径（DB 不存 base64，存文件路径）
}
```

`Content` 即 `[]ContentBlock`，JSON 序列化时有智能处理：
- 单纯文本 → 序列化为 `"hello"` 字符串
- 多块/含图片 → 序列化为 `[{type, text, image}]` 数组
- 反序列化兼容两种格式

## Agent 生命周期

### Manager

`agent.Manager` 是 Agent 的核心管理器，持有所有运行时组件：

```go
type Manager struct {
    cfg       *config.Config
    store     Store              // SQLite 持久化
    docker    *container.Manager // Docker 容器管理
    llmClient *llm.Client        // LLM API 客户端
    pools     map[string]*mcp.Pool   // agentID → MCP 连接池
    routers   map[string]*mcp.Router // agentID → 工具路由器
    mu        sync.RWMutex           // 并发保护
}
```

### Create（创建 Agent）

```
1. 生成 agentID（时间戳格式）
2. 确定 MCP Servers（用户指定 / 配置默认值）
3. 确定 LLM model（用户指定 / 配置默认值）
4. 创建 workspace 目录：~/.seaturt/workspaces/<agentID>/
5. 创建 .seaturt/ 子目录
6. 生成 SYSTEM.md → workspace/.seaturt/SYSTEM.md
   ├── 静态部分：身份、行为准则、工作目录、端口说明
   ├── 动态部分：桌面指令（desktop=true 时）、MCP Server 列表
   └── 可选：用户自定义附加指令（system_prompt 字段）
7. docker.CreateContainer
   ├── 镜像：config.sandbox_image
   ├── 环境变量：HOST_UID, HOST_GID, 用户自定义 EnvVars
   ├── 挂载：workspacePath → /workspace（+ ExtraMounts）
   ├── Label：seaturt.agent_id, seaturt.managed
   └── 容器名：seaturt-<agentID>
8. docker.StartContainer
9. 查询实际端口映射（ContainerInspect）
10. 生成 PORTS.md → workspace/.seaturt/PORTS.md
11. mcp.Pool.Connect — 为每个 MCP Server 创建 docker exec 会话
12. mcp.NewRouter — 建立 tool_name → MCP Client 路由表
13. store.CreateAgent — 持久化到 SQLite
```

### Start（启动已停止的 Agent）

```
1. 从 DB 读取 Agent
2. docker.StartContainer
3. 查询端口映射，重新生成 PORTS.md（以防端口变化）
4. mcp.Pool.Connect（重建所有 MCP 连接）
5. mcp.NewRouter（重建路由表）
6. 更新内存状态（pools, routers）
7. store.UpdateAgentStatus → running
```

### Stop（停止 Agent）

```
1. pool.CloseAll（关闭 MCP 连接）
2. 清理内存（delete pools/routers）
3. docker.StopContainer
4. store.UpdateAgentStatus → stopped
```

### Delete（删除 Agent）

```
1. pool.CloseAll
2. docker.RemoveContainer（force）
3. store.DeleteMessages
4. store.DeleteAgent
注：workspace 目录保留不删
```

## Agent Loop（对话循环）

Agent Loop 是 Agent 的"大脑"——接收用户消息后，自主循环调用 LLM 和工具，直到得到最终回复。

### 流程

```
用户消息
   │
   ▼
┌──────────────────────────────────────────────┐
│  Agent Loop (最多 50 次迭代)                   │
│                                              │
│  1. 从 mcp.Router 收集所有可用工具             │
│     → 转换为 OpenAI function calling 格式     │
│                                              │
│  2. 加载 system prompt                       │
│     ├── 读取 /workspace/.seaturt/SYSTEM.md   │
│     ├── 文件存在 → 使用文件内容               │
│     └── 文件不存在 → fallback 到默认 prompt   │
│     每次 Chat 都重新读取，支持热更新           │
│                                              │
│  3. 循环:                                     │
│     ┌─────────────────────────────────────┐  │
│     │ 调用 LLM (streaming SSE)            │  │
│     │   ├── 纯文本回复 → 结束循环          │  │
│     │   └── tool_calls → 继续             │  │
│     │         │                           │  │
│     │   ┌─────▼─────────────────────┐     │  │
│     │   │ 对每个 tool_call:          │     │  │
│     │   │   mcp.Router.Route(name)  │     │  │
│     │   │   → MCP Client.CallTool() │     │  │
│     │   │   → formatToolResult()    │     │  │
│     │   │     (text/image → Content) │     │  │
│     │   └───────────────────────────┘     │  │
│     │         │                           │  │
│     │   tool results 追加到 messages       │  │
│     │   回到循环顶部，再次调用 LLM          │  │
│     └─────────────────────────────────────┘  │
│                                              │
│  4. 最终回复（文本）返回给用户                  │
└──────────────────────────────────────────────┘
```

### LoopConfig

```go
type LoopConfig struct {
    LLMClient    *llm.Client
    Router       *mcp.Router
    SystemPrompt string  // 如果非空，使用此 prompt；否则 fallback 到 DefaultSystemPrompt
}
```

Chat Handler 在每次对话时从 workspace 加载最新 SYSTEM.md 内容传入 `LoopConfig.SystemPrompt`。

### 流式事件（SSE）

Agent Loop 通过 `StreamFunc` 回调实时推送事件到客户端：

| 事件类型 | 数据 | 说明 |
|---------|------|------|
| `text_delta` | `{content: "..."}` | LLM 生成的增量文本 |
| `tool_call` | `{name, arguments}` | LLM 发起工具调用 |
| `tool_result` | `{name, content}` | 工具执行结果 |
| `error` | `{error: "..."}` | 错误信息 |
| `done` | `{}` | 循环结束 |

### 工具结果格式化

`formatToolResult()` 将 MCP 返回的 `CallToolResult` 转为 `llm.Content`：

- `ToolContent.Type = "text"` → `ContentBlock{Type: "text", Text: "..."}`
- `ToolContent.Type = "image"` → `ContentBlock{Type: "image", Image: {Data: base64, MimeType: ...}}`
- 截断保护：文本最大 50000 字符，image 不截断

这样截屏等工具返回的图片可以直接传给 LLM，LLM 能"看到"图片内容。

## MCP 通信

### 连接建立

每个 Agent 对应一个 `mcp.Pool`（连接池），Pool 中每个 MCP Server 各有一个 `mcp.Client`。

```
Agent 容器
├── mcp-server-core    ← Client 1 (docker exec stdio)
├── mcp-server-browser ← Client 2 (docker exec stdio)
└── mcp-server-desktop ← Client 3 (docker exec stdio, v0.0.3)
```

### 通信链路

```
Manager.Create
   │
   ▼
Pool.Connect(serverConfigs)
   │  对每个 MCP Server:
   │
   ▼
container.ExecStdio("mcp-server-core")
   │  返回 HijackedResponse (stdin/stdout 双向流)
   │
   ▼
Transport(hijackedConn)
   │  JSON-RPC 2.0 读写
   │
   ▼
Client.Initialize()
   │  MCP 握手 → 获取 server capabilities
   │
   ▼
Client.ListTools()
   │  获取工具定义列表 → 缓存
   │
   ▼
Router.Rebuild(pool)
   │  tool_name → Client 映射表
   │
   ▼
就绪，等待 Loop 调用
```

### 工具路由

`mcp.Router` 维护 `tool_name → *Client` 映射：

```
用户说 "列出当前目录文件"
   │
   ▼ LLM 决定调用 shell_exec
Agent Loop → Router.Route("shell_exec")
   │
   ▼ 路由到 mcp-server-core 的 Client
Client.CallTool("shell_exec", {command: "ls -la"})
   │
   ▼ docker exec stdio → 容器内 MCP Server 执行
返回结果 → formatToolResult → 回传 LLM
```

## LLM 调用层

### 多 Provider 支持

通过 `config.yaml` 配置多个 Provider，每个 Provider 可有多个 Model：

```yaml
llm:
  providers:
    openai:
      base_url: "https://api.openai.com/v1"
      api_type: "openai"
      api_key: "sk-..."
      models:
        - id: "gpt-4o"
          input_types: ["text", "image"]
        - id: "gpt-4o-mini"
          input_types: ["text"]
    anthropic:
      base_url: "https://api.anthropic.com"
      api_type: "anthropic"
      models:
        - id: "claude-sonnet-4-20250514"
          input_types: ["text", "image"]
```

`config.ResolveLLM("gpt-4o")` 解析出完整的 `LLMEndpoint`（URL + Key + 格式化器 + 模型能力）。

### ContentFormatter

根据 API 类型选择格式化器，将内部 `Content` 转为目标 Provider 的 wire format：

| Provider | 纯文本 | 混合内容 (text + image) |
|----------|--------|----------------------|
| **OpenAI** | `"content": "hello"` | `"content": [{type:"text",...}, {type:"image_url", image_url:{url:"data:..."}}]` |
| **Anthropic** | `"content": "hello"` | `"content": [{type:"text",...}, {type:"image", source:{type:"base64",...}}]` |

### 流式 SSE 解析

`llm.Client.ChatCompletionStream()` 通过 SSE 流读取 LLM 响应：

```
data: {"choices":[{"delta":{"content":"Hello"}}]}
data: {"choices":[{"delta":{"content":" World"}}]}
data: {"choices":[{"delta":{"tool_calls":[{...}]}}]}
data: [DONE]
```

`consumeSSE()` 逐行解析 `data:` 行，增量组装 content 和 tool_calls，实时回调 `StreamFunc` 推送事件。

## 持久化

### SQLite Schema

```sql
-- Agent 表
CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'created',
    container_id TEXT,
    image TEXT,
    workspace_path TEXT,
    config TEXT,          -- JSON (AgentConfig)
    created_at DATETIME,
    updated_at DATETIME
);

-- 消息表
CREATE TABLE messages (
    id TEXT PRIMARY KEY,  -- UUID
    agent_id TEXT NOT NULL,
    role TEXT NOT NULL,    -- user | assistant | tool
    content TEXT,          -- JSON (Content)
    tool_calls TEXT,       -- JSON
    created_at DATETIME,
    FOREIGN KEY (agent_id) REFERENCES agents(id)
);
```

### 图片外部化

SQLite 不适合存大量 base64 数据。消息持久化时：

```
写入 (CreateMessage):
  Content 中的 image.Data (base64)
    → 写入文件 {workspace}/.seaturt/uploads/{uuid}.{ext}
    → DB 中 image.Data = ""，image.FilePath = 文件路径

读取 (ListMessages):
  image.FilePath 不为空
    → 从文件读取 base64 数据
    → 填回 image.Data
    → 返回完整的 Content
```

## API 接口

### Agent CRUD

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/agents` | 创建 Agent（含容器启动 + MCP 连接） |
| `GET` | `/api/agents` | 列出所有 Agent |
| `GET` | `/api/agents/:id` | 查询单个 Agent |
| `POST` | `/api/agents/:id/start` | 启动已停止的 Agent |
| `POST` | `/api/agents/:id/stop` | 停止 Agent |
| `DELETE` | `/api/agents/:id` | 删除 Agent（含容器销毁） |
| `GET` | `/api/agents/:id/ports` | 查询端口映射表 |
| `GET` | `/api/agents/:id/system-prompt` | 获取当前 SYSTEM.md 内容 |
| `PUT` | `/api/agents/:id/system-prompt` | 更新 SYSTEM.md（热更新，下次 Chat 生效） |

### 对话

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/agents/:id/chat` | 发送消息（SSE 流式返回） |
| `GET` | `/api/agents/:id/history` | 获取对话历史 |
| `DELETE` | `/api/agents/:id/history` | 清空对话历史 |

### Chat 请求

支持两种 Content-Type：

**JSON**（纯文本）：
```json
{"message": "列出当前目录文件"}
```

**multipart/form-data**（图片上传）：
```
message: "这张图片里有什么？"
images: [file1.png, file2.jpg]
```

- 图片验证：jpeg/png/gif/webp，可配置最大尺寸
- multipart 上传时，若 Content-Type 为空或 `application/octet-stream`，自动通过 magic bytes 检测真实 MIME 类型
- 发送前检查模型是否支持图片输入（`ValidateContent`）

### Chat SSE 响应

```
event: text_delta
data: {"content": "当前目录包含以下文件：\n"}

event: tool_call
data: {"name": "shell_exec", "arguments": "{\"command\":\"ls -la\"}"}

event: tool_result
data: {"name": "shell_exec", "content": "total 16\ndrwxr-xr-x ..."}

event: text_delta
data: {"content": "目录中有 3 个文件..."}

event: done
data: {}
```

## 文件清单

| 文件 | 行数 | 职责 |
|------|------|------|
| `internal/agent/manager.go` | ~380 | Agent 生命周期管理 + SYSTEM.md/PORTS.md 生成 + system prompt 加载 |
| `internal/agent/model.go` | ~55 | 数据模型（Agent, Message, Config, Status, CreateAgentRequest） |
| `internal/agent/loop.go` | ~250 | Agent Loop（LLM ⟷ Tool 自主循环），LoopConfig 含 SystemPrompt 字段 |
| `internal/agent/systemprompt.go` | ~120 | GenerateSystemMD() + GeneratePortsMD() + 模板常量 |
| `internal/agent/loop_test.go` | 96 | Loop 单元测试 |
| `internal/api/agent_handler.go` | ~140 | Agent CRUD API + ports + system-prompt 端点 |
| `internal/api/chat_handler.go` | ~310 | Chat SSE + 历史 + 图片上传（含 MIME 类型自动检测） |
| `internal/api/router.go` | ~95 | Gin 路由注册 + 中间件 |
| `internal/llm/client.go` | 328 | LLM HTTP 调用 + SSE 流式解析 |
| `internal/llm/content.go` | 119 | 统一内容模型（text/image/file） |
| `internal/llm/provider.go` | 207 | OpenAI/Anthropic 格式化器 |
| `internal/llm/tools.go` | 34 | MCP → OpenAI 工具定义转换 |
| `internal/llm/validate.go` | 35 | 输入类型验证 |
| `internal/mcp/client.go` | 181 | MCP JSON-RPC 客户端 |
| `internal/mcp/pool.go` | 101 | 多 MCP Server 连接池 |
| `internal/mcp/router.go` | 99 | 工具名称路由 |
| `internal/mcp/protocol.go` | 121 | MCP 协议类型定义 |
| `internal/mcp/transport.go` | 148 | Docker exec stdio 传输层 |
| `internal/store/sqlite.go` | 337 | SQLite CRUD + 图片外部化 |
| `internal/config/config.go` | 266 | 多 Provider 配置加载 |

**总计约 3200 行 Go 代码**。

## 关键设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| Agent Loop 最大迭代 | 50 次 | 防止无限循环，覆盖复杂任务 |
| 工具结果截断 | 50000 字符 | 防止超长输出占满上下文窗口 |
| 图片不截断 | 透传 | 截屏等图片需要完整传给 LLM |
| 图片外部化存储 | 文件系统 | SQLite 不适合存大 base64 |
| MCP 通信 | docker exec stdio | 不暴露端口、跨平台、天然隔离 |
| LLM 流式 | SSE + 回调 | 实时推送到客户端，体验好 |
| 多 Provider | ContentFormatter 接口 | 同一套内部模型，适配不同 API 格式 |
| 工具路由 | name → Client map | 多 MCP Server 的工具统一寻址 |
