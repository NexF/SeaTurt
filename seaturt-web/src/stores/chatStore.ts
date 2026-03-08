import { create } from "zustand"
import { ChatMessage, UIToolCall, ContentBlock, Message } from "@/types"
import * as api from "@/services/api"

interface PerAgentState {
  messages: ChatMessage[]
  isStreaming: boolean
  abortController: AbortController | null
}

interface ChatStore {
  /** Per-agent chat state keyed by agent ID */
  agentStates: Record<string, PerAgentState>

  /** Helpers to read per-agent state */
  getMessages: (agentId: string) => ChatMessage[]
  getIsStreaming: (agentId: string) => boolean

  loadHistory: (agentId: string) => Promise<void>
  clearHistory: (agentId: string) => Promise<void>
  sendMessage: (agentId: string, text: string, images?: File[]) => void
  stopStreaming: (agentId: string) => void
  cancelToolCall: (agentId: string, toolCallId: string) => void
  reset: () => void
}

const emptyState: PerAgentState = { messages: [], isStreaming: false, abortController: null }

function getAgentState(states: Record<string, PerAgentState>, agentId: string): PerAgentState {
  return states[agentId] || emptyState
}

function setAgentState(
  states: Record<string, PerAgentState>,
  agentId: string,
  patch: Partial<PerAgentState>
): Record<string, PerAgentState> {
  const prev = states[agentId] || { ...emptyState }
  return { ...states, [agentId]: { ...prev, ...patch } }
}

