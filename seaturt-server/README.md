# SeaTurt Server

SeaTurt Server 是一个 **AI Agent 沙箱服务平台**的后端。它将 LLM（大语言模型）与 Docker 容器化沙箱环境结合，让 AI Agent 能在隔离的容器内执行 shell 命令、操作文件、浏览网页、甚至操作 KDE 桌面 GUI。

- **语言**：Go 1.25
- **Web 框架**：Gin
- **数据库**：SQLite（纯 Go 驱动，modernc.org/sqlite）
- **容器引擎**：Docker（通过 Docker SDK for Go 交互）
- **前端**：生产模式下通过 `go:embed` 嵌入前端构建产物

---

## 目录结构

```
seaturt-server/
├── cmd/server/                       # 入口点
│   ├── main.go                       # 服务启动、组件初始化
│   └── web/dist/                     # 嵌入式前端构建产物（生产模式）
│
├── internal/                         # 核心业务代码
│   ├── agent/                        # ★ Agent 生命周期 + Agent Loop
│   │   ├── model.go                  # 数据模型：Agent, Session, Message, CronJob
│   │   ├── manager.go                # Agent Manager：创建/启动/停止/删除/MCP加载
│   │   ├── loop.go                   # Agent Loop：LLM ↔ Tool 循环执行引擎
│   │   ├── eventbus.go               # 事件总线：SessionBus + GlobalBus + EventHub
│   │   └── systemprompt.go           # 系统提示词生成与模板渲染
│   │
│   ├── api/                          # HTTP API 路由与处理器
│   │   ├── router.go                 # Gin 路由注册、CORS、静态文件
│   │   ├── agent_handler.go          # Agent CRUD + 端口 + 桌面 + 模型列表
│   │   ├── chat_handler.go           # 对话接口：发消息、取消对话/工具调用
│   │   ├── session_handler.go        # Session CRUD
│   │   ├── event_handler.go          # SSE 事件流（全局 + 会话级）
│   │   ├── file_handler.go           # 工作区文件：列表/读取/上传
│   │   └── cronjob_handler.go        # 定时任务 CRUD + 手动触发
│   │
│   ├── llm/                          # LLM 客户端层
│   │   ├── client.go                 # OpenAI 兼容 HTTP 客户端（流式/非流式）
│   │   ├── content.go                # 统一内容模型：text / image / file
│   │   ├── provider.go               # Provider 格式化器：OpenAI / Anthropic
│   │   ├── tools.go                  # MCP → OpenAI Tool 定义转换
│   │   └── validate.go               # 内容类型 vs 模型能力校验
│   │
│   ├── mcp/                          # MCP (Model Context Protocol) 实现
│   │   ├── protocol.go               # JSON-RPC 2.0 + MCP 协议类型定义
│   │   ├── registry.go               # ToolRegistry：从 YAML 加载工具定义
│   │   ├── router.go                 # MCP Router：工具名路由 + 调度
│   │   ├── executor.go               # Executor：docker exec 启动 MCP 进程
│   │   ├── ephemeral_transport.go    # 临时 Transport：单次工具调用通信
│   │   └── discover_transport.go     # 发现 Transport：初始化 + tools/list
│   │
│   ├── container/                    # Docker 容器管理
│   │   └── docker.go                 # Docker API 封装（创建/启停/exec/文件复制/端口映射）
│   │
│   ├── store/                        # 数据持久化层
│   │   └── sqlite.go                 # SQLite CRUD（Agent/Message/Session/CronJob）+ 图片外化
│   │
│   ├── config/                       # 配置系统
│   │   └── config.go                 # YAML + 环境变量配置加载
│   │
│   ├── builtin/                      # 内置工具（非 MCP，直接在服务端执行）
│   │   ├── handler.go                # Handler 接口
│   │   ├── registry.go               # 定时任务工具定义
│   │   ├── router.go                 # Builtin Router
│   │   └── cron_handlers.go          # 定时任务工具实现
│   │
│   └── cron/                         # 定时任务调度器
│       └── scheduler.go              # Cron/At 任务调度 + 执行保护
│
├── prompts/
│   └── system.md                     # 系统提示词模板（Go template 语法）
│
├── docker/sandbox/                   # 沙箱 Docker 镜像
│   ├── Dockerfile                    # Ubuntu KDE + Selkies + Node.js + 开发工具
│   ├── svc-selkies-run               # s6 服务：Selkies WebRTC 远程桌面
│   ├── svc-browser-daemon-run        # s6 服务：Browser MCP 守护进程
│   ├── svc-wechat-run                # s6 服务：微信启动器
│   └── mcp-servers/                  # MCP Server 源码
│       ├── core/                     # shell_exec, file_read/write 等基础工具 (Go)
│       ├── desktop/                  # screenshot, mouse/keyboard 操作 (Go)
│       ├── browser/                  # 浏览器自动化 - Playwright (Go + Node.js)
│       ├── search/                   # 网页搜索 (Python)
│       └── wechat/                   # 微信自动化 - AT-SPI2 (Python)
│
├── tests/
│   ├── integration/                  # 集成测试（Mock LLM，~14 个测试文件）
│   ├── e2e/                          # 端到端测试（真实 LLM + Docker）
│   └── testdata/                     # 测试数据
│
├── Makefile                          # 构建/测试/发布命令
├── config.yaml.example               # 配置文件示例
├── go.mod / go.sum                   # Go 依赖管理
└── release/                          # 发布产物
```

