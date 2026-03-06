# SeaTurt 小海龟

基于容器隔离的智能助手平台。每个 Agent 运行在独立的 Docker 容器中，通过 MCP 协议执行工具调用，支持多 Agent 并行运行。

## 核心特性

- **容器隔离** — 每个 Agent 独立 Docker 容器，互不干扰
- **MCP 协议** — 通过 `docker exec` stdio 与容器内 MCP Server 通信
- **内置工具** — shell 执行、文件读写（`mcp-server-core`）+ 桌面操作（`mcp-server-desktop`）
- **多模态输入** — 支持文字 + 图片（JSON base64 / multipart 上传），自动适配 OpenAI / Anthropic 格式
- **桌面环境** — 内置 KDE Plasma + Selkies WebRTC，Agent 可截屏、模拟鼠标键盘操作
- **流式对话** — SSE 实时推送 Agent 执行过程和结果
- **Workspace 挂载** — 宿主机与容器共享文件目录
- **统一端口映射** — 18 个常用端口自动映射（SSH/HTTP/VNC/Vite/DB 等）
- **持久化存储** — SQLite（WAL 模式）保存对话历史，图片自动外部化落盘
- **文件管理** — REST API 列出/读取/上传工作空间文件
- **结构化日志** — slog 结构化日志，记录 LLM 请求/响应/错误详情

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

- Go 1.23+
- Node.js 18+（前端构建）
- Docker（推荐 [OrbStack](https://orbstack.dev)）

### 构建沙箱镜像

```bash
cd seaturt-server

# 构建沙箱镜像（统一镜像，内置桌面环境 + MCP Server）
make build-image
```

### 开发模式（前后端分离）

前后端分别启动，适合开发调试：

```bash
# 终端 1：启动后端
cd seaturt-server
make build && ./bin/containeragent-server

# 终端 2：启动前端 dev server
cd seaturt-web
npm install
npm run dev          # → http://localhost:5173
```

### 生产模式（单一二进制）

前端通过 `go:embed` 嵌入后端二进制，部署时只需一个可执行文件：

```bash
cd seaturt-server

# 一键构建：编译前端 → 复制到 embed 目录 → 编译 Go 二进制
make release

# 产物在 bin/seaturt（约 36MB，内含前端静态资源）
ls -lh bin/seaturt

# 启动（前端自动嵌入，访问 http://localhost:8080 即可）
./bin/seaturt

# 或后台启动
nohup ./bin/seaturt > server.log 2>&1 &

# 实时查看日志
tail -f server.log
```

`make release` 等价于以下步骤：

```bash
# 1. 构建前端
cd seaturt-web && npm run build && cd ..

# 2. 复制前端产物到 Go embed 目录
rm -rf cmd/server/web/dist
cp -r ../seaturt-web/dist cmd/server/web/dist

# 3. 编译 Go 二进制（go:embed 自动打包前端）
go build -o bin/seaturt ./cmd/server/
```

### 配置

服务通过 `config.yaml` 配置（搜索顺序：`$CONFIG_PATH` → `./config.yaml` → `~/.seaturt/config.yaml`）。

也支持环境变量覆盖：

```bash
# 兼容模式：未配置 providers 时，自动创建 default provider
export LLM_API_KEY=sk-xxx
export LLM_BASE_URL=https://api.openai.com/v1

# 其他可覆盖项
export SERVER_PORT=8080
export SANDBOX_IMAGE=seaturt/sandbox:latest
export DEFAULT_MODEL=auto
```

### 基本使用

```bash
# 创建 Agent（使用默认模型和 MCP Server）
curl -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{"name": "coder"}'

# 创建 Agent（指定模型）
curl -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{"name": "coder", "model": "claude-sonnet-4-20250514"}'

# 对话（纯文本）
curl -N -X POST http://localhost:8080/api/agents/<agent-id>/chat \
  -H "Content-Type: application/json" \
  -d '{"content": [{"type": "text", "text": "列出 workspace 中的文件"}]}'

# 对话（图片 + 文字，JSON base64）
curl -N -X POST http://localhost:8080/api/agents/<agent-id>/chat \
  -H "Content-Type: application/json" \
  -d '{"content": [{"type": "text", "text": "描述这张图片"}, {"type": "image", "image": {"data": "<base64>", "mime_type": "image/jpeg"}}]}'

# 对话（multipart 文件上传）
curl -N -X POST http://localhost:8080/api/agents/<agent-id>/chat \
  -F "text=描述这张图片" -F "image=@photo.jpg"

# 查询桌面 Selkies 访问信息
curl http://localhost:8080/api/agents/<agent-id>/desktop

# 获取端口映射
curl http://localhost:8080/api/agents/<agent-id>/ports

# 查看对话历史
curl http://localhost:8080/api/agents/<agent-id>/history

# 列出工作空间文件
curl http://localhost:8080/api/agents/<agent-id>/files

# 停止 & 删除
curl -X POST http://localhost:8080/api/agents/<agent-id>/stop
curl -X DELETE http://localhost:8080/api/agents/<agent-id>
```

### 运行测试

```bash
cd seaturt-server

# 构建测试镜像
make build-test-image

# 单元测试
make test

# 集成测试
make test-integration
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
| [v0.0.3 桌面环境](docs/v0.0.3/development.md) | VNC 桌面 + 截屏能力设计与实现 |

## License

MIT
