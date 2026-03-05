package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IT-39: GenerateSystemMD produces valid content with base sections
func TestGenerateSystemMD_Default(t *testing.T) {
	t.Parallel()
	cfg := SystemPromptConfig{
		MCPServers: []MCPServerConfig{
			{Name: "core", Command: "mcp-server-core"},
		},
	}

	md := GenerateSystemMD(cfg)

	assert.Contains(t, md, "# Agent 系统指令")
	assert.Contains(t, md, "## 身份")
	assert.Contains(t, md, "## 行为准则")
	assert.Contains(t, md, "## 工作目录")
	assert.Contains(t, md, "## 端口使用")
	assert.Contains(t, md, "## 可用工具")
	assert.Contains(t, md, "- core")
	assert.NotContains(t, md, "## 桌面环境")
	assert.NotContains(t, md, "## 附加指令")
}

// IT-43: desktop: true includes desktop section
func TestGenerateSystemMD_Desktop(t *testing.T) {
	t.Parallel()
	cfg := SystemPromptConfig{
		Desktop: true,
		MCPServers: []MCPServerConfig{
			{Name: "core", Command: "mcp-server-core"},
			{Name: "desktop", Command: "mcp-server-desktop"},
		},
	}

	md := GenerateSystemMD(cfg)

	assert.Contains(t, md, "## 桌面环境")
	assert.Contains(t, md, "screenshot")
	assert.Contains(t, md, "mouse_click")
	assert.Contains(t, md, "keyboard_type")
	assert.Contains(t, md, "- core")
	assert.Contains(t, md, "- desktop")
}

// IT-42: custom system_prompt appended as extra rules
func TestGenerateSystemMD_ExtraRules(t *testing.T) {
	t.Parallel()
	cfg := SystemPromptConfig{
		MCPServers: []MCPServerConfig{
			{Name: "core", Command: "mcp-server-core"},
		},
		ExtraRules: "你是一个全栈开发助手，擅长分析和解决问题。",
	}

	md := GenerateSystemMD(cfg)

	assert.Contains(t, md, "## 附加指令")
	assert.Contains(t, md, "你是一个全栈开发助手")
}

// IT-41: no MCP servers produces no tools section
func TestGenerateSystemMD_NoMCPServers(t *testing.T) {
	t.Parallel()
	cfg := SystemPromptConfig{}
	md := GenerateSystemMD(cfg)

	assert.NotContains(t, md, "## 可用工具")
}

// IT-40: GeneratePortsMD produces valid markdown table
func TestGeneratePortsMD(t *testing.T) {
	t.Parallel()
	portMap := map[string]string{
		"22":   "32768",
		"80":   "32769",
		"3000": "32770",
		"8080": "32771",
	}

	md := GeneratePortsMD(portMap)

	assert.Contains(t, md, "# 端口映射")
	assert.Contains(t, md, "| 容器端口 | 宿主机端口 | 用途 |")

	// Check sorted order (22 should come before 80)
	idx22 := strings.Index(md, "| 22 |")
	idx80 := strings.Index(md, "| 80 |")
	idx3000 := strings.Index(md, "| 3000 |")
	idx8080 := strings.Index(md, "| 8080 |")
	require.Greater(t, idx22, -1)
	require.Greater(t, idx80, -1)
	assert.Less(t, idx22, idx80)
	assert.Less(t, idx80, idx3000)
	assert.Less(t, idx3000, idx8080)

	// Check descriptions
	assert.Contains(t, md, "SSH")
	assert.Contains(t, md, "HTTP")
	assert.Contains(t, md, "前端开发 (React/Next.js)")
	assert.Contains(t, md, "后端开发 (Go/Java)")

	// Check footer
	assert.Contains(t, md, "**重要**")
	assert.Contains(t, md, "localhost:<宿主机端口>")
}

// IT-40b: empty port map produces header but no rows
func TestGeneratePortsMD_Empty(t *testing.T) {
	t.Parallel()
	md := GeneratePortsMD(map[string]string{})

	assert.Contains(t, md, "# 端口映射")
	assert.Contains(t, md, "| 容器端口 | 宿主机端口 | 用途 |")
	// No actual port rows
	assert.NotContains(t, md, "| 22 |")
}

// IT-40c: unknown port gets "—" description
func TestGeneratePortsMD_UnknownPort(t *testing.T) {
	t.Parallel()
	portMap := map[string]string{
		"12345": "55555",
	}
	md := GeneratePortsMD(portMap)
	assert.Contains(t, md, "| 12345 | 55555 | — |")
}

// GetPortDescription returns correct descriptions
func TestGetPortDescription(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "SSH", GetPortDescription("22"))
	assert.Equal(t, "HTTP", GetPortDescription("80"))
	assert.Equal(t, "PostgreSQL", GetPortDescription("5432"))
	assert.Equal(t, "—", GetPortDescription("99999"))
}
