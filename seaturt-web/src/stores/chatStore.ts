import { create } from "zustand"
import { ChatMessage, ChatSegment, UIToolCall, ContentBlock, Message } from "@/types"
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

// Convert a sequence of history Messages into ChatMessages with proper segments.
// The backend stores: user, assistant (with tool_calls), tool, ..., assistant (final).
// We merge consecutive assistant+tool sequences into a single ChatMessage bubble
// to match the streaming behavior.
function convertHistoryMessages(msgs: Message[]): ChatMessage[] {
  const result: ChatMessage[] = []
  let currentAssistant: ChatMessage | null = null

  const flushAssistant = () => {
    if (currentAssistant) {
      result.push(currentAssistant)
      currentAssistant = null
    }
  }

  const ensureAssistant = (msg: Message): ChatMessage => {
    if (!currentAssistant) {
      currentAssistant = {
        id: msg.id,
        role: "assistant",
        content: "",
        toolCalls: [],
        segments: [],
        isStreaming: false,
      }
    }
    return currentAssistant
  }

  for (const msg of msgs) {
    if (msg.role === "user") {
      flushAssistant()

      let text = ""
      let images: { data: string; mime_type: string }[] = []
      if (typeof msg.content === "string") {
        text = msg.content
      } else if (Array.isArray(msg.content)) {
        const textBlocks = msg.content.filter((b) => b.type === "text")
        const imageBlocks = msg.content.filter((b) => b.type === "image")
        text = textBlocks.map((b) => b.text || "").join("")
        images = imageBlocks
          .filter((b) => b.image)
          .map((b) => ({ data: b.image!.data, mime_type: b.image!.mime_type }))
      }
      result.push({
        id: msg.id,
        role: "user",
        content: text,
        images,
        toolCalls: [],
        segments: [],
        isStreaming: false,
      })
    } else if (msg.role === "assistant") {
      // Merge into current assistant bubble (don't flush — keeps multi-iteration in one bubble)
      const assistant = ensureAssistant(msg)

      let text = ""
      if (typeof msg.content === "string") {
        text = msg.content
      } else if (Array.isArray(msg.content)) {
        text = msg.content
          .filter((b) => b.type === "text")
          .map((b) => b.text || "")
          .join("")
      }

      if (text) {
        assistant.content += (assistant.content ? "\n\n" : "") + text
        assistant.segments!.push({ type: "text", text })
      }

      // If this assistant message has tool_calls, add them to segments
      if (msg.tool_calls) {
        try {
          const tcs = JSON.parse(msg.tool_calls) as Array<{
            id: string
            type: string
            function: { name: string; arguments: string }
          }>
          for (const tc of tcs) {
            const uiTc: UIToolCall = {
              id: tc.id,
              name: tc.function.name,
              arguments: tc.function.arguments,
              isComplete: false,
            }
            assistant.toolCalls!.push(uiTc)
            assistant.segments!.push({ type: "tool_call", toolCall: uiTc })
          }
        } catch {}
      }
    } else if (msg.role === "tool") {
      // Match this tool result to the current assistant's toolCalls
      const assistant = currentAssistant as ChatMessage | null
      const toolCalls = assistant?.toolCalls
      if (toolCalls) {
        const tc = toolCalls.find((t: UIToolCall) => t.id === msg.tool_call_id)
        if (tc) {
          let resultContent: ContentBlock[] = []
          if (typeof msg.content === "string") {
            resultContent = [{ type: "text", text: msg.content }]
          } else if (Array.isArray(msg.content)) {
            resultContent = msg.content
          }
          tc.result = resultContent
          tc.isComplete = true
          const firstText = resultContent.find((b) => b.type === "text")?.text || ""
          tc.isError = firstText.startsWith("Error")
        }
      }
    }
  }

  flushAssistant()
  return result
}

export const useChatStore = create<ChatStore>((set, get) => ({
  messages: [],
  isStreaming: false,
  abortController: null,

  loadHistory: async (agentId) => {
    set({ messages: [], isStreaming: false })
    try {
      const history = await api.getHistory(agentId)
      const msgs = convertHistoryMessages(history)
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
      segments: [],
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

          // Clone mutable arrays
          const segments = [...(last.segments || [])]
          const toolCalls = [...(last.toolCalls || [])]

          switch (event.type) {
            case "text_delta": {
              const delta = event.data as { content: string }
              last.content += delta.content

              // Append to the last text segment, or create a new one
              const lastSeg = segments[segments.length - 1]
              if (lastSeg && lastSeg.type === "text") {
                segments[segments.length - 1] = {
                  type: "text",
                  text: lastSeg.text + delta.content,
                }
              } else {
                segments.push({ type: "text", text: delta.content })
              }
              break
            }
            case "tool_call": {
              const tc = event.data as { id: string; name: string; arguments: string }
              const uiTc: UIToolCall = {
                id: tc.id,
                name: tc.name,
                arguments: tc.arguments,
                isComplete: false,
              }
              toolCalls.push(uiTc)
              segments.push({ type: "tool_call", toolCall: uiTc })
              break
            }
            case "tool_result": {
              const tr = event.data as {
                tool_call_id: string
                content: ContentBlock[]
                is_error: boolean
              }
              const idx = toolCalls.findIndex((t) => t.id === tr.tool_call_id)
              if (idx !== -1) {
                toolCalls[idx] = {
                  ...toolCalls[idx],
                  result: tr.content,
                  isError: tr.is_error,
                  isComplete: true,
                }
                // Also update the reference in segments
                const segIdx = segments.findIndex(
                  (seg) => seg.type === "tool_call" && seg.toolCall.id === tr.tool_call_id
                )
                if (segIdx !== -1) {
                  segments[segIdx] = { type: "tool_call", toolCall: toolCalls[idx] }
                }
              }
              break
            }
            case "error": {
              const err = event.data as { message: string }
              const errText = `\n\n**Error:** ${err.message}`
              last.content += errText
              const lastSeg = segments[segments.length - 1]
              if (lastSeg && lastSeg.type === "text") {
                segments[segments.length - 1] = {
                  type: "text",
                  text: lastSeg.text + errText,
                }
              } else {
                segments.push({ type: "text", text: errText })
              }
              break
            }
            case "done": {
              last.isStreaming = false
              break
            }
          }

          last.segments = segments
          last.toolCalls = toolCalls
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
