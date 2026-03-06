# Docker 容器管理

## 概述

SeaTurt 为每个 Agent 创建独立的 Docker 容器作为沙箱环境。本文档详细说明容器的网络模型、通信机制、端口管理、镜像策略及生命周期管理。

## 当前架构

### 网络模式

当前使用 Docker **默认 bridge 网络**。`CreateContainer` 时未设置 `NetworkMode`，Docker 自动将容器接入 `docker0` 网桥。

```
宿主机 (macOS / Linux)
│
├── docker0 网桥 (172.17.0.0/16)
│   ├── 172.17.0.2  ─ seaturt-agent_aaa (Agent A)
│   ├── 172.17.0.3  ─ seaturt-agent_bbb (Agent B)
│   └── 172.17.0.4  ─ seaturt-agent_ccc (Agent C)
│
└── SeaTurt Server (:8080)
    └── 通过 docker exec 与容器通信（不走网络）
```

**关键特征**：
- 每个容器有独立 IP（如 `172.17.0.2`），容器间可互相访问
- **不暴露任何端口** — 没有 `EXPOSE`，没有 `PortBindings`
- **不依赖网络通信** — 所有交互通过 `docker exec` stdin/stdout 完成

### 通信机制：docker exec stdio

SeaTurt 与容器的通信**完全不走网络**，而是通过 Docker Engine API 的 exec 机制：

```
SeaTurt Server
    │
    │  docker exec -i <container> /workspace/.seaturt/tools/mcp-server-core
    │  （通过 Docker SDK 的 ContainerExecCreate + ContainerExecAttach）
    │
    ▼
容器内 MCP Server 进程
    stdin  ← Server 写入 JSON-RPC 请求
    stdout → Server 读取 JSON-RPC 响应
```

v0.1.3 起，MCP 通信采用 **Executor 模式**：每次 tool call 启动一个新的 `docker exec` 进程，完成 MCP 握手 → 调用 → 返回后进程退出（无状态、无长连接）。

代码位置：`internal/container/docker.go`

| 方法 | 用途 | 通信方式 |
|------|------|---------|
| `Exec()` | 一次性命令（如健康检查、复制文件） | exec → 读取 stdout/stderr → 返回结果 |
| `ExecStdio()` | MCP 工具调用（Executor 模式） | exec → hijack 连接 → MCP JSON-RPC → 关闭 |

`ExecStdio` 返回 `types.HijackedResponse`，底层是一个 TCP 连接被 Docker daemon hijack 后的双向字节流。`ephemeralTransport` 在此之上实现单次 MCP JSON-RPC 通信。

### 为什么不用网络？

| 方式 | 端口管理 | 安全 | 实现复杂度 | 多 Agent |
|------|---------|------|----------|---------|
| **docker exec stdio** | 不需要 | 不暴露端口 ✅ | 简单 | 天然隔离 ✅ |
| 容器暴露端口 | 需要映射管理 | 暴露端口 ⚠️ | 中等 | 需要避免冲突 |
| host 网络 | 不需要 | 全端口暴露 ❌ | 简单 | 端口冲突 ❌ |

对于 MCP 这种请求-响应模式的协议，exec stdio 是最佳选择。

## 容器生命周期

```
Create          Start           Stop            Delete
  │               │               │               │
  ▼               ▼               ▼               ▼
┌─────────┐   ┌─────────┐   ┌─────────┐   ┌─────────┐
│ created │──►│ running │──►│ stopped │──►│ (删除)  │
└─────────┘   └─────────┘   └─────────┘   └─────────┘
                  │               │
                  │   Start       │
                  │◄──────────────┘
```

### Create 流程（`manager.Create`）

```
1. 生成 agentID（时间戳）
2. 确定 MCP Servers（用户指定 / 默认 core + desktop）
3. 确定 model
4. 创建 workspace 目录 (~/.seaturt/workspaces/<agentID>/)
5. 创建 .seaturt/ 子目录
6. WriteBuiltinTools → workspace/.seaturt/tools/*.yaml（工具定义 YAML）
7. 生成 SYSTEM.md → workspace/.seaturt/SYSTEM.md
8. docker.CreateContainer
   ├── 镜像：seaturt/sandbox:latest（统一镜像）
   ├── 注入环境变量：PUID, PGID, TZ, 用户自定义
   ├── 绑定挂载：workspacePath → /workspace
   ├── 端口映射：20 个常用端口统一预映射到 127.0.0.1 随机端口
   ├── ShmSize：2GB（浏览器渲染需要）
   ├── Label：seaturt.agent_id, seaturt.managed
   └── 容器名：seaturt-<agentID>
9. docker.StartContainer
10. copyMCPBinaries — 从容器 /opt/seaturt/mcp-bins/ 复制 bin 到 /workspace/.seaturt/tools/
11. 查询实际端口映射（ContainerInspect）
    └── 填充 DesktopPort/DesktopURL（Selkies 端口 3000）
12. 生成 PORTS.md → workspace/.seaturt/PORTS.md
13. 创建 ToolRegistry（从 YAML 加载）+ Executor + Router
14. store.CreateAgent — 持久化到 SQLite
```

