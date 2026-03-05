# Docker 容器管理

## 概述

SeaTurt 为每个 Agent 创建独立的 Docker 容器作为沙箱环境。本文档详细说明容器的网络模型、通信机制、端口管理、镜像策略及生命周期管理。

## 当前架构（v0.0.1 ~ v0.0.2）

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
    │  docker exec -i <container> mcp-server-core
    │  （通过 Docker SDK 的 ContainerExecCreate + ContainerExecAttach）
    │
    ▼
容器内 MCP Server 进程
    stdin  ← Server 写入 JSON-RPC 请求
    stdout → Server 读取 JSON-RPC 响应
```

代码位置：`internal/container/docker.go`

| 方法 | 用途 | 通信方式 |
|------|------|---------|
| `Exec()` | 一次性命令（如健康检查） | exec → 读取 stdout/stderr → 返回结果 |
| `ExecStdio()` | MCP 长连接（交互式会话） | exec → hijack 连接 → 双向 stdin/stdout 流 |

`ExecStdio` 返回 `types.HijackedResponse`，底层是一个 TCP 连接被 Docker daemon hijack 后的双向字节流。MCP Transport 层在此之上实现 JSON-RPC 协议。

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
2. 确定 MCP Servers（用户指定 / 默认 core）
3. 确定 model
4. 创建 workspace 目录 (~/.seaturt/workspaces/<agentID>/)
5. 创建 .seaturt/ 子目录
6. 生成 SYSTEM.md → workspace/.seaturt/SYSTEM.md
7. docker.CreateContainer
   ├── 注入环境变量：HOST_UID, HOST_GID, 用户自定义
   ├── 绑定挂载：workspacePath → /workspace
   ├── Label：seaturt.agent_id, seaturt.managed
   └── 容器名：seaturt-<agentID>
8. docker.StartContainer
9. 查询实际端口映射（ContainerInspect）
10. 生成 PORTS.md → workspace/.seaturt/PORTS.md
11. mcp.Pool.Connect — 为每个 MCP Server 创建 exec 会话
12. mcp.NewRouter — 建立 tool_name → MCP Server 路由表
13. store.CreateAgent — 持久化到 SQLite
```

### Start 流程（`manager.Start`）

```
1. 从 DB 读取 Agent 信息
2. docker.StartContainer（重启已停止的容器）
3. 查询端口映射，重新生成 PORTS.md（以防端口变化）
4. mcp.Pool.Connect（重建所有 MCP 连接）
5. mcp.NewRouter（重建路由表）
6. 更新内存状态（pools, routers map）
7. store.UpdateAgentStatus → running
```

### Stop 流程（`manager.Stop`）

```
1. 关闭 MCP 连接池（pool.CloseAll）
2. 清理内存状态（delete pools/routers）
3. docker.StopContainer
4. store.UpdateAgentStatus → stopped
```

### Delete 流程（`manager.Delete`）

```
1. 关闭 MCP 连接池
2. docker.RemoveContainer（force）
3. store.DeleteMessages
4. store.DeleteAgent
注意：workspace 目录不删除（保留用户数据）
```

## 镜像设计

### 当前镜像（`seaturt/sandbox:latest`）

多阶段构建，最终镜像约 300MB：

```
golang:1.23-alpine (builder)
    └── 编译 mcp-server-core

ubuntu:22.04 (runtime)
    ├── 基础工具：curl, wget, git, vim, python3, jq, ripgrep
    ├── mcp-server-core（Go 静态二进制）
    ├── gosu + sudo（权限管理）
    ├── agent 用户（非 root，无密码 sudo）
    ├── entrypoint.sh（动态 UID/GID 匹配）
    └── CMD: tail -f /dev/null（保持容器存活）
```

### entrypoint.sh 做了什么

```bash
1. 如果设置了 HOST_UID，修改 agent 用户的 UID 与宿主机一致
2. 如果设置了 HOST_GID，修改 agent 用户组的 GID 与宿主机一致
3. chown /workspace 确保 agent 可访问
4. gosu agent "$@" — 以 agent 身份执行 CMD
```

目的：容器内 agent 用户的 UID/GID 与宿主机一致，workspace 挂载目录的文件权限不会出现 permission denied。

