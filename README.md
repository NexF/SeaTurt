# SeaTurt 小海龟

基于容器隔离的智能助手平台。每个 Agent 运行在独立的 Docker 容器中，通过 MCP 协议执行工具调用，支持多 Agent 并行运行。

## 核心特性

- **容器隔离** — 每个 Agent 独立 Docker 容器，互不干扰
- **MCP 协议** — 通过 `docker exec` stdio 与容器内 MCP Server 通信
- **多 Tool 支持** — 内置 shell、文件读写，可扩展 git/browser/db
- **多模态输入** — 支持文字 + 图片（JSON base64 / multipart 上传），自动适配 OpenAI / Anthropic 格式
- **流式对话** — SSE 实时推送 Agent 执行过程和结果
- **Workspace 挂载** — 宿主机与容器共享文件目录
- **持久化存储** — SQLite 保存对话历史，图片自动落盘

## 架构概览

```
用户 → REST API → Agent Manager → LLM (tool_call)
                                     ↓
                              Tool Router → MCP Client → docker exec → 容器内 MCP Server
                                     ↑
                              MCP 执行结果 → 回传 LLM → 最终响应 → SSE 推送
```

## 快速开始

### 前置条件

- Go 1.21+
- Docker（推荐 [OrbStack](https://orbstack.dev)）

### 构建 & 启动

```bash
cd seaturt-server

# 构建沙箱镜像
docker build -t seaturt/sandbox:latest -f docker/sandbox/Dockerfile docker/sandbox/

# 启动服务
export LLM_API_KEY=sk-xxx
export LLM_BASE_URL=https://api.openai.com/v1
go run ./cmd/server/
```

### 基本使用

```bash
# 创建 Agent
curl -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{"name": "coder", "config": {"model": "claude-sonnet-4-20250514", "mcp_servers": [{"name": "core", "command": "mcp-server-core"}]}}'

# 对话（纯文本）
curl -X POST http://localhost:8080/api/agents/<agent-id>/chat \
  -H "Content-Type: application/json" \
  -d '{"content": [{"type": "text", "text": "列出 workspace 中的文件"}]}'

# 对话（图片 + 文字）
curl -X POST http://localhost:8080/api/agents/<agent-id>/chat \
  -H "Content-Type: application/json" \
  -d '{"content": [{"type": "text", "text": "描述这张图片"}, {"type": "image", "image": {"data": "<base64>", "mime_type": "image/jpeg"}}]}'

# 对话（multipart 文件上传）
curl -X POST http://localhost:8080/api/agents/<agent-id>/chat \
  -F "text=描述这张图片" -F "image=@photo.jpg"

# 停止 & 删除
curl -X POST http://localhost:8080/api/agents/<agent-id>/stop
curl -X DELETE http://localhost:8080/api/agents/<agent-id>
```

### 运行测试

```bash
# 构建测试镜像
docker build -t seaturt/sandbox:test -f docker/sandbox/Dockerfile docker/sandbox/

# 集成测试
go test ./tests/integration/... -v -tags=integration -timeout 10m
```

## 文档

| 文档 | 说明 |
|------|------|
| [架构设计](docs/architecture.md) | 核心架构、请求流转、技术选型、多模态适配 |
| [Agent 架构](docs/agent.md) | Agent 数据模型、生命周期、Loop 循环、MCP/LLM 通信、持久化 |
| [API 参考](docs/api.md) | REST API、数据模型、多模态请求示例 |
| [Docker 容器管理](docs/docker.md) | 网络模式、端口映射、通信机制、镜像策略、生命周期 |
| [MCP 通信](docs/mcp.md) | MCP 协议、Tool Router、MCP Server 清单、多模态支持 |
| [容器与安全](docs/container.md) | 镜像设计、安全措施、Workspace 挂载 |
| [测试指南](docs/testing.md) | 测试策略、运行方式、用例矩阵 |
| [v0.0.2 多模态开发](docs/v0.0.2/development.md) | 多模态支持完整开发记录 |
| [v0.0.3 桌面环境](docs/v0.0.3/development.md) | VNC 桌面 + 截屏能力设计与开发计划 |

## License

MIT
