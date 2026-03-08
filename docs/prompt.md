# System Prompt 机制

## 概览

Agent 的 system prompt 采用 **模板驱动 + 延迟渲染** 方式：
- **创建时**：将 `prompts/system.md` 模板**原样复制**到 `workspace/.seaturt/SYSTEM.md`（保留 Go template 占位符）
- **Chat 时**：读取模板 → 实时渲染占位符（当前时间、MCP Server 列表等）→ 作为 system message 发给 LLM
- 支持运行时热更新（用户编辑模板后下次 Chat 立即生效）

## 核心流程

```
创建 Agent                              每次 Chat
   │                                       │
   ▼                                       ▼
copySystemPromptTemplate()          LoadSystemPrompt()
   │                                       │
   ▼                                       ▼
复制模板原文到 SYSTEM.md             读取 SYSTEM.md（模板）
（保留 {{.CurrentDate}} 等占位符）          │
   │                                       ▼
   │  如有 ExtraRules                从 registry 获取 MCP Server 列表
   │  → 追加到模板末尾               获取当前时间
   │                                       │
   ▼                                       ▼
{workspace}/.seaturt/SYSTEM.md      RenderSystemTemplate()
                                    渲染 Go template 占位符
                                           │
                                           ▼
                                    文件不存在？
                                    ├─ 是 → DefaultSystemPrompt（硬编码 fallback）
                                    └─ 否 → 渲染失败？
                                           ├─ 是 → 返回原始模板内容
                                           └─ 否 → 渲染后的内容
                                           │
                                           ▼
                                    注入 messages[0]（role: "system"）
                                           │
                                           ▼
                                    发送给 LLM
```

## 关键文件

| 文件 | 职责 |
|------|------|
| `seaturt-server/prompts/system.md` | system prompt 模板文件（Go template 语法，源码） |
| `internal/agent/systemprompt.go` | `RenderSystemTemplate()` 渲染模板；`GenerateSystemMD()` + `generateSystemMDFallback()` 供测试和 fallback |
| `internal/agent/loop.go` | `DefaultSystemPrompt` fallback + `RunLoop()` 注入 system message |
| `internal/agent/manager.go` | `Create()` 复制模板；`LoadSystemPrompt()` 读模板+实时渲染；`copySystemPromptTemplate()` 辅助函数 |
| `internal/config/config.go` | `GetPromptsDir()` 定位 prompts 目录 |
| `internal/api/chat_handler.go` | Chat 时调用 `LoadSystemPrompt()` 传入 `LoopConfig` |
| `internal/api/agent_handler.go` | `GET/PUT /api/agents/:id/system-prompt` 接口 |
| `seaturt-web/.../AgentSettings.tsx` | 前端 System Prompt 编辑器 UI |

## 1. 模板文件（prompts/system.md）

模板使用 Go `text/template` 语法，包含以下占位符：

| 占位符 | 类型 | 渲染时机 | 来源 |
|--------|------|----------|------|
| `{{.CurrentDate}}` | `string` | Chat 时 | `time.Now().Format("2006-01-02")` |
| `{{.CurrentTime}}` | `string` | Chat 时 | `time.Now().Format("15:04:05")` |
| `{{.MCPServers}}` | `[]MCPServerConfig` | Chat 时 | `registry.ServerNames()` 运行时状态 |
| `{{.Name}}` | `string` | Chat 时 | `range .MCPServers` 内部，单个 server 名称 |
| `{{.ExtraRules}}` | `string` | Create 时追加 | 用户自定义附加指令（静态文本，非模板渲染） |
| `{{.EnvVars}}` | `map[string]string` | 预留 | 当前模板未使用 |

### 模板内容概要

- **身份**：编程助手，运行在沙箱容器中
- **当前时间**：`{{.CurrentDate}} {{.CurrentTime}}`（每次 Chat 实时获取）
- **行为准则**：优先用工具、精确修改、非交互模式、简洁输出、工具调用格式约束
- **工作目录**：`/workspace`（宿主机共享）
- **端口使用**：引导查看 PORTS.md
- **桌面环境**：KDE Plasma + Selkies 远程桌面
- **可用工具**：`{{range .MCPServers}}` 动态列出（来自 registry 运行时状态）
- **附加指令**：`{{.ExtraRules}}`（如有）

