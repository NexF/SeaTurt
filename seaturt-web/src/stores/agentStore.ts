import { create } from "zustand"
import { Agent, AgentStatus, ModelItem, Session, CronJob } from "@/types"
import * as api from "@/services/api"

type AgentOperation = "starting" | "stopping" | "deleting"

interface AgentStore {
  agents: Agent[]
  models: ModelItem[]
  defaultModel: string
  selectedAgentId: string | null
  loading: boolean
  error: string | null
  operatingAgents: Record<string, AgentOperation>

  // Session state
  sessions: Record<string, Session[]>  // agentId -> sessions
  selectedSessionId: string | null
  expandedAgentIds: Set<string>

  // CronJob state
  cronJobs: Record<string, CronJob[]>  // agentId -> cronJobs

  fetchAgents: () => Promise<void>
  fetchModels: () => Promise<void>
  selectAgent: (id: string | null) => void
  createAgent: (name: string, model?: string, provider?: string) => Promise<Agent>
  startAgent: (id: string) => Promise<void>
  stopAgent: (id: string) => Promise<void>
  deleteAgent: (id: string) => Promise<void>
  refreshAgent: (id: string) => Promise<void>
  getOperation: (id: string) => AgentOperation | undefined

  // Session methods
  fetchSessions: (agentId: string) => Promise<void>
  createSession: (agentId: string) => Promise<Session>
  deleteSession: (agentId: string, sessionId: string) => Promise<void>
  renameSession: (agentId: string, sessionId: string, title: string) => Promise<void>
  selectSession: (agentId: string, sessionId: string) => void
  toggleAgent: (agentId: string) => void

  // CronJob methods
  fetchCronJobs: (agentId: string) => Promise<void>
  createCronJob: (agentId: string, body: {
    type: "cron" | "at"
    cron_expr?: string
    run_at?: string
    prompt: string
    session_strategy?: string
  }) => Promise<CronJob>
  updateCronJob: (agentId: string, jobId: string, body: {
    prompt?: string
    cron_expr?: string
    run_at?: string
    enabled?: boolean
  }) => Promise<void>
  deleteCronJob: (agentId: string, jobId: string) => Promise<void>
  triggerCronJob: (agentId: string, jobId: string) => Promise<void>
}

const POLL_INTERVAL = 1500
const POLL_TIMEOUT = 30_000

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
  sessions: {},
  selectedSessionId: null,
  expandedAgentIds: new Set<string>(),
  cronJobs: {},

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
      set((s) => {
        const { [id]: _, ...restSessions } = s.sessions
        const newExpanded = new Set(s.expandedAgentIds)
        newExpanded.delete(id)
        return {
          agents: s.agents.filter((a) => a.id !== id),
          selectedAgentId: s.selectedAgentId === id ? null : s.selectedAgentId,
          selectedSessionId: s.selectedAgentId === id ? null : s.selectedSessionId,
          sessions: restSessions,
          expandedAgentIds: newExpanded,
        }
      })
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

  // --- Session methods ---

  fetchSessions: async (agentId) => {
    try {
      const res = await api.listSessions(agentId)
      set((s) => ({
        sessions: { ...s.sessions, [agentId]: res.sessions },
      }))
    } catch (err) {
      console.warn("Failed to fetch sessions:", err)
    }
  },

  createSession: async (agentId) => {
    const session = await api.createSession(agentId)
    set((s) => ({
      sessions: {
        ...s.sessions,
        [agentId]: [session, ...(s.sessions[agentId] || [])],
      },
      selectedAgentId: agentId,
      selectedSessionId: session.id,
    }))
    return session
  },

  deleteSession: async (agentId, sessionId) => {
    await api.deleteSession(agentId, sessionId)
    set((s) => {
      const agentSessions = (s.sessions[agentId] || []).filter((sess) => sess.id !== sessionId)
      const newSelectedSessionId =
        s.selectedSessionId === sessionId
          ? agentSessions[0]?.id || null
          : s.selectedSessionId
      return {
        sessions: { ...s.sessions, [agentId]: agentSessions },
        selectedSessionId: newSelectedSessionId,
      }
    })
  },

  renameSession: async (agentId, sessionId, title) => {
    await api.updateSession(agentId, sessionId, title)
    set((s) => ({
      sessions: {
        ...s.sessions,
        [agentId]: (s.sessions[agentId] || []).map((sess) =>
          sess.id === sessionId ? { ...sess, title } : sess
        ),
      },
    }))
  },

  selectSession: (agentId, sessionId) => {
    set({ selectedAgentId: agentId, selectedSessionId: sessionId })
  },

  toggleAgent: (agentId) => {
    const { expandedAgentIds, selectedAgentId, sessions } = get()
    const newExpanded = new Set(expandedAgentIds)

    if (newExpanded.has(agentId)) {
      // Collapse
      newExpanded.delete(agentId)
      set({ expandedAgentIds: newExpanded })
    } else {
      // Expand
      newExpanded.add(agentId)
      set({ expandedAgentIds: newExpanded, selectedAgentId: agentId })

      // Fetch sessions and auto-select
      get().fetchSessions(agentId).then(() => {
        const agentSessions = get().sessions[agentId] || []
        if (agentSessions.length > 0) {
          get().selectSession(agentId, agentSessions[0].id)
        } else {
          // No sessions — auto-create one
          get().createSession(agentId)
        }
      })
    }
  },

  // --- CronJob methods ---

  fetchCronJobs: async (agentId) => {
    try {
      const res = await api.listCronJobs(agentId)
      set((s) => ({
        cronJobs: { ...s.cronJobs, [agentId]: res.cron_jobs },
      }))
    } catch (err) {
      console.warn("Failed to fetch cron jobs:", err)
    }
  },

  createCronJob: async (agentId, body) => {
    const job = await api.createCronJob(agentId, body)
    set((s) => ({
      cronJobs: {
        ...s.cronJobs,
        [agentId]: [job, ...(s.cronJobs[agentId] || [])],
      },
    }))
    return job
  },

  updateCronJob: async (agentId, jobId, body) => {
    const updated = await api.updateCronJob(agentId, jobId, body)
    set((s) => ({
      cronJobs: {
        ...s.cronJobs,
        [agentId]: (s.cronJobs[agentId] || []).map((j) =>
          j.id === jobId ? updated : j
        ),
      },
    }))
  },

  deleteCronJob: async (agentId, jobId) => {
    await api.deleteCronJob(agentId, jobId)
    set((s) => ({
      cronJobs: {
        ...s.cronJobs,
        [agentId]: (s.cronJobs[agentId] || []).filter((j) => j.id !== jobId),
      },
    }))
  },

  triggerCronJob: async (agentId, jobId) => {
    await api.triggerCronJob(agentId, jobId)
  },
}))
