# TODO

## MCP "技能"页

> 产品上将 MCP Server 称为"技能"，对用户更友好。

### 需求

1. **前端"技能"管理页** — 展示当前 Agent 已加载的技能列表，支持启用/禁用开关
2. **MCP 热加载** — 用户将 bin/py 文件放到 workspace 约定目录（如 `.seaturt/tools/`），后端自动扫描发现新的 MCP Server，无需重启 Agent
3. **Config 配置** — 每个 MCP Server 支持 config 配置项（如 API Key、参数等），前端可编辑
4. **启用/禁用** — 用户可决定是否启用某个已发现的技能

### 后端改动（预估）

| 改动 | 说明 |
|------|------|
| Executor 动态管理 | `Registry.Register()` / `Registry.Unregister()` — 支持单个 MCP 的热插拔 |
| 文件系统 watcher | 监听 `.seaturt/tools/` 目录变化，自动发现新 MCP Server |
| Config schema | MCP Server 支持声明 config schema，前端据此渲染配置表单 |
| 技能管理 API | `GET /api/agents/:id/tools` — 列出技能（含状态、config）|
|  | `PUT /api/agents/:id/tools/:name` — 启用/禁用、更新 config |
|  | `POST /api/agents/:id/tools/:name/reload` — 重新加载单个技能 |
| MCPServerConfig 扩展 | 增加 `enabled`、`config`、`source`（builtin/discovered）等字段 |
| Router 热更新 | 技能启用/禁用后重建 ToolRegistry 路由表 |

### 前端改动（预估）

- Agent 设置面板新增"技能"Tab
- 技能卡片：图标 + 名称 + 描述 + 启用开关
- Config 编辑表单（根据 schema 动态渲染）
- 已发现但未启用的技能显示为灰色

### 待定问题

- [ ] tools 目录约定路径：`.seaturt/tools/` 还是 `workspace/tools/`？
- [ ] MCP Server 的 config schema 格式：JSON Schema？自定义？
- [ ] 发现机制：纯文件扫描 or MCP Server 自带 manifest？
- [ ] 是否需要全局技能（跨 Agent 共享）vs 仅 per-Agent？

---

## Agent 定时任务（Cron + At）

> 让 Agent 具备定时任务能力，不再只被动等待用户消息。

### 需求

1. **Cron 定时任务** — 用户可为 Agent 配置周期性定时触发规则（cron 表达式），到点自动向 Agent 发送预设 prompt
2. **At 一次性任务** — 用户可指定一个未来时间点，到时执行一次后自动禁用
3. **任务管理** — 前端可查看、创建、编辑、删除定时任务，查看执行历史

### 场景举例

- 每天 9:00 让 Agent 抓取新闻摘要并推送到 Session（cron）
- 每 30 分钟执行一次监控脚本，异常时通知用户（cron）
- 明天 9 点提醒我开会（at）

### 后端改动（预估）

| 改动 | 说明 |
|------|------|
| 调度器 | 内嵌 cron scheduler（如 `robfig/cron`），管理所有 Agent 的定时任务；at 类型用单次触发 |
| `cron_jobs` 表 | `id`, `agent_id`, `type`, `session_id`, `cron_expr`, `run_at`, `prompt`, `enabled`, `last_run_at`, `next_run_at` |
| 任务执行 | 触发时：检查 Agent 状态 → 必要时自动 Start → 向指定 Session 发送 prompt → 记录执行结果；at 类型执行后自动 enabled=false |
| 任务管理 API | `GET /api/agents/:id/cron-jobs` — 列出定时任务 |
|  | `POST /api/agents/:id/cron-jobs` — 创建定时任务 |
|  | `PUT /api/agents/:id/cron-jobs/:jid` — 更新（含启用/禁用） |
|  | `DELETE /api/agents/:id/cron-jobs/:jid` — 删除 |
|  | `GET /api/agents/:id/cron-jobs/:jid/history` — 执行历史 |

### 前端改动（预估）

- 对话面板顶栏 Row 1 新增 ⏰ 按钮，打开独立的定时任务管理面板（不嵌入设置）
- 创建表单支持 cron/at 类型切换
- 任务卡片：类型标识 + 表达式/时间 + prompt + 启用开关 + 下次执行时间
- 执行历史列表：时间、状态、Session 链接

### 待定问题

- [x] cron 精度：分钟级
- [x] 定时任务的 Session 策略：默认 `fixed`（复用固定 Session），绑定的 Session 被删除时自动重建
- [x] 支持一次性定时任务（at 语义）
- [ ] 执行历史保留策略：保留最近 N 条 or 按时间清理？
- [ ] 是否支持一次性定时任务（at 语义）？