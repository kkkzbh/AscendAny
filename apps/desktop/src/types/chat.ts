export type Role = "user" | "assistant" | "system";

export type ToolActivityStatus = "running" | "done" | "error";

export interface ChatToolActivity {
  id: string;
  label: string;
  status: ToolActivityStatus;
}

export interface ChatProblemRef {
  problemId: string;
  title: string | null;
  difficulty: number | null;
  knowledgePoints: string[];
  reason: string | null;
}

export interface ChatChoiceOption {
  id: string;
  label: string;
}

export interface ChatMathStep {
  title?: string;
  tex: string;
  note?: string;
}

export type ChatCalloutTone = "info" | "warn" | "tip";

export type ChatBlock =
  | { kind: "text"; text: string }
  | { kind: "tool"; activity: ChatToolActivity }
  | { kind: "problem"; problem: ChatProblemRef }
  | {
      kind: "choice";
      question: string;
      options: ChatChoiceOption[];
      answerIdx?: number;
      explanation?: string;
    }
  | { kind: "math_steps"; steps: ChatMathStep[] }
  | { kind: "code"; lang: string; code: string }
  | { kind: "node_ref"; point: string; label?: string }
  | { kind: "callout"; tone: ChatCalloutTone; markdown: string };

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
