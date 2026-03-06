import { useState, useRef, useCallback } from "react"
import { Send, Paperclip, X, Square } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useChatStore } from "@/stores/chatStore"

interface Props {
  agentId: string
  disabled: boolean
}

export default function InputBar({ agentId, disabled }: Props) {
  const { sendMessage, isStreaming, stopStreaming } = useChatStore()
  const [text, setText] = useState("")
  const [images, setImages] = useState<File[]>([])
  const [previews, setPreviews] = useState<string[]>([])
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const handleSend = useCallback(() => {
    const trimmed = text.trim()
    if (!trimmed && images.length === 0) return
    if (isStreaming || disabled) return

    sendMessage(agentId, trimmed, images.length > 0 ? images : undefined)
    setText("")
    setImages([])
    setPreviews((prev) => {
      prev.forEach(URL.revokeObjectURL)
      return []
    })

    setTimeout(() => textareaRef.current?.focus(), 0)
  }, [text, images, isStreaming, disabled, agentId, sendMessage])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || [])
    addImages(files)
    e.target.value = ""
  }

  const addImages = (files: File[]) => {
    const imageFiles = files.filter((f) => f.type.startsWith("image/"))
    setImages((prev) => [...prev, ...imageFiles])
    setPreviews((prev) => [...prev, ...imageFiles.map((f) => URL.createObjectURL(f))])
  }

  const removeImage = (idx: number) => {
    URL.revokeObjectURL(previews[idx])
    setImages((prev) => prev.filter((_, i) => i !== idx))
    setPreviews((prev) => prev.filter((_, i) => i !== idx))
  }

  const handlePaste = (e: React.ClipboardEvent) => {
    const items = Array.from(e.clipboardData?.items || [])
    const imageItems = items.filter((item) => item.type.startsWith("image/"))
    if (imageItems.length > 0) {
      e.preventDefault()
      const files = imageItems.map((item) => item.getAsFile()).filter(Boolean) as File[]
      addImages(files)
    }
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    const files = Array.from(e.dataTransfer?.files || [])
    addImages(files)
  }

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
  }

  // Auto-resize textarea
  const handleInput = () => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = "auto"
    el.style.height = Math.min(el.scrollHeight, 200) + "px"
  }

  return (
    <div className="border-t border-border px-4 py-3 flex-shrink-0">
      {/* Image previews */}
      {previews.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-2">
          {previews.map((src, i) => (
            <div key={i} className="relative group">
              <img
                src={src}
                alt="preview"
                className="h-16 w-16 object-cover rounded-lg border border-border"
              />
              <button
                onClick={() => removeImage(i)}
                className="absolute -top-1.5 -right-1.5 bg-destructive text-destructive-foreground rounded-full p-0.5 opacity-0 group-hover:opacity-100 transition-opacity cursor-pointer"
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          ))}
        </div>
      )}

      <div
        className="flex items-end gap-2"
        onDrop={handleDrop}
        onDragOver={handleDragOver}
      >
        <Button
          variant="ghost"
          size="icon"
          className="h-9 w-9 flex-shrink-0"
          onClick={() => fileInputRef.current?.click()}
          disabled={disabled}
        >
          <Paperclip className="h-4 w-4" />
        </Button>
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          multiple
          className="hidden"
          onChange={handleFileChange}
        />

        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          onInput={handleInput}
          placeholder={disabled ? "Agent 未运行" : "输入消息... (Enter 发送, Shift+Enter 换行)"}
          disabled={disabled || isStreaming}
          className="flex-1 resize-none bg-secondary rounded-lg px-3 py-2 text-sm outline-none placeholder:text-muted-foreground disabled:opacity-50 min-h-[36px] max-h-[200px]"
          rows={1}
        />

        {isStreaming ? (
          <Button
            variant="destructive"
            size="icon"
            className="h-9 w-9 flex-shrink-0"
            onClick={stopStreaming}
          >
            <Square className="h-4 w-4" />
          </Button>
        ) : (
          <Button
            size="icon"
            className="h-9 w-9 flex-shrink-0"
            onClick={handleSend}
            disabled={disabled || (!text.trim() && images.length === 0)}
          >
            <Send className="h-4 w-4" />
          </Button>
        )}
      </div>
    </div>
  )
}
