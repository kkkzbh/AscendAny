export type Role = "user" | "assistant" | "system";

export interface ChatMessage {
  id: string;
  role: Role;
  content: string;
  timestamp: number;
  /** Assistant role id snapshot when this message was generated. */
  roleId?: string;
  /** Transient flag for an assistant message currently receiving streamed text. */
  streaming?: boolean;
}

export interface ChatSession {
  id: string;
  title: string;
  messages: ChatMessage[];
  summary: string;
  createdAt: number;
  updatedAt: number;
}
