export type AgentStatus = "created" | "running" | "stopped" | "error"

export interface MCPServerConfig {
  name: string
  command: string
}

export interface AgentConfig {
  provider: string
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
  desktop_port?: string
  desktop_url?: string
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
  reasoning_content?: string
  tool_calls?: string
  tool_call_id?: string
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

export interface ReasoningDelta {
  content: string
}

export interface StreamEvent {
  type: "text_delta" | "reasoning_delta" | "tool_call" | "tool_result" | "error" | "done" | "cancelled"
  data: TextDelta | ReasoningDelta | ToolCallEvent | ToolResultEvent | { message: string } | null
}

export interface FileEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
}

export interface DesktopInfo {
  desktop_port?: string
  desktop_url?: string
  status: string
}

// Chat UI types — ordered segments for interleaved text + tool calls
export type ChatSegment =
  | { type: "text"; text: string }
  | { type: "reasoning"; text: string }
  | { type: "tool_call"; toolCall: UIToolCall }

export interface ChatMessage {
  id: string
  role: "user" | "assistant"
  content: string // plain text (kept for backward compat and simple rendering)
  reasoningContent?: string // accumulated reasoning/thinking content
  images?: { data: string; mime_type: string }[]
  toolCalls?: UIToolCall[] // flat list for quick ID lookup
  segments?: ChatSegment[] // ordered interleaved content
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