### 容器运行状态

```
容器启动后：
  PID 1: entrypoint.sh → gosu agent tail -f /dev/null
  （容器空闲，等待 docker exec 启动 MCP Server）

docker exec 建立 MCP 连接后：
  PID 1: tail -f /dev/null
  PID X: mcp-server-core（由 docker exec 启动，stdin/stdout 连接到 Server）
```

MCP Server 进程的生命周期由 exec 会话控制，会话断开时进程自动退出。

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
- ✅ 支持任意协议（HTTP/WebSocket/VNC/SSH/TCP）
- ✅ macOS/Linux 无平台差异
- ✅ `HostPort: ""` 自动分配，多 Agent 天然不冲突
- ✅ 统一模型，无需区分桌面/普通容器的网络处理逻辑

#### 预映射端口清单

| 类别 | 容器端口 | 用途 |
|------|---------|------|
| **远程访问** | 22 | SSH |
| **桌面** | 5900 | VNC Server (TigerVNC) |
| **桌面** | 6080 | noVNC Web (websockify) |
| **Web 服务** | 80 | HTTP |
| **Web 服务** | 443 | HTTPS |
| **前端开发** | 3000 | React / Next.js / Vite 等 |
| **前端开发** | 3001 | 前端备用 |
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

共 **20 个端口**，每个 Agent 消耗 20 个宿主机端口（32768~60999 范围共约 28000 个，理论可支持 1400 个并发 Agent）。

#### 实现代码

```go
// defaultMappedPorts 所有 Agent 容器统一映射的端口
var defaultMappedPorts = []int{
    22,                         // SSH
    80, 443,                    // HTTP / HTTPS
    3000, 3001, 5173, 5174,     // 前端开发
    4000,                       // 通用
    5432, 3306, 27017, 6379,    // 数据库 / 缓存
    5900, 6080,                 // VNC / noVNC
    8000, 8001, 8080, 8081,     // 后端开发
    8888, 9000,                 // Jupyter / PHP
}

func buildPortBindings() (nat.PortSet, nat.PortMap) {
    exposedPorts := nat.PortSet{}
    portBindings := nat.PortMap{}
    for _, p := range defaultMappedPorts {
        port := nat.Port(fmt.Sprintf("%d/tcp", p))
        exposedPorts[port] = struct{}{}
        portBindings[port] = []nat.PortBinding{
            {HostIP: "127.0.0.1", HostPort: ""},
        }
    }
    return exposedPorts, portBindings
}
```

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
    // 返回: {"22": "32768", "80": "32769", "3000": "32770", "5900": "32771", ...}
}
```

Agent 或用户可通过 API 查询某个 Agent 的端口映射表，知道容器内的 3000 端口对应宿主机的哪个端口。

#### 资源开销评估

| 项目 | 开销 |
|------|------|
| 宿主机端口占用 | 20 端口/Agent，不监听时无消耗 |
| iptables 规则 | 20 条 DNAT/Agent，对性能影响极小 |
| 内存 | 端口映射本身无额外内存开销 |
| 10 个 Agent | 200 个端口，完全无压力 |
| 端口范围 | 32768~60999 共 ~28000 个，远远够用 |

#### 备选方案（不采用，仅记录）

| 方案 | 说明 | 不采用原因 |
|------|------|----------|
| host 网络模式 | `--network=host`，容器直接用宿主机网络 | 多 Agent 端口冲突，安全性差 |
| Server 反向代理 | docker exec curl 转发 HTTP | 仅支持 HTTP，不支持 WebSocket/VNC 等协议 |
| 容器 IP 直连 | 访问 172.17.x.x | macOS/Windows 不可达 |

### 通信方式总结

| 通信类型 | 方式 | 说明 |
|---------|------|------|
| MCP 协议 | docker exec stdio | 不走网络，双向字节流 |
| 其他所有服务 | 统一端口映射 | VNC/HTTP/WS/SSH/DB 全覆盖 |

## v0.0.3 改造：桌面容器

### 双镜像策略

v0.0.3 引入桌面环境后，维护两个镜像变体：

| 镜像 | 大小 | 内容 | 使用场景 |
|------|------|------|---------|
| `seaturt/sandbox:latest` | ~300MB | 基础工具 + mcp-server-core | 普通 Agent |
| `seaturt/sandbox-desktop:latest` | ~2GB | 基础 + GNOME + VNC + noVNC + Firefox + mcp-server-desktop | 桌面 Agent |

通过 `agent.config.desktop: true/false` 自动选择镜像。

### 端口映射

v0.0.3 起，所有容器（包括桌面和普通）统一使用预映射方案，详见上文「采用方案：统一端口映射」。

VNC/noVNC 所需的 5900/6080 已包含在统一端口列表中，无需桌面容器额外处理。

桌面容器的额外配置仅为 ShmSize：

```go
if opts.Desktop {
    hostCfg.ShmSize = 2 * 1024 * 1024 * 1024  // 2GB，浏览器渲染需要
}
```

### 环境变量扩展

桌面模式时额外注入：

| 变量 | 值 | 用途 |
|------|---|------|
| `DESKTOP_ENABLED` | `true` | entrypoint 据此启动桌面服务 |
| `RESOLUTION` | `1920x1080x24` | Xvfb 虚拟显示分辨率 |
| `DISPLAY` | `:99` | X11 显示编号 |

### 容器内进程模型

```
桌面容器启动后：

