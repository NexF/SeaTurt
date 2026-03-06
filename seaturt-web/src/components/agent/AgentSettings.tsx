import { useEffect, useState, useCallback } from "react"
import { Save, Loader2, RotateCcw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Agent } from "@/types"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"

interface Props {
  agent: Agent
  open: boolean
  onOpenChange: (open: boolean) => void
}

export default function AgentSettings({ agent, open, onOpenChange }: Props) {
  const [prompt, setPrompt] = useState("")
  const [originalPrompt, setOriginalPrompt] = useState("")
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  const fetchPrompt = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetch(`/api/agents/${agent.id}/system-prompt`)
      if (res.ok) {
        const data = await res.json()
        setPrompt(data.system_prompt || "")
        setOriginalPrompt(data.system_prompt || "")
      }
    } catch (err) {
      console.warn("Failed to load system prompt:", err)
    } finally {
      setLoading(false)
    }
  }, [agent.id])

  useEffect(() => {
    if (open) {
      fetchPrompt()
      setSaved(false)
    }
  }, [open, fetchPrompt])

  const handleSave = async () => {
    setSaving(true)
    try {
      const res = await fetch(`/api/agents/${agent.id}/system-prompt`, {
        method: "PUT",
        body: prompt,
      })
      if (res.ok) {
        setOriginalPrompt(prompt)
        setSaved(true)
        setTimeout(() => setSaved(false), 2000)
      }
    } catch (err) {
      console.error("Failed to save system prompt:", err)
    } finally {
      setSaving(false)
    }
  }

  const handleReset = () => {
    setPrompt(originalPrompt)
  }

  const isDirty = prompt !== originalPrompt

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Agent 设置 — {agent.name}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 flex-1 min-h-0 flex flex-col">
          {/* Info */}
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <span className="text-muted-foreground">模型</span>
              <p className="font-medium">{agent.config.model}</p>
            </div>
            <div>
              <span className="text-muted-foreground">状态</span>
              <p className="font-medium">{agent.status}</p>
            </div>
            <div>
              <span className="text-muted-foreground">镜像</span>
              <p className="font-mono text-xs truncate">{agent.image}</p>
            </div>
            <div>
              <span className="text-muted-foreground">MCP Servers</span>
              <p className="font-medium">{agent.config.mcp_servers?.length || 0} 个</p>
            </div>
          </div>

          {/* System Prompt */}
          <div className="flex-1 min-h-0 flex flex-col">
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">System Prompt</label>
              <div className="flex items-center gap-1">
                {saved && (
                  <span className="text-xs text-green-500 mr-2">已保存</span>
                )}
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={handleReset}
                  disabled={!isDirty || saving}
                  className="h-7 px-2"
                >
                  <RotateCcw className="h-3.5 w-3.5 mr-1" />
                  重置
                </Button>
                <Button
                  size="sm"
                  onClick={handleSave}
                  disabled={!isDirty || saving}
                  className="h-7 px-3"
                >
                  {saving ? (
                    <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
                  ) : (
                    <Save className="h-3.5 w-3.5 mr-1" />
                  )}
                  保存
                </Button>
              </div>
            </div>
            {loading ? (
              <div className="flex-1 flex items-center justify-center">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : (
              <textarea
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                className="flex-1 min-h-[300px] w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono leading-relaxed resize-none focus:outline-none focus:ring-1 focus:ring-ring"
                placeholder="输入 System Prompt..."
                spellCheck={false}
              />
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
