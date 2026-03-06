# API 参考

## 模型

```
GET    /api/models                  # 获取可选模型列表（v0.1.0）
```

## Agent 管理

```
POST   /api/agents                  # 创建 Agent
GET    /api/agents                  # 列出所有 Agent
GET    /api/agents/:id              # 获取 Agent 详情
POST   /api/agents/:id/stop        # 停止 Agent
POST   /api/agents/:id/start       # 启动/恢复 Agent
DELETE /api/agents/:id              # 删除 Agent
GET    /api/agents/:id/ports        # 查询端口映射表（v0.0.4）
GET    /api/agents/:id/system-prompt   # 获取当前 SYSTEM.md 内容（v0.0.4）
PUT    /api/agents/:id/system-prompt   # 更新 SYSTEM.md（v0.0.4）
GET    /api/agents/:id/desktop      # 查询桌面状态和 VNC 访问信息（v0.0.3）
```

## 对话交互

```
POST   /api/agents/:id/chat        # 发送消息（SSE 流式响应）
GET    /api/agents/:id/history      # 获取对话历史
DELETE /api/agents/:id/history      # 清空对话历史
```

## 文件操作

```
GET    /api/agents/:id/files        # 列出 workspace 文件
GET    /api/agents/:id/files/*path  # 读取文件
PUT    /api/agents/:id/files/*path  # 上传/写入文件
DELETE /api/agents/:id/files/*path  # 删除文件
```

## WebSocket 实时通信

```
WS     /api/agents/:id/ws          # 实时对话 + 容器输出流
```

## 数据模型

### Model（v0.1.0）

`GET /api/models` 返回所有可选模型列表，供前端创建 Agent 时下拉选择。

```json
{
  "models": [
    {
      "id": "auto",
      "name": "auto",
      "provider": "gongfeng"
    },
    {
      "id": "gpt-4o",
      "name": "GPT-4o",
      "provider": "openai"
    }
  ],
  "default_model": "auto"
}
```

前端显示 `name`，创建 Agent 时传递 `id`。

### Agent

```json
{
  "id": "agent_abc123",
  "name": "我的编程助手",
  "status": "running",           // created | running | stopped | error
  "container_id": "docker_xyz",
  "image": "seaturt/sandbox:latest",
  "workspace_path": "/home/user/.seaturt/workspaces/agent_abc123",
  "config": {
    "model": "auto",
    "mcp_servers": [
      { "name": "core", "command": "mcp-server-core" }
    ],
    "extra_mounts": [],
    "env_vars": {}
  },
  "created_at": "2026-03-05T14:00:00Z",
  "updated_at": "2026-03-05T14:30:00Z"
}
```

### ContentBlock（v0.0.2）

Chat 请求使用 `ContentBlock` 数组作为消息内容，支持多模态输入：

```json
// 纯文本
{"type": "text", "text": "你好"}

// 图片
{"type": "image", "image": {"data": "<base64>", "mime_type": "image/png"}}
```

模型是否支持图片由 `config.yaml` 中 `models[].input` 字段声明（如 `[text, image]`）。向不支持图片的模型发送图片会返回 `400`。

## 使用示例

### 获取可选模型列表（v0.1.0）

```bash
curl http://localhost:8080/api/models
```

### 创建 Agent

```bash
# 最简创建（使用默认模型，自动挂载所有默认 MCP Servers）
curl -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{
    "name": "我的编程助手"
  }'

# 指定模型（model 传 id，可从 GET /api/models 获取）
curl -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{
    "name": "代码助手",
    "model": "gpt-4o"
  }'

# 启用桌面模式
curl -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{
    "name": "全栈助手",
    "model": "auto",
    "desktop": true
  }'
```

> 名称允许任意字符（中文、英文、符号均可），仅作展示用途，后端自动生成唯一 `agent_id`。
> MCP Servers 默认挂载所有配置中的 `default_mcp_servers`，无需手动指定。
> 桌面模式 Agent 使用 `seaturt/sandbox-desktop:latest` 镜像，自动添加 `mcp-server-desktop`，ShmSize 设为 2GB。

### 对话

#### 纯文本（JSON）

```bash
curl -X POST http://localhost:8080/api/agents/agent_abc123/chat \
  -H "Content-Type: application/json" \
  -d '{
    "content": [
      {"type": "text", "text": "请阅读 workspace 中的代码，帮我写单元测试"}
    ]
  }'
```

#### 多模态：文字 + 图片（JSON base64）

```bash
curl -X POST http://localhost:8080/api/agents/agent_abc123/chat \
  -H "Content-Type: application/json" \
  -d '{
    "content": [
      {"type": "text", "text": "描述这张图片"},
      {"type": "image", "image": {"data": "<base64编码>", "mime_type": "image/jpeg"}}
    ]
  }'
```

#### 多模态：文件上传（multipart/form-data）

```bash
curl -X POST http://localhost:8080/api/agents/agent_abc123/chat \
  -F "text=描述这张图片" \
  -F "image=@photo.jpg"
```

支持的图片格式：`image/jpeg`、`image/png`、`image/gif`、`image/webp`。大小上限由 `max_image_size` 配置（默认 20MB）。

### 端口查询（v0.0.4）

```bash
# 查询 Agent 的端口映射
curl http://localhost:8080/api/agents/agent_abc123/ports
```

响应：

```json
{
  "ports": {
    "22":    {"host_port": "32768", "description": "SSH"},
    "80":    {"host_port": "32769", "description": "HTTP"},
    "3000":  {"host_port": "32770", "description": "前端开发 (React/Next.js)"},
    "8080":  {"host_port": "32783", "description": "后端开发 (Go/Java)"}
  }
}
```

### System Prompt 管理（v0.0.4）

```bash
# 查看当前 system prompt
curl http://localhost:8080/api/agents/agent_abc123/system-prompt

# 更新 system prompt（热更新，下次 Chat 自动生效）
curl -X PUT http://localhost:8080/api/agents/agent_abc123/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"content": "你是一个专注于数据分析的助手。请始终使用 Python pandas 处理数据。"}'
```

### 生命周期管理

```bash
# 放文件到 workspace
cp myproject/ ~/.seaturt/workspaces/agent_abc123/

# 停止
curl -X POST http://localhost:8080/api/agents/agent_abc123/stop

# 删除
curl -X DELETE http://localhost:8080/api/agents/agent_abc123

# 列出所有
curl http://localhost:8080/api/agents
```

### 桌面状态查询（v0.0.3）

```bash
# 查询桌面 Agent 的远程桌面访问信息
curl http://localhost:8080/api/agents/agent_abc123/desktop
```

响应（桌面 Agent 运行中）：

```json
{
  "desktop_enabled": true,
  "kasmvnc_port": "32770",
  "kasmvnc_url": "http://localhost:32770",
  "resolution": "auto",
  "status": "running"
}
```

响应（非桌面 Agent）：

```json
{
  "desktop_enabled": false,
  "status": "running"
}
```
