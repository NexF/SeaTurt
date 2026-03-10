import { useEffect, useRef, useCallback, useState } from "react"
import { Settings, FolderOpen, Trash2, Plus, Pencil, Check, X, Clock } from "lucide-react"
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
import { Agent, AgentStatus, Session } from "@/types"
import { useChatStore } from "@/stores/chatStore"
import { useAgentStore } from "@/stores/agentStore"
import MessageBubble from "./MessageBubble"
import InputBar from "./InputBar"
import AgentSettings from "@/components/agent/AgentSettings"
import CronJobPanel from "@/components/cron/CronJobPanel"
import { useSessionEvents } from "@/hooks/useSessionEvents"
import { cn } from "@/lib/utils"

const EMPTY_SESSIONS: Session[] = []

const statusDot: Record<AgentStatus, string> = {
  running: "bg-green-500",
  stopped: "bg-gray-400",
  created: "bg-gray-400",
  error: "bg-orange-500",
}

interface Props {
  agent: Agent
  sessionId: string
  onToggleWorkspace: () => void
  workspaceOpen: boolean
}

export default function ChatPanel({ agent, sessionId, onToggleWorkspace, workspaceOpen }: Props) {
  const messages = useChatStore((s) => s.getMessages(sessionId))
  const isStreaming = useChatStore((s) => s.getIsStreaming(sessionId))
  const loadHistory = useChatStore((s) => s.loadHistory)
  const clearHistory = useChatStore((s) => s.clearHistory)
  const bottomRef = useRef<HTMLDivElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const userScrolledUp = useRef(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [cronPanelOpen, setCronPanelOpen] = useState(false)

  // Session-level SSE subscription — receives cron / multi-tab events (v0.3.1)
  useSessionEvents(agent.id, sessionId)

  // Session title editing
  const sessions = useAgentStore((s) => s.sessions[agent.id] ?? EMPTY_SESSIONS)
  const renameSession = useAgentStore((s) => s.renameSession)
  const createSession = useAgentStore((s) => s.createSession)
  const currentSession = sessions.find((s) => s.id === sessionId)
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState("")
  const titleInputRef = useRef<HTMLInputElement>(null)

  const handleStartEdit = () => {
    setTitleDraft(currentSession?.title || "")
    setEditingTitle(true)
    setTimeout(() => titleInputRef.current?.focus(), 0)
  }

  const handleSaveTitle = async () => {
    const trimmed = titleDraft.trim()
    if (trimmed && trimmed !== currentSession?.title) {
      await renameSession(agent.id, sessionId, trimmed)
    }
    setEditingTitle(false)
  }

  const handleCancelEdit = () => {
    setEditingTitle(false)
  }

  const [clearOpen, setClearOpen] = useState(false)

  const handleClearHistory = async () => {
    await clearHistory(agent.id, sessionId)
    setClearOpen(false)
  }

  useEffect(() => {
    if (sessionId) {
      loadHistory(agent.id, sessionId)
    }
  }, [agent.id, sessionId, loadHistory])

  // Auto scroll
  useEffect(() => {
    if (!userScrolledUp.current) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" })
    }
  }, [messages])

  const handleScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 50
    userScrolledUp.current = !atBottom
  }, [])

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <header className="border-b border-border flex-shrink-0">
        {/* Row 1: Agent info */}
        <div className="flex items-center justify-between px-4 py-2">
          <div className="flex items-center gap-3">
            <span className="font-semibold">{agent.name}</span>
            <span className="text-xs text-muted-foreground">{agent.config.model}</span>
            <span className={cn("w-2 h-2 rounded-full", statusDot[agent.status])} />
          </div>
          <div className="flex items-center gap-1">
            <TooltipProvider delayDuration={300}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => setCronPanelOpen(true)}>
                    <Clock className="h-4 w-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>定时任务</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => setSettingsOpen(true)}>
                    <Settings className="h-4 w-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>设置</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    className={cn("h-8 w-8", workspaceOpen && "bg-accent")}
                    onClick={onToggleWorkspace}
                  >
                    <FolderOpen className="h-4 w-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>Workspace</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>
        </div>
        {/* Row 2: Session title + new session */}
        <div className="flex items-center justify-between px-4 py-1.5 bg-muted/30">
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <span className="text-sm text-muted-foreground flex-shrink-0">📝</span>
            {editingTitle ? (
              <div className="flex items-center gap-1 min-w-0 flex-1">
                <input
                  ref={titleInputRef}
                  value={titleDraft}
                  onChange={(e) => setTitleDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") handleSaveTitle()
                    if (e.key === "Escape") handleCancelEdit()
                  }}
                  onBlur={handleSaveTitle}
                  className="text-sm bg-background border border-border rounded px-2 py-0.5 min-w-0 flex-1 outline-none focus:ring-1 focus:ring-ring"
                  maxLength={50}
                />
                <Button variant="ghost" size="icon" className="h-6 w-6 flex-shrink-0" onClick={handleSaveTitle}>
                  <Check className="h-3.5 w-3.5" />
                </Button>
                <Button variant="ghost" size="icon" className="h-6 w-6 flex-shrink-0" onClick={handleCancelEdit}>
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : (
              <button
                onClick={handleStartEdit}
                className="flex items-center gap-1.5 min-w-0 group cursor-pointer"
              >
                <span className="text-sm truncate">{currentSession?.title || "新对话"}</span>
                <Pencil className="h-3 w-3 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity flex-shrink-0" />
              </button>
            )}
          </div>
          <div className="flex items-center gap-1 flex-shrink-0">
            <TooltipProvider delayDuration={300}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-muted-foreground"
                    onClick={() => setClearOpen(true)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>清空对话</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 gap-1 text-xs text-muted-foreground flex-shrink-0"
                    onClick={() => createSession(agent.id)}
                  >
                    <Plus className="h-3.5 w-3.5" />
                    新对话
                  </Button>
                </TooltipTrigger>
                <TooltipContent>创建新对话</TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>
        </div>
      </header>

      {/* Messages area */}
      <div
        className="flex-1 overflow-y-auto px-4 py-4 space-y-3"
        onScroll={handleScroll}
        ref={scrollRef}
      >
        {messages.length === 0 && (
          <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
            发送消息开始对话
          </div>
        )}
        {messages.map((msg) => (
          <MessageBubble key={msg.id} message={msg} agentId={agent.id} sessionId={sessionId} />
        ))}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <InputBar agentId={agent.id} sessionId={sessionId} disabled={agent.status !== "running"} />

      {/* Settings Dialog */}
      <AgentSettings agent={agent} open={settingsOpen} onOpenChange={setSettingsOpen} />

      {/* CronJob Panel */}
      <CronJobPanel agent={agent} open={cronPanelOpen} onOpenChange={setCronPanelOpen} />

      {/* Clear History Confirmation */}
      <AlertDialog open={clearOpen} onOpenChange={setClearOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认清空</AlertDialogTitle>
            <AlertDialogDescription>
              确定要清空当前对话的所有消息吗？此操作不可撤销。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={handleClearHistory} className="bg-destructive text-destructive-foreground">
              清空
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
