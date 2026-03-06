export type AgentStatus = "created" | "running" | "stopped" | "error"

export interface MCPServerConfig {
  name: string
  command: string
}

export interface AgentConfig {
  model: string
  mcp_servers: MCPServerConfig[]
  extra_mounts?: string[]
  env_vars?: Record<string, string>
}

export interface Agent {
  id: string
  name: string
  status: AgentStatus
  container_id: string
  image: string
  workspace_path: string
  config: AgentConfig
  kasmvnc_port?: string
  kasmvnc_url?: string
  created_at: string
  updated_at: string
}

export interface ModelItem {
  id: string
  name: string
  provider: string
}

export interface ModelsResponse {
  models: ModelItem[]
  default_model: string
}

export interface ContentBlock {
  type: "text" | "image"
  text?: string
  image?: {
    data: string
    mime_type: string
  }
}

export interface Message {
  id: string
  agent_id: string
  role: "user" | "assistant" | "tool"
  content: string | ContentBlock[]
  tool_calls?: string
  created_at: string
}

export interface TextDelta {
  content: string
}

export interface ToolCallEvent {
  id: string
  name: string
  arguments: string
}

export interface ToolResultEvent {
  tool_call_id: string
  content: ContentBlock[]
  is_error: boolean
}

export interface StreamEvent {
  type: "text_delta" | "tool_call" | "tool_result" | "error" | "done"
  data: TextDelta | ToolCallEvent | ToolResultEvent | { message: string } | null
}

export interface FileEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
}

export interface DesktopInfo {
  kasmvnc_port?: string
  kasmvnc_url?: string
  status: string
}

// Chat UI types
export interface ChatMessage {
  id: string
  role: "user" | "assistant"
  content: string
  images?: { data: string; mime_type: string }[]
  toolCalls?: UIToolCall[]
  isStreaming?: boolean
}

export interface UIToolCall {
  id: string
  name: string
  arguments: string
  result?: ContentBlock[]
  isError?: boolean
  isComplete: boolean
}
