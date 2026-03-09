import { create } from "zustand"
import { ChatMessage, UIToolCall, ContentBlock, Message } from "@/types"
import { useAgentStore } from "@/stores/agentStore"
import * as api from "@/services/api"

interface PerSessionState {
  messages: ChatMessage[]
  isStreaming: boolean
  abortController: AbortController | null
}

interface ChatStore {
  sessionStates: Record<string, PerSessionState>

  getMessages: (sessionId: string) => ChatMessage[]
  getIsStreaming: (sessionId: string) => boolean

  loadHistory: (agentId: string, sessionId: string) => Promise<void>
  clearHistory: (agentId: string, sessionId: string) => Promise<void>
  sendMessage: (agentId: string, sessionId: string, text: string, images?: File[]) => void
  stopStreaming: (agentId: string, sessionId: string) => void
  cancelToolCall: (agentId: string, sessionId: string, toolCallId: string) => void
  reset: () => void
}

const emptyState: PerSessionState = { messages: [], isStreaming: false, abortController: null }

function getSessionState(states: Record<string, PerSessionState>, sessionId: string): PerSessionState {
  return states[sessionId] || emptyState
}

function setSessionState(
  states: Record<string, PerSessionState>,
  sessionId: string,
  patch: Partial<PerSessionState>
): Record<string, PerSessionState> {
  const prev = states[sessionId] || { ...emptyState }
  return { ...states, [sessionId]: { ...prev, ...patch } }
}

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
  sessionStates: {},

  getMessages: (sessionId) => getSessionState(get().sessionStates, sessionId).messages,
  getIsStreaming: (sessionId) => getSessionState(get().sessionStates, sessionId).isStreaming,

  loadHistory: async (agentId, sessionId) => {
    const state = getSessionState(get().sessionStates, sessionId)
    if (state.isStreaming) return
    if (state.messages.length > 0) return

    set((s) => ({ sessionStates: setSessionState(s.sessionStates, sessionId, { messages: [] }) }))
    try {
      const history = await api.getHistory(agentId, sessionId)
      const msgs = convertHistoryMessages(history)
      set((s) => ({ sessionStates: setSessionState(s.sessionStates, sessionId, { messages: msgs }) }))
    } catch (err) {
      console.warn("Failed to load history:", err)
      set((s) => ({ sessionStates: setSessionState(s.sessionStates, sessionId, { messages: [] }) }))
    }
  },

  clearHistory: async (agentId, sessionId) => {
    await api.deleteHistory(agentId, sessionId)
    set((s) => ({
      sessionStates: setSessionState(s.sessionStates, sessionId, { messages: [] }),
    }))
  },

  sendMessage: (agentId, sessionId, text, images) => {
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
      const prev = getSessionState(s.sessionStates, sessionId)
      return {
        sessionStates: setSessionState(s.sessionStates, sessionId, {
          messages: [...prev.messages, userMsg, assistantMsg],
          isStreaming: true,
        }),
      }
    })

    const controller = api.streamChat(
      agentId,
      sessionId,
      { text, images },
      (event) => {
        set((s) => {
          const prev = getSessionState(s.sessionStates, sessionId)
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
              const existingIdx = toolCalls.findIndex((t) => t && t.id === tc.id)
              if (existingIdx !== -1) {
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
            case "session_updated": {
              const update = event.data as { session_id: string; title: string }
              if (update.session_id && update.title) {
                // Directly update local state — backend already persisted the title
                useAgentStore.setState((s) => {
                  const newSessions = { ...s.sessions }
                  for (const aid of Object.keys(newSessions)) {
                    const list = newSessions[aid]
                    if (list?.some((sess) => sess.id === update.session_id)) {
                      newSessions[aid] = list.map((sess) =>
                        sess.id === update.session_id ? { ...sess, title: update.title } : sess
                      )
                      break
                    }
                  }
                  return { sessions: newSessions }
                })
              }
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
          return { sessionStates: setSessionState(s.sessionStates, sessionId, { ...prev, messages: msgs }) }
        })
      },
      () => {
        set((s) => ({
          sessionStates: setSessionState(s.sessionStates, sessionId, { isStreaming: false, abortController: null }),
        }))
      },
      (err) => {
        console.error("Stream error:", err)
        set((s) => {
          const prev = getSessionState(s.sessionStates, sessionId)
          const msgs = [...prev.messages]
          if (msgs.length > 0) {
            const last = { ...msgs[msgs.length - 1] }
            last.content += `\n\n**Error:** ${err.message}`
            last.isStreaming = false
            msgs[msgs.length - 1] = last
          }
          return {
            sessionStates: setSessionState(s.sessionStates, sessionId, {
              messages: msgs,
              isStreaming: false,
              abortController: null,
            }),
          }
        })
      }
    )

    set((s) => ({
      sessionStates: setSessionState(s.sessionStates, sessionId, { abortController: controller }),
    }))
  },

  stopStreaming: (agentId, sessionId) => {
    const state = getSessionState(get().sessionStates, sessionId)
    if (!state.abortController) return

    api.cancelChat(agentId, sessionId).catch(() => {})
    state.abortController.abort()

    set((s) => {
      const prev = getSessionState(s.sessionStates, sessionId)
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
        sessionStates: setSessionState(s.sessionStates, sessionId, {
          messages: msgs,
          isStreaming: false,
          abortController: null,
        }),
      }
    })
  },

  cancelToolCall: (agentId, sessionId, toolCallId) => {
    api.cancelToolCall(agentId, sessionId, toolCallId).catch(() => {})

    set((s) => {
      const prev = getSessionState(s.sessionStates, sessionId)
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
      return { sessionStates: setSessionState(s.sessionStates, sessionId, { ...prev, messages: msgs }) }
    })
  },

  reset: () => set({ sessionStates: {} }),
}))
