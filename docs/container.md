# 容器与安全

## Workspace 挂载

- 每个 Agent 在宿主机上有独立的 workspace 目录
- 容器内挂载路径：`/workspace`
- 宿主机路径：`~/.seaturt/workspaces/<agent-id>/`
- 用户可将文件放入 workspace 供 Agent 读取
- Agent 生成的文件自动出现在宿主机 workspace 中
- 支持用户指定自定义目录作为额外挂载

### `.seaturt/` 运行时目录（v0.0.4）

```
/workspace/.seaturt/
├── SYSTEM.md      # Agent system prompt（Server 创建时生成，支持运行时热更新）
├── PORTS.md       # 端口映射表（容器启动后动态生成）
└── uploads/       # 图片外部化存储（v0.0.2）
```

- **SYSTEM.md**：Agent 的身份、行为准则、工具使用指南，每次 Chat 重新读取
- **PORTS.md**：20 个预映射端口的容器端口 ↔ 宿主机端口对照表
- Agent 可通过 `file_write` 修改 SYSTEM.md 实现"自我调优"

## 容器镜像设计

基础镜像预装：

```dockerfile
FROM ubuntu:22.04

# 基础工具
RUN apt-get update && apt-get install -y \
    curl wget git vim nano \
    python3 python3-pip \
    nodejs npm \
    build-essential \
    jq tree ripgrep

# 安装 MCP Servers
COPY mcp-server-core    /usr/local/bin/mcp-server-core
COPY mcp-server-browser /usr/local/bin/mcp-server-browser
COPY mcp-server-db      /usr/local/bin/mcp-server-db
COPY mcp-server-git     /usr/local/bin/mcp-server-git

# 浏览器 MCP Server 依赖（可选）
RUN npx playwright install --with-deps chromium || true

# 非 root 用户（运行时通过 entrypoint 动态匹配宿主机 UID/GID）
RUN apt-get update && apt-get install -y --no-install-recommends gosu sudo && rm -rf /var/lib/apt/lists/*
RUN useradd -m -s /bin/bash agent \
    && echo "agent ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/agent \
    && chmod 0440 /etc/sudoers.d/agent

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

WORKDIR /workspace
ENTRYPOINT ["/entrypoint.sh"]
# 容器保持运行，MCP Server 由 docker exec 按需启动
CMD ["tail", "-f", "/dev/null"]
```

## 安全设计

| 维度 | 措施 |
|------|------|
| 文件系统 | 仅 `/workspace` 可读写，其他目录只读 |
| 权限 | 容器内以非 root 用户运行（entrypoint 动态匹配宿主机 UID/GID），支持无密码 sudo |
| 超时 | 单次命令执行超时 5 分钟，可配置 |
| 审计 | 记录所有 Agent 执行的命令和文件操作日志 |