---

## 架构概览

```
┌──────────────────────────────────────────────────────────────────┐
│                         Frontend (SPA)                           │
│              SSE 订阅 /api/.../events  ←───── 实时事件           │
│              POST /api/.../chat ──────────→ 发送消息             │
└────────────────────────┬─────────────────────────────────────────┘
                         │ HTTP / SSE
┌────────────────────────▼─────────────────────────────────────────┐
│                      API Layer (Gin)                              │
│  agent_handler │ chat_handler │ session_handler │ event_handler   │
│  file_handler  │ cronjob_handler                                 │
└────────────────────────┬─────────────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────────────┐
│                    Agent Manager                                  │
│  Agent 生命周期管理 │ MCP 加载 │ 活跃会话/工具调用取消管理        │
│  EventHub (GlobalBus + SessionBus)                               │
└────────┬──────────────┬──────────────────┬───────────────────────┘
         │              │                  │
    ┌────▼────┐   ┌─────▼──────┐    ┌─────▼──────┐
    │ Agent   │   │   LLM      │    │   Store    │
    │ Loop    │   │  Client    │    │  (SQLite)  │
    └────┬────┘   └────────────┘    └────────────┘
         │
    ┌────▼────────────────────────────────────────┐
    │           CompositeRouter                    │
    │   ┌─────────────┐  ┌──────────────────┐     │
    │   │ MCP Router  │  │ Builtin Router   │     │
    │   │ (容器内工具) │  │ (服务端内置工具)  │     │
    │   └──────┬──────┘  └──────────────────┘     │
    └──────────┼──────────────────────────────────┘
               │
    ┌──────────▼──────────────────────────────────┐
    │         MCP Executor                         │
    │  docker exec → 启动 MCP Server 进程          │
    │  stdin/stdout JSON-RPC → 执行工具 → 退出     │
    └──────────┬──────────────────────────────────┘
               │
    ┌──────────▼──────────────────────────────────┐
    │       Docker Container (沙箱)                │
    │  KDE 桌面 │ 开发工具 │ Playwright │ MCP 二进制 │
    └─────────────────────────────────────────────┘
```

---

## 核心模块详解

### 1. 服务启动流程 (`cmd/server/main.go`)

按以下顺序初始化所有组件：

1. `config.Load()` → 加载 YAML 配置 + 环境变量覆盖
2. `initLogger()` → slog 结构化日志
3. `store.New()` → SQLite 数据库 + 自动迁移
4. `container.NewManager()` → Docker 客户端
5. `llm.NewClient()` → 默认 LLM 客户端
6. `agent.NewManager()` → Agent Manager（核心协调者）
7. `cron.NewScheduler()` → 定时任务调度器
8. `builtin.NewRouter()` → 内置工具路由
9. `agentMgr.SyncAgentStates()` → 启动时同步 Agent 与 Docker 容器状态
10. `scheduler.LoadAll()` + `Start()` → 加载并启动所有定时任务
11. `api.NewServer()` → Gin HTTP 服务器启动

生产模式下通过 `//go:embed web/dist/*` 嵌入前端静态文件。

### 2. Agent Loop —— 核心 AI 循环 (`internal/agent/loop.go`)

这是系统最核心的逻辑：

```
用户消息 → LLM 推理 → 返回工具调用？
  ├── 是：执行工具 → 将结果反馈给 LLM → 继续循环
  └── 否：输出最终回复 → 结束
```

