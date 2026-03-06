import { useState } from "react"
import { ChevronRight, ChevronDown, Loader2, Check, X, Wrench } from "lucide-react"
import { UIToolCall } from "@/types"
import { cn } from "@/lib/utils"
import { useChatStore } from "@/stores/chatStore"

interface Props {
  toolCall: UIToolCall
  agentId: string
}

export default function ToolCallBlock({ toolCall, agentId }: Props) {
  const [expanded, setExpanded] = useState(false)
  const cancelToolCall = useChatStore((s) => s.cancelToolCall)

  const isRunning = !toolCall.isComplete
  const isError = toolCall.isError

  // Parse arguments for display
  let argsDisplay = toolCall.arguments
  try {
    const parsed = JSON.parse(toolCall.arguments)
    argsDisplay = JSON.stringify(parsed, null, 2)
  } catch {}

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
        isRunning && "border-l-orange-400",
        toolCall.isComplete && !isError && "border-l-green-500",
        isError && "border-l-destructive"
      )}
    >
      {/* Header */}
      <button
        className="flex items-center gap-2 w-full px-3 py-2 text-left hover:bg-accent/30 transition-colors cursor-pointer"
        onClick={() => setExpanded(!expanded)}
      >
        <Wrench className="h-3.5 w-3.5 text-muted-foreground flex-shrink-0" />
        <span className="text-xs font-mono font-medium truncate flex-1">
          {toolCall.name}
        </span>
        <span className="flex-shrink-0">
          {isRunning && <Loader2 className="h-3.5 w-3.5 animate-spin text-orange-400" />}
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
              cancelToolCall(agentId, toolCall.id)
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
              <pre className="text-xs font-mono bg-background rounded p-2 overflow-x-auto max-h-40 overflow-y-auto">
                {argsDisplay}
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
