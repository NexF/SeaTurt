# 架构设计

## 产品概述

SeaTurt 小海龟是一个类似 OpenClaw 的智能助手平台，核心理念是**每个 Agent 运行在独立的 Docker 容器中**，通过挂载 workspace 目录实现与宿主机的数据交换。支持多 Agent 并行运行，每个 Agent 拥有完全隔离的运行环境，可独立管理生命周期（创建、运行、停止、删除）。

## 目标用户

- 需要 AI 助手执行代码、文件操作等任务的开发者
- 需要同时运行多个 AI 任务且要求资源隔离的用户
- 需要安全沙箱环境来执行 AI 生成代码的团队

## 系统架构

```
┌─────────────────────────────────────────────────────────────┐
│                        宿主机 (Host)                         │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │               SeaTurt Server                            │  │
│  │  ┌─────────┐ ┌──────────┐ ┌────────────────────────┐ │  │
│  │  │ REST API│ │ Agent Mgr│ │    Container Mgr       │ │  │
│  │  └─────────┘ └──────────┘ └────────────────────────┘ │  │
│  │  ┌─────────┐ ┌──────────┐ ┌────────────────────────┐ │  │
│  │  │ WS Hub  │ │ LLM Proxy│ │  MCP Client Pool       │ │  │
│  │  │         │ │          │ │  (N clients per agent)  │ │  │
│  │  └─────────┘ └──────────┘ ├────────────────────────┤ │  │
│  │                           │  Tool Router            │ │  │
│  │                           │  tool_name → MCP Server │ │  │
│  │                           └────────────────────────┘ │  │
│  └──────────────────┬────────────────────────────────────┘  │
│                     │ MCP protocol (stdio via docker exec)  │
│       ┌─────────────┼──────────────┐                        │
│       ▼             ▼              ▼                        │
│  ┌───────────┐ ┌───────────┐ ┌───────────┐                 │
│  │ Agent-A   │ │ Agent-B   │ │ Agent-C   │  ...            │
│  │ Container │ │ Container │ │ Container │                 │
│  │           │ │           │ │           │                 │
│  │┌─────────┐│ │┌─────────┐│ │┌─────────┐│                 │
│  ││MCP: core││ ││MCP: core││ ││MCP: core││                 │
│  ││ shell   ││ ││ shell   ││ ││ shell   ││                 │
│  ││ file_rw ││ ││ file_rw ││ ││ file_rw ││                 │
│  │├─────────┤│ │├─────────┤│ │└─────────┘│                 │
│  ││MCP: db  ││ ││MCP: web ││ │           │                 │
│  ││ query   ││ ││ browse  ││ │ (仅 core) │                 │
│  │└─────────┘│ │└─────────┘│ │           │                 │
│  │           │ │┌─────────┐│ │           │                 │
│  │           │ ││MCP: desk││ │           │                 │
│  │           │ ││ screensht│ │           │                 │
│  │           │ │└─────────┘│ │           │                 │
│  │/workspace │ │/workspace │ │/workspace │                 │
│  │  ↕ mount  │ │  ↕ mount  │ │  ↕ mount  │                 │
│  └────┬──────┘ └────┬──────┘ └────┬──────┘                 │
│       │             │             │                         │
│  ┌────▼─────┐ ┌────▼─────┐ ┌────▼─────┐                   │
│  │ ~/work/  │ │ ~/work/  │ │ ~/work/  │                   │
│  │ agent-a/ │ │ agent-b/ │ │ agent-c/ │                   │
│  └──────────┘ └──────────┘ └──────────┘                   │
│       宿主机 workspace 目录                                 │
└─────────────────────────────────────────────────────────────┘
```

## 请求流转

```
用户发送消息
    → Server 接收请求
    → Server 从 workspace/.seaturt/SYSTEM.md 加载 system prompt（不存在则 fallback 到默认）
    → Server 汇总该 Agent 所有 MCP Server 的 tools/list，合并为完整 tools 列表
    → Server 调用 LLM API（携带 system prompt + 合并后的 tools 定义）
    → LLM 返回 tool_call（如 shell_exec, db_query）
    → Tool Router 根据 tool_name 路由到对应的 MCP Server
    → Server 通过 docker exec 调用目标 MCP Server 执行
    → MCP Server 在容器内本地执行，返回结果
    → Server 将结果喂回 LLM，继续循环
    → LLM 返回最终文本响应 → 推送给用户
```

## Workspace 提示文件（v0.0.4）

每个 Agent 的 workspace 下有 `.seaturt/` 目录，存放运行时配置文件：

```
/workspace/
├── .seaturt/
│   ├── SYSTEM.md     ← Agent system prompt（创建时生成，支持热更新）
│   ├── PORTS.md      ← 端口映射表（容器启动后生成）
│   └── uploads/      ← 图片外部化存储（v0.0.2）
└── （用户工作文件）
```

### SYSTEM.md

System prompt 由 `GenerateSystemMD()` 动态组装：
- **静态部分**：身份、行为准则、工作目录说明、端口使用指南
- **动态部分**：MCP Server 列表、桌面指令（desktop=true 时）、用户自定义附加指令

