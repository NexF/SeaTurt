# UI 设计稿

> 本文档包含所有线框图、交互细节、布局规范、响应式规则和主题设计。
> 持续更新，当前已包含 v0.1.1 桌面预览优化、v0.1.2 取消机制 UI、v0.2.0 Session 多会话。

---

## 核心概念（v0.2.0 更新）

### Agent vs Session

| 概念 | 性质 | 说明 |
|------|------|------|
| **Agent** | 重量级 | 绑定容器（Docker + 桌面环境 + MCP Server），创建/启停成本高 |
| **Session** | 轻量级 | Agent 下的一次独立对话，共享 Agent 的容器和工具，只有消息历史是隔离的 |

**类比**：Agent ≈ ChatGPT 中的 GPT（一个配置好的环境），Session ≈ 一次对话（conversation）。

**关键规则**：
- 一个 Agent 下可以有多个 Session
- 所有 Session **共享同一个容器**（文件系统、桌面、MCP Server）
- 每个 Session 有**独立的消息历史**和**独立的 LLM 上下文**
- 新建 Session 是即时的（不需要启动容器）
- 删除 Session 只删消息，不影响容器
- Agent 停止后，所有 Session 的历史保留，重启后可继续

---

## 整体布局

```
┌───────────────────────────────────────────────────────────────────┐
│ ┌──────────┐ ┌──────────────────────────────────┐ ┌────────────┐ │
│ │ Agent    │ │ Agent 名称  模型  状态   [⚙][📁] │ │            │ │
│ │ + Session│ ├──────────────────────────────────┤ │ Workspace  │ │
│ │ 列表     │ │                                  │ │ 文件树     │ │
│ │          │ │         对话区域                   │ │            │ │
│ │ 侧边栏   │ │     （消息气泡 + Tool 卡片）       │ │ 桌面预览   │ │
│ │          │ │                                  │ │            │ │
│ │ (固定)   │ │                                  │ │ (可收起)   │ │
│ │          │ ├──────────────────────────────────┤ │            │ │
│ │          │ │ [📎] 输入消息...          [发送]  │ │ (可收起)   │ │
│ └──────────┘ └──────────────────────────────────┘ └────────────┘ │
└───────────────────────────────────────────────────────────────────┘
     240px              自适应                         320px
```

- **左侧边栏**：固定 240px，Agent 列表（可折叠展开 Session 列表）+ 新建按钮
- **中间主区域**：当前 Session 的对话面板，自适应宽度
- **右侧边栏**：320px，可收起，当前 Agent 的 Workspace 文件树 + 桌面预览

---

## 页面线框图

### 左侧边栏：Agent + Session 列表（v0.2.0 更新）

```
┌──────────────────────────────────────────────────────┐
│ ┌─────────────┐ ┌──────────────────────────────────┐ │
│ │  SeaTurt 🐢 │ │                                  │ │
│ │             │ │                                  │ │
│ │ + 新建Agent │ │                                  │ │
│ │             │ │                                  │ │
│ │ ┌─────────┐ │ │                                  │ │
│ │ │● coder  │ │ │                                  │ │
│ │ │  gpt-4o │ │ │     当前 Session 的对话内容        │ │
│ │ │ [+新对话]│ │ │                                  │ │
│ │ ├─────────┤ │ │                                  │ │
│ │ │ ▸ 整理周报│ │ │                                  │ │
│ │ │ ▸ 数据分析│ │ │                                  │ │
│ │ │ ● 代码审查│ │ │  ← 当前选中的 Session             │ │
│ │ └─────────┘ │ │                                  │ │
│ │ ┌─────────┐ │ │                                  │ │
│ │ │○ writer │ │ │                                  │ │
│ │ │  claude │ │ │                                  │ │
│ │ └─────────┘ │ │                                  │ │
│ │             │ │                                  │ │
│ └─────────────┘ └──────────────────────────────────┘ │
└──────────────────────────────────────────────────────┘
```