**关键特性**：
- **最大迭代次数**：50 次（防止无限循环）
- **工具输出截断**：每个工具输出最大 50,000 字符
- **流式输出**：`text_delta`、`reasoning_delta`、`tool_call_delta`、`tool_result`、`done`、`error`、`cancelled`
- **取消机制**：支持整个对话取消和单个工具调用取消
- **中断恢复**：取消时自动 backfill 缺失的工具结果消息（保证 LLM API 格式正确）
- **CompositeRouter**：合并 MCP Router（容器内工具）和 Builtin Router（服务端内置工具）

### 3. Agent Manager (`internal/agent/manager.go`)

系统的中央协调器，管理 Agent 全生命周期：

| 操作 | 描述 |
|------|------|
| Create | 创建 Agent → 创建 Docker 容器 → 启动 → 复制 MCP 二进制 → 发现工具 → 写入 YAML → 加载 Registry |
| Start | 启动已停止 Agent → 重启容器 → 重新加载 MCP 工具 |
| Stop | 停止 Agent → 清理运行时状态 → 停止容器 |
| Delete | 删除 Agent → 容器 + 消息 + 定时任务全部清理 |
| SyncAgentStates | 启动时与 Docker 实际状态同步（running/stopped/removed） |
| ExecutePrompt | 实现 `cron.AgentExecutor` 接口，供定时任务调度器调用 |

**运行时状态**（内存中 map）：
- `registries` — 每个 Agent 的工具注册表
- `routers` — 每个 Agent 的工具路由器
- `activeSessions` — 活跃对话取消函数
- `activeToolCalls` — 活跃工具调用取消函数
- `eventHub` — 中央事件发布中心

### 4. MCP 系统 (`internal/mcp/`)

采用 **"Ephemeral Process"（临时进程）** 架构 —— 不维持长连接，每次工具调用启动新进程：

```
Agent Loop 调用工具
  → MCP Router 解析 "core-shell_exec" → server="core", tool="shell_exec"
    → Executor: docker exec 启动 mcp-server-core 进程
      → ephemeralTransport: JSON-RPC initialize → tools/call → 读取结果
        → 进程退出（stdin EOF）
```

**工具名格式**：`"mcpname-toolname"`，如 `core-shell_exec`、`desktop-screenshot`。

**MCP Server 列表**：
| Server | 语言 | 工具 |
|--------|------|------|
| core | Go | `shell_exec`, `file_read`, `file_write`, `file_list` 等 |
| desktop | Go | `screenshot`, `mouse_click`, `mouse_move`, `keyboard_type`, `keyboard_key`, `open_app` |
| browser | Go + Node.js | 浏览器自动化（基于 Playwright Chromium） |
| search | Python | 网页搜索 |
| wechat | Python | 微信自动化（AT-SPI2 辅助功能接口） |

### 5. SSE 事件系统 (`internal/agent/eventbus.go`)

双层事件总线架构：

- **GlobalBus**：广播 Agent 级别事件（`session_created`, `session_updated`, `session_deleted`, `cron_execution_*`）
- **SessionBus**：每个会话独立的流式事件推送
  - **Snapshot 机制**：新订阅者连接时获取当前 turn 的累积事件（中途刷新不丢失）
  - 非阻塞发布（慢消费者丢弃事件）
  - 终止事件（done/cancelled/error）后自动清空 buffer

### 6. LLM 客户端 (`internal/llm/`)

- 支持 OpenAI / Anthropic / DeepSeek 等多种 Provider
- 流式 SSE 解析与组装（处理 Anthropic 的 index 复用问题）
- 自动处理推理模型的 `reasoning_content`
- 工具调用参数规范化（空参数 → `{}`，非法 JSON → `_raw` 包装）
- 请求/响应 dump 到 `/tmp/seaturt-llm-dumps/` 便于调试
- `ContentFormatter` 接口：OpenAI / Anthropic 两种实现
- `Content` 类型自定义 JSON 序列化（单文本 → 字符串，混合 → 数组）

### 7. 数据库层 (`internal/store/sqlite.go`)

5 张表：

| 表名 | 说明 |
|------|------|
| `agents` | Agent 元数据（id, name, status, container_id, config JSON） |
| `messages` | 对话消息（role, content JSON, reasoning_content, tool_calls） |
| `sessions` | 会话（多会话支持） |
| `cron_jobs` | 定时任务（cron / at 两种类型） |
| `cron_job_executions` | 执行记录 |

**特殊功能**：**图片外化** — 大 base64 图片数据保存到 `.seaturt/uploads/` 文件，DB 中只存路径，读取时自动还原。

### 8. Docker / 沙箱 (`internal/container/docker.go`)

