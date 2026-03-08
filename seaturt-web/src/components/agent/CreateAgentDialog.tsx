import { useState } from "react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAgentStore } from "@/stores/agentStore"
import { Loader2 } from "lucide-react"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export default function CreateAgentDialog({ open, onOpenChange }: Props) {
  const { models, defaultModel, createAgent, selectAgent } = useAgentStore()
  const [name, setName] = useState("")
  const [model, setModel] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")

  const handleCreate = async () => {
    if (!name.trim()) {
      setError("请输入 Agent 名称")
      return
    }
    setLoading(true)
    setError("")

    const selectedModel = model || defaultModel || undefined
    const selectedProvider = models.find((m) => m.id === selectedModel)?.provider

    const agent = await createAgent(
      name.trim(),
      selectedModel,
      selectedProvider
    ).catch((e: Error) => {
      setError(e.message)
      return null
    })

    setLoading(false)
    if (agent) {
      selectAgent(agent.id)
      onOpenChange(false)
      setName("")
      setModel("")
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[400px]">
        <DialogHeader>
          <DialogTitle>新建 Agent</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="space-y-2">
            <Label htmlFor="agent-name">名称</Label>
            <Input
              id="agent-name"
              placeholder="我的助手"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && handleCreate()}
            />
          </div>

          <div className="space-y-2">
            <Label>模型</Label>
            <Select value={model || defaultModel} onValueChange={setModel}>
              <SelectTrigger>
                <SelectValue placeholder="选择模型" />
              </SelectTrigger>
              <SelectContent>
                {models.map((m) => (
                  <SelectItem key={m.id} value={m.id}>
                    {m.name}
                    <span className="text-muted-foreground ml-2 text-xs">
                      ({m.provider})
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {error && (
            <p className="text-sm text-destructive">{error}</p>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={handleCreate} disabled={loading}>
            {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            创建
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