PID 1: entrypoint.sh → gosu agent tail -f /dev/null
       ├── start-desktop.sh（后台）
       │   ├── Xvfb :99           — 虚拟显示服务
       │   ├── gnome-session      — GNOME 桌面
       │   ├── x0vncserver :5900  — VNC Server
       │   └── websockify :6080   — noVNC WebSocket 代理
       │
       └── (等待 docker exec)

docker exec 建立 MCP 连接后：
PID X: mcp-server-core     — 基础 tool（shell, file）
PID Y: mcp-server-desktop  — 桌面 tool（screenshot, mouse, keyboard）
```

### ShmSize 说明

Chrome/Firefox 等浏览器使用 `/dev/shm`（共享内存）进行渲染。Docker 默认 `/dev/shm` 只有 64MB，浏览器打开复杂页面时会因内存不足而崩溃。

桌面容器将 `/dev/shm` 增大到 2GB：

```go
hostCfg.ShmSize = 2 * 1024 * 1024 * 1024
```

等效于 `docker run --shm-size=2g`。

## 文件/目录参考

| 路径 | 说明 |
|------|------|
| `internal/container/docker.go` | Docker SDK 封装：容器 CRUD、exec、镜像管理 |
| `internal/agent/manager.go` | Agent 生命周期：协调容器、MCP、存储 |
| `docker/sandbox/Dockerfile` | 基础沙箱镜像构建 |
| `docker/sandbox/entrypoint.sh` | 容器入口：UID/GID 匹配 + 降权 |
| `docker/sandbox/mcp-servers/core/` | mcp-server-core 源码 |
| `docker/sandbox/start-desktop.sh` | 桌面启动脚本（v0.0.3 新增） |
| `docker/sandbox/mcp-servers/desktop/` | mcp-server-desktop 源码（v0.0.3 新增） |
| `config.yaml` | `docker_host`, `sandbox_image` 等配置 |

## 常见问题

### Q：容器内的服务，宿主机怎么访问？

所有 Agent 容器创建时已统一映射 20 个常用端口（VNC/HTTP/SSH/DB 等），通过端口查询 API 获取宿主机映射端口即可访问。MCP 通信仍走 docker exec stdio，不走网络。

### Q：为什么 macOS 不能直接访问容器 IP？

macOS 上 Docker Desktop 在 Linux VM 里运行，容器 IP（172.17.x.x）在 VM 内部网络，宿主机无法直达。只能通过端口映射或 docker exec。Linux 上可以直接访问。

### Q：多个 Agent 端口会冲突吗？

不会。`HostPort: ""` 让 Docker 自动分配可用端口（32768~65535 范围），每个容器分配到不同的宿主机端口。

### Q：容器停止再启动，端口映射会变吗？

会。`docker stop` + `docker start` 后端口映射保持不变。但 `docker rm` + 重新 `docker create` 后端口会重新分配。当前 SeaTurt 的 Stop/Start 不删除容器，所以端口不变。
