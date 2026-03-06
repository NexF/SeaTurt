# 测试指南

## 概述

项目采用 **单元测试 + 集成测试 + E2E 测试** 三层策略。

| 层面 | Build Tag | 超时 | 说明 |
|------|-----------|------|------|
| 单元测试 | 无 | 1min | 纯函数/组件独立测试，无外部依赖 |
| 集成测试 | `integration` | 10min | Mock LLM + 真实 Docker，验证组件协作（支持并行） |
| E2E 测试 | `e2e` | 30min | 真实 LLM + 真实 Docker，验证端到端流程 |

## 前置条件

- **Docker Engine** 已安装并运行
- **测试镜像** `seaturt/sandbox:test` 已构建
- **Go 1.21+**

```bash
# 构建测试镜像
make build-test-image
# 或手动
docker build -t seaturt/sandbox:test -f docker/sandbox/Dockerfile docker/sandbox/

# 构建桌面测试镜像（可选，桌面测试需要）
make build-desktop-image
```

## 运行单元测试

```bash
# 全部单元测试
go test ./internal/... -v

# Config 路径展开测试
go test ./internal/config/ -v -run TestLoad

# SSE 解析测试
go test ./internal/llm/ -v -run TestConsumeSSE
```

## 运行集成测试

所有集成测试均支持 **并行执行**（`t.Parallel()`），每个测试拥有独立的 Docker 容器和 SQLite DB，互不干扰。

```bash
# 全部（默认并发数 = GOMAXPROCS ≈ CPU 核数）
go test ./tests/integration/... -v -tags=integration -timeout 10m

# 指定并发数（推荐，避免 Docker 资源争抢）
go test ./tests/integration/... -v -tags=integration -timeout 10m -parallel 4

# 单个
go test ./tests/integration/... -v -tags=integration -run TestContainerLifecycle

# 按类
go test ./tests/integration/... -v -tags=integration -run "TestMCP"
```

**`-parallel` 建议值：**

| 机器配置 | 建议值 | 理由 |
|---------|-------|------|
| 4C 8G | 2~3 | 每个容器约占 0.5C + 200M 内存 |
| 8C 16G | 4~6 | 留余量给 Docker daemon |
| 16C 32G+ | 8~10 | Docker daemon 本身也有开销 |
| CI (GitHub Actions) | 3~4 | runner 通常 2~4 核 |

> Docker 不可用或镜像不存在时，测试会自动 SKIP。

## 运行 E2E 测试

E2E 测试需要真实 LLM provider。可通过 `config.yaml` 或环境变量配置：

```bash
# 方式1：使用 config.yaml（推荐，从项目目录运行即可）
cd seaturt-server
go test ./tests/e2e/... -v -tags=e2e -timeout 30m

# 方式2：通过环境变量
export LLM_API_KEY=sk-xxx
export LLM_BASE_URL=https://api.openai.com/v1
go test ./tests/e2e/... -v -tags=e2e -timeout 30m

# 单个测试
go test ./tests/e2e/... -v -tags=e2e -run TestE2E_BasicChat
go test ./tests/e2e/... -v -tags=e2e -run TestE2E_ChineseChat
```

> LLM provider 未配置、Docker 不可用或镜像不存在时，E2E 测试会自动 SKIP。

## 测试架构

### 目录结构

```
internal/
├── config/
│   └── config_test.go            # expandHome、Load() 路径展开单元测试
├── llm/
│   ├── client_test.go            # consumeSSE 解析单元测试（data:/data: 格式、UTF-8）
│   ├── validate_test.go          # ValidateContent 模型能力校验单元测试
│   ├── content_test.go           # ContentBlock JSON 序列化/反序列化单元测试
│   └── provider_test.go          # OpenAI/Anthropic Formatter 格式转换单元测试
tests/
├── integration/
│   ├── setup_test.go             # TestMain、Docker 初始化、辅助函数
│   ├── container_test.go         # IT-01, IT-02
│   ├── mcp_test.go               # IT-03 ~ IT-06
│   ├── loop_test.go              # IT-09
│   ├── api_test.go               # IT-10, IT-11
│   ├── multimodal_test.go        # IT-12 ~ IT-22（多模态全链路测试）
│   ├── workspace_test.go         # IT-39 ~ IT-48（Workspace 提示文件 + System Prompt 测试）
│   ├── desktop_test.go           # IT-29 ~ IT-38（桌面环境 + VNC 测试，v0.0.3）
│   └── mock_llm.go               # Mock LLM HTTP Server（支持多模态请求校验 + 请求捕获）
├── e2e/
│   ├── setup_test.go             # TestMain、真实 LLM 初始化
│   ├── chat_test.go              # E2E-01 ~ E2E-04
│   └── config_test.go            # E2E-05
└── testdata/
    └── fixtures/
```

### TestMain 生命周期

```
TestMain
  ├── 1. 连接 Docker Engine（失败则 SKIP）
  ├── 2. 检查测试镜像（不存在则 SKIP）
  ├── 3. 创建临时 workspace 根目录
  ├── 4. 创建临时 SQLite 数据库
  ├── 5. m.Run()  ← 并行执行所有测试（每个测试独立容器+DB）
  └── 6. 清理：关闭 DB、删除临时目录、清理残留容器
```