**层级结构：**

```
侧边栏
├─ + 新建 Agent（按钮）
├─ ▾ Agent: coder (● running)          ← 点击展开/折叠 Session 列表
│   ├─ [+ 新对话]                      ← 快速新建 Session
│   ├─ 整理周报       3月5日            ← Session 条目（名称 + 时间）
│   ├─ 数据分析       3月6日
│   └─ ● 代码审查     3月8日            ← 当前活跃 Session（高亮）
├─ ▸ Agent: writer (○ stopped)          ← 折叠状态，不显示 Session
└─ 主题切换 🌙/☀️
```

**Agent 卡片行为：**
- 点击 Agent 名称区域 → 展开/折叠该 Agent 的 Session 列表
- 展开时自动选中最近的 Session（如果没有则新建一个）
- Agent 状态图标（●/○/⚠）显示在名称左侧
- 模型名显示在 Agent 名称下方（次要文字）
- 右键菜单不变（启动/停止/设置/删除）

**Session 条目行为：**
- 点击 → 切换到该 Session 的对话
- Session 名称：取首条用户消息的前 20 字作为标题，未发送过消息的显示"新对话"
- 右侧显示最后消息时间（今天显示时分，之前显示月日）
- 当前选中的 Session 高亮背景
- 鼠标 hover 显示删除按钮（🗑，点击需二次确认）

**[+ 新对话] 按钮：**
- 位于 Agent 展开后的 Session 列表顶部
- 点击 → 创建新 Session → 自动选中并切换到空对话
- Agent 未运行时也可以创建 Session（但输入框禁用，提示"请先启动 Agent"）

---

### 对话面板（主区域，v0.2.0 更新）

```
┌──────────────────────────────────────────────────────┐
│ Sidebar │  coder (gpt-4o) ● Running        [⚙][📁] │
│         │  📝 代码审查                      [+新对话] │
│         │───────────────────────────────────────────│
│         │                                           │
│         │  👤 帮我把上周的会议纪要整理成周报           │
│         │                                           │
│         │  🤖 好的，我来读取会议纪要并整理成周报格式。 │
│         │                                           │
│         │  ┌─ 🔧 file_read ─────────────────────┐   │
│         │  │ 📄 会议纪要/0305.md                 │   │
│         │  │ ✓ 读取成功                           │   │
│         │  └─────────────────────────────────────┘   │
│         │                                           │
│         │  🤖 周报已整理完成，包含以下要点：          │
│         │     - 项目进展 3 项                         │
│         │     - 待跟进事项 2 项                       │
│         │     你可以在右侧文件树中点击查看。           │
│         │                                           │
│         │───────────────────────────────────────────│
│         │ [📎] 在这里输入消息...              [发送] │
│         └───────────────────────────────────────────│
└──────────────────────────────────────────────────────┘
```

**顶栏组成（两行）：**
- **第一行**：Agent 名称 + 模型 + 运行状态 + `[⚙]` 设置 + `[📁]` Workspace
- **第二行**：Session 标题（可编辑）+ `[+ 新对话]` 快捷按钮
  - 点击 Session 标题可以 inline 编辑重命名
  - `[+ 新对话]` → 在当前 Agent 下新建 Session 并切换过去

**对话区域：**
- 用户消息（`👤`）右对齐，深色背景
- 助手消息（`🤖`）左对齐，浅色背景
- Tool 调用以独立卡片形式穿插在对话流中
- 自动滚动到底部，手动上滚时暂停

**输入栏：**
- `[📎]` 图片上传按钮
- 文本输入框，支持多行
- Enter 发送，Shift+Enter 换行
- 流式输出中输入框禁用，显示"停止生成"按钮
- **两粒度取消（v0.1.2 更新）：**
  - **停止生成按钮**：取消整轮对话（Cancel Chat），终止 RunLoop 和所有 tool 执行
  - **Tool 卡片取消按钮**：取消单个 MCP 工具调用（Cancel Tool Call），Agent 继续推理

