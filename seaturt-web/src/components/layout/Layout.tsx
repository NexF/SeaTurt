import { useState } from "react"
import Sidebar from "./Sidebar"
import { useAgentStore } from "@/stores/agentStore"
import ChatPanel from "@/components/chat/ChatPanel"
import WorkspacePanel from "@/components/workspace/WorkspacePanel"
import WelcomePage from "@/components/WelcomePage"

export default function Layout() {
  const selectedAgentId = useAgentStore((s) => s.selectedAgentId)
  const agents = useAgentStore((s) => s.agents)
  const [rightPanelOpen, setRightPanelOpen] = useState(true)

  const selectedAgent = agents.find((a) => a.id === selectedAgentId)

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar />

      <main className="flex-1 flex flex-col min-w-0">
        {selectedAgent ? (
          <ChatPanel
            key={selectedAgent.id}
            agent={selectedAgent}
            onToggleWorkspace={() => setRightPanelOpen((v) => !v)}
            workspaceOpen={rightPanelOpen}
          />
        ) : (
          <WelcomePage />
        )}
      </main>

      {selectedAgent && rightPanelOpen && (
        <aside className="w-80 border-l border-border flex-shrink-0 hidden lg:flex flex-col bg-sidebar">
          <WorkspacePanel key={selectedAgent.id} agent={selectedAgent} />
        </aside>
      )}
    </div>
  )
}
