package builtin

import (
	"github.com/seaturt/server/internal/mcp"
)

// builtinServerName is the virtual MCP server name for built-in tools.
const builtinServerName = "builtin"

// CronToolDefinitions returns the tool definitions for cron management tools.
func CronToolDefinitions() []mcp.ToolDefinition {
	return []mcp.ToolDefinition{
		{
			Name:        "create_cron_job",
			Description: "创建定时任务。type=\"cron\" 为周期性任务（需提供 cron_expr），type=\"at\" 为一次性任务（需提供 run_at）。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"cron", "at"},
						"description": "任务类型：cron（周期性）或 at（一次性）",
					},
					"cron_expr": map[string]any{
						"type":        "string",
						"description": "5 位 cron 表达式（分 时 日 月 周），type=cron 时必填。例如 \"0 9 * * *\" 表示每天 9:00",
					},
					"run_at": map[string]any{
						"type":        "string",
						"description": "一次性执行时间（RFC 3339 格式），type=at 时必填。例如 \"2026-03-10T09:00:00+08:00\"",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "定时任务触发时发送给 Agent 的 prompt",
					},
					"session_strategy": map[string]any{
						"type":        "string",
						"enum":        []string{"fixed", "new"},
						"description": "Session 策略：fixed（复用固定 Session）或 new（每次新建）。默认 fixed",
					},
				},
				"required": []string{"type", "prompt"},
			},
		},
		{
			Name:        "list_cron_jobs",
			Description: "列出当前 Agent 的所有定时任务。",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "update_cron_job",
			Description: "更新定时任务。可以修改 prompt、cron_expr、enabled 等字段。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "要更新的定时任务 ID",
					},
					"cron_expr": map[string]any{
						"type":        "string",
						"description": "新的 cron 表达式",
					},
					"run_at": map[string]any{
						"type":        "string",
						"description": "新的一次性执行时间（RFC 3339）",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "新的 prompt",
					},
					"enabled": map[string]any{
						"type":        "boolean",
						"description": "是否启用",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "delete_cron_job",
			Description: "删除定时任务。",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "要删除的定时任务 ID",
					},
				},
				"required": []string{"id"},
			},
		},
	}
}