### Start 流程（`manager.Start`）

```
1. 从 DB 读取 Agent 信息
2. docker.StartContainer（重启已停止的容器）
3. 查询端口映射，重新生成 PORTS.md（以防端口变化）
4. 创建 ToolRegistry + Executor + Router
5. 更新内存状态（routers map）
6. store.UpdateAgentStatus → running
```

### Stop 流程（`manager.Stop`）

```
1. 清理内存状态（delete routers）
2. docker.StopContainer
3. store.UpdateAgentStatus → stopped
```

### Delete 流程（`manager.Delete`）

```
1. 清理内存状态
2. docker.RemoveContainer（force）
3. store.DeleteMessages
4. store.DeleteAgent
注意：workspace 目录不删除（保留用户数据）
```

## 镜像设计

### 统一镜像（`seaturt/sandbox:latest`）

v0.1.2 起统一为单一镜像（不再区分桌面/非桌面），基于 LinuxServer Webtop 构建，约 3GB：

```
golang:1.23-alpine (builder)
    └── 编译 mcp-server-core + mcp-server-desktop

lscr.io/linuxserver/webtop:ubuntu-kde (runtime)
    ├── KDE Plasma 桌面 + Selkies WebRTC（基础镜像自带）
    ├── 开发工具：curl, wget, git, vim, python3, jq, ripgrep, build-essential
    ├── mcp-server-core（Go 静态二进制 → /opt/seaturt/mcp-bins/）
    ├── mcp-server-desktop（Go 静态二进制 → /opt/seaturt/mcp-bins/）
    ├── xdotool + wmctrl + scrot + ImageMagick（桌面自动化）
    ├── 中文字体 fonts-noto-cjk + emoji
    ├── Selkies 自定义启动脚本（--enable-shared + 锁定 1080p）
    ├── s6-overlay 进程管理（通过 PUID/PGID 管理用户权限）
    └── EXPOSE 3000 3001（Selkies HTTP/HTTPS）
```

> **MCP Server 二进制部署**：镜像中 bin 放在 `/opt/seaturt/mcp-bins/`（staging），
> Agent 创建时由 `manager.copyMCPBinaries` 复制到 `/workspace/.seaturt/tools/`，
> Executor 通过 `filepath.Join(toolsDir, command)` 定位执行。

### 桌面方案：LinuxServer Webtop (KDE + Selkies WebRTC)