---

### Workspace 侧边栏（右侧可收起）

```
┌────────────────────────┐
│ 📁 Workspace           │
│                        │
│ ▸ .seaturt/            │
│ ▾ 周报/                │
│   ├── 2025-W10.md      │
│   └── 2025-W11.md      │
│ ▾ 整理/                │
│   ├── 旅行计划.xlsx     │
│   └── 会议纪要.docx     │
│ ├── 数据分析.csv        │
│ └── 统计图表.png        │
│                        │
│ 🖥 桌面 (实时预览)      │
│ ┌────────────────────┐ │
│ │ ╔═══════╗  ╔═════╗ │ │
│ │ ║ 终端  ║  ║ 文件║ │ │
│ │ ╚═══════╝  ╚═════╝ │ │
│ │   KDE 桌面实时流     │ │
│ │  (iframe view-only) │ │
│ └────────────────────┘ │
│  点击打开完整桌面 →     │
└────────────────────────┘
```

**两个区块（可折叠）：**

| 区块 | 内容 |
|------|------|
| 📁 文件树 | workspace 目录结构，点击可预览文档、表格、图片等文件 |
| 🖥 桌面 | Selkies WebRTC 实时桌面预览（iframe view-only 模式），点击跳转新标签页打开完整桌面 |

**桌面实时预览设计（v0.1.1 更新）：**
- 使用 `<iframe>` 加载 Selkies WebRTC URL，实时视频流
- iframe 本身设为 1920×1080，通过 CSS `transform: scale()` 缩放到面板宽度
- 外层容器 `overflow: hidden` 裁剪，实际显示约 280×158px（16:9 比例）
- iframe 上覆盖一层透明遮罩（`pointer-events: none`），禁止在缩略图上直接操作
- 点击遮罩 → `window.open(desktopUrl, '_blank')` 打开完整桌面
- **多连接共存**：Selkies 支持并发连接，预览 iframe 和新标签页完整桌面互不影响
- **分辨率固定 1080p**：Selkies 启动参数 `--manual-width=1920 --manual-height=1080`，预览和完整桌面分辨率一致
- Agent 未运行时显示灰色占位图 + "Agent 未运行"提示

---

## 交互细节

### 创建 Agent 对话框

```
┌─────── 新建 Agent ────────────────────┐
│                                       │
│  名称     [我的日常助手               ]  │
│                                       │
│  模型     [GPT-4o              ▾]     │
│                                       │
│             [取消]  [创建]             │
└───────────────────────────────────────┘
```

**字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| 名称 | 文本输入 | 是 | 允许任意字符，作为 Agent 展示名称；后端自动生成唯一 `agent_id`（用户不可见） |
| 模型 | 下拉选择 | 是 | 下拉列表显示模型名称（如 GPT-4o），内部传递 `model_id`，可选项从后端配置获取 |
| ~~启用桌面环境~~ | ~~开关~~ | — | 已移除：所有 Agent 统一使用内置桌面环境（Selkies WebRTC） |

**交互流程：**
1. 点击"+ 新建 Agent"按钮
2. 弹出 Dialog
3. 填写表单
4. 点击"创建" → 调用 `POST /api/agents`
5. 成功 → 关闭 Dialog，Agent 出现在列表中
6. 失败 → 显示错误提示，表单不关闭

---

### Tool 调用卡片

```
┌─ 🔧 shell_exec ─────────────────────────────────┐
│ $ python3 analyze.py --input data.csv            │
│   --output 统计图表.png                           │
│                                                  │
│ ▾ 执行结果                                        │
│ ┌──────────────────────────────────────────────┐ │
│ │ 数据分析完成：                                │ │
│ │   总记录数: 1,024                             │ │
│ │   已生成图表: 统计图表.png                     │ │
│ └──────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────┘
```

