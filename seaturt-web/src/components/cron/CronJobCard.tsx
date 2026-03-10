import { useState } from "react"
import { Play, Trash2, Clock } from "lucide-react"
import { Switch } from "@/components/ui/switch"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { CronJob } from "@/types"
import { useAgentStore } from "@/stores/agentStore"
import CronJobHistory from "./CronJobHistory"

interface Props {
  job: CronJob
  agentId: string
  onEdit: (job: CronJob) => void
  onViewSession?: (sessionId: string) => void
}

function formatNextRun(iso: string | null): string {
  if (!iso) return "—"
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function describeSchedule(job: CronJob): string {
  if (job.type === "at") {
    return job.run_at ? `一次性: ${formatNextRun(job.run_at)}` : "一次性"
  }
  // Simple cron description
  const parts = job.cron_expr.split(/\s+/)
  if (parts.length !== 5) return job.cron_expr

  const [min, hour, dom, , dow] = parts
  if (min.startsWith("*/")) return `每 ${min.slice(2)} 分钟`
  if (hour.startsWith("*/")) return `每 ${hour.slice(2)} 小时`
  if (dom === "*" && dow === "*") return `每天 ${hour.padStart(2, "0")}:${min.padStart(2, "0")}`
  if (dow === "1") return `每周一 ${hour.padStart(2, "0")}:${min.padStart(2, "0")}`
  if (dom === "1") return `每月 1 号 ${hour.padStart(2, "0")}:${min.padStart(2, "0")}`
  return job.cron_expr
}

export default function CronJobCard({ job, agentId, onEdit, onViewSession }: Props) {
  const updateCronJob = useAgentStore((s) => s.updateCronJob)
  const deleteCronJob = useAgentStore((s) => s.deleteCronJob)
  const triggerCronJob = useAgentStore((s) => s.triggerCronJob)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [triggering, setTriggering] = useState(false)

  const handleToggle = async (checked: boolean) => {
    try {
      await updateCronJob(agentId, job.id, { enabled: checked })
    } catch (err) {
      console.error("Failed to toggle cron job:", err)
    }
  }

  const handleDelete = async () => {
    try {
      await deleteCronJob(agentId, job.id)
      setDeleteOpen(false)
    } catch (err) {
      console.error("Failed to delete cron job:", err)
    }
  }

  const handleTrigger = async () => {
    setTriggering(true)
    try {
      await triggerCronJob(agentId, job.id)
    } catch (err) {
      console.error("Failed to trigger cron job:", err)
    } finally {
      setTimeout(() => setTriggering(false), 1000)
    }
  }

  return (
    <>
      <div
        className={`border border-border rounded-lg p-3 transition-colors hover:border-ring min-w-0 overflow-hidden ${
          !job.enabled ? "opacity-55" : ""
        }`}
      >
        {/* Header */}
        <div className="flex items-center justify-between mb-1.5">
          <div
            className="flex items-center gap-2.5 flex-1 min-w-0 cursor-pointer"
            onClick={() => onEdit(job)}
          >
            <span className="font-mono text-xs font-medium bg-accent text-foreground px-2 py-0.5 rounded whitespace-nowrap">
              {job.type === "cron" ? job.cron_expr : "AT"}
            </span>
            <span className="text-sm truncate">{job.prompt}</span>
          </div>
          <div className="flex items-center gap-1 flex-shrink-0">
            <Switch checked={job.enabled} onCheckedChange={handleToggle} />
            <TooltipProvider delayDuration={300}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={handleTrigger}
                    disabled={!job.enabled || triggering}
                  >
                    <Play className={`h-3.5 w-3.5 ${triggering ? "animate-pulse" : ""}`} />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>立即执行</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-destructive hover:text-destructive"
                    onClick={() => setDeleteOpen(true)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>删除</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>
        </div>

        {/* Meta */}
        <div className="flex items-center gap-4 text-[11px] text-muted-foreground flex-wrap">
          <span className="flex items-center gap-1">
            <Clock className="h-3 w-3" />
            {describeSchedule(job)}
          </span>
          <span>{job.session_strategy === "fixed" ? "固定 Session" : "每次新建"}</span>
          {job.enabled && job.next_run_at && (
            <span>下次执行：{formatNextRun(job.next_run_at)}</span>
          )}
          {!job.enabled && <span>已禁用</span>}
        </div>

        {/* Execution History — card-level block, below meta */}
        <CronJobHistory agentId={agentId} jobId={job.id} onViewSession={onViewSession} />
      </div>

      {/* Delete Confirm */}
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除定时任务？</AlertDialogTitle>
            <AlertDialogDescription>
              将删除任务「{job.cron_expr || "AT"} — {job.prompt.slice(0, 30)}
              {job.prompt.length > 30 ? "..." : ""}」及其所有执行历史，此操作不可撤销。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete} className="bg-destructive text-destructive-foreground">
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
