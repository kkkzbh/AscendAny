export type Role = "user" | "assistant" | "system";

export type ToolActivityStatus = "running" | "done" | "error";

export interface ChatToolActivity {
  id: string;
  label: string;
  status: ToolActivityStatus;
}

export type ChatBlock =
  | { kind: "text"; text: string }
  | { kind: "tool"; activity: ChatToolActivity };

export interface ChatMessage {
  id: string;
  role: Role;
  content: string;
  timestamp: number;
  /**
   * Ordered text/tool segments preserving SSE arrival order so the bubble
   * can render text and tool chips interleaved instead of grouped. Optional
   * for legacy/in-test messages built without going through the store.
   */
  blocks?: ChatBlock[];
  roleId?: string;
  reasoningContent?: string;
  reasoningStartedAt?: number;
  reasoningEndedAt?: number;
  /** Derived from blocks; kept for legacy persistence compatibility. */
  toolActivities?: ChatToolActivity[];
  streaming?: boolean;
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
