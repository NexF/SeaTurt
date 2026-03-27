# mcp-server-browser

Chromium 浏览器自动化 MCP Server，基于 Playwright MCP 提供完整的浏览器操作能力。

## 基本信息

| 属性 | 值 |
|------|------|
| 名称 | `mcp-server-browser` |
| 语言 | Go（MCP proxy）+ Node.js（daemon） |
| 协议版本 | `2024-11-05` |
| 传输方式 | stdio → Unix Socket → @playwright/mcp |
| 默认启用 | ✅ 桌面 Agent |
| 核心依赖 | `@playwright/mcp@^0.0.68` |

## 架构

与其他 MCP Server 不同，browser 采用 **proxy + daemon 双进程架构**：

```
docker exec → mcp-server-browser (Go, ephemeral)
                 ↓ Unix Socket (/tmp/mcp-browser.sock)
              server.js (Node.js, long-running daemon)
                 ↓ stdio
              @playwright/mcp (Playwright subprocess)
                 ↓
              Chromium Browser (headed, DISPLAY=:1)
```

**为什么需要 daemon？**

浏览器是有状态的（页面、cookies、localStorage），不能像 core/desktop 那样每次 tool call 都启动新进程。daemon 模式下：

1. **Go proxy**（`main.go`）：短命进程，通过 `docker exec` 启动。读 stdin → 转发到 Unix Socket → 读回响应 → 写 stdout
2. **Node.js daemon**（`server.js`）：长驻进程，由 s6-overlay 管理。监听 Unix Socket，管理 Playwright 子进程生命周期
3. **@playwright/mcp**：Playwright 官方 MCP Server，被 daemon 以 stdio 模式 spawn

## 源码结构

```
mcp-servers/browser/
├── go.mod         # Go module 定义
├── main.go        # Go MCP proxy — stdin/stdout ↔ Unix Socket 转发
├── server.js      # Node.js daemon — 管理 Playwright 生命周期
└── package.json   # Node.js 依赖（@playwright/mcp）
```

## Go Proxy（main.go）

简单的 stdin ↔ Unix Socket 转发器：

- 从 stdin 逐行读取 JSON-RPC 请求
- 过滤 `notifications/initialized`（消费掉，不转发）
- 其余请求通过 Unix Socket 发给 daemon，等待响应回写 stdout
- **连接重试**：最多 60 次，每次间隔 500ms（总计约 30s），等待 daemon 启动
- **超时**：每次 Socket 读写超时 60s

## Node.js Daemon（server.js）

### 浏览器状态机

```
closed ──── open_browser ───→ open
  ↑                            │
  └──── close_browser ────────┘
        (or process exit)
```

- `closed`：无 Playwright 子进程，浏览器未运行
- `open`：Playwright 子进程运行中，Chromium 可见

### 启动流程

1. 清理旧的 Unix Socket 文件
2. 监听 `/tmp/mcp-browser.sock`
3. 等待 proxy 连接和请求

### Playwright 子进程管理

- 通过 `npx @playwright/mcp@latest` 启动
- 参数：`--browser=chromium --user-data-dir=/opt/browser-daemon/user-data --caps=vision,pdf`
- **Session 持久化**：user-data 目录保留 cookies、localStorage 等
- **Vision + PDF 能力**：启用截图和 PDF 生成支持

## Tools

### 自定义 Tools

#### `open_browser`

启动 Chromium 浏览器（headed 模式，显示在 X11 桌面上）。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| （无参数） | | | |

- 必须先调用此 tool 才能使用其他浏览器操作
- 如果浏览器已打开，返回提示而非报错
- 启动 Playwright 子进程并完成 MCP 握手

#### `close_browser`

关闭 Chromium 浏览器，释放内存。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| （无参数） | | | |

- daemon 保持运行，之后可以再次 `open_browser`
- 先发 SIGTERM，等 1 秒后若未退出则 SIGKILL

### Playwright Tools（动态发现）

Daemon 启动时通过 **tools discovery** 机制从 `@playwright/mcp` 获取完整 tool 列表。以下为 `@playwright/mcp@0.0.68` 提供的所有 tools：

#### 核心自动化