每次 Chat 时从文件重新读取，支持运行时修改（Agent 可通过 `file_write` 自我调优）。文件不存在时 fallback 到 `DefaultSystemPrompt`。

### PORTS.md

端口映射表由 `GeneratePortsMD()` 在容器启动后生成，包含 20 个预映射端口的容器端口 ↔ 宿主机端口对照表。Stop/Start 后重新生成。

## 多模态支持（v0.0.2）

### 内部统一格式

SeaTurt 使用统一的 `ContentBlock` 作为内部消息格式，支持 `text`、`image`、`file` 类型。不同 Provider 的多模态格式在发送前自动转换。

### Provider 格式适配

```
用户请求（ContentBlock 数组）
    → ValidateContent() 校验模型是否支持该输入类型
    → Formatter 按 Provider API 类型转换格式
    → 发送给 LLM
```

| Provider API | Formatter | 图片格式 |
|-------------|-----------|---------|
| `openai-completions` | `OpenAIFormatter` | `{"type":"image_url","image_url":{"url":"data:...;base64,..."}}` |
| `anthropic-messages` | `AnthropicFormatter` | `{"type":"image","source":{"type":"base64","media_type":"...","data":"..."}}` |

Formatter 在 `llm.NewClient()` 创建时根据 `ProviderConfig.API` 字段绑定，同一 client 始终使用同一 formatter。

### 模型能力声明

通过 `config.yaml` 中 `models[].input` 字段声明模型支持的输入类型：

```yaml
models:
  - id: gpt-4o
    input: [text, image]    # 支持多模态
  - id: gpt-3.5-turbo
    input: [text]           # 仅文本
```

`input` 为空时默认只允许 `[text]`。向不支持的模型发送图片返回 `400`。

### 图片存储

用户发送的图片自动外部化到 `{workspace}/.seaturt/uploads/`，SQLite 中仅存储文件路径，读取时按需还原 base64 数据。

### MCP 多模态

MCP `ToolContent` 扩展支持 `image` 类型。`file_read` 读取二进制图片文件时返回 `{"type":"image","data":"base64...","mimeType":"image/png"}`，Agent Loop 自动转为 `ContentBlock` 传给 LLM。

## 桌面环境（v0.0.3）

### 概述

桌面模式为 Agent 容器提供完整的 KDE Plasma 桌面环境，通过 KasmVNC 提供高质量的 Web 远程访问，使 Agent 具备 GUI 操作能力（截屏、鼠标点击、键盘输入、窗口管理等）。

### 双镜像策略

| 镜像 | 大小 | 基础镜像 | 内容 | 使用场景 |
|------|------|---------|------|---------|
| `seaturt/sandbox:latest` | ~300MB | `ubuntu:22.04` | 基础工具 + mcp-server-core | 普通 Agent（默认） |
| `seaturt/sandbox-desktop:latest` | ~3GB | `lscr.io/linuxserver/webtop:ubuntu-kde` | KDE Plasma + KasmVNC + 开发工具 + mcp-server-core/desktop | 桌面 Agent |

通过 `desktop: true` 创建 Agent 时自动选择桌面镜像。

### 桌面容器进程模型

```
桌面容器启动后（由 s6-overlay 管理）：

PID 1: /init (s6-overlay)
       ├── KasmVNC Server      — VNC + Web Server (端口 3000/3001)
       ├── KWin (X11)          — KDE 窗口管理器
       ├── Plasmashell          — KDE 桌面面板
       ├── PulseAudio           — 音频服务
       └── (其他 KDE 服务)

docker exec 建立 MCP 连接后：
PID X: mcp-server-core     — 基础 tool（shell, file）
PID Y: mcp-server-desktop  — 桌面 tool（screenshot, mouse, keyboard）
```

### 桌面模式自动配置

创建 `desktop: true` Agent 时，Manager 自动执行：
1. 追加 `mcp-server-desktop` 到 MCP Server 列表
2. 选择 `seaturt/sandbox-desktop:latest` 镜像
3. 注入环境变量 `PUID`/`PGID`（LinuxServer 用户权限映射）、`TZ`（时区）
4. 设置 ShmSize 为 2GB（浏览器渲染需要）
5. 启动后查询端口映射，填充 KasmVNC 访问信息（端口 3000/3001）

## 技术选型

| 组件 | 技术 | 理由 |
|------|------|------|
| **后端服务** | Go (Gin) | 高性能、Docker SDK 原生支持 |
| **容器管理** | Docker Engine API | 直接通过 SDK 管理容器生命周期 |
| **MCP 通信** | stdio over docker exec | 标准 MCP 传输方式，无需容器暴露端口 |
| **MCP Server** | Go / Python | 容器内 MCP Server，提供 Tool 实现 |
| **数据存储** | SQLite | 轻量、单机部署无需额外依赖 |
| **实时通信** | WebSocket | 流式输出 Agent 执行结果 |
| **Agent 镜像** | 自定义 Dockerfile | 预装 MCP Server + 常用工具 |
| **LLM 集成** | OpenAI 兼容接口 | 支持多种模型后端，自动适配多模态格式 |
