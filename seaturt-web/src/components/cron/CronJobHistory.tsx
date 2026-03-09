import { useState, useEffect } from "react"
import { ChevronRight } from "lucide-react"
import { CronJobExecution } from "@/types"
import * as api from "@/services/api"

interface Props {
  agentId: string
  jobId: string
  onViewSession?: (sessionId: string) => void
}

const statusConfig: Record<string, { label: string; className: string }> = {
  success: { label: "成功", className: "bg-green-500/10 text-green-600 dark:text-green-400" },
  failed: { label: "失败", className: "bg-destructive/10 text-destructive" },
  skipped: { label: "跳过", className: "bg-orange-500/10 text-orange-600 dark:text-orange-400" },
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  return `${Math.floor(s / 60)}m ${s % 60}s`
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export default function CronJobHistory({ agentId, jobId, onViewSession }: Props) {
  const [expanded, setExpanded] = useState(false)
  const [executions, setExecutions] = useState<CronJobExecution[]>([])
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    if (expanded && !loaded) {
      api.listCronJobHistory(agentId, jobId)
        .then((res) => {
          setExecutions(res.executions)
          setLoaded(true)
        })
        .catch(console.warn)
    }
  }, [expanded, loaded, agentId, jobId])

  return (
    <div className="mt-1.5">
      <button
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground transition-colors cursor-pointer py-0.5"
      >
        <ChevronRight className={`h-3 w-3 transition-transform duration-200 ${expanded ? "rotate-90" : ""}`} />
        执行历史
      </button>
      {expanded && (
        <div className="border-t border-border mt-2 pt-2">
          {executions.length === 0 ? (
            <p className="text-xs text-muted-foreground py-2">暂无执行记录</p>
          ) : (
            <div>
              {executions.slice(0, 10).map((exec, i) => {
                const cfg = statusConfig[exec.status] || statusConfig.failed
                return (
                  <div
                    key={exec.id}
                    className={`flex items-center gap-3 py-1.5 text-xs ${i > 0 ? "border-t border-border" : ""}`}
                  >
                    <span className="font-mono text-[11px] text-muted-foreground whitespace-nowrap">
                      {formatTime(exec.started_at)}
                    </span>
                    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium ${cfg.className}`}>
                      {cfg.label}
                    </span>
                    <span className="text-[11px] text-muted-foreground whitespace-nowrap">
                      {exec.status === "skipped" ? "—" : `耗时 ${formatDuration(exec.duration)}`}
                    </span>
                    {exec.error && (
                      <span className="text-[11px] text-muted-foreground truncate max-w-[150px] ml-auto" title={exec.error}>
                        {exec.error}
                      </span>
                    )}
                    {exec.session_id && onViewSession && (
                      <button
                        onClick={() => onViewSession(exec.session_id)}
                        className="text-[11px] text-primary hover:underline ml-auto cursor-pointer"
                      >
                        查看对话 →
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