**执行中状态（v0.1.2 更新 — 可单独取消）：**

```
┌─ 🔧 shell_exec ──────────────── ⏳ 执行中  [取消] ┐
│ $ python3 heavy_task.py --timeout 300              │
│                                                    │
│  ◌ 正在执行...                                      │
└────────────────────────────────────────────────────┘
```

- 执行中的 tool call 卡片头部右侧显示 **[取消]** 按钮（文字按钮，`text-xs`）
- 点击 [取消] → 调用 `POST /api/agents/:id/chat/cancel-tool/:tool_call_id`
- 只取消该工具调用，**不中断整轮对话**——Agent 继续推理

**已取消状态：**

```
┌─ 🔧 shell_exec ──────────────────────── ⊘ 已取消 ┐
│ $ python3 heavy_task.py --timeout 300              │
│                                                    │
│  ⊘ 用户取消了此工具调用                              │
└────────────────────────────────────────────────────┘
```

- 已取消的卡片左侧色条为灰色，图标 `⊘`
- LLM 会收到 tool result "用户取消了此工具调用"，自行决定后续动作

**状态流转：**

```
执行中 (spinner + [取消]按钮)
  → 成功 (✓ 绿色) → 可折叠查看结果
  → 失败 (✗ 红色) → 展开显示错误信息
  → 已取消 (⊘ 灰色) → 显示"用户取消了此工具调用"
```

**设计规范：**
- 工具名称 + 图标显示在卡片头部
- 命令/参数以 `mono` 字体、代码样式显示
- 结果默认折叠（`▸`），点击展开（`▾`）
- 执行中显示 spinner 动画 + 右侧 [取消] 按钮
- 完成后显示 ✓（成功）、✗（失败）或 ⊘（已取消）
- 卡片背景色比消息气泡略深，带圆角和左侧色条

---

### 图片消息

**用户发送的图片：**
- 消息气泡内 inline 显示缩略图（最大 300px 宽）
- 点击缩略图 → 弹出 Lightbox 查看原图
- 多张图片网格排列

**Tool 返回的图片（如截屏）：**
- 在 Tool 结果区域内渲染
- 同样支持点击放大

**上传方式：**
- 拖拽文件到输入区域
- 粘贴（Ctrl/Cmd+V）
- 点击 `[📎]` 按钮选择文件

**上传预览：**
- 选择图片后在输入栏上方显示缩略图预览
- 可单独删除某张图片
- 发送时与文字一起 multipart 提交

---

### Agent 右键菜单

```
┌──────────────┐
│ ▶ 启动       │
│ ⏸ 停止       │
│ ──────────── │
│ ⚙ 设置       │
│ 📋 复制 ID   │
│ ──────────── │
│ 🗑 删除       │
└──────────────┘
```

- 运行中的 Agent：显示"停止"，隐藏"启动"
- 已停止的 Agent：显示"启动"，隐藏"停止"
- 删除需二次确认

---

### 消息气泡

**用户消息：**
```
                              ┌──────────────────────┐
                              │ 帮我写一个 TODO 应用  │
                              └──────────────────────┘
```
- 右对齐
- 主题色背景（深蓝/深紫）
- 白色文字

**助手消息：**
```
┌─────────────────────────────────────────┐
│ 好的，我来帮你整理这些数据。            │
│                                         │
│ 分析结果如下：                           │
│ - **销售额**：同比增长 12%               │
│ - **Top 3 产品**：A、B、C               │
│                                         │
│ 详细报表已保存到 `整理/月度报告.xlsx`。  │
└─────────────────────────────────────────┘
```
- 左对齐
- 略浅背景
- Markdown 渲染（加粗、列表、链接、表格等）
- 代码块带语法高亮 + 复制按钮 + 语言标签

---

## 响应式设计

