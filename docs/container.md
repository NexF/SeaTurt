# 容器与安全

## Workspace 挂载

- 每个 Agent 在宿主机上有独立的 workspace 目录
- 容器内挂载路径：`/workspace`
- 宿主机路径：`~/.seaturt/workspaces/<agent-id>/`
- 用户可将文件放入 workspace 供 Agent 读取
- Agent 生成的文件自动出现在宿主机 workspace 中
- 支持用户指定自定义目录作为额外挂载

### `.seaturt/` 运行时目录（v0.0.4+）

```
/workspace/.seaturt/
├── SYSTEM.md      # Agent system prompt（Server 创建时生成，支持运行时热更新）
├── PORTS.md       # 端口映射表（容器启动后动态生成）
├── tools/         # MCP Server 工具定义 YAML + 二进制（v0.1.3）
│   ├── core.yaml
│   ├── desktop.yaml
│   ├── mcp-server-core
│   └── mcp-server-desktop
└── uploads/       # 图片外部化存储（v0.0.2）
```

- **SYSTEM.md**：Agent 的身份、行为准则、工具使用指南，每次 Chat 重新读取
- **PORTS.md**：20 个预映射端口的容器端口 ↔ 宿主机端口对照表
- **tools/**：MCP Server 的 YAML 定义和可执行二进制，由 `WriteBuiltinTools` + `copyMCPBinaries` 在 Agent 创建时部署
- Agent 可通过 `file_write` 修改 SYSTEM.md 实现"自我调优"

## 容器镜像设计

### 统一镜像（`seaturt/sandbox:latest`）

v0.1.2 起统一为单一镜像，所有 Agent 均内置 KDE Plasma 桌面环境 + Selkies WebRTC 远程访问。

多阶段构建，最终镜像约 3GB：

```
golang:1.23-alpine (builder)
    └── 编译 mcp-server-core + mcp-server-desktop

lscr.io/linuxserver/webtop:ubuntu-kde (runtime)
    ├── KDE Plasma 桌面 + Selkies WebRTC（基础镜像自带）
    ├── 开发工具：curl, wget, git, vim, python3, jq, ripgrep, build-essential
    ├── mcp-server-core（Go 静态二进制，staging → /opt/seaturt/mcp-bins/）
    ├── mcp-server-desktop（Go 静态二进制，staging → /opt/seaturt/mcp-bins/）
    ├── xdotool + wmctrl + scrot + ImageMagick（桌面自动化工具）
    ├── 中文字体 fonts-noto-cjk + emoji
    ├── Selkies 启动脚本覆盖（--enable-shared + 锁定 1080p）
    ├── s6-overlay 进程管理（基础镜像自带，通过 PUID/PGID 管理用户权限）
    └── EXPOSE 3000 3001（Selkies HTTP/HTTPS）
```

桌面环境由基础镜像的 s6-overlay 自动启动和管理，无需自定义 entrypoint 或启动脚本。

> **历史说明**：v0.0.3 ~ v0.1.1 采用"双镜像策略"（`sandbox` + `sandbox-desktop`），
> v0.1.2 统一为单一镜像，不再区分桌面/非桌面 Agent。

## 安全设计

| 维度 | 措施 |
|------|------|
| 文件系统 | 仅 `/workspace` 可读写，其他目录只读 |
| 权限 | 容器内通过 s6-overlay `/init` + `PUID`/`PGID` 管理用户权限，支持无密码 sudo |
| 超时 | shell_exec 默认 120s 超时，可配置（最大 1800s）；支持 background 模式启动长进程 |
| 审计 | 记录所有 Agent 执行的命令和文件操作日志 |
