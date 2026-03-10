import { useState, useEffect } from "react"
import { Plus, Clock } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Agent, CronJob } from "@/types"
import { useAgentStore } from "@/stores/agentStore"
import CronJobCard from "./CronJobCard"
import CronJobForm from "./CronJobForm"

const EMPTY_CRON_JOBS: CronJob[] = []

interface Props {
  agent: Agent
  open: boolean
  onOpenChange: (open: boolean) => void
}

export default function CronJobPanel({ agent, open, onOpenChange }: Props) {
  const cronJobs = useAgentStore((s) => s.cronJobs[agent.id] ?? EMPTY_CRON_JOBS)
  const fetchCronJobs = useAgentStore((s) => s.fetchCronJobs)
  const selectSession = useAgentStore((s) => s.selectSession)
  const [formOpen, setFormOpen] = useState(false)
  const [editJob, setEditJob] = useState<CronJob | null>(null)

  useEffect(() => {
    if (open) {
      fetchCronJobs(agent.id)
    }
  }, [open, agent.id, fetchCronJobs])

  const handleCreate = () => {
    setEditJob(null)
    setFormOpen(true)
  }

  const handleEdit = (job: CronJob) => {
    setEditJob(job)
    setFormOpen(true)
  }

  const handleViewSession = (sessionId: string) => {
    onOpenChange(false)
    selectSession(agent.id, sessionId)
  }

  const handleFormClose = (open: boolean) => {
    setFormOpen(open)
    if (!open) {
      setEditJob(null)
      // Refresh list after create/edit
      fetchCronJobs(agent.id)
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-[672px] max-h-[80vh] flex flex-col overflow-hidden">
          <DialogHeader>
            <DialogTitle>定时任务 — {agent.name}</DialogTitle>
          </DialogHeader>

          {cronJobs.length === 0 ? (
            /* Empty State */
            <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
              <Clock className="h-12 w-12 mb-3 opacity-40" />
              <p className="text-sm mb-4">暂无定时任务</p>
              <Button size="sm" onClick={handleCreate}>
                <Plus className="h-3.5 w-3.5 mr-1" />
                新建任务
              </Button>
            </div>
          ) : (
            /* Job List */
            <>
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs text-muted-foreground">共 {cronJobs.length} 个任务</span>
                <Button size="sm" onClick={handleCreate}>
                  <Plus className="h-3.5 w-3.5 mr-1" />
                  新建任务
                </Button>
              </div>
              <div className="flex-1 -mx-6 px-6 overflow-y-auto overflow-x-hidden">
                <div className="space-y-2 pb-2">
                  {cronJobs.map((job) => (
                    <CronJobCard
                      key={job.id}
                      job={job}
                      agentId={agent.id}
                      onEdit={handleEdit}
                      onViewSession={handleViewSession}
                    />
                  ))}
                </div>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>

      {/* Create/Edit Form Dialog */}
      <CronJobForm
        agentId={agent.id}
        open={formOpen}
        onOpenChange={handleFormClose}
        editJob={editJob}
      />
    </>
  )
}
