import ReactMarkdown, { Components } from "react-markdown"
import remarkGfm from "remark-gfm"
import { useState } from "react"
import { ChatMessage } from "@/types"
import { cn } from "@/lib/utils"
import ToolCallBlock from "./ToolCallBlock"
import { Loader2, ChevronRight, ChevronDown, Brain } from "lucide-react"

// Open all markdown links in a new tab
const markdownComponents: Components = {
  a: ({ href, children, ...props }) => (
    <a href={href} target="_blank" rel="noopener noreferrer" {...props}>
      {children}
    </a>
  ),
}

interface Props {
  message: ChatMessage
  agentId: string
}

export default function MessageBubble({ message, agentId }: Props) {
  const isUser = message.role === "user"
  const hasSegments = message.segments && message.segments.length > 0

  return (
    <div className={cn("flex", isUser ? "justify-end" : "justify-start")}>
      <div
        className={cn(
          "max-w-[80%] rounded-xl px-4 py-3",
          isUser
            ? "bg-secondary text-secondary-foreground"
            : "bg-transparent"
        )}
      >
        {/* User images */}
        {isUser && message.images && message.images.length > 0 && (
          <div className="flex flex-wrap gap-2 mb-2">
            {message.images.map((img, i) => (
              <img
                key={i}
                src={img.data.startsWith("blob:") ? img.data : `data:${img.mime_type};base64,${img.data}`}
                alt="uploaded"
                className="max-w-[300px] max-h-[200px] rounded-lg object-cover cursor-pointer hover:opacity-80 transition-opacity"
                onClick={() => window.open(img.data.startsWith("blob:") ? img.data : `data:${img.mime_type};base64,${img.data}`, "_blank")}
              />
            ))}
          </div>
        )}

        {/* Ordered segments: interleaved text + reasoning + tool calls */}
        {!isUser && hasSegments ? (
          <div className="space-y-2">
            {message.segments!.map((seg, i) => {
              if (seg.type === "reasoning") {
                return <ReasoningBlock key={i} text={seg.text} isStreaming={!!message.isStreaming} />
              }
              if (seg.type === "text") {
                return (
                  <div key={i} className="text-sm prose prose-sm dark:prose-invert max-w-none prose-pre:bg-[hsl(240,23%,9%)] prose-pre:rounded-lg prose-code:text-primary">
                    <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
                      {seg.text}
                    </ReactMarkdown>
                  </div>
                )
              }
              if (seg.type === "tool_call") {
                return <ToolCallBlock key={seg.toolCall.id} toolCall={seg.toolCall} agentId={agentId} />
              }
              return null
            })}
          </div>
        ) : (
          /* Fallback: plain content for user messages or messages without segments */
          message.content && (
            <div className={cn("text-sm", !isUser && "prose prose-sm dark:prose-invert max-w-none prose-pre:bg-[hsl(240,23%,9%)] prose-pre:rounded-lg prose-code:text-primary")}>
              {isUser ? (
                <p className="whitespace-pre-wrap m-0">{message.content}</p>
              ) : (
                <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
                  {message.content}
                </ReactMarkdown>
              )}
            </div>
          )
        )}

        {/* Streaming indicator */}
        {message.isStreaming && !message.content && (!message.segments || message.segments.length === 0) && (
          <div className="flex items-center gap-2 text-muted-foreground">
            <Loader2 className="h-3 w-3 animate-spin" />
            <span className="text-xs">思考中...</span>
          </div>
        )}
      </div>
    </div>
  )
}

// ReasoningBlock renders a collapsible "thinking process" section.
// Auto-expands during streaming, collapses when streaming ends.
function ReasoningBlock({ text, isStreaming }: { text: string; isStreaming: boolean }) {
  const [manualToggle, setManualToggle] = useState<boolean | null>(null)
  const isOpen = manualToggle !== null ? manualToggle : isStreaming

  return (
    <div className="border border-border/50 rounded-lg overflow-hidden">
      <button
        type="button"
        className="flex items-center gap-1.5 w-full px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted/50 transition-colors"
        onClick={() => setManualToggle(isOpen ? false : true)}
      >
        <Brain className="h-3 w-3 shrink-0" />
        <span>思考过程</span>
        {isStreaming && <Loader2 className="h-3 w-3 animate-spin ml-1" />}
        {isOpen ? <ChevronDown className="h-3 w-3 ml-auto" /> : <ChevronRight className="h-3 w-3 ml-auto" />}
      </button>
      {isOpen && (
        <div className="px-3 pb-2 text-xs text-muted-foreground/80 prose prose-sm dark:prose-invert max-w-none border-t border-border/30">
          <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>{text}</ReactMarkdown>
        </div>
      )}
    </div>
  )
}
