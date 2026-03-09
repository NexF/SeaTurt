import { Agent, ModelsResponse, Message, Session, FileEntry, DesktopInfo, ContentBlock, CronJob, CronJobExecution } from "@/types"

const BASE = "/api"

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, options)
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.json()
}

// Models
export async function fetchModels(): Promise<ModelsResponse> {
  return request<ModelsResponse>("/models")
}

// Agents
export async function listAgents(): Promise<Agent[]> {
  return request<Agent[]>("/agents")
}

export async function createAgent(body: {
  name: string
  provider?: string
  model?: string
}): Promise<Agent> {
  return request<Agent>("/agents", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function getAgent(id: string): Promise<Agent> {
  return request<Agent>(`/agents/${id}`)
}

export async function startAgent(id: string): Promise<void> {
  await request(`/agents/${id}/start`, { method: "POST" })
}

export async function stopAgent(id: string): Promise<void> {
  await request(`/agents/${id}/stop`, { method: "POST" })
}

export async function deleteAgent(id: string): Promise<void> {
  await request(`/agents/${id}`, { method: "DELETE" })
}

// Sessions
export async function listSessions(agentId: string): Promise<{ sessions: Session[] }> {
  return request<{ sessions: Session[] }>(`/agents/${agentId}/sessions`)
}

export async function createSession(agentId: string, title?: string): Promise<Session> {
  return request<Session>(`/agents/${agentId}/sessions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title }),
  })
}

export async function updateSession(agentId: string, sessionId: string, title: string): Promise<Session> {
  return request<Session>(`/agents/${agentId}/sessions/${sessionId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title }),
  })
}

export async function deleteSession(agentId: string, sessionId: string): Promise<void> {
  await fetch(`${BASE}/agents/${agentId}/sessions/${sessionId}`, { method: "DELETE" })
}

// Chat (session-level)
export async function getHistory(agentId: string, sessionId: string): Promise<Message[]> {
  return request<Message[]>(`/agents/${agentId}/sessions/${sessionId}/history`)
}

export async function deleteHistory(agentId: string, sessionId: string): Promise<void> {
  await request(`/agents/${agentId}/sessions/${sessionId}/history`, { method: "DELETE" })
}

export async function cancelChat(agentId: string, sessionId: string): Promise<void> {
  await request(`/agents/${agentId}/sessions/${sessionId}/chat/cancel`, { method: "POST" })
}

export async function cancelToolCall(agentId: string, sessionId: string, toolCallId: string): Promise<void> {
  await request(`/agents/${agentId}/sessions/${sessionId}/chat/cancel-tool/${toolCallId}`, { method: "POST" })
}

export interface ChatPayload {
  text: string
  images?: File[]
}

export function streamChat(
  agentId: string,
  sessionId: string,
  payload: ChatPayload,
  onEvent: (event: { type: string; data: unknown }) => void,
  onDone: () => void,
  onError: (err: Error) => void
): AbortController {
  const controller = new AbortController()

  const doFetch = async () => {
    let body: BodyInit
    let headers: Record<string, string> = {}

    if (payload.images && payload.images.length > 0) {
      const formData = new FormData()
      formData.append("text", payload.text)
      payload.images.forEach((img) => formData.append("image", img))
      body = formData
    } else {
      const content: ContentBlock[] = [{ type: "text", text: payload.text }]
      headers["Content-Type"] = "application/json"
      body = JSON.stringify({ content })
    }

    const res = await fetch(`${BASE}/agents/${agentId}/sessions/${sessionId}/chat`, {
      method: "POST",
      headers,
      body,
      signal: controller.signal,
    })

    if (!res.ok) {
      const errBody = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(errBody.error || res.statusText)
    }

    const reader = res.body?.getReader()
    if (!reader) throw new Error("No response body")

    const decoder = new TextDecoder()
    let buffer = ""

    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split("\n")
      buffer = lines.pop() || ""

      for (const line of lines) {
        const trimmed = line.trim()
        if (trimmed.startsWith("data: ")) {
          const jsonStr = trimmed.slice(6)
          const parsed = JSON.parse(jsonStr)
          onEvent(parsed)
        }
      }
    }

    onDone()
  }

  doFetch().catch((err) => {
    if (err.name !== "AbortError") {
      onError(err)
    }
  })

  return controller
}

// Files
export async function listFiles(
  agentId: string,
  path?: string
): Promise<{ files: FileEntry[] }> {
  const query = path ? `?path=${encodeURIComponent(path)}` : ""
  return request(`/agents/${agentId}/files${query}`)
}

export function getFileUrl(agentId: string, filepath: string): string {
  return `${BASE}/agents/${agentId}/files/${filepath}`
}

// Desktop
export async function getDesktop(agentId: string): Promise<DesktopInfo> {
  return request<DesktopInfo>(`/agents/${agentId}/desktop`)
}

// CronJobs (v0.3.0)
export async function listCronJobs(agentId: string): Promise<{ cron_jobs: CronJob[] }> {
  return request<{ cron_jobs: CronJob[] }>(`/agents/${agentId}/cron-jobs`)
}

export async function createCronJob(agentId: string, body: {
  type: "cron" | "at"
  cron_expr?: string
  run_at?: string
  prompt: string
  session_strategy?: string
  session_id?: string
}): Promise<CronJob> {
  return request<CronJob>(`/agents/${agentId}/cron-jobs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function getCronJob(agentId: string, jobId: string): Promise<CronJob> {
  return request<CronJob>(`/agents/${agentId}/cron-jobs/${jobId}`)
}

export async function updateCronJob(agentId: string, jobId: string, body: {
  type?: string
  cron_expr?: string
  run_at?: string
  prompt?: string
  session_strategy?: string
  session_id?: string
  enabled?: boolean
}): Promise<CronJob> {
  return request<CronJob>(`/agents/${agentId}/cron-jobs/${jobId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
}

export async function deleteCronJob(agentId: string, jobId: string): Promise<void> {
  await request(`/agents/${agentId}/cron-jobs/${jobId}`, { method: "DELETE" })
}

export async function triggerCronJob(agentId: string, jobId: string): Promise<void> {
  await request(`/agents/${agentId}/cron-jobs/${jobId}/trigger`, { method: "POST" })
}

export async function listCronJobHistory(agentId: string, jobId: string): Promise<{ executions: CronJobExecution[] }> {
  return request<{ executions: CronJobExecution[] }>(`/agents/${agentId}/cron-jobs/${jobId}/history`)
}
