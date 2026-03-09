import { useState } from "react"
import { Play, Square, Trash2, Copy, Loader2, ChevronRight, ChevronDown } from "lucide-react"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
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
import { Agent, AgentStatus } from "@/types"
import { useAgentStore } from "@/stores/agentStore"
import { cn } from "@/lib/utils"

const statusDot: Record<AgentStatus, string> = {
  running: "bg-green-500",
  stopped: "bg-gray-400",
  created: "bg-gray-400",
  error: "bg-orange-500",
}

const statusLabel: Record<AgentStatus, string> = {
  running: "运行中",
  stopped: "已停止",
  created: "已创建",
  error: "异常",
}

const operationLabel: Record<string, string> = {
  starting: "启动中…",
  stopping: "停止中…",
  deleting: "删除中…",
}

interface Props {
  agent: Agent
  selected: boolean
  expanded: boolean
  onToggle: () => void
}

export default function AgentCard({ agent, selected, expanded, onToggle }: Props) {
  const { startAgent, stopAgent, deleteAgent, operatingAgents } = useAgentStore()
  const [deleteOpen, setDeleteOpen] = useState(false)

  const operation = operatingAgents[agent.id]
  const isOperating = !!operation

  const handleStart = () => {
    if (isOperating) return
    startAgent(agent.id)
  }

  const handleStop = () => {
    if (isOperating) return
    stopAgent(agent.id)
  }

  const handleDelete = () => {
    if (isOperating) return
    deleteAgent(agent.id)
    setDeleteOpen(false)
  }

  const handleCopyId = () => {
    navigator.clipboard.writeText(agent.id)
  }

  const displayStatus = operation
    ? operationLabel[operation]
    : statusLabel[agent.status]

  return (
    <>
      <ContextMenu>
        <ContextMenuTrigger>
          <button
            onClick={onToggle}
            disabled={isOperating}
            className={cn(
              "w-full text-left rounded-lg px-3 py-2.5 transition-colors cursor-pointer",
              "hover:bg-accent/50",
              selected && "bg-accent",
              isOperating && "opacity-60 cursor-not-allowed"
            )}
          >
            <div className="flex items-center gap-2 mb-0.5">
              {expanded ? (
                <ChevronDown className="w-3.5 h-3.5 flex-shrink-0 text-muted-foreground" />
              ) : (
                <ChevronRight className="w-3.5 h-3.5 flex-shrink-0 text-muted-foreground" />
              )}
              {isOperating ? (
                <Loader2 className="w-3 h-3 flex-shrink-0 animate-spin text-muted-foreground" />
              ) : (
                <span
                  className={cn("w-2 h-2 rounded-full flex-shrink-0", statusDot[agent.status])}
                />
              )}
              <span className="font-medium text-sm truncate">{agent.name}</span>
            </div>
            <div className="flex items-center gap-2 pl-4">
              <span className="text-2xs text-muted-foreground truncate">
                {agent.config.model}
              </span>
              <span className="text-2xs text-muted-foreground">·</span>
              <span className={cn(
                "text-2xs text-muted-foreground",
                isOperating && "text-primary"
              )}>
                {displayStatus}
              </span>
            </div>
          </button>
        </ContextMenuTrigger>

        <ContextMenuContent className="w-44">
          {agent.status !== "running" ? (
            <ContextMenuItem onClick={handleStart} disabled={isOperating}>
              {operation === "starting" ? (
                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
              ) : (
                <Play className="h-4 w-4 mr-2" />
              )}
              {operation === "starting" ? "启动中…" : "启动"}
            </ContextMenuItem>
          ) : (
            <ContextMenuItem onClick={handleStop} disabled={isOperating}>
              {operation === "stopping" ? (
                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
              ) : (
                <Square className="h-4 w-4 mr-2" />
              )}
              {operation === "stopping" ? "停止中…" : "停止"}
            </ContextMenuItem>
          )}
          <ContextMenuSeparator />
          <ContextMenuItem onClick={handleCopyId}>
            <Copy className="h-4 w-4 mr-2" />
            复制 ID
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            onClick={() => setDeleteOpen(true)}
            disabled={isOperating}
            className="text-destructive focus:text-destructive"
          >
            {operation === "deleting" ? (
              <Loader2 className="h-4 w-4 mr-2 animate-spin" />
            ) : (
              <Trash2 className="h-4 w-4 mr-2" />
            )}
            {operation === "deleting" ? "删除中…" : "删除"}
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除</AlertDialogTitle>
            <AlertDialogDescription>
              确定要删除 Agent「{agent.name}」吗？此操作不可撤销。
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