| 断点 | 屏幕宽度 | 布局 | 说明 |
|------|---------|------|------|
| Desktop | ≥ 1200px | 三栏 | 左侧栏 + 对话 + 右侧栏全部展开 |
| Tablet | 768~1199px | 两栏 | 左侧栏 + 对话，右侧栏为浮层（点击按钮打开） |
| Mobile | < 768px | 单栏 | 所有侧边栏为抽屉式，Agent 列表和 Workspace 均可收起 |

**各断点行为：**

### Desktop (≥ 1200px)
- 左侧栏固定显示，240px
- 右侧栏默认显示，320px，可手动收起
- 对话区域自适应剩余宽度

### Tablet (768~1199px)
- 左侧栏固定显示，240px（可缩窄为图标模式 60px）
- 右侧栏默认隐藏，点击 `[📁]` 按钮从右侧滑出浮层
- 浮层带半透明遮罩

### Mobile (< 768px)
- 左侧栏默认隐藏，点击汉堡菜单从左侧滑出
- 右侧栏默认隐藏，点击 `[📁]` 从底部上滑
- 输入栏固定在底部
- 顶栏简化，仅显示 Agent 名称 + 菜单按钮

---

## 主题

### 深色主题（默认）

对标 ChatGPT / Claude 的现代暗色调。

| 元素 | 颜色 | 说明 |
|------|------|------|
| 背景（主区域） | `#1e1e2e` | 深蓝灰 |
| 背景（侧边栏） | `#181825` | 更深一层 |
| 文字（主要） | `#cdd6f4` | 浅灰白 |
| 文字（次要） | `#6c7086` | 暗灰 |
| 强调色 | `#89b4fa` | 蓝色，用于链接、选中态 |
| 用户消息气泡 | `#313244` | 深灰 |
| 助手消息气泡 | `#1e1e2e` | 同主背景 |
| Tool 卡片背景 | `#11111b` | 最深色 |
| Tool 卡片左色条 | `#fab387`（执行中）/ `#a6e3a1`（成功）/ `#f38ba8`（失败）/ `#6c7086`（已取消） | 橙/绿/红/灰 |
| 成功 | `#a6e3a1` | 绿色 |
| 警告 | `#f9e2af` | 黄色 |
| 错误 | `#f38ba8` | 红色 |
| 边框 | `#313244` | 微弱分隔线 |

### 浅色主题

| 元素 | 颜色 |
|------|------|
| 背景（主区域） | `#eff1f5` |
| 背景（侧边栏） | `#e6e9ef` |
| 文字（主要） | `#4c4f69` |
| 强调色 | `#1e66f5` |
| 用户消息气泡 | `#dce0e8` |
| 助手消息气泡 | `#eff1f5` |

### 切换方式

- 顶栏/侧边栏底部放置主题切换按钮（🌙/☀️）
- 通过 Tailwind CSS `dark:` 前缀实现
- 默认跟随系统偏好（`prefers-color-scheme`）
- 用户手动切换后存 `localStorage`

---

## 动效规范

| 场景 | 动效 | 时长 |
|------|------|------|
| 侧边栏展开/收起 | slide + fade | 200ms |
| Dialog 弹出 | scale(0.95→1) + fade | 150ms |
| Tool 卡片结果展开 | height 过渡 | 200ms |
| 消息出现 | fade-in + slide-up | 100ms |
| 右键菜单 | scale(0.9→1) + fade | 100ms |
| SSE 打字效果 | 无动效，直接追加文字 | — |
| Loading spinner | rotate 360° | 1s loop |

---

## 间距与字体

| 属性 | 值 |
|------|-----|
| 基准字号 | 14px |
| 代码字号 | 13px |
| 行高 | 1.6 |
| 代码行高 | 1.5 |
| 消息气泡圆角 | 12px |
| Tool 卡片圆角 | 8px |
| 按钮圆角 | 6px |
| 对话区域内边距 | 16px ~ 24px |
| 消息间距 | 12px |
| 代码字体 | `JetBrains Mono`, `Fira Code`, `monospace` |
| 正文字体 | `Inter`, `-apple-system`, `sans-serif` |