## 2. 渲染函数（systemprompt.go）

### SystemPromptConfig

```go
type SystemPromptConfig struct {
    MCPServers  []MCPServerConfig // MCP Server 列表
    EnvVars     map[string]string // 自定义环境变量（预留）
    ExtraRules  string            // 用户自定义附加规则
    CurrentDate string            // 当前日期，如 "2026-03-08"
    CurrentTime string            // 当前时间，如 "15:04:05"
}
```

### RenderSystemTemplate()

纯模板渲染函数，用于 Chat 时将模板 + config 渲染为最终 prompt：

```go
func RenderSystemTemplate(tmplContent string, cfg SystemPromptConfig) (string, error) {
    tmpl, err := template.New("system").Parse(tmplContent)
    // ...
    var buf strings.Builder
    tmpl.Execute(&buf, cfg)
    return buf.String(), nil
}
```

### GenerateSystemMD() / generateSystemMDFallback()

保留供测试和 fallback 使用。`GenerateSystemMD(cfg, promptsDir)` 从文件读模板并渲染；失败时 `generateSystemMDFallback(cfg)` 使用硬编码常量拼接。

## 3. 创建时复制模板（manager.go → Create）

```go
// Create() 中，loadMCPServers + registry.LoadFromDir 之后：
promptsDir := m.cfg.GetPromptsDir()
copySystemPromptTemplate(promptsDir, seaturtDir, req.SystemPrompt)
```

`copySystemPromptTemplate()` 做三件事：
1. 从 `promptsDir/system.md` 读取模板文件
2. 如果读取失败，fallback 到硬编码常量（`systemPromptBase + systemPromptDesktop`）
3. 如有 `extraRules`，追加 `## 附加指令` section 到模板末尾
4. 原样写入 `workspace/.seaturt/SYSTEM.md`（**不渲染占位符**）

同时生成 `PORTS.md`（端口映射表，由 `GeneratePortsMD()` 生成）。

## 4. Chat 时读取+渲染（manager.go → LoadSystemPrompt）

```go
func (m *Manager) LoadSystemPrompt(ag *Agent) string {
    path := filepath.Join(ag.WorkspacePath, ".seaturt", "SYSTEM.md")
    data, err := os.ReadFile(path)
    if err != nil {
        return DefaultSystemPrompt  // fallback
    }

    // 从 registry 获取实际运行的 MCP Server 列表
    m.mu.RLock()
    reg := m.registries[ag.ID]
    m.mu.RUnlock()

    var mcpServers []MCPServerConfig
    if reg != nil {
        for _, name := range reg.ServerNames() {
            mcpServers = append(mcpServers, MCPServerConfig{Name: name})
        }
    }

    now := time.Now()
    cfg := SystemPromptConfig{
        MCPServers:  mcpServers,
        CurrentDate: now.Format("2006-01-02"),
        CurrentTime: now.Format("15:04:05"),
    }

    rendered, err := RenderSystemTemplate(string(data), cfg)
    if err != nil {
        return string(data) // 渲染失败返回原始内容
    }
    return rendered
}
```

每次 Chat **都重新读文件+重新渲染**：
- **时间**：每次 Chat 获取实时时间
- **MCP 列表**：从 registry 运行时状态获取（动态加载的 MCP 自动出现）
- **模板修改**：用户编辑 SYSTEM.md 后下次 Chat 立即生效

## 5. 注入 LLM（loop.go → RunLoop）

```go
prompt := cfg.SystemPrompt
if prompt == "" {
    prompt = DefaultSystemPrompt
}
if len(history) == 0 || history[0].Role != "system" {
    history = prepend(history, ChatMessage{
        Role:    "system",
        Content: prompt,
    })
}
```

