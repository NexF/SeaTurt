import { create } from "zustand"
import { ChatMessage, UIToolCall, ContentBlock, Message } from "@/types"
import * as api from "@/services/api"

interface ChatStore {
  messages: ChatMessage[]
  isStreaming: boolean
  abortController: AbortController | null

  loadHistory: (agentId: string) => Promise<void>
  clearHistory: (agentId: string) => Promise<void>
  sendMessage: (agentId: string, text: string, images?: File[]) => void
  stopStreaming: () => void
  reset: () => void
}

function convertHistoryMessage(msg: Message): ChatMessage | null {
  if (msg.role === "tool") return null

  let text = ""
  let images: { data: string; mime_type: string }[] = []

  if (typeof msg.content === "string") {
    // Backend returns content as plain string
    text = msg.content
  } else if (Array.isArray(msg.content)) {
    // Backend returns content as ContentBlock[]
    const textBlocks = msg.content.filter((b) => b.type === "text")
    const imageBlocks = msg.content.filter((b) => b.type === "image")
    text = textBlocks.map((b) => b.text || "").join("")
    images = imageBlocks
      .filter((b) => b.image)
      .map((b) => ({ data: b.image!.data, mime_type: b.image!.mime_type }))
  }

  return {
    id: msg.id,
    role: msg.role as "user" | "assistant",
    content: text,
    images,
    toolCalls: [],
    isStreaming: false,
  }
}

export const useChatStore = create<ChatStore>((set, get) => ({
  messages: [],
  isStreaming: false,
  abortController: null,

  loadHistory: async (agentId) => {
    // 切换 agent 时先清空旧消息
    set({ messages: [], isStreaming: false })
    try {
      const history = await api.getHistory(agentId)
      const msgs: ChatMessage[] = []
      for (const m of history) {
        const converted = convertHistoryMessage(m)
        if (converted) msgs.push(converted)
      }
      set({ messages: msgs })
    } catch (err) {
      console.warn("Failed to load history:", err)
      set({ messages: [] })
    }
  },

  clearHistory: async (agentId) => {
    await api.deleteHistory(agentId)
    set({ messages: [] })
  },

  sendMessage: (agentId, text, images) => {
    const userMsg: ChatMessage = {
      id: `user_${Date.now()}`,
      role: "user",
      content: text,
      images: [],
      isStreaming: false,
    }

    // Preview uploaded images
    if (images && images.length > 0) {
      userMsg.images = images.map((f) => ({
        data: URL.createObjectURL(f),
        mime_type: f.type,
      }))
    }

    const assistantMsg: ChatMessage = {
      id: `assistant_${Date.now()}`,
      role: "assistant",
      content: "",
      toolCalls: [],
      isStreaming: true,
    }

    set((s) => ({
      messages: [...s.messages, userMsg, assistantMsg],
      isStreaming: true,
    }))

    const controller = api.streamChat(
      agentId,
      { text, images },
      (event) => {
        set((s) => {
          const msgs = [...s.messages]
          const last = { ...msgs[msgs.length - 1] }
          msgs[msgs.length - 1] = last

          switch (event.type) {
            case "text_delta": {
              const delta = event.data as { content: string }
              last.content += delta.content
              break
            }
            case "tool_call": {
              const tc = event.data as { id: string; name: string; arguments: string }
              const toolCalls = [...(last.toolCalls || [])]
              toolCalls.push({
                id: tc.id,
                name: tc.name,
                arguments: tc.arguments,
                isComplete: false,
              })
              last.toolCalls = toolCalls
              break
            }
            case "tool_result": {
              const tr = event.data as {
                tool_call_id: string
                content: ContentBlock[]
                is_error: boolean
              }
              const toolCalls = [...(last.toolCalls || [])]
              const idx = toolCalls.findIndex((t) => t.id === tr.tool_call_id)
              if (idx !== -1) {
                toolCalls[idx] = {
                  ...toolCalls[idx],
                  result: tr.content,
                  isError: tr.is_error,
                  isComplete: true,
                }
              }
              last.toolCalls = toolCalls
              break
            }
            case "error": {
              const err = event.data as { message: string }
              last.content += `\n\n**Error:** ${err.message}`
              break
            }
            case "done": {
              last.isStreaming = false
              break
            }
          }

          return { messages: msgs }
        })
      },
      () => set({ isStreaming: false, abortController: null }),
      (err) => {
        console.error("Stream error:", err)
        set((s) => {
          const msgs = [...s.messages]
          if (msgs.length > 0) {
            const last = { ...msgs[msgs.length - 1] }
            last.content += `\n\n**Error:** ${err.message}`
            last.isStreaming = false
            msgs[msgs.length - 1] = last
          }
          return { messages: msgs, isStreaming: false, abortController: null }
        })
      }
    )

    set({ abortController: controller })
  },

  stopStreaming: () => {
    const { abortController } = get()
    if (abortController) {
      abortController.abort()
      set((s) => {
        const msgs = [...s.messages]
        if (msgs.length > 0) {
          const last = { ...msgs[msgs.length - 1] }
          last.isStreaming = false
          msgs[msgs.length - 1] = last
        }
        return { messages: msgs, isStreaming: false, abortController: null }
      })
    }
  },

  reset: () => set({ messages: [], isStreaming: false, abortController: null }),
}))