---

## Session 设计详解（v0.2.0）

### 数据模型

**后端 Session 结构：**

```go
type Session struct {
    ID        string    `json:"id"`         // 唯一标识，自动生成
    AgentID   string    `json:"agent_id"`   // 所属 Agent
    Title     string    `json:"title"`      // 显示名称
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"` // 最后一条消息的时间
}
```

**Message 表变更：**

```
之前：Message.AgentID  → 消息绑定到 Agent
之后：Message.SessionID → 消息绑定到 Session（SessionID 可反查 AgentID）
```

**前端类型：**

```typescript
interface Session {
  id: string
  agent_id: string
  title: string
  created_at: string
  updated_at: string
}
```

### API 变更

**新增 Session API：**

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/agents/:id/sessions` | 获取 Agent 下所有 Session 列表（按 updated_at 降序） |
| `POST` | `/api/agents/:id/sessions` | 新建 Session（可选 title，默认"新对话"） |
| `PUT` | `/api/agents/:id/sessions/:sid` | 更新 Session（重命名 title） |
| `DELETE` | `/api/agents/:id/sessions/:sid` | 删除 Session（同时删除关联消息） |

**现有 API 路由变更：**

```
之前：POST   /api/agents/:id/chat               → 发消息
之后：POST   /api/agents/:id/sessions/:sid/chat  → 发消息到指定 Session

之前：GET    /api/agents/:id/history             → 获取历史
之后：GET    /api/agents/:id/sessions/:sid/history

之前：DELETE /api/agents/:id/history             → 清空历史
之后：DELETE /api/agents/:id/sessions/:sid/history

之前：POST   /api/agents/:id/chat/cancel         → 取消对话
之后：POST   /api/agents/:id/sessions/:sid/chat/cancel

之前：POST   /api/agents/:id/chat/cancel-tool/:toolCallId
之后：POST   /api/agents/:id/sessions/:sid/chat/cancel-tool/:toolCallId
```

**不变的 API**（Agent 级别，与 Session 无关）：
- `POST/GET/DELETE /api/agents` — Agent CRUD
- `POST /api/agents/:id/start|stop` — 启停
- `GET /api/agents/:id/desktop|ports|files` — 容器相关
- `GET|PUT /api/agents/:id/system-prompt` — 系统提示词

### 前端状态管理变更

**agentStore 变更：**

```typescript
interface AgentStore {
  // 新增
  sessions: Record<string, Session[]>         // agentId -> sessions
  selectedSessionId: string | null
  fetchSessions(agentId: string): Promise<void>
  createSession(agentId: string): Promise<Session>
  deleteSession(agentId: string, sessionId: string): Promise<void>
  renameSession(agentId: string, sessionId: string, title: string): Promise<void>
  selectSession(agentId: string, sessionId: string): void
}
```

**chatStore 变更：**

```typescript
// 之前：以 agentId 为 key 隔离状态
agentStates: Record<string, PerAgentState>

// 之后：以 sessionId 为 key 隔离状态
sessionStates: Record<string, PerSessionState>

interface PerSessionState {
  messages: ChatMessage[]
  isStreaming: boolean
  abortController: AbortController | null
}
```

**选中逻辑：**

```
用户点击 Agent → 展开 Session 列表 → 自动选中最近的 Session
                                   → 如果没有 Session → 自动创建一个
用户点击 Session → 切换到该 Session 的对话
用户点击 [+ 新对话] → 创建新 Session → 自动选中
```

### Session 标题自动生成

- 新建时默认标题："新对话"
- 用户发送第一条消息后，自动截取消息前 20 个字符作为标题
- 调用 `PUT /api/agents/:id/sessions/:sid` 更新标题
- 用户也可以在顶栏手动编辑标题