- 如果 `LoopConfig.SystemPrompt` 非空，使用它（即 `LoadSystemPrompt()` 的渲染结果）
- 否则 fallback 到 `DefaultSystemPrompt`（4 行英文硬编码）
- 作为 `messages[0]`（role=system）发送给 LLM

### DefaultSystemPrompt（fallback）

```
You are a helpful coding assistant running inside a sandboxed container.
You have access to tools that let you execute shell commands, read/write files, and more.
Always prefer using tools to answer questions when appropriate.
Be concise and precise in your responses.
```

## 6. API 管理

### GET /api/agents/:id/system-prompt

返回当前 SYSTEM.md **模板**内容（含占位符）：

```json
{ "system_prompt": "# Agent 系统指令\n\n## 当前时间\n{{.CurrentDate}} {{.CurrentTime}}\n..." }
```

### PUT /api/agents/:id/system-prompt

直接覆写 SYSTEM.md 文件（body 即为新内容），下次 Chat 生效：

```bash
curl -X PUT http://localhost:8080/api/agents/{id}/system-prompt \
  --data-binary '# 自定义 prompt...'
```

注意：用户可以选择保留或移除模板占位符。如果保留 `{{.CurrentDate}}` 等，Chat 时会被实时渲染；如果写入纯文本，渲染后等价于原样返回。

## 7. 前端编辑（AgentSettings.tsx）

Agent 设置弹窗中提供 System Prompt 文本编辑器：
- 打开时 `GET /api/agents/:id/system-prompt` 加载（显示的是模板，含占位符）
- 编辑后点击「保存」`PUT /api/agents/:id/system-prompt` 写回
- 支持「重置」回退到上次保存的版本

## 8. workspace 中的 SYSTEM.md 示例

创建后（模板，含占位符）：
```markdown
# Agent 系统指令

## 身份
你是一个运行在沙箱容器中的编程助手...

## 当前时间
{{.CurrentDate}} {{.CurrentTime}}

## 行为准则
- 优先使用工具...

## 工作目录
当前工作目录为 `/workspace`...

## 端口使用
容器内以下端口已映射到宿主机...

## 桌面环境
本容器已启用 KDE Plasma 桌面环境...
{{if .MCPServers}}
## 可用工具

当前已加载的 MCP Server：
{{range .MCPServers}}- {{.Name}}
{{end}}
{{end}}
```

Chat 时渲染后（发给 LLM 的实际内容）：
```markdown
# Agent 系统指令

## 身份
你是一个运行在沙箱容器中的编程助手...

## 当前时间
2026-03-08 15:30:42

## 行为准则
- 优先使用工具...

## 工作目录
当前工作目录为 `/workspace`...

## 端口使用
容器内以下端口已映射到宿主机...

## 桌面环境
本容器已启用 KDE Plasma 桌面环境...

## 可用工具

当前已加载的 MCP Server：
- core
- desktop
- browser
- search
```

## 数据流总结

```
用户创建 Agent (system_prompt 字段)
        │
        ▼
Manager.Create()
  → copySystemPromptTemplate()   ← 复制 prompts/system.md 模板原文
  → ExtraRules? → 追加到模板末尾
  → 写入 {workspace}/.seaturt/SYSTEM.md（模板，含占位符）
        │
        │  (运行时可修改模板)
        │  ├─ PUT /api/agents/:id/system-prompt  ← 用户通过 API/前端
        │  └─ Agent 自身 file_write              ← Agent 自我调优
        │
        ▼
用户 Chat
  → ChatHandler.Chat()
  → Manager.LoadSystemPrompt()
     ├─ 读 SYSTEM.md 模板
     ├─ registry.ServerNames() → MCP 列表（运行时状态）
     ├─ time.Now() → 当前时间
     └─ RenderSystemTemplate() → 渲染占位符
  → LoopConfig{SystemPrompt: 渲染后的内容}
  → RunLoop()
  → messages[0] = {role: "system", content: prompt}
  → 发送给 LLM
```
