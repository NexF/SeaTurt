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
  const agents = useAgentStore((s) => s.agents)
  const [rightPanelOpen, setRightPanelOpen] = useState(true)
  const [leftDrawerOpen, setLeftDrawerOpen] = useState(false)

  // Track all agent IDs that have been visited so their ChatPanel instances stay alive
  const visitedAgentIdsRef = useRef<Set<string>>(new Set())
  if (selectedAgentId) {
    visitedAgentIdsRef.current.add(selectedAgentId)
  }
  // Clean up visited IDs for agents that no longer exist
  const agentIdSet = new Set(agents.map((a) => a.id))
  for (const id of visitedAgentIdsRef.current) {
    if (!agentIdSet.has(id)) visitedAgentIdsRef.current.delete(id)
  }
  const visitedAgentIds = Array.from(visitedAgentIdsRef.current)

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
        {visitedAgentIds.map((id) => {
          const agent = agents.find((a) => a.id === id)
          if (!agent) return null
          const isActive = id === selectedAgentId
          return (
            <div
              key={id}
              className="flex flex-col h-full"
              style={{ display: isActive ? "flex" : "none" }}
            >
              <ChatPanel
                agent={agent}
                onToggleWorkspace={() => setRightPanelOpen((v) => !v)}
                workspaceOpen={rightPanelOpen}
              />
            </div>
          )
        })}

        {/* Show welcome page only when no agent is selected */}
        {!selectedAgentId && <WelcomePage />}
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