镜像基于 [LinuxServer Webtop](https://github.com/linuxserver/docker-webtop) 构建，提供完整的 KDE Plasma 桌面环境和 **Selkies WebRTC** 远程访问方案。

**为什么不用 GNOME？** GNOME Shell 42+（Ubuntu 22.04）的 Mutter 合成器需要 EGL/GPU 支持，在 Xvnc 环境下直接报 `Unsupported session type`，无法在标准 Docker 容器中运行。KDE Plasma 使用 KWin (X11) 作为窗口管理器，支持 Mesa 软件渲染（llvmpipe），可在无 GPU 的容器中正常工作。

**VNC 技术**：LinuxServer Webtop 使用 **Selkies**（开源 WebRTC/WebSocket 远程桌面）。Selkies 支持：
- **多连接共存**：通过 `--enable-shared` + URL hash `#shared` 实现 controller/viewer 分离
- **固定分辨率**：`--is-manual-resolution-mode` + `--manual-width=1920 --manual-height=1080`
- **低延迟**：WebRTC 比传统 VNC 的 WebSocket 方案延迟更低

### 容器内进程模型

```
容器启动后（由 s6-overlay 管理）：

PID 1: /init (s6-overlay)
       ├── Selkies Server     — WebRTC + Web Server (端口 3000/3001)
       ├── KWin (X11)          — KDE 窗口管理器
       ├── Plasmashell          — KDE 桌面面板
       ├── PulseAudio           — 音频服务
       └── (其他 KDE 服务)

docker exec 执行 MCP 工具调用时（Executor 模式，每次调用一个短命进程）：
PID X: mcp-server-core     — 基础 tool（shell_exec, file_read/write/list）
PID Y: mcp-server-desktop  — 桌面 tool（screenshot, mouse_click, keyboard_type 等）
```

MCP Server 进程的生命周期由 Executor 控制：exec → 握手 → 调用 → 关闭 stdin → 进程退出。

### 环境变量

基于 LinuxServer Webtop，使用 `PUID`/`PGID` 管理用户权限：

| 变量 | 值 | 用途 |
|------|---|------|
| `PUID` | 宿主机 UID | LinuxServer 用户权限映射 |
| `PGID` | 宿主机 GID | LinuxServer 用户组权限映射 |
| `TZ` | `Asia/Shanghai` | 容器时区 |
| `SELKIES_ENABLE_SHARED` | `true` | Selkies 多连接共存 |
| `SELKIES_IS_MANUAL_RESOLUTION_MODE` | `true` | 锁定分辨率 |
| `SELKIES_MANUAL_WIDTH` / `HEIGHT` | `1920` / `1080` | 固定 1080p |

> 注意：`SELKIES_*` 环境变量仅影响前端 UI 显示，不会自动传为 CLI 参数。
> 实际生效的是自定义启动脚本 `svc-selkies-run`，其中显式传递 CLI 参数。

### Selkies 启动脚本覆盖

Selkies 的 s6 启动脚本硬编码了 `selkies --addr=localhost --mode=websockets`，不读取环境变量。
通过 Dockerfile 覆盖 `svc-selkies-run`：

```bash
#!/usr/bin/with-contenv bash
exec s6-setuidgid abc \
  selkies \
    --addr="localhost" \
    --mode="websockets" \
    --enable-shared=true \
    --is-manual-resolution-mode=true \
    --manual-width=1920 \
    --manual-height=1080
```

### ShmSize 说明

Chrome/Firefox 等浏览器使用 `/dev/shm`（共享内存）进行渲染。Docker 默认 `/dev/shm` 只有 64MB，浏览器打开复杂页面时会因内存不足而崩溃。

所有容器将 `/dev/shm` 增大到 2GB：

```go
hostCfg.ShmSize = 2 * 1024 * 1024 * 1024
```

等效于 `docker run --shm-size=2g`。

## 端口映射

### Docker 端口映射基础

Docker 端口映射**只能在容器创建时指定**，运行中不可动态添加。

```go
// 创建时指定端口映射
containerCfg.ExposedPorts = nat.PortSet{
    "5900/tcp": struct{}{},
}
hostCfg.PortBindings = nat.PortMap{
    "5900/tcp": []nat.PortBinding{
        {HostIP: "127.0.0.1", HostPort: ""},  // "" = Docker 自动分配
    },
}
```

| 参数 | 说明 |
|------|------|
| `HostIP: "127.0.0.1"` | 仅绑定 localhost，不暴露到外网 |
| `HostIP: "0.0.0.0"` | 绑定所有网卡（默认值，不安全） |
| `HostPort: ""` | Docker 自动分配可用端口（32768~65535） |
| `HostPort: "8080"` | 指定固定端口（多容器会冲突） |

### 容器 IP 直连

Bridge 网络下每个容器有独立 IP（如 `172.17.0.2`）：

```go
info, _ := cli.ContainerInspect(ctx, containerID)
ip := info.NetworkSettings.Networks["bridge"].IPAddress  // "172.17.0.2"
```

| 平台 | 宿主机 → 容器 IP | 原因 |
|------|----------------|------|
| **Linux** | 可达 ✅ | docker0 网桥在宿主机网络命名空间内 |
| **macOS** | 不可达 ❌ | Docker 跑在 Linux VM 里，容器 IP 在 VM 内部网络 |
| **Windows (WSL2)** | 不可达 ❌ | 同 macOS，Docker 跑在 WSL2 VM 内 |

**结论**：跨平台兼容只能用端口映射或 docker exec，不能依赖容器 IP。

### 采用方案：统一端口映射（v0.0.3+）

**所有 Agent 容器在创建时统一预映射常用端口**，宿主机端口由 Docker 自动分配（32768~60999 范围），全部绑定 `127.0.0.1`。

这是最简单、最一致的方案：
- ✅ 标准 Docker 方式，简单可靠
- ✅ 支持任意协议（HTTP/WebSocket/WebRTC/SSH/TCP）
- ✅ macOS/Linux 无平台差异
- ✅ `HostPort: ""` 自动分配，多 Agent 天然不冲突

#### 预映射端口清单

| 类别 | 容器端口 | 用途 |
|------|---------|------|
| **远程访问** | 22 | SSH |
| **桌面** | 3000 | Selkies HTTP Web 访问 |
| **桌面** | 3001 | Selkies HTTPS Web 访问 |
| **Web 服务** | 80 | HTTP |
| **Web 服务** | 443 | HTTPS |
| **前端开发** | 5173 | Vite 默认端口 |
| **前端开发** | 5174 | Vite 备用 |
| **后端开发** | 8000 | Python (uvicorn / Django) |
| **后端开发** | 8001 | 后端备用 |
| **后端开发** | 8080 | Go / Java / 通用 |
| **后端开发** | 8081 | 后端备用 |
| **后端开发** | 9000 | PHP / 其他 |
| **数据库** | 5432 | PostgreSQL |
| **数据库** | 3306 | MySQL |
| **数据库** | 27017 | MongoDB |
| **缓存/MQ** | 6379 | Redis |
| **通用** | 4000 | 通用开发端口 |
| **通用** | 8888 | Jupyter Notebook |
| **桌面** | 3000 | Selkies WebRTC |

共 **19 个端口**，每个 Agent 消耗 19 个宿主机端口（32768~60999 范围共约 28000 个，理论可支持 1470+ 个并发 Agent）。

#### 端口查询 API

容器启动后，通过 `ContainerInspect` 获取 Docker 分配的实际宿主机端口：

```go
func (m *Manager) GetMappedPorts(ctx context.Context, containerID string) (map[string]string, error) {
    info, err := m.cli.ContainerInspect(ctx, containerID)
    if err != nil {
        return nil, err
    }
    result := make(map[string]string)
    for port, bindings := range info.NetworkSettings.Ports {
        if len(bindings) > 0 {
            result[port.Port()] = bindings[0].HostPort
        }
    }
    return result, nil
    // 返回: {"22": "32768", "80": "32769", "3000": "32770", ...}
}
```

#### 资源开销评估

| 项目 | 开销 |
|------|------|
| 宿主机端口占用 | 20 端口/Agent，不监听时无消耗 |
| iptables 规则 | 20 条 DNAT/Agent，对性能影响极小 |
| 内存 | 端口映射本身无额外内存开销 |
| 10 个 Agent | 200 个端口，完全无压力 |
| 端口范围 | 32768~60999 共 ~28000 个，远远够用 |

### 通信方式总结

| 通信类型 | 方式 | 说明 |
|---------|------|------|
| MCP 协议 | docker exec stdio（Executor 模式） | 不走网络，每次调用一个短命进程 |
| 桌面访问 | Selkies WebRTC（端口 3000/3001） | 统一端口映射 |
| 其他所有服务 | 统一端口映射 | HTTP/WS/SSH/DB 全覆盖 |

## 文件/目录参考

| 路径 | 说明 |
|------|------|
| `internal/container/docker.go` | Docker SDK 封装：容器 CRUD、exec、镜像管理、统一端口映射 |
| `internal/agent/manager.go` | Agent 生命周期：协调容器、MCP Executor/Router、存储 |
| `internal/mcp/executor.go` | MCP Executor：docker exec + 握手 + 调用 + 关闭 |
| `internal/mcp/ephemeral_transport.go` | 单次 MCP JSON-RPC 传输层 |
| `internal/mcp/router.go` | 工具路由：tool_name → Executor 调用 |
| `internal/mcp/registry.go` | 工具注册表：从 YAML 加载工具定义 |
| `internal/mcp/builtin_tools.go` | 内置工具定义 + WriteBuiltinTools() |
| `internal/config/config.go` | 配置加载 |
| `docker/sandbox/Dockerfile` | 统一沙箱镜像构建（基于 linuxserver/webtop:ubuntu-kde） |
| `docker/sandbox/svc-selkies-run` | Selkies 自定义启动脚本（--enable-shared + 1080p） |
| `docker/sandbox/mcp-servers/core/` | mcp-server-core 源码 |
| `docker/sandbox/mcp-servers/desktop/` | mcp-server-desktop 源码 |
| `config.yaml` | `docker_host`, `sandbox_image` 等配置 |

## 常见问题

### Q：容器内的服务，宿主机怎么访问？

所有 Agent 容器创建时已统一映射 20 个常用端口（Selkies/HTTP/SSH/DB 等），通过端口查询 API 获取宿主机映射端口即可访问。MCP 通信仍走 docker exec stdio，不走网络。

### Q：为什么 macOS 不能直接访问容器 IP？

macOS 上 Docker Desktop 在 Linux VM 里运行，容器 IP（172.17.x.x）在 VM 内部网络，宿主机无法直达。只能通过端口映射或 docker exec。Linux 上可以直接访问。

### Q：多个 Agent 端口会冲突吗？

不会。`HostPort: ""` 让 Docker 自动分配可用端口（32768~65535 范围），每个容器分配到不同的宿主机端口。

### Q：容器停止再启动，端口映射会变吗？

`docker stop` + `docker start` 后端口映射保持不变。但 `docker rm` + 重新 `docker create` 后端口会重新分配。当前 SeaTurt 的 Stop/Start 不删除容器，所以端口不变。
