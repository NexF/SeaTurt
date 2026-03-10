# mcp-server-desktop

X11 桌面 GUI 操作 MCP Server，为 Agent 提供截图、鼠标、键盘、窗口管理等桌面交互能力。

## 基本信息

| 属性 | 值 |
|------|------|
| 名称 | `mcp-server-desktop` |
| 语言 | Go 1.23 |
| 协议版本 | `2024-11-05` |
| 传输方式 | stdio（JSON-RPC 2.0） |
| 默认启用 | ✅ 桌面 Agent |
| 依赖 | X11 桌面环境、xdotool、wmctrl、ImageMagick |
| DISPLAY | 默认 `:1`（LinuxServer Webtop / Selkies） |

## 源码结构

```
mcp-servers/desktop/
├── go.mod              # Go module 定义
├── main.go             # MCP 协议层：JSON-RPC 路由、消息读写
├── tools.go            # Tool 实现：所有桌面操作
└── mcp-server-desktop  # 预编译二进制（可选）
```

## Tools

### `screenshot`

桌面截图，默认叠加坐标网格辅助 LLM 定位点击位置。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `region` | object | ❌ | 截取区域 `{x, y, width, height}` |
| `show_grid` | boolean | ❌ | 是否叠加坐标网格（默认 `true`） |

**返回**：`type: "image"`（PNG base64）

**坐标网格**：
- 红色半透明网格线，每 **100px** 一条
- 交叉点标注绝对桌面坐标（如 `400,300`）
- 顶部和左侧有轴标签
- 区域截图时标签显示**绝对桌面坐标**，LLM 可直接用于点击
- 通过 ImageMagick（`identify` + `convert`）绘制

**实现细节**：
- 使用 `import` 命令（ImageMagick）截图
- 网格通过 `convert -draw` 叠加
- 如果网格叠加失败，静默回退为无网格截图

**示例**：
```json
{"name": "screenshot", "arguments": {}}
{"name": "screenshot", "arguments": {"show_grid": false}}
{"name": "screenshot", "arguments": {"region": {"x": 100, "y": 200, "width": 800, "height": 600}}}
```

### `mouse_click`

模拟鼠标点击。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `x` | integer | ✅ | X 坐标 |
| `y` | integer | ✅ | Y 坐标 |
| `button` | string | ❌ | 鼠标按钮：`left`（默认）、`right`、`middle` |

- 使用 `xdotool mousemove --sync` 先移动到位置，再 `click`

### `mouse_move`

移动鼠标到指定坐标。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `x` | integer | ✅ | X 坐标 |
| `y` | integer | ✅ | Y 坐标 |

### `mouse_drag`

鼠标拖拽操作。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `from_x` | integer | ✅ | 起始 X 坐标 |
| `from_y` | integer | ✅ | 起始 Y 坐标 |
| `to_x` | integer | ✅ | 终点 X 坐标 |
| `to_y` | integer | ✅ | 终点 Y 坐标 |

- 实现：`mousemove → mousedown → mousemove → mouseup`

### `keyboard_type`

模拟键盘输入文本。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `text` | string | ✅ | 要输入的文本 |

- 使用 `xdotool type --clearmodifiers --delay 50`
- 每个字符间隔 50ms，模拟真实输入

### `keyboard_key`

模拟按键或组合键。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `key` | string | ✅ | 按键名或组合键（如 `Return`、`ctrl+c`、`alt+Tab`） |

- 使用 `xdotool key --clearmodifiers`

### `window_list`

列出桌面上所有可见窗口。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| （无参数） | | | |

- 使用 `wmctrl -l` 获取窗口列表

### `window_focus`

聚焦指定窗口。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `window_id` | string | ❌ | X11 窗口 ID（如 `0x04000006`） |
| `title` | string | ❌ | 窗口标题子串匹配 |

- 二选一：通过 `xdotool windowactivate`（ID）或 `wmctrl -a`（标题）聚焦
- 两者都未提供时返回错误

### `open_app`

打开应用程序。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `app` | string | ✅ | 应用名称或启动命令（如 `firefox`、`gnome-terminal`） |

**启动机制**：
- 通过 `setsid` 完全分离进程，避免 MCP 进程退出时杀死 GUI 应用
- 启动后等待 **1 秒**检查进程是否存活
- 若已退出则读取 stderr 返回错误信息，不再静默失败
- stderr 输出重定向到临时文件，用后清除

### `desktop_wait`

等待桌面渲染稳定后截屏。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `delay_ms` | integer | ❌ | 等待毫秒数（默认 1000，最大 10000） |

**返回**：`type: "image"`（PNG base64）— 等待后自动调用 `screenshot` 截图

## 系统依赖

| 工具 | 用途 |
|------|------|
| `import`（ImageMagick） | 截图 |
| `convert`（ImageMagick） | 网格叠加 |
| `identify`（ImageMagick） | 获取图片尺寸 |
| `xdotool` | 鼠标、键盘模拟 |
| `wmctrl` | 窗口管理 |

## LLM 看到的 Tool 名称

| LLM 调用名 | 实际 Tool |
|-----------|----------|
| `desktop-screenshot` | `screenshot` |
| `desktop-mouse_click` | `mouse_click` |
| `desktop-mouse_move` | `mouse_move` |
| `desktop-mouse_drag` | `mouse_drag` |
| `desktop-keyboard_type` | `keyboard_type` |
| `desktop-keyboard_key` | `keyboard_key` |
| `desktop-window_list` | `window_list` |
| `desktop-window_focus` | `window_focus` |
| `desktop-open_app` | `open_app` |
| `desktop-desktop_wait` | `desktop_wait` |
