import { useEffect, useRef } from "react"
import { useChatStore } from "@/stores/chatStore"

const BASE = "/api"

/**
 * useSessionEvents — Session-level SSE subscription hook
 *
 * Connects to GET /api/agents/:id/sessions/:sid/events on mount.
 * This is the SOLE channel for receiving all streaming events (user chat,
 * cron-triggered executions, etc.). POST /chat only triggers the agent loop
 * and returns immediately — it does not stream events itself.
 *
 * On connect, the server first sends a "snapshot" with all accumulated
 * events from the current turn, then sends incremental events.
 */
export function useSessionEvents(agentId: string, sessionId: string) {
  const eventSourceRef = useRef<EventSource | null>(null)
  // Track whether we've already connected for this session to avoid duplicate connections
  const connectedSessionRef = useRef<string | null>(null)

  useEffect(() => {
    if (!agentId || !sessionId) return
    // Prevent duplicate connection for the same session
    if (connectedSessionRef.current === sessionId && eventSourceRef.current) return

    // Close any existing connection
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
    }

    const url = `${BASE}/agents/${agentId}/sessions/${sessionId}/events`
    const es = new EventSource(url)
    eventSourceRef.current = es
    connectedSessionRef.current = sessionId

    const processEvent = (event: { type: string; data: unknown }) => {
      useChatStore.getState().handleStreamEvent(sessionId, event)
    }

    es.onmessage = (e) => {
      try {
        const parsed = JSON.parse(e.data)

        // Skip "connected" handshake
        if (parsed.type === "connected") return

        // Handle snapshot (batch of accumulated events from current turn)
        if (parsed.type === "snapshot" && Array.isArray(parsed.events)) {
          for (const event of parsed.events) {
            processEvent(event)
          }
          return
        }

        // Regular incremental event
        processEvent(parsed)
      } catch {
        // Ignore parse errors
      }
    }

    es.onerror = () => {
      // EventSource auto-reconnects on error
    }

    return () => {
      es.close()
      eventSourceRef.current = null
      connectedSessionRef.current = null
    }
  }, [agentId, sessionId])
}