- 创建容器：绑定 `/workspace` 卷，映射 18 个常用端口到 `127.0.0.1` 随机端口
- `ExecStdio()` — 交互式 exec（hijacked 连接），用于 MCP stdio 通信
- `CopyToContainer()` — 通过 tar 归档将文件复制到容器
- 共享内存 2GB（用于浏览器渲染）

**沙箱镜像**（`docker/sandbox/Dockerfile`）：
- 基于 `linuxserver/webtop:ubuntu-kde`（KDE Plasma 桌面 + s6 进程管理器）
- 包含开发工具、CJK 字体、桌面自动化工具
- Node.js 20 + Playwright Chromium
- Selkies WebRTC 远程桌面（端口 3000/3001）

### 9. Builtin 工具 (`internal/builtin/`)

不通过 MCP 协议，直接在服务端执行的内置工具（前缀 `builtin-`）：

- `builtin-create_cron_job` — 创建定时/延迟任务
- `builtin-list_cron_jobs` — 列出当前 Agent 的定时任务
- `builtin-update_cron_job` — 更新定时任务
- `builtin-delete_cron_job` — 删除定时任务

通过 `CompositeRouter` 与 MCP Router 合并，Agent 在对话中可直接调用。

### 10. 定时任务调度 (`internal/cron/scheduler.go`)

| 类型 | 表达式 | 行为 |
|------|--------|------|
| `cron` | 5 位 cron 表达式 | 周期性执行 |
| `at` | RFC 3339 时间 | 一次性执行后自动禁用 |

- 执行跳过保护（同一任务不允许重叠执行）
- 每次执行记录 success / failed / skipped

---

## API 路由一览

```
GET    /health                                         # 健康检查

GET    /api/events                                     # 全局 SSE 事件流
GET    /api/models                                     # 所有可用模型

POST   /api/agents                                     # 创建 Agent
GET    /api/agents                                     # 列出所有 Agent
GET    /api/agents/:id                                 # 获取单个 Agent
POST   /api/agents/:id/start                           # 启动 Agent
POST   /api/agents/:id/stop                            # 停止 Agent
DELETE /api/agents/:id                                 # 删除 Agent
GET    /api/agents/:id/ports                           # 端口映射
GET    /api/agents/:id/system-prompt                   # 获取系统提示词
PUT    /api/agents/:id/system-prompt                   # 更新系统提示词
GET    /api/agents/:id/desktop                         # 桌面状态 + URL

GET    /api/agents/:id/sessions                        # 列出会话
POST   /api/agents/:id/sessions                        # 创建会话
PUT    /api/agents/:id/sessions/:sid                   # 更新会话
DELETE /api/agents/:id/sessions/:sid                   # 删除会话

POST   /api/agents/:id/sessions/:sid/chat              # 发送消息（异步）
POST   /api/agents/:id/sessions/:sid/chat/cancel       # 取消对话
POST   /api/agents/:id/sessions/:sid/chat/cancel-tool/:toolCallId  # 取消工具调用
GET    /api/agents/:id/sessions/:sid/history           # 对话历史
DELETE /api/agents/:id/sessions/:sid/history            # 清空对话历史
GET    /api/agents/:id/sessions/:sid/events            # 会话 SSE 事件流

GET    /api/agents/:id/files                           # 列出工作区文件
GET    /api/agents/:id/files/*filepath                 # 读取文件
POST   /api/agents/:id/files                           # 上传文件

GET    /api/agents/:id/cron-jobs                       # 列出定时任务
POST   /api/agents/:id/cron-jobs                       # 创建定时任务
GET    /api/agents/:id/cron-jobs/:jid                  # 获取定时任务
PUT    /api/agents/:id/cron-jobs/:jid                  # 更新定时任务
DELETE /api/agents/:id/cron-jobs/:jid                  # 删除定时任务
POST   /api/agents/:id/cron-jobs/:jid/trigger          # 手动触发
GET    /api/agents/:id/cron-jobs/:jid/history          # 执行历史
```

### 对话交互模式

Chat 接口采用 **异步 + SSE** 模式：

1. `POST /chat` 立即返回 `{turn_id, message_id}`
2. Agent Loop 在后台 goroutine 中运行
3. 所有流式事件通过 `GET /events`（Session SSE）推送
4. 上下文不依赖 HTTP 请求生命周期（使用 `context.Background()`）

---

## 数据流