### Mock LLM（高保真）

`mock_llm.go` 提供 `MockLLMServer`，模拟 OpenAI 兼容 API：

- 按序列返回响应，每次请求按顺序消费
- 支持非流式和 SSE 流式，根据 `stream` 字段自动切换
- **SSE 格式与真实上游一致**：使用 `data:{json}` 无空格格式（而非 `data: {json}`）
- `CallCount` 字段记录调用次数
- `SetRequestCapture(func(body []byte))` 支持注入回调捕获原始请求体，用于验证 system prompt 等场景

```go
mockServer := NewMockLLMServer([]MockLLMResponse{
    {ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.FunctionCall{
        Name: "shell_exec", Arguments: `{"command":"echo hello"}`,
    }}}},
    {Content: "执行成功"},
})
defer mockServer.Close()
```

### 辅助函数

| 函数 | 说明 |
|------|------|
| `createTestContainer(t)` | 创建并启动测试容器，自动清理 |
| `cleanupTestContainers()` | 清理所有残留测试容器 |
| `newTestServer(t, responses)` | 创建完整 API Server 栈 |
| `doRequest(t, ts, method, path, body)` | HTTP 请求快捷方法 |
| `sendChat(t, ts, agentID, text)` | 发送 Chat 并消费完整 SSE 流 |
| `createDesktopAgent(t, ts, name)` | 创建桌面 Agent，失败返回 nil（用于 t.Skip 模式） |

## 测试用例矩阵

### 单元测试

| 测试函数 | 场景 |
|---------|------|
| `TestExpandHome` | `~` 路径展开（各种边界情况） |
| `TestLoad_YAMLPathsExpandHome` | YAML 中 `~` 路径经过 Load() 后被正确展开 |
| `TestLoad_AbsolutePathsUnchanged` | 绝对路径不被修改 |
| `TestLoad_EnvOverrideTakePrecedence` | 环境变量覆盖 YAML 且 `~` 仍被展开 |
| `TestLoad_DefaultsWhenNoConfig` | 无配置文件时使用默认值且 `~` 展开 |
| `TestConsumeSSE_DataWithSpace` | SSE `data: {json}` 格式（标准） |
| `TestConsumeSSE_DataWithoutSpace` | SSE `data:{json}` 格式（真实上游） |
| `TestConsumeSSE_ChineseUTF8Content` | 中文 UTF-8 内容正确解析 |
| `TestConsumeSSE_MultipleDeltas` | 多个 delta 拼接 |
| `TestConsumeSSE_ToolCalls` | tool_calls 解析 |
| `TestConsumeSSE_IgnoresNonDataLines` | 忽略注释和其他 SSE 行 |
| `TestConsumeSSE_MixedSpaceFormats` | 同一 stream 中混合有/无空格格式 |

### 集成测试（已实现）

| 编号 | 测试函数 | 场景 |
|------|---------|------|
| IT-01 | `TestContainerLifecycle` | 容器生命周期 create → start → stop → delete |
| IT-02 | `TestWorkspaceMount` | Workspace 双向挂载 |
| IT-03 | `TestMCPClientConnect` | MCP initialize 握手 |
| IT-04 | `TestMCPToolsList` | tools/list 返回预期 Tool |
| IT-05 | `TestMCPShellExec` | shell_exec 执行命令 |
| IT-06 | `TestMCPFileReadWrite` | MCP 文件读写 + 宿主机同步 |
| IT-09 | `TestAgentLoop` | Agent Loop（Mock LLM → MCP → 回传） |
| IT-09b | `TestAgentLoopMultipleTools` | 多 Tool 链式调用 |
| IT-10 | `TestAgentCRUDAPI` | Agent CRUD 全流程 |
| IT-10b | `TestAgentCreateValidation` | 创建参数校验 |
| IT-11 | `TestChatAPIStreaming` | Chat SSE 流式 |
| IT-11b | `TestChatAPIAgentNotRunning` | 已停止 Agent 聊天返回 400 |
| IT-12 | `TestChat_PureText` | 纯文本消息正常工作 |
| IT-15 | `TestChat_MultipartImageUpload` | multipart/form-data 图片上传 → 自动解析为 ContentBlock |
| IT-19 | `TestChat_FileReadImage` | file_read 图片 → 返回 image 类型 ToolContent |
| IT-20 | `TestChat_HistoryRoundtrip` | 多模态历史 roundtrip |
| IT-21 | `TestChat_ImageSizeLimit` | 图片尺寸超限 → 400 |
| IT-39 | `TestWorkspaceFiles_SystemMD` | 创建 Agent 后 `.seaturt/SYSTEM.md` 存在且内容正确 |
| IT-40 | `TestWorkspaceFiles_PortsMD` | 创建 Agent 后 `.seaturt/PORTS.md` 存在且包含正确端口映射 |
| IT-41 | `TestSystemPrompt_Default` | 未指定 system_prompt 时使用默认内容 |
| IT-42 | `TestSystemPrompt_Custom` | 指定 system_prompt 时追加到 SYSTEM.md |
| IT-43 | `TestSystemPrompt_Desktop` | SYSTEM.md 包含桌面相关指令（统一镜像，所有 Agent 均含桌面） |
| IT-44 | `TestSystemPrompt_UsedInLoop` | Chat 时 LLM 收到的 system message 来自 SYSTEM.md |
| IT-45 | `TestSystemPrompt_HotReload` | 修改 SYSTEM.md → 下次 Chat 使用新内容 |
| IT-46 | `TestSystemPrompt_Fallback` | 删除 SYSTEM.md → 使用 DefaultSystemPrompt |
| IT-47 | `TestPortsMD_Regenerated` | Stop/Start 后 PORTS.md 重新生成 |
| IT-48 | `TestPortsAPI` | `GET /api/agents/:id/ports` 返回正确映射 |
| IT-29 | `TestDesktop_ContainerWithDesktop` | 容器启动 → Selkies WebRTC 桌面端口可用 |
| IT-30 | `TestDesktop_WebRTCAccess` | Selkies Web 端口映射 |
| IT-31 | `TestDesktop_Screenshot` | desktop MCP Server screenshot 工具 |
| IT-37 | `TestDesktop_DynamicPorts` | 多 Agent 端口不冲突 |
| IT-38 | `TestDesktop_DesktopAPI` | `GET /api/agents/:id/desktop` 返回正确信息 |

