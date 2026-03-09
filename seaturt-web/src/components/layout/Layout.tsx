import { useState, useEffect, useRef } from "react"
import Sidebar from "./Sidebar"
import { useAgentStore } from "@/stores/agentStore"
import ChatPanel from "@/components/chat/ChatPanel"
import WorkspacePanel from "@/components/workspace/WorkspacePanel"
import WelcomePage from "@/components/WelcomePage"
import { Menu, X } from "lucide-react"
import { Button } from "@/components/ui/button"

function useBreakpoint() {
  const [bp, setBp] = useState<"mobile" | "tablet" | "desktop">("desktop")
  useEffect(() => {
    const check = () => {
      const w = window.innerWidth
      if (w < 768) setBp("mobile")
      else if (w < 1200) setBp("tablet")
      else setBp("desktop")
    }
    check()
    window.addEventListener("resize", check)
    return () => window.removeEventListener("resize", check)
  }, [])
  return bp
}

export default function Layout() {
  const selectedAgentId = useAgentStore((s) => s.selectedAgentId)
  const selectedSessionId = useAgentStore((s) => s.selectedSessionId)
  const agents = useAgentStore((s) => s.agents)
  const [rightPanelOpen, setRightPanelOpen] = useState(true)
  const [leftDrawerOpen, setLeftDrawerOpen] = useState(false)

  // Track all session IDs that have been visited so their ChatPanel instances stay alive
  const visitedSessionsRef = useRef<Map<string, { agentId: string; sessionId: string }>>(new Map())
  if (selectedAgentId && selectedSessionId) {
    visitedSessionsRef.current.set(selectedSessionId, { agentId: selectedAgentId, sessionId: selectedSessionId })
  }
  // Clean up visited sessions for agents that no longer exist
  const agentIdSet = new Set(agents.map((a) => a.id))
  for (const [sid, entry] of visitedSessionsRef.current) {
    if (!agentIdSet.has(entry.agentId)) visitedSessionsRef.current.delete(sid)
  }
  const visitedSessions = Array.from(visitedSessionsRef.current.values())

  const bp = useBreakpoint()
  const selectedAgent = agents.find((a) => a.id === selectedAgentId)

  // Close left drawer when agent is selected on mobile
  useEffect(() => {
    if (selectedAgentId && bp === "mobile") setLeftDrawerOpen(false)
  }, [selectedAgentId, bp])

  const showRightPanel = selectedAgent && rightPanelOpen && bp === "desktop"

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Left sidebar: always visible on tablet+, drawer on mobile */}
      {bp !== "mobile" ? (
        <Sidebar />
      ) : (
        <>
          {/* Mobile drawer overlay */}
          {leftDrawerOpen && (
            <div
              className="fixed inset-0 bg-black/40 z-40"
              onClick={() => setLeftDrawerOpen(false)}
            />
          )}
          <div
            className={`fixed inset-y-0 left-0 z-50 w-60 bg-sidebar border-r border-border transform transition-transform duration-200 ${
              leftDrawerOpen ? "translate-x-0" : "-translate-x-full"
            }`}
          >
            <Sidebar />
          </div>
        </>
      )}

      <main className="flex-1 flex flex-col min-w-0">
        {/* Mobile top bar */}
        {bp === "mobile" && (
          <div className="flex items-center px-3 py-2 border-b border-border flex-shrink-0">
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8 mr-2"
              onClick={() => setLeftDrawerOpen(true)}
            >
              <Menu className="h-4 w-4" />
            </Button>
            <span className="font-semibold text-sm truncate">
              {selectedAgent?.name || "SeaTurt"}
            </span>
          </div>
        )}

        {/* Keep-alive: render all visited ChatPanels, hide non-active ones */}
        {visitedSessions.map(({ agentId, sessionId }) => {
          const agent = agents.find((a) => a.id === agentId)
          if (!agent) return null
          const isActive = sessionId === selectedSessionId
          return (
            <div
              key={sessionId}
              className="flex flex-col h-full"
              style={{ display: isActive ? "flex" : "none" }}
            >
              <ChatPanel
                agent={agent}
                sessionId={sessionId}
                onToggleWorkspace={() => setRightPanelOpen((v) => !v)}
                workspaceOpen={rightPanelOpen}
              />
            </div>
          )
        })}

        {/* Show welcome page only when no session is selected */}
        {!selectedSessionId && <WelcomePage />}
      </main>

      {/* Right panel: always on desktop, floating on tablet, hidden on mobile (accessible via toggle) */}
      {showRightPanel && (
        <aside className="w-80 border-l border-border flex-shrink-0 flex flex-col bg-sidebar">
          <WorkspacePanel key={selectedAgent.id} agent={selectedAgent} />
        </aside>
      )}

      {/* Tablet: floating right panel */}
      {selectedAgent && rightPanelOpen && bp === "tablet" && (
        <>
          <div
            className="fixed inset-0 bg-black/40 z-40"
            onClick={() => setRightPanelOpen(false)}
          />
          <aside className="fixed inset-y-0 right-0 z-50 w-80 border-l border-border flex flex-col bg-sidebar">
            <div className="flex justify-end p-2">
              <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setRightPanelOpen(false)}>
                <X className="h-4 w-4" />
              </Button>
            </div>
            <WorkspacePanel key={selectedAgent.id} agent={selectedAgent} />
          </aside>
        </>
      )}
    </div>
  )
}
