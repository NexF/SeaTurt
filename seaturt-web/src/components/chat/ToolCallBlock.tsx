import { useEffect, useMemo, useRef, useState } from "react"
import { ChevronRight, ChevronDown, Loader2, Check, X, Wrench } from "lucide-react"
import { UIToolCall } from "@/types"
import { cn } from "@/lib/utils"
import { useChatStore } from "@/stores/chatStore"

interface Props {
  toolCall: UIToolCall
  agentId: string
  sessionId: string
}

export default function ToolCallBlock({ toolCall, agentId, sessionId }: Props) {
  // Track whether user has manually toggled; null = no manual override
  const [manualToggle, setManualToggle] = useState<boolean | null>(null)
  const cancelToolCall = useChatStore((s) => s.cancelToolCall)
  const argsPreRef = useRef<HTMLPreElement>(null)

  const isRunning = !toolCall.isComplete
  const isError = toolCall.isError
  const isStreaming = toolCall.isStreaming

  // Auto-expand while active (streaming args or executing tool), auto-collapse when done.
  // User's manual toggle takes priority until state changes.
  const isActive = isStreaming || isRunning
  const expanded = manualToggle !== null ? manualToggle : isActive

  // Reset manual override when active state changes (e.g. streaming→running→complete)
  useEffect(() => {
    setManualToggle(null)
  }, [isActive])

  // Format arguments: raw text while streaming, formatted JSON when complete
  const argsDisplay = useMemo(() => {
    if (isStreaming) {
      return toolCall.arguments
    }
    try {
      const parsed = JSON.parse(toolCall.arguments)
      return JSON.stringify(parsed, null, 2)
    } catch {
      return toolCall.arguments
    }
  }, [toolCall.arguments, isStreaming])

  // Auto-scroll args area to bottom during streaming
  useEffect(() => {
    if (isStreaming && argsPreRef.current) {
      argsPreRef.current.scrollTop = argsPreRef.current.scrollHeight
    }
  }, [argsDisplay, isStreaming])

  // Get result text
  const resultText = toolCall.result
    ?.map((b) => {
      if (b.type === "text") return b.text
      if (b.type === "image") return "[image]"
      return ""
    })
    .join("\n")

  // Result images
  const resultImages = toolCall.result?.filter((b) => b.type === "image" && b.image) || []

  return (
    <div
      className={cn(
        "rounded-lg border overflow-hidden",
        "bg-popover",
        "border-l-[3px]",
        isStreaming && "border-l-blue-400",
        isRunning && !isStreaming && "border-l-orange-400",
        toolCall.isComplete && !isError && "border-l-green-500",
        isError && "border-l-destructive"
      )}
    >
      {/* Header */}
      <button
        className="flex items-center gap-2 w-full px-3 py-2 text-left hover:bg-accent/30 transition-colors cursor-pointer"
        onClick={() => setManualToggle(!expanded)}
      >
        <Wrench className="h-3.5 w-3.5 text-muted-foreground flex-shrink-0" />
        <span className="text-xs font-mono font-medium truncate flex-1">
          {toolCall.name}
        </span>
        <span className="flex-shrink-0">
          {isStreaming && <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-400" />}
          {isRunning && !isStreaming && <Loader2 className="h-3.5 w-3.5 animate-spin text-orange-400" />}
          {toolCall.isComplete && !isError && (
            <Check className="h-3.5 w-3.5 text-green-500" />
          )}
          {isError && <X className="h-3.5 w-3.5 text-destructive" />}
        </span>
        {isRunning && (
          <button
            className="text-xs text-muted-foreground hover:text-foreground transition-colors px-1"
            onClick={(e) => {
              e.stopPropagation()
              cancelToolCall(agentId, sessionId, toolCall.id)
            }}
          >
            取消
          </button>
        )}
        {expanded ? (
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
        )}
      </button>

      {/* Expanded content */}
      {expanded && (
        <div className="border-t border-border px-3 py-2 space-y-2">
          {argsDisplay && (
            <div>
              <p className="text-2xs text-muted-foreground mb-1">参数</p>
              <pre ref={argsPreRef} className="text-xs font-mono bg-background rounded p-2 overflow-x-auto max-h-40 overflow-y-auto whitespace-pre-wrap break-all">
                {argsDisplay}
                {isStreaming && <span className="animate-pulse text-blue-400">▊</span>}
              </pre>
            </div>
          )}
          {toolCall.isComplete && resultText && (
            <div>
              <p className="text-2xs text-muted-foreground mb-1">结果</p>
              <pre
                className={cn(
                  "text-xs font-mono bg-background rounded p-2 overflow-x-auto max-h-60 overflow-y-auto whitespace-pre-wrap",
                  isError && "text-destructive"
                )}
              >
                {resultText}
              </pre>
            </div>
          )}
          {resultImages.map((img, i) => (
            <img
              key={i}
              src={`data:${img.image!.mime_type};base64,${img.image!.data}`}
              alt="tool result"
              className="max-w-full max-h-60 rounded cursor-pointer"
              onClick={() =>
                window.open(`data:${img.image!.mime_type};base64,${img.image!.data}`, "_blank")
              }
            />
          ))}
        </div>
      )}
    </div>
  )
}
