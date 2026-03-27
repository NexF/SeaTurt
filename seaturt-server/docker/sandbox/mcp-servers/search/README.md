# mcp-server-search

网页搜索和内容抓取 MCP Server，为 Agent 提供实时互联网信息获取能力。

## 基本信息

| 属性 | 值 |
|------|------|
| 名称 | `mcp-server-search` |
| 语言 | Python 3 |
| 协议版本 | `2024-11-05` |
| 传输方式 | stdio（JSON-RPC 2.0） |
| 默认启用 | ✅ 所有 Agent |
| 打包方式 | PyInstaller（单文件可执行） |
| 核心依赖 | ddgs、httpx、html2text |

## 源码结构

```
mcp-servers/search/
├── main.py                 # MCP 协议层：JSON-RPC 路由、tool 定义
├── search.py               # web_search 实现（DuckDuckGo）
├── fetch.py                # web_fetch 实现（httpx + html2text）
├── requirements.txt        # Python 依赖
├── build.sh                # PyInstaller 打包脚本
├── mcp-server-search.spec  # PyInstaller 配置
└── build/                  # 构建产物
    └── dist/
        └── mcp-server-search  # 单文件可执行二进制
```

## Tools

### `web_search`

搜索网页，返回标题、URL 和内容摘要。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | ✅ | 搜索查询（自然语言或关键词） |
| `max_results` | integer | ❌ | 最大结果数（默认 5，最大 10） |
| `search_depth` | string | ❌ | 搜索深度：`basic`（默认，快速）或 `advanced`（深度提取） |

**实现**：
- 使用 [ddgs](https://pypi.org/project/ddgs/) 库（DuckDuckGo Search）
- 后端自动选择（`backend="auto"`）
- 结果限制在 1-10 条

**返回格式**（Markdown）：
```
Search results for: "查询内容"

1. [标题](URL)
   内容摘要

2. [标题](URL)
   内容摘要

(N results)
```

**示例**：
```json
{"name": "web_search", "arguments": {"query": "Python asyncio tutorial", "max_results": 3}}
{"name": "web_search", "arguments": {"query": "最新天气预报", "search_depth": "advanced"}}
```

### `web_fetch`

抓取 URL 内容，提取可读文本并转为 Markdown。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `url` | string | ✅ | 要抓取的 URL |
| `max_length` | integer | ❌ | 最大内容长度（默认 8000，最大 32000 字符） |

**实现**：
- 使用 [httpx](https://www.python-httpx.org/) 发送 HTTP 请求
  - 自动跟随重定向
  - 30 秒超时
  - 自定义 User-Agent（Chrome 浏览器 UA）
- HTML 页面通过 [html2text](https://pypi.org/project/html2text/) 转为 Markdown
  - 忽略图片（`ignore_images = True`）
  - 保留强调格式
  - 不自动换行（`body_width = 0`）
- 非 HTML 内容直接返回原文
- 超过 `max_length` 时截断并提示

**返回格式**：
```
Title: 页面标题
URL: 最终 URL
Content-Length: 1234 chars

（Markdown 内容）

(content truncated at 8000 chars)  ← 仅截断时显示
```

**错误处理**：
- 超时：`Fetch failed: request timed out after 30s for {url}`
- HTTP 错误：`Fetch failed: HTTP {status_code} for {url}`
- 其他异常：`Fetch failed: {error}`

**示例**：
```json
{"name": "web_fetch", "arguments": {"url": "https://docs.python.org/3/library/asyncio.html"}}
{"name": "web_fetch", "arguments": {"url": "https://example.com/api-docs", "max_length": 16000}}
```

## 构建

使用 PyInstaller 打包为单文件可执行二进制：

```bash
cd mcp-servers/search/
./build.sh
# 输出: build/dist/mcp-server-search
```

打包配置（`mcp-server-search.spec`）：
- `--onefile`：单文件模式
- `--clean`：清理临时文件
- UPX 压缩：启用

## Python 依赖

| 包 | 版本 | 用途 |
|------|------|------|
| `ddgs` | ≥7.0.0 | DuckDuckGo 搜索 |
| `html2text` | ≥2024.2.26 | HTML 转 Markdown |
| `httpx` | ≥0.27.0 | HTTP 客户端 |
| `pyinstaller` | ≥6.0 | 打包工具（仅构建时） |

## LLM 看到的 Tool 名称

| LLM 调用名 | 实际 Tool |
|-----------|----------|
| `search-web_search` | `web_search` |
| `search-web_fetch` | `web_fetch` |
