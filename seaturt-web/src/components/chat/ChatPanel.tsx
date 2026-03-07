import { useEffect, useRef, useCallback, useState } from "react"
import { Settings, FolderOpen, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { Agent, AgentStatus } from "@/types"
import { useChatStore } from "@/stores/chatStore"
import MessageBubble from "./MessageBubble"
import InputBar from "./InputBar"
import AgentSettings from "@/components/agent/AgentSettings"
import { cn } from "@/lib/utils"

const statusDot: Record<AgentStatus, string> = {
  running: "bg-green-500",
  stopped: "bg-gray-400",
  created: "bg-gray-400",
  error: "bg-orange-500",
}

interface Props {
  agent: Agent
  onToggleWorkspace: () => void
  workspaceOpen: boolean
}

export default function ChatPanel({ agent, onToggleWorkspace, workspaceOpen }: Props) {
  const messages = useChatStore((s) => s.getMessages(agent.id))
  const isStreaming = useChatStore((s) => s.getIsStreaming(agent.id))
  const loadHistory = useChatStore((s) => s.loadHistory)
  const clearHistory = useChatStore((s) => s.clearHistory)
  const bottomRef = useRef<HTMLDivElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const userScrolledUp = useRef(false)
  const [settingsOpen, setSettingsOpen] = useState(false)

  useEffect(() => {
    loadHistory(agent.id)
  }, [agent.id, loadHistory])

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

  const handleClearHistory = async () => {
    if (confirm("确定要清空对话历史吗？")) {
      await clearHistory(agent.id)
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <header className="flex items-center justify-between px-4 py-3 border-b border-border flex-shrink-0">
        <div className="flex items-center gap-3">
          <span className="font-semibold">{agent.name}</span>
          <span className="text-xs text-muted-foreground">{agent.config.model}</span>
          <span className={cn("w-2 h-2 rounded-full", statusDot[agent.status])} />
        </div>
        <div className="flex items-center gap-1">
          <TooltipProvider delayDuration={300}>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" className="h-8 w-8" onClick={handleClearHistory}>
                  <Trash2 className="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>清空对话</TooltipContent>
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
          <MessageBubble key={msg.id} message={msg} agentId={agent.id} />
        ))}
        <div ref={bottomRef} />
      </div>

      {/* Input */}
      <InputBar agentId={agent.id} disabled={agent.status !== "running"} />

      {/* Settings Dialog */}
      <AgentSettings agent={agent} open={settingsOpen} onOpenChange={setSettingsOpen} />
    </div>
  )
}