// Convert a sequence of history Messages into ChatMessages with proper segments.
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
      const assistant = ensureAssistant(msg)

      if (msg.reasoning_content) {
        assistant.reasoningContent = (assistant.reasoningContent || "") + msg.reasoning_content
        assistant.segments!.push({ type: "reasoning", text: msg.reasoning_content })
      }

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
      const cur = currentAssistant as ChatMessage | null
      if (cur && cur.toolCalls) {
        const tc = cur.toolCalls.find((t: UIToolCall) => t.id === msg.tool_call_id)
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
  agentStates: {},

  getMessages: (agentId) => getAgentState(get().agentStates, agentId).messages,
  getIsStreaming: (agentId) => getAgentState(get().agentStates, agentId).isStreaming,

  loadHistory: async (agentId) => {
    const state = getAgentState(get().agentStates, agentId)
    // If already streaming for this agent, don't reload — keep current state
    if (state.isStreaming) return

    // If already has messages loaded, don't re-fetch (component just re-showed)
    if (state.messages.length > 0) return

    set((s) => ({ agentStates: setAgentState(s.agentStates, agentId, { messages: [] }) }))
    try {
      const history = await api.getHistory(agentId)
      const msgs = convertHistoryMessages(history)
      set((s) => ({ agentStates: setAgentState(s.agentStates, agentId, { messages: msgs }) }))
    } catch (err) {
      console.warn("Failed to load history:", err)
      set((s) => ({ agentStates: setAgentState(s.agentStates, agentId, { messages: [] }) }))
    }
  },

  clearHistory: async (agentId) => {
    await api.deleteHistory(agentId)
    set((s) => ({
      agentStates: setAgentState(s.agentStates, agentId, { messages: [] }),
    }))
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

    set((s) => {
      const prev = getAgentState(s.agentStates, agentId)
      return {
        agentStates: setAgentState(s.agentStates, agentId, {
          messages: [...prev.messages, userMsg, assistantMsg],
          isStreaming: true,
        }),
      }
    })

    const controller = api.streamChat(
      agentId,
      { text, images },
      (event) => {
        set((s) => {
          const prev = getAgentState(s.agentStates, agentId)
          const msgs = [...prev.messages]
          const last = { ...msgs[msgs.length - 1] }
          msgs[msgs.length - 1] = last

          const segments = [...(last.segments || [])]
          const toolCalls = [...(last.toolCalls || [])]

          switch (event.type) {
            case "reasoning_delta": {
              const delta = event.data as { content: string }
              last.reasoningContent = (last.reasoningContent || "") + delta.content
              const lastSeg = segments[segments.length - 1]
              if (lastSeg && lastSeg.type === "reasoning") {
                segments[segments.length - 1] = {
                  type: "reasoning",
                  text: lastSeg.text + delta.content,
                }
              } else {
                segments.push({ type: "reasoning", text: delta.content })
              }
              break
            }
            case "text_delta": {
              const delta = event.data as { content: string }
              last.content += delta.content
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
            case "tool_call_delta": {
              const delta = event.data as { index: number; id?: string; name?: string; arguments?: string }
              if (delta.id && delta.name) {
                // New tool call starts — create card
                const tc: UIToolCall = {
                  id: delta.id,
                  name: delta.name,
                  arguments: delta.arguments ?? "",
                  isComplete: false,
                  isStreaming: true,
                }
                toolCalls.push(tc)
                segments.push({ type: "tool_call", toolCall: tc })
              } else {
                // Append arguments to the last streaming tool call
                const lastIdx = toolCalls.length - 1
                if (lastIdx >= 0 && toolCalls[lastIdx].isStreaming) {
                  const updated = {
                    ...toolCalls[lastIdx],
                    arguments: toolCalls[lastIdx].arguments + (delta.arguments ?? ""),
                  }
                  toolCalls[lastIdx] = updated
                  const segIdx = segments.findIndex(
                    (seg) => seg.type === "tool_call" && seg.toolCall?.id === updated.id
                  )
                  if (segIdx !== -1) {
                    segments[segIdx] = { type: "tool_call", toolCall: updated }
                  }
                }
              }
              break
            }
            case "tool_call": {
              const tc = event.data as { id: string; name: string; arguments: string }
              // tool_call event is the "complete confirmation" after all deltas
              const existingIdx = toolCalls.findIndex((t) => t && t.id === tc.id)
              if (existingIdx !== -1) {
                // Update with final complete arguments and mark streaming done
                toolCalls[existingIdx] = {
                  ...toolCalls[existingIdx],
                  arguments: tc.arguments,
                  isStreaming: false,
                }
                const segIdx = segments.findIndex(
                  (seg) => seg.type === "tool_call" && seg.toolCall.id === tc.id
                )
                if (segIdx !== -1) {
                  segments[segIdx] = { type: "tool_call", toolCall: toolCalls[existingIdx] }
                }
              } else {
                // Fallback: no delta was received (non-streaming LLM or missed deltas)
                const uiTc: UIToolCall = {
                  id: tc.id,
                  name: tc.name,
                  arguments: tc.arguments,
                  isComplete: false,
                  isStreaming: false,
                }
                toolCalls.push(uiTc)
                segments.push({ type: "tool_call", toolCall: uiTc })
              }
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
            case "cancelled": {
              for (let i = 0; i < toolCalls.length; i++) {
                if (!toolCalls[i].isComplete) {
                  toolCalls[i] = {
                    ...toolCalls[i],
                    isComplete: true,
                    isError: true,
                    result: [{ type: "text", text: "已取消" }],
                  }
                }
              }
              for (let i = 0; i < segments.length; i++) {
                const seg = segments[i]
                if (seg.type === "tool_call" && !seg.toolCall.isComplete) {
                  const updated = toolCalls.find(tc => tc.id === seg.toolCall.id)
                  if (updated) {
                    segments[i] = { type: "tool_call", toolCall: updated }
                  }
                }
              }
              last.isStreaming = false
              break
            }
          }

          last.segments = segments
          last.toolCalls = toolCalls
          return { agentStates: setAgentState(s.agentStates, agentId, { ...prev, messages: msgs }) }
        })
      },
      () => {
        set((s) => ({
          agentStates: setAgentState(s.agentStates, agentId, { isStreaming: false, abortController: null }),
        }))
      },
      (err) => {
        console.error("Stream error:", err)
        set((s) => {
          const prev = getAgentState(s.agentStates, agentId)
          const msgs = [...prev.messages]
          if (msgs.length > 0) {
            const last = { ...msgs[msgs.length - 1] }
            last.content += `\n\n**Error:** ${err.message}`
            last.isStreaming = false
            msgs[msgs.length - 1] = last
          }
          return {
            agentStates: setAgentState(s.agentStates, agentId, {
              messages: msgs,
              isStreaming: false,
              abortController: null,
            }),
          }
        })
      }
    )

    set((s) => ({
      agentStates: setAgentState(s.agentStates, agentId, { abortController: controller }),
    }))
  },

  stopStreaming: (agentId) => {
    const state = getAgentState(get().agentStates, agentId)
    if (!state.abortController) return

    api.cancelChat(agentId).catch(() => {})
    state.abortController.abort()

    set((s) => {
      const prev = getAgentState(s.agentStates, agentId)
      const msgs = [...prev.messages]
      if (msgs.length > 0) {
        const last = { ...msgs[msgs.length - 1] }
        last.isStreaming = false
        if (last.toolCalls?.length) {
          last.toolCalls = last.toolCalls.map(tc =>
            tc.isComplete ? tc : { ...tc, isComplete: true, isError: true, result: [{ type: "text", text: "已取消" }] }
          )
        }
        if (last.segments?.length) {
          last.segments = last.segments.map(seg =>
            seg.type === "tool_call" && !seg.toolCall.isComplete
              ? { type: "tool_call" as const, toolCall: { ...seg.toolCall, isComplete: true, isError: true, result: [{ type: "text" as const, text: "已取消" }] } }
              : seg
          )
        }
        msgs[msgs.length - 1] = last
      }
      return {
        agentStates: setAgentState(s.agentStates, agentId, {
          messages: msgs,
          isStreaming: false,
          abortController: null,
        }),
      }
    })
  },

  cancelToolCall: (agentId, toolCallId) => {
    api.cancelToolCall(agentId, toolCallId).catch(() => {})

    set((s) => {
      const prev = getAgentState(s.agentStates, agentId)
      const msgs = [...prev.messages]
      const last = { ...msgs[msgs.length - 1] }
      if (last.toolCalls) {
        last.toolCalls = last.toolCalls.map(tc =>
          tc.id === toolCallId && !tc.isComplete
            ? { ...tc, isComplete: true, isError: true, result: [{ type: "text", text: "用户取消了此工具调用" }] }
            : tc
        )
      }
      if (last.segments) {
        last.segments = last.segments.map(seg =>
          seg.type === "tool_call" && seg.toolCall.id === toolCallId && !seg.toolCall.isComplete
            ? { type: "tool_call" as const, toolCall: { ...seg.toolCall, isComplete: true, isError: true, result: [{ type: "text" as const, text: "用户取消了此工具调用" }] } }
            : seg
        )
      }
      msgs[msgs.length - 1] = last
      return { agentStates: setAgentState(s.agentStates, agentId, { ...prev, messages: msgs }) }
    })
  },

  reset: () => set({ agentStates: {} }),
}))
