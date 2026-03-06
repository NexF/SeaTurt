# TODO

## MCP "技能"页

> 产品上将 MCP Server 称为"技能"，对用户更友好。

### 需求

1. **前端"技能"管理页** — 展示当前 Agent 已加载的技能列表，支持启用/禁用开关
2. **MCP 热加载** — 用户将 bin/py 文件放到 workspace 约定目录（如 `.seaturt/skills/`），后端自动扫描发现新的 MCP Server，无需重启 Agent
3. **Config 配置** — 每个 MCP Server 支持 config 配置项（如 API Key、参数等），前端可编辑
4. **启用/禁用** — 用户可决定是否启用某个已发现的技能

### 后端改动（预估）

| 改动 | 说明 |
|------|------|
| Executor 动态管理 | `Registry.Register()` / `Registry.Unregister()` — 支持单个 MCP 的热插拔 |
| 文件系统 watcher | 监听 `.seaturt/skills/` 目录变化，自动发现新 MCP Server |
| Config schema | MCP Server 支持声明 config schema，前端据此渲染配置表单 |
| 技能管理 API | `GET /api/agents/:id/skills` — 列出技能（含状态、config）|
|  | `PUT /api/agents/:id/skills/:name` — 启用/禁用、更新 config |
|  | `POST /api/agents/:id/skills/:name/reload` — 重新加载单个技能 |
| MCPServerConfig 扩展 | 增加 `enabled`、`config`、`source`（builtin/discovered）等字段 |
| Router 热更新 | 技能启用/禁用后重建 ToolRegistry 路由表 |

### 前端改动（预估）

- Agent 设置面板新增"技能"Tab
- 技能卡片：图标 + 名称 + 描述 + 启用开关
- Config 编辑表单（根据 schema 动态渲染）
- 已发现但未启用的技能显示为灰色

### 待定问题

- [ ] skills 目录约定路径：`.seaturt/skills/` 还是 `workspace/skills/`？
- [ ] MCP Server 的 config schema 格式：JSON Schema？自定义？
- [ ] 发现机制：纯文件扫描 or MCP Server 自带 manifest？
- [ ] 是否需要全局技能（跨 Agent 共享）vs 仅 per-Agent？

---

## Browser 技能 — 基于 Playwright MCP 的轻量浏览器操作

> 与桌面 VNC 方案互补，提供轻量级的浏览器操作能力。

### 方案

使用微软官方的 [`@playwright/mcp`](https://github.com/microsoft/playwright-mcp)（MIT 开源），作为容器内的内置 MCP Server。

### 对比现有桌面方案

| | 桌面 VNC（现有） | Playwright MCP（新增） |
|---|---|---|
| **重量** | 重（完整 GNOME + VNC） | 轻（Node 进程 + 无头浏览器） |
| **能力** | 任意 GUI 操作 | 仅浏览器 |
| **精度** | 截屏 + 坐标点击，容易偏 | CSS 选择器精确定位 |
| **速度** | 慢（截屏→LLM→坐标） | 快（直接 API 调用） |
| **适合** | 桌面软件操作 | 网页搜索、填表、信息提取 |

### 需要做的事

- [ ] 容器镜像安装 Node.js + Playwright 浏览器依赖
- [ ] 将 `@playwright/mcp` 注册为内置 MCP Server
- [ ] 配合"技能"页支持启用/禁用
- [ ] 考虑是否默认启用（轻量 Agent 不需要完整桌面时可只用 Browser 技能）

### 待定问题

- [ ] 是否需要同时支持有头模式（配合 Selkies WebRTC 让用户看到浏览器操作过程）？
- [ ] 和桌面 Agent 的浏览器是否复用同一个实例？