| Tool | 说明 |
|------|------|
| `browser_navigate` | 导航到指定 URL |
| `browser_navigate_back` | 返回上一页（浏览器历史记录） |
| `browser_click` | 点击页面元素 |
| `browser_hover` | 鼠标悬停在页面元素上 |
| `browser_drag` | 在两个元素之间执行拖放操作 |
| `browser_type` | 在可编辑元素中输入文本 |
| `browser_select_option` | 在下拉菜单中选择选项 |
| `browser_fill_form` | 批量填充多个表单字段 |
| `browser_press_key` | 按下键盘按键 |
| `browser_file_upload` | 上传一个或多个文件 |
| `browser_handle_dialog` | 处理对话框（alert / confirm / prompt） |
| `browser_snapshot` | 捕获页面可访问性快照（推荐用于后续操作定位） |
| `browser_take_screenshot` | 对当前页面截图（仅供查看，不可基于截图执行操作） |
| `browser_evaluate` | 在页面中执行 JavaScript 表达式 |
| `browser_run_code` | 运行 Playwright 代码片段 |
| `browser_wait_for` | 等待文本出现/消失，或等待指定时间 |
| `browser_close` | 关闭当前页面 |
| `browser_resize` | 调整浏览器窗口大小 |
| `browser_console_messages` | 返回所有控制台消息 |
| `browser_network_requests` | 返回页面加载以来的所有网络请求 |

#### 标签页管理

| Tool | 说明 |
|------|------|
| `browser_tabs` | 列出、创建、关闭或切换浏览器标签页 |

#### 浏览器安装

| Tool | 说明 |
|------|------|
| `browser_install` | 安装配置中指定的浏览器（遇到浏览器未安装错误时调用） |

#### 基于坐标的操作（需启用 `--caps=vision`）

| Tool | 说明 |
|------|------|
| `browser_mouse_click_xy` | 在给定坐标点击鼠标左键 |
| `browser_mouse_move_xy` | 将鼠标移动到给定坐标 |
| `browser_mouse_drag_xy` | 按住鼠标左键拖动到给定坐标 |
| `browser_mouse_down` | 按下鼠标 |
| `browser_mouse_up` | 松开鼠标 |
| `browser_mouse_wheel` | 滚动鼠标滚轮 |

#### PDF 生成（需启用 `--caps=pdf`）

| Tool | 说明 |
|------|------|
| `browser_pdf_save` | 将页面保存为 PDF 文件 |

#### 测试断言（需启用 `--caps=testing`）

| Tool | 说明 |
|------|------|
| `browser_generate_locator` | 为给定元素生成 Playwright 定位符（测试用） |
| `browser_verify_element_visible` | 验证元素在页面上是否可见 |
| `browser_verify_list_visible` | 验证列表在页面上是否可见 |
| `browser_verify_text_visible` | 验证文本在页面上是否可见 |
| `browser_verify_value` | 验证元素的值 |

> 实际可用的 tools 取决于 `@playwright/mcp` 版本和启动时通过 `--caps` 参数启用的能力集。当前 daemon 默认启用 `--caps=vision,pdf`，未启用 `testing`。

### Tool Discovery 机制

发现 Playwright 提供的 tools 有两种方式：

1. **浏览器已打开**：直接查询正在运行的 MCP 子进程（`tools/list`）
2. **浏览器未打开**：spawn 一个临时的 `--isolated` 子进程，获取 tools 后立即退出（不打开可见浏览器）

发现结果会**缓存**，避免重复 spawn。

## 安全约束

- 所有浏览器操作 tool 在调用前会检查 `browserState === "open"`，未打开时返回错误提示
- daemon 监听 Unix Socket（本地通信），不暴露网络端口
- 优雅关闭：收到 SIGTERM/SIGINT 时关闭浏览器、关闭 Socket Server

## LLM 看到的 Tool 名称

所有 tool 在注册到 Agent 时会加上 `browser-` 前缀，以区分不同 MCP Server 的同名工具：

| LLM 调用名 | 实际 Tool |
|-----------|----------|
| `browser-open_browser` | `open_browser`（自定义） |
| `browser-close_browser` | `close_browser`（自定义） |
| `browser-browser_navigate` | `browser_navigate`（Playwright） |
| `browser-browser_click` | `browser_click`（Playwright） |
| `browser-browser_type` | `browser_type`（Playwright） |
| `browser-browser_snapshot` | `browser_snapshot`（Playwright） |
| `browser-browser_take_screenshot` | `browser_take_screenshot`（Playwright） |
| `browser-browser_tabs` | `browser_tabs`（Playwright） |
| `browser-browser_pdf_save` | `browser_pdf_save`（Playwright） |
| `browser-browser_mouse_click_xy` | `browser_mouse_click_xy`（Playwright, vision） |
| ... | ...（所有 Playwright tools 均加 `browser-` 前缀） |