```
Frontend (SSE 订阅 /api/agents/:id/sessions/:sid/events)
    ↕
POST /api/agents/:id/sessions/:sid/chat
    → 保存用户消息 → 发布 user_message → 启动后台 goroutine
    → RunLoop:
        ├── LLM ChatCompletion (流式)
        │     → 发布 text_delta / reasoning_delta / tool_call_delta
        ├── 工具调用 → CompositeRouter
        │     ├── MCP Router → Executor → docker exec → MCP 进程 → 结果
        │     └── Builtin Router → CronHandler → 直接执行
        │     → 发布 tool_result
        └── 循环直到无工具调用 → 发布 done
```

---

## 配置系统

配置加载优先级：`$CONFIG_PATH` → `./config.yaml` → `~/.seaturt/config.yaml`。环境变量覆盖 YAML。

参考 `config.yaml.example`：

```yaml
server_port: 8080
log_level: debug
sandbox_image: seaturt/sandbox:latest
workspace_root: ~/.seaturt/workspaces
db_path: ~/.seaturt/data.db

providers:
  deepseek:
    base_url: https://api.deepseek.com/v1
    api: openai-completions
    api_key: <your-key>
    models:
      - id: deepseek-chat
        name: DeepSeek V3
        reasoning: false
        input: [text]
        context_window: 64000
        max_tokens: 8192

default_provider: deepseek
default_model: deepseek-chat

default_mcp_servers:
  - name: core
    command: mcp-server-core
  - name: desktop
    command: mcp-server-desktop
  - name: browser
    command: mcp-server-browser

container:
  shm_size: 2147483648  # 2GB
```

**支持的环境变量**：`SERVER_PORT`, `LOG_LEVEL`, `DOCKER_HOST`, `SANDBOX_IMAGE`, `WORKSPACE_ROOT`, `DB_PATH`, `COMMAND_TIMEOUT`, `DEFAULT_PROVIDER`, `DEFAULT_MODEL`, `MAX_IMAGE_SIZE`, `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL`

---

## 构建与运行

```bash
# 编译服务端
make build              # → bin/containeragent-server

# 编译并运行
make run

# 构建前端（需要 seaturt-web 在上级目录）
make build-web

# 编译 MCP Server 二进制
make build-mcp

# 全量发布（前端 + 后端 + MCP + Docker 镜像）
make release

# 构建沙箱 Docker 镜像
make build-test-image   # → containeragent/sandbox:test
make build-image        # → seaturt/sandbox:latest

# 代码整理
make tidy               # go mod tidy
make lint               # golangci-lint
```

---

## 测试

```bash
# 单元测试（internal/ 下各模块）
make test

# 集成测试（使用 Mock LLM Server，无需真实 LLM/Docker）
make test-integration   # timeout 10m

# 端到端测试（需要真实 LLM API + Docker）
make test-e2e           # timeout 30m
```

**测试结构**：
- `internal/*/` 下的 `*_test.go` — 各模块单元测试
- `tests/integration/` — 集成测试（Mock LLM，覆盖 API、对话取消、工具取消、CORS、桌面、文件、历史、MCP、多模态等）
- `tests/e2e/` — 端到端测试

---

## 关键依赖

| 依赖 | 用途 |
|------|------|
| `github.com/gin-gonic/gin` | HTTP Web 框架 |
| `github.com/docker/docker` | Docker API 客户端 |
| `modernc.org/sqlite` | 纯 Go SQLite 驱动（无 CGO） |
| `gopkg.in/yaml.v3` | YAML 配置解析 |
| `github.com/robfig/cron/v3` | Cron 表达式调度 |
| `github.com/stretchr/testify` | 测试断言库 |

---

## 关键设计决策

1. **Ephemeral MCP Process**：每次工具调用启动新进程而非维持长连接。牺牲了一些延迟（约几十 ms），但换来了极高的稳定性和简洁性——无需管理连接池、心跳、重连。

2. **异步 Chat + SSE**：`POST /chat` 立即返回，Agent Loop 在后台 goroutine 运行。所有事件通过 SSE 推送。HTTP 请求和 Agent 执行完全解耦。

3. **CompositeRouter**：将容器内 MCP 工具和服务端内置工具统一到同一个路由接口，Agent Loop 无需区分工具来源。

4. **图片外化存储**：大 base64 图片从 SQLite 中剥离到文件系统，避免数据库膨胀，读取时透明还原。

5. **工具名命名空间**：`"mcpname-toolname"` 格式（如 `core-shell_exec`），通过首个 `-` 分隔实现路由，避免不同 MCP Server 工具名冲突。

6. **Snapshot 机制**：SSE 订阅者中途连接时可获取当前 turn 的累积事件快照，解决页面刷新丢失问题。
