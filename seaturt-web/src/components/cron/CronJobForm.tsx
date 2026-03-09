import { useState } from "react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAgentStore } from "@/stores/agentStore"
import { CronJob } from "@/types"
import CronExprInput from "./CronExprInput"
import AtTimeInput from "./AtTimeInput"

interface Props {
  agentId: string
  open: boolean
  onOpenChange: (open: boolean) => void
  editJob?: CronJob | null
}

export default function CronJobForm({ agentId, open, onOpenChange, editJob }: Props) {
  const createCronJob = useAgentStore((s) => s.createCronJob)
  const updateCronJob = useAgentStore((s) => s.updateCronJob)

  const [jobType, setJobType] = useState<"cron" | "at">(editJob?.type || "cron")
  const [cronExpr, setCronExpr] = useState(editJob?.cron_expr || "")
  const [runAt, setRunAt] = useState(editJob?.run_at ? toDatetimeLocal(editJob.run_at) : "")
  const [prompt, setPrompt] = useState(editJob?.prompt || "")
  const [sessionStrategy, setSessionStrategy] = useState(editJob?.session_strategy || "fixed")
  const [cronExprError, setCronExprError] = useState("")
  const [runAtError, setRunAtError] = useState("")
  const [promptError, setPromptError] = useState("")
  const [submitting, setSubmitting] = useState(false)

  const isEdit = !!editJob

  const resetForm = () => {
    setJobType(editJob?.type || "cron")
    setCronExpr(editJob?.cron_expr || "")
    setRunAt(editJob?.run_at ? toDatetimeLocal(editJob.run_at) : "")
    setPrompt(editJob?.prompt || "")
    setSessionStrategy(editJob?.session_strategy || "fixed")
    setCronExprError("")
    setRunAtError("")
    setPromptError("")
  }

  const validate = (): boolean => {
    let valid = true
    setCronExprError("")
    setRunAtError("")
    setPromptError("")

    if (!prompt.trim()) {
      setPromptError("请输入执行 Prompt")
      valid = false
    }

    if (jobType === "cron") {
      if (!cronExpr.trim()) {
        setCronExprError("请输入 Cron 表达式")
        valid = false
      } else {
        const parts = cronExpr.trim().split(/\s+/)
        if (parts.length !== 5) {
          setCronExprError("Cron 表达式必须是 5 个字段（分 时 日 月 周）")
          valid = false
        }
      }
    } else {
      if (!runAt) {
        setRunAtError("请选择执行时间")
        valid = false
      } else {
        const d = new Date(runAt)
        if (d <= new Date()) {
          setRunAtError("执行时间必须在未来")
          valid = false
        }
      }
    }

    return valid
  }

  const handleSubmit = async () => {
    if (!validate()) return
    setSubmitting(true)
    try {
      if (isEdit && editJob) {
        await updateCronJob(agentId, editJob.id, {
          prompt: prompt.trim(),
          ...(jobType === "cron" ? { cron_expr: cronExpr.trim() } : {}),
          ...(jobType === "at" ? { run_at: new Date(runAt).toISOString() } : {}),
        })
      } else {
        await createCronJob(agentId, {
          type: jobType,
          prompt: prompt.trim(),
          ...(jobType === "cron" ? { cron_expr: cronExpr.trim() } : {}),
          ...(jobType === "at" ? { run_at: new Date(runAt).toISOString() } : {}),
          session_strategy: sessionStrategy,
        })
      }
      onOpenChange(false)
      resetForm()
    } catch (err) {
      console.error("Failed to save cron job:", err)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { onOpenChange(v); if (!v) resetForm() }}>
      <DialogContent className="sm:max-w-[520px] max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{isEdit ? "编辑定时任务" : "新建定时任务"}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          {/* Task Type */}
          {!isEdit && (
            <div className="space-y-2">
              <Label>任务类型</Label>
              <div className="flex gap-1.5">
                <button
                  type="button"
                  onClick={() => setJobType("cron")}
                  className={`text-xs px-3 py-1.5 rounded-full border transition-colors cursor-pointer ${
                    jobType === "cron"
                      ? "bg-primary text-primary-foreground border-primary"
                      : "border-border text-muted-foreground hover:bg-accent hover:text-foreground"
                  }`}
                >
                  周期性（Cron）
                </button>
                <button
                  type="button"
                  onClick={() => setJobType("at")}
                  className={`text-xs px-3 py-1.5 rounded-full border transition-colors cursor-pointer ${
                    jobType === "at"
                      ? "bg-primary text-primary-foreground border-primary"
                      : "border-border text-muted-foreground hover:bg-accent hover:text-foreground"
                  }`}
                >
                  一次性（At）
                </button>
              </div>
            </div>
          )}

          {/* Cron Expression / At Time */}
          {jobType === "cron" ? (
            <CronExprInput value={cronExpr} onChange={setCronExpr} error={cronExprError} />
          ) : (
            <AtTimeInput value={runAt} onChange={setRunAt} error={runAtError} />
          )}

          {/* Prompt */}
          <div className="space-y-2">
            <Label>执行 Prompt</Label>
            <Textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder="定时任务触发时发送给 Agent 的消息..."
              className={promptError ? "border-destructive" : ""}
              rows={3}
            />
            {promptError && <p className="text-xs text-destructive">{promptError}</p>}
            <p className="text-[11px] text-muted-foreground">此 prompt 将在定时触发时自动发送给 Agent</p>
          </div>

          {/* Session Strategy */}
          {!isEdit && (
            <div className="space-y-2">
              <Label>Session 策略</Label>
              <Select value={sessionStrategy} onValueChange={(v) => setSessionStrategy(v as "fixed" | "new")}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="fixed">固定 Session（复用同一会话）</SelectItem>
                  <SelectItem value="new">每次新建 Session</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-[11px] text-muted-foreground">
                固定：每次执行发到同一个对话；新建：每次创建独立对话
              </p>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => { onOpenChange(false); resetForm() }}>
            取消
          </Button>
          <Button size="sm" onClick={handleSubmit} disabled={submitting}>
            {submitting ? "保存中..." : isEdit ? "保存" : "创建"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function toDatetimeLocal(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}
