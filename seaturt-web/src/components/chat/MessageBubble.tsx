import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import { ChatMessage } from "@/types"
import { cn } from "@/lib/utils"
import ToolCallBlock from "./ToolCallBlock"
import { Loader2 } from "lucide-react"

interface Props {
  message: ChatMessage
}

export default function MessageBubble({ message }: Props) {
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

        {/* Ordered segments: interleaved text + tool calls */}
        {!isUser && hasSegments ? (
          <div className="space-y-2">
            {message.segments!.map((seg, i) => {
              if (seg.type === "text") {
                return (
                  <div key={i} className="text-sm prose prose-sm dark:prose-invert max-w-none prose-pre:bg-[hsl(240,23%,9%)] prose-pre:rounded-lg prose-code:text-primary">
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>
                      {seg.text}
                    </ReactMarkdown>
                  </div>
                )
              }
              if (seg.type === "tool_call") {
                return <ToolCallBlock key={seg.toolCall.id} toolCall={seg.toolCall} />
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
                <ReactMarkdown remarkPlugins={[remarkGfm]}>
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