> 统一镜像内置桌面环境，所有 Agent 均支持桌面功能，不再需要单独的桌面镜像。

### E2E 测试（已实现）

| 编号 | 测试函数 | 场景 |
|------|---------|------|
| E2E-01 | `TestE2E_BasicChat` | 真实 LLM 基础对话 + SSE 格式验证 |
| E2E-02 | `TestE2E_ChineseChat` | 中文对话 + UTF-8 编码验证（无乱码） |
| E2E-03 | `TestE2E_ToolExecution` | 真实 LLM 工具调用端到端 |
| E2E-04 | `TestE2E_SSEFormatValidation` | SSE 原始格式 + Content-Type charset 验证 |
| E2E-05 | `TestE2E_ConfigLoading` | config.yaml 加载 + 路径展开验证 |

### 待实现（Phase 2）

| 编号 | 场景 |
|------|------|
| IT-07 | Tool Router 多 MCP Server 路由 |
| IT-08 | MCP Client Pool 多连接 |
| IT-13 | 容器异常恢复 |
| IT-14 | 命令超时 |
| IT-16 | 多 Agent 并行 |
| IT-17 | 大输出截断 |
| IT-32 | `mouse_click` tool 执行成功（需桌面镜像） |
| IT-33 | `keyboard_type` tool 输入文字（需桌面镜像） |
| IT-34 | `open_app firefox` → 浏览器启动（需桌面镜像） |
| IT-35 | 截屏 → LLM 多模态传递（需桌面镜像 + Mock LLM） |
| E2E-06 | 多 MCP Server E2E |

## 编写新测试

1. 在 `tests/integration/` 下创建文件，首行加 `//go:build integration`
2. package 为 `integration`
3. **必须加 `t.Parallel()`**，确保测试可并行执行
4. 使用 `createTestContainer(t)` 获取容器（每次创建独立容器，天然隔离）
5. 使用 `testify` 的 `require`/`assert` 做断言
6. **避免使用全局共享状态**，每个测试应自包含

```go
//go:build integration

package integration

func TestMyFeature(t *testing.T) {
    t.Parallel()
    containerID, wsPath := createTestContainer(t)
    // ... 测试逻辑 ...
}
```

## CI 集成

```yaml
jobs:
  integration:
    runs-on: ubuntu-latest
    services:
      docker:
        image: docker:dind
        options: --privileged
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: make build-test-image
      - run: go test ./tests/integration/... -v -tags=integration -timeout 10m -parallel 4

  e2e:
    runs-on: ubuntu-latest
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - run: make build-test-image
      - run: go test ./tests/e2e/... -v -tags=e2e -timeout 30m
        env:
          LLM_API_KEY: ${{ secrets.LLM_API_KEY }}
          LLM_BASE_URL: ${{ secrets.LLM_BASE_URL }}
```

## 故障排查

| 现象 | 原因 | 解决 |
|------|------|------|
| 全部 SKIP | Docker 未运行或镜像不存在 | 启动 Docker，`make build-test-image` |
| 容器创建超时 | Docker 资源不足 | 清理无用容器/镜像，降低 `-parallel` 值 |
| 并行测试 OOM | 并发容器过多 | 降低 `-parallel` 值（如 2~3） |
| MCP 连接失败 | MCP Server 不在镜像中 | 检查镜像构建 |
| Mock LLM "no more responses" | 响应序列不够 | 增加 MockLLMResponse 数量 |
| SSE 解析失败 | 流式格式不匹配 | 检查 Content-Type 和 `data:` 前缀 |
| 桌面测试全部 SKIP | 桌面镜像未构建 | `make build-desktop-image` |
