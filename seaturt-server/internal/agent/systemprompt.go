package agent

import (
	"fmt"
	"sort"
	"strings"
)

// SystemPromptConfig holds the configuration for generating SYSTEM.md.
type SystemPromptConfig struct {
	MCPServers []MCPServerConfig // configured MCP servers
	EnvVars    map[string]string // custom environment variables
	ExtraRules string            // user-defined extra rules (optional)
}

const systemPromptBase = `# Agent 系统指令

## 身份
你是一个运行在沙箱容器中的编程助手。你拥有完整的 Linux 环境，可以执行 shell 命令、读写文件、安装软件包等。

## 行为准则
- 优先使用工具（shell_exec / file_read / file_write 等）来完成任务，而非凭记忆回答
- 执行命令前思考可能的副作用
- 对文件的修改要精确，优先使用 sed/patch 等工具做局部修改，避免重写整个文件
- 安装软件包时使用非交互模式（如 apt-get -y）
- 输出要简洁精确

## 工作目录
当前工作目录为 ` + "`/workspace`" + `，这是与宿主机共享的挂载目录。你在此创建的文件可以被宿主机直接访问。

## 端口使用
容器内以下端口已映射到宿主机（详见 /workspace/.seaturt/PORTS.md）。
启动 Web 服务、数据库等时，请优先使用这些端口，其他端口无法从宿主机访问。
`

const systemPromptDesktop = `
## 桌面环境
本容器已启用 KDE Plasma 桌面环境，通过 KasmVNC 提供浏览器内远程桌面访问。你可以使用以下工具操作桌面：
- ` + "`screenshot`" + ` — 截取桌面截图
- ` + "`mouse_click`" + ` / ` + "`mouse_move`" + ` — 鼠标操作
- ` + "`keyboard_type`" + ` / ` + "`keyboard_key`" + ` — 键盘操作
- ` + "`open_app`" + ` — 打开应用程序（如 firefox、terminal）

桌面通过 KasmVNC 提供远程访问（端口 3000/3001）。
`

// GenerateSystemMD generates the content for SYSTEM.md based on the given config.
func GenerateSystemMD(cfg SystemPromptConfig) string {
	var buf strings.Builder
	buf.WriteString(systemPromptBase)

	// Desktop section always included (unified desktop image)
	buf.WriteString(systemPromptDesktop)

	// MCP Server list
	if len(cfg.MCPServers) > 0 {
		buf.WriteString("\n## 可用工具\n\n当前已加载的 MCP Server：\n")
		for _, s := range cfg.MCPServers {
			buf.WriteString(fmt.Sprintf("- %s\n", s.Name))
		}
		buf.WriteString("\n")
	}

	if cfg.ExtraRules != "" {
		buf.WriteString("\n## 附加指令\n\n")
		buf.WriteString(cfg.ExtraRules)
		buf.WriteString("\n")
	}

	return buf.String()
}

// portDescriptions maps container ports to their human-readable descriptions.
var portDescriptions = map[int]string{
	22:    "SSH",
	80:    "HTTP",
	443:   "HTTPS",
	3000:  "KasmVNC (桌面 Web 访问)",
	3001:  "KasmVNC (HTTPS)",
	3306:  "MySQL",
	4000:  "通用开发",
	5173:  "Vite",
	5174:  "Vite (备用)",
	5432:  "PostgreSQL",
	6379:  "Redis",
	8000:  "后端开发 (Python/uvicorn)",
	8001:  "后端开发 (备用)",
	8080:  "后端开发 (Go/Java)",
	8081:  "后端开发 (备用)",
	8888:  "Jupyter Notebook",
	9000:  "PHP / 其他",
	27017: "MongoDB",
}

// portEntry represents a single port mapping entry for sorting.
type portEntry struct {
	ContainerPort int
	HostPort      string
	Description   string
}

// GeneratePortsMD generates the content for PORTS.md from a port mapping.
// portMap keys are container port strings (e.g. "22"), values are host port strings (e.g. "32768").
func GeneratePortsMD(portMap map[string]string) string {
	var entries []portEntry
	for containerPort, hostPort := range portMap {
		var cp int
		fmt.Sscanf(containerPort, "%d", &cp)
		desc := portDescriptions[cp]
		if desc == "" {
			desc = "—"
		}
		entries = append(entries, portEntry{
			ContainerPort: cp,
			HostPort:      hostPort,
			Description:   desc,
		})
	}

	// Sort by container port
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ContainerPort < entries[j].ContainerPort
	})

	var buf strings.Builder
	buf.WriteString("# 端口映射\n\n")
	buf.WriteString("以下端口已映射到宿主机。启动服务请使用「容器端口」，用户通过「宿主机端口」访问。\n\n")
	buf.WriteString("| 容器端口 | 宿主机端口 | 用途 |\n")
	buf.WriteString("|---------|----------|------|\n")

	for _, e := range entries {
		buf.WriteString(fmt.Sprintf("| %d | %s | %s |\n", e.ContainerPort, e.HostPort, e.Description))
	}

	buf.WriteString("\n**重要**：\n")
	buf.WriteString("- 启动服务时使用「容器端口」列中的端口号\n")
	buf.WriteString("- 告诉用户访问时使用 `localhost:<宿主机端口>`\n")
	buf.WriteString("- 未列出的端口无法从宿主机访问\n")

	return buf.String()
}

// GetPortDescription returns the human-readable description for a container port.
func GetPortDescription(containerPort string) string {
	var cp int
	fmt.Sscanf(containerPort, "%d", &cp)
	desc := portDescriptions[cp]
	if desc == "" {
		return "—"
	}
	return desc
}
