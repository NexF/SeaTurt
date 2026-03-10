import { useEffect, useRef } from "react"
import { useAgentStore } from "@/stores/agentStore"

const BASE = "/api"

/**
 * useGlobalEvents — Global SSE subscription hook (v0.3.1)
 *
 * Connects to GET /api/events on mount, stays connected for the page lifetime.
 * Handles agent-level events:
 *   - session_created → refreshes the session list for that agent
 *   - session_updated → updates session title in local state
 *   - session_deleted → refreshes session list
 *
 * Should be called once at Layout/App level.
 */
export function useGlobalEvents() {
  const eventSourceRef = useRef<EventSource | null>(null)

  useEffect(() => {
    const url = `${BASE}/events`
    console.log("[useGlobalEvents] connecting to", url)
    const es = new EventSource(url)
    eventSourceRef.current = es

    es.onopen = () => {
      console.log("[useGlobalEvents] SSE connection opened")
    }

    es.onmessage = (e) => {
      console.log("[useGlobalEvents] raw SSE data:", e.data)
      try {
        const event = JSON.parse(e.data)
        console.log("[useGlobalEvents] parsed event:", event.type, event)

        // Skip the initial "connected" handshake event
        if (event.type === "connected") {
          console.log("[useGlobalEvents] handshake received")
          return
        }

        const agentId = event.agent_id
        if (!agentId) {
          console.warn("[useGlobalEvents] event missing agent_id:", event)
          return
        }

        switch (event.type) {
          case "session_created": {
            const data = event.data as { session_id?: string; title?: string }
            const existingSessions = useAgentStore.getState().sessions[agentId] || []
            // Skip if session already exists locally (e.g. created by the current tab via REST API)
            if (data?.session_id && existingSessions.some((s) => s.id === data.session_id)) {
              console.log("[useGlobalEvents] session_created skipped (already exists locally)", data.session_id)
              break
            }
            console.log("[useGlobalEvents] session_created → fetchSessions", agentId)
            useAgentStore.getState().fetchSessions(agentId)
            break
          }
          case "session_updated": {
            const data = event.data as { session_id?: string; title?: string }
            console.log("[useGlobalEvents] session_updated:", data)
            if (data?.session_id && data?.title) {
              useAgentStore.setState((s) => {
                const newSessions = { ...s.sessions }
                for (const aid of Object.keys(newSessions)) {
                  const list = newSessions[aid]
                  if (list?.some((sess) => sess.id === data.session_id)) {
                    newSessions[aid] = list.map((sess) =>
                      sess.id === data.session_id ? { ...sess, title: data.title! } : sess
                    )
                    break
                  }
                }
                return { sessions: newSessions }
              })
            }
            break
          }
          case "session_deleted": {
            console.log("[useGlobalEvents] session_deleted → fetchSessions", agentId)
            useAgentStore.getState().fetchSessions(agentId)
            break
          }
          // cron_execution_started / cron_execution_finished can be handled here if needed
        }
      } catch (err) {
        console.error("[useGlobalEvents] parse error:", err, "raw:", e.data)
      }
    }

    es.onerror = (err) => {
      console.error("[useGlobalEvents] SSE error, readyState:", es.readyState, err)
      // EventSource auto-reconnects on error, no action needed
    }

    return () => {
      console.log("[useGlobalEvents] closing SSE connection")
      es.close()
      eventSourceRef.current = null
    }
  }, [])
}
