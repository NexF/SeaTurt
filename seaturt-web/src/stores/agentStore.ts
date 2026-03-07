import { create } from "zustand"
import { Agent, AgentStatus, ModelItem } from "@/types"
import * as api from "@/services/api"

type AgentOperation = "starting" | "stopping" | "deleting"

interface AgentStore {
  agents: Agent[]
  models: ModelItem[]
  defaultModel: string
  selectedAgentId: string | null
  loading: boolean
  error: string | null
  /** Tracks in-progress operations per agent */
  operatingAgents: Record<string, AgentOperation>

  fetchAgents: () => Promise<void>
  fetchModels: () => Promise<void>
  selectAgent: (id: string | null) => void
  createAgent: (name: string, model?: string, provider?: string) => Promise<Agent>
  startAgent: (id: string) => Promise<void>
  stopAgent: (id: string) => Promise<void>
  deleteAgent: (id: string) => Promise<void>
  refreshAgent: (id: string) => Promise<void>
  getOperation: (id: string) => AgentOperation | undefined
}

const POLL_INTERVAL = 1500
const POLL_TIMEOUT = 30_000

/** Poll agent status until it reaches the target or times out. */
async function pollUntilStatus(
  agentId: string,
  targetStatus: AgentStatus,
  refreshAgent: (id: string) => Promise<void>,
  getAgent: () => Agent | undefined,
): Promise<boolean> {
  const start = Date.now()
  while (Date.now() - start < POLL_TIMEOUT) {
    await new Promise((r) => setTimeout(r, POLL_INTERVAL))
    try {
      await refreshAgent(agentId)
      const agent = getAgent()
      if (agent?.status === targetStatus) return true
    } catch {
      // agent might have been deleted; bail out
      return false
    }
  }
  return false
}

export const useAgentStore = create<AgentStore>((set, get) => ({
  agents: [],
  models: [],
  defaultModel: "",
  selectedAgentId: null,
  loading: false,
  error: null,
  operatingAgents: {},

  fetchAgents: async () => {
    try {
      set({ loading: true, error: null })
      const agents = await api.listAgents()
      set({ agents, loading: false })
    } catch (err) {
      console.warn("Failed to fetch agents:", err)
      set({ loading: false, error: (err as Error).message })
    }
  },

  fetchModels: async () => {
    try {
      const res = await api.fetchModels()
      set({ models: res.models, defaultModel: res.default_model })
    } catch (err) {
      console.warn("Failed to fetch models:", err)
    }
  },

  selectAgent: (id) => set({ selectedAgentId: id }),

  createAgent: async (name, model, provider) => {
    const agent = await api.createAgent({ name, model, provider })
    set((s) => ({ agents: [...s.agents, agent] }))
    return agent
  },

  startAgent: async (id) => {
    set((s) => ({ operatingAgents: { ...s.operatingAgents, [id]: "starting" } }))
    try {
      await api.startAgent(id)
      await get().refreshAgent(id)
      // If not yet running, poll
      const current = get().agents.find((a) => a.id === id)
      if (current?.status !== "running") {
        await pollUntilStatus(
          id, "running", get().refreshAgent,
          () => get().agents.find((a) => a.id === id),
        )
      }
    } catch (err) {
      console.error("Failed to start agent:", err)
    } finally {
      set((s) => {
        const { [id]: _, ...rest } = s.operatingAgents
        return { operatingAgents: rest }
      })
    }
  },

  stopAgent: async (id) => {
    set((s) => ({ operatingAgents: { ...s.operatingAgents, [id]: "stopping" } }))
    try {
      await api.stopAgent(id)
      await get().refreshAgent(id)
      // If not yet stopped, poll
      const current = get().agents.find((a) => a.id === id)
      if (current?.status !== "stopped") {
        await pollUntilStatus(
          id, "stopped", get().refreshAgent,
          () => get().agents.find((a) => a.id === id),
        )
      }
    } catch (err) {
      console.error("Failed to stop agent:", err)
    } finally {
      set((s) => {
        const { [id]: _, ...rest } = s.operatingAgents
        return { operatingAgents: rest }
      })
    }
  },

  deleteAgent: async (id) => {
    set((s) => ({ operatingAgents: { ...s.operatingAgents, [id]: "deleting" } }))
    try {
      await api.deleteAgent(id)
      set((s) => ({
        agents: s.agents.filter((a) => a.id !== id),
        selectedAgentId: s.selectedAgentId === id ? null : s.selectedAgentId,
      }))
    } catch (err) {
      console.error("Failed to delete agent:", err)
    } finally {
      set((s) => {
        const { [id]: _, ...rest } = s.operatingAgents
        return { operatingAgents: rest }
      })
    }
  },

  refreshAgent: async (id) => {
    const updated = await api.getAgent(id)
    set((s) => ({
      agents: s.agents.map((a) => (a.id === id ? updated : a)),
    }))
  },

  getOperation: (id) => get().operatingAgents[id],
}))
