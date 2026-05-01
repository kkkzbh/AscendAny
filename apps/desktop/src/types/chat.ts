export type Role = "user" | "assistant" | "system";

export type ToolActivityStatus = "running" | "done" | "error";

export interface ChatToolActivity {
  id: string;
  label: string;
  status: ToolActivityStatus;
}

export interface ChatMessage {
  id: string;
  role: Role;
  content: string;
  timestamp: number;
  /** Assistant role id snapshot when this message was generated. */
  roleId?: string;
  /** Provider reasoning text shown separately from the final answer. */
  reasoningContent?: string;
  /** Timestamp when provider reasoning first started streaming. */
  reasoningStartedAt?: number;
  /** Timestamp when provider reasoning stopped streaming. */
  reasoningEndedAt?: number;
  /** Public, user-facing summaries for assistant tool calls. */
  toolActivities?: ChatToolActivity[];
  /** Transient flag for an assistant message currently receiving streamed text. */
  streaming?: boolean;
  /** Transient flag for an assistant message currently receiving streamed reasoning. */
  reasoningStreaming?: boolean;
}

export interface ChatSession {
  id: string;
  title: string;
  messages: ChatMessage[];
  summary: string;
  draft?: string;
  createdAt: number;
  updatedAt: number;
}
