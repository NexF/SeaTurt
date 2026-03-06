import { useEffect, useState } from "react"
import { Plus, Loader2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useAgentStore } from "@/stores/agentStore"
import AgentCard from "@/components/agent/AgentCard"
import CreateAgentDialog from "@/components/agent/CreateAgentDialog"

export default function Sidebar() {
  const { agents, loading, fetchAgents, fetchModels, selectAgent, selectedAgentId } =
    useAgentStore()
  const [createOpen, setCreateOpen] = useState(false)

  useEffect(() => {
    fetchAgents()
    fetchModels()
  }, [fetchAgents, fetchModels])

  // Poll agents every 5s for status updates
  useEffect(() => {
    const interval = setInterval(fetchAgents, 5000)
    return () => clearInterval(interval)
  }, [fetchAgents])

  return (
    <aside className="w-60 flex-shrink-0 border-r border-border bg-sidebar flex flex-col">
      <div className="p-4 flex items-center gap-2">
        <span className="text-lg font-semibold tracking-tight">SeaTurt</span>
        <span className="text-lg">🐢</span>
      </div>

      <div className="px-3 pb-3">
        <Button
          variant="outline"
          className="w-full justify-start gap-2"
          onClick={() => setCreateOpen(true)}
        >
          <Plus className="h-4 w-4" />
          新建 Agent
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto px-2 space-y-1">
        {loading && agents.length === 0 && (
          <div className="flex justify-center py-8">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        )}
        {agents.map((agent) => (
          <AgentCard
            key={agent.id}
            agent={agent}
            selected={agent.id === selectedAgentId}
            onClick={() => selectAgent(agent.id)}
          />
        ))}
        {!loading && agents.length === 0 && (
          <p className="text-center text-sm text-muted-foreground py-8">
            还没有 Agent，点击上方创建
          </p>
        )}
      </div>

      <CreateAgentDialog open={createOpen} onOpenChange={setCreateOpen} />
    </aside>
  )
}
