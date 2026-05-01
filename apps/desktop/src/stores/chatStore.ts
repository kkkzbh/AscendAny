import { create } from "zustand";
import type { ChatMessage, ChatSession, ChatToolActivity } from "@/types/chat";

interface ChatState {
  sessions: ChatSession[];
  activeSessionId: string | null;
  newSessionDraft: string;
  aiWorkTaskIds: string[];
  isAiWorking: boolean;
  resetForAccount: () => void;
  hydrateFromLocalState: (chat: unknown) => void;
  createSession: (options?: { title?: string }) => string;
  startNewSessionDraft: () => void;
  selectSession: (sessionId: string) => void;
  deleteSession: (sessionId: string) => void;
  getActiveSession: () => ChatSession | null;
  getCurrentDraft: () => string;
  setCurrentDraft: (draft: string) => void;
  addMessage: (
    role: ChatMessage["role"],
    content: string,
    options?: { roleId?: string },
  ) => void;
  createAssistantDraft: (roleId: string) => string;
  appendMessageContent: (messageId: string, contentDelta: string) => void;
  appendMessageReasoning: (messageId: string, reasoningDelta: string) => void;
  upsertMessageToolActivity: (
    messageId: string,
    activity: ChatToolActivity,
  ) => void;
  finalizeMessageReasoning: (messageId: string) => void;
  finalizeMessage: (messageId: string) => void;
  removeMessage: (messageId: string) => void;
  clearContext: () => void;
  setSummary: (summary: string) => void;
  startAiWork: (source: "manual" | "auto") => string;
  finishAiWork: (taskId: string) => void;
}

const DEFAULT_SESSION_TITLE = "新对话";

let _sessionCounter = 0;
function generateSessionId(): string {
  _sessionCounter += 1;
  return `session_${Date.now()}_${_sessionCounter}`;
}

function createEmptySession(title = DEFAULT_SESSION_TITLE): ChatSession {
  const now = Date.now();
  return {
    id: generateSessionId(),
    title,
    messages: [],
    summary: "",
    draft: "",
    createdAt: now,
    updatedAt: now,
  };
}

let _msgCounter = 0;
function generateId(): string {
  _msgCounter += 1;
  return `msg_${Date.now()}_${_msgCounter}`;
}

let _workCounter = 0;
function generateWorkId(source: "manual" | "auto"): string {
  _workCounter += 1;
  return `work_${source}_${Date.now()}_${_workCounter}`;
}

function formatSessionTitle(messages: ChatMessage[]): string {
  const firstUserMessage = messages.find((message) => message.role === "user");
  const content = firstUserMessage?.content.replace(/\s+/g, " ").trim() ?? "";
  if (!content) {
    return DEFAULT_SESSION_TITLE;
  }
  return content.length > 18 ? `${content.slice(0, 18)}...` : content;
}

function normalizeToolActivities(value: unknown): ChatToolActivity[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const items = value
    .filter((item): item is Partial<ChatToolActivity> =>
      Boolean(item) && typeof item === "object",
    )
    .map((item): ChatToolActivity | null => {
      const id = typeof item.id === "string" ? item.id.trim() : "";
      const label = typeof item.label === "string" ? item.label.trim() : "";
      if (!id || !label) {
        return null;
      }
      const status: ChatToolActivity["status"] = item.status === "error" ? "error" : "done";
      return { id, label, status };
    })
    .filter((item): item is ChatToolActivity => item !== null);
  return items.length > 0 ? items : undefined;
}

function normalizeMessages(value: unknown): ChatMessage[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .filter((message): message is Partial<ChatMessage> =>
      Boolean(message) && typeof message === "object",
    )
    .map((message) => ({
      id:
        typeof message.id === "string" && message.id.trim()
          ? message.id
          : generateId(),
      role:
        message.role === "user" ||
        message.role === "assistant" ||
        message.role === "system"
          ? message.role
          : "system",
      content: typeof message.content === "string" ? message.content : "",
      reasoningContent:
        typeof message.reasoningContent === "string"
          ? message.reasoningContent
          : undefined,
      reasoningStartedAt:
        typeof message.reasoningStartedAt === "number" &&
        Number.isFinite(message.reasoningStartedAt)
          ? message.reasoningStartedAt
          : undefined,
      reasoningEndedAt:
        typeof message.reasoningEndedAt === "number" &&
        Number.isFinite(message.reasoningEndedAt)
          ? message.reasoningEndedAt
          : undefined,
      toolActivities: normalizeToolActivities(message.toolActivities),
      timestamp:
        typeof message.timestamp === "number" && Number.isFinite(message.timestamp)
          ? message.timestamp
          : Date.now(),
      roleId: typeof message.roleId === "string" ? message.roleId : undefined,
      streaming: false,
      reasoningStreaming: false,
    }));
}

function normalizeSession(value: unknown): ChatSession {
  const candidate =
    value && typeof value === "object" ? (value as Partial<ChatSession>) : {};
  const messages = normalizeMessages(candidate.messages);
  const createdAt =
    typeof candidate.createdAt === "number" && Number.isFinite(candidate.createdAt)
      ? candidate.createdAt
      : Date.now();
  const updatedAt =
    typeof candidate.updatedAt === "number" && Number.isFinite(candidate.updatedAt)
      ? candidate.updatedAt
      : createdAt;
  const title =
    typeof candidate.title === "string" && candidate.title.trim()
      ? candidate.title.trim()
      : formatSessionTitle(messages);

  return {
    id:
      typeof candidate.id === "string" && candidate.id.trim()
        ? candidate.id
        : generateSessionId(),
    title,
    messages,
    summary: typeof candidate.summary === "string" ? candidate.summary : "",
    draft: typeof candidate.draft === "string" ? candidate.draft : "",
    createdAt,
    updatedAt,
  };
}

function getActiveSessionFromState(
  state: Pick<ChatState, "sessions" | "activeSessionId">,
): ChatSession | null {
  if (!state.activeSessionId) {
    return null;
  }
  return state.sessions.find((session) => session.id === state.activeSessionId) ?? null;
}

function normalizeChatSnapshot(
  value: unknown,
): Pick<ChatState, "sessions" | "activeSessionId" | "newSessionDraft"> {
  const persisted = (value ?? {}) as Partial<ChatState & { session?: unknown }>;
  const sessions = Array.isArray(persisted.sessions)
    ? persisted.sessions.map((session) => normalizeSession(session))
    : persisted.session
      ? [normalizeSession(persisted.session)]
      : [];
  const requestedActiveId =
    typeof persisted.activeSessionId === "string" ? persisted.activeSessionId : null;
  const activeSessionId =
    requestedActiveId && sessions.some((session) => session.id === requestedActiveId)
      ? requestedActiveId
      : sessions.length > 0 && persisted.activeSessionId !== null
        ? sessions[0]!.id
        : null;
  return {
    sessions,
    activeSessionId,
    newSessionDraft:
      typeof persisted.newSessionDraft === "string" ? persisted.newSessionDraft : "",
  };
}

function pickChatSnapshot(
  state: ChatState,
): Pick<ChatState, "sessions" | "activeSessionId" | "newSessionDraft"> {
  return {
    sessions: state.sessions,
    activeSessionId: state.activeSessionId,
    newSessionDraft: state.newSessionDraft,
  };
}

function persistChatSnapshot(
  snapshot: Pick<ChatState, "sessions" | "activeSessionId" | "newSessionDraft">,
): void {
  const api = typeof window === "undefined" ? undefined : window.electronAPI;
  if (!api?.localStateSaveChat) {
    return;
  }
  void api.localStateSaveChat(snapshot).catch(() => {
    // Local UI state remains authoritative until the next successful save.
  });
}

export const useChatStore = create<ChatState>()(
  (set, get) => {
    return {
      sessions: [],
      activeSessionId: null,
      newSessionDraft: "",
      aiWorkTaskIds: [],
      isAiWorking: false,

      resetForAccount: () => {
        set({
          sessions: [],
          activeSessionId: null,
          newSessionDraft: "",
          aiWorkTaskIds: [],
          isAiWorking: false,
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      hydrateFromLocalState: (chat) => {
        const normalized = normalizeChatSnapshot(chat);
        set({
          ...normalized,
          aiWorkTaskIds: [],
          isAiWorking: false,
        });
      },

      createSession: (options) => {
        const session = createEmptySession(options?.title ?? DEFAULT_SESSION_TITLE);
        set((state) => ({
          sessions: [session, ...state.sessions],
          activeSessionId: session.id,
          aiWorkTaskIds: [],
          isAiWorking: false,
        }));
        persistChatSnapshot(pickChatSnapshot(get()));
        return session.id;
      },

      startNewSessionDraft: () => {
        set((state) => {
          const currentDraft =
            state.activeSessionId === null
              ? state.newSessionDraft
              : state.sessions.find((session) => session.id === state.activeSessionId)?.draft ?? "";
          return {
            activeSessionId: null,
            newSessionDraft: state.newSessionDraft ? state.newSessionDraft : currentDraft,
            aiWorkTaskIds: [],
            isAiWorking: false,
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      selectSession: (sessionId) => {
        set((state) => {
          if (!state.sessions.some((session) => session.id === sessionId)) {
            return state;
          }
          return {
            activeSessionId: sessionId,
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      deleteSession: (sessionId) => {
        set((state) => {
          const nextSessions = state.sessions.filter((session) => session.id !== sessionId);
          if (nextSessions.length === 0) {
            return {
              sessions: [],
              activeSessionId: null,
              aiWorkTaskIds: [],
              isAiWorking: false,
            };
          }
          const wasActive = state.activeSessionId === sessionId;
          return {
            sessions: nextSessions,
            activeSessionId: wasActive ? nextSessions[0]!.id : state.activeSessionId,
            aiWorkTaskIds: wasActive ? [] : state.aiWorkTaskIds,
            isAiWorking: wasActive ? false : state.isAiWorking,
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      getActiveSession: () => getActiveSessionFromState(get()),

      getCurrentDraft: () => {
        const state = get();
        if (!state.activeSessionId) {
          return state.newSessionDraft;
        }
        return state.sessions.find((session) => session.id === state.activeSessionId)?.draft ?? "";
      },

      setCurrentDraft: (draft) => {
        set((state) => {
          if (!state.activeSessionId) {
            return { newSessionDraft: draft };
          }
          return {
            sessions: state.sessions.map((session) =>
              session.id === state.activeSessionId ? { ...session, draft } : session,
            ),
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      addMessage: (role, content, options) => {
        set((state) => {
          const activeSession = getActiveSessionFromState(state);
          const nextMessage: ChatMessage = {
            id: generateId(),
            role,
            content,
            timestamp: Date.now(),
            roleId: role === "assistant" ? options?.roleId : undefined,
            streaming: false,
            reasoningStreaming: false,
          };

          if (!activeSession) {
            const session = createEmptySession();
            const messages = [nextMessage];
            const nextSession = {
              ...session,
              messages,
              title: role === "user" ? formatSessionTitle(messages) : session.title,
              draft: "",
              updatedAt: Date.now(),
            };
            return {
              sessions: [nextSession, ...state.sessions],
              activeSessionId: nextSession.id,
              newSessionDraft: role === "user" ? "" : state.newSessionDraft,
            };
          }
          const nextSessions = state.sessions.map((session) => {
            if (session.id !== activeSession.id) {
              return session;
            }
            const messages = [...session.messages, nextMessage];
            return {
              ...session,
              messages,
              title: session.title === DEFAULT_SESSION_TITLE ? formatSessionTitle(messages) : session.title,
              updatedAt: Date.now(),
            };
          });
          return {
            sessions: nextSessions,
            activeSessionId: activeSession.id,
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      createAssistantDraft: (roleId) => {
        const messageId = generateId();
        set((state) => {
          const activeSession = getActiveSessionFromState(state);
          if (!activeSession) {
            return state;
          }
          const nextMessage: ChatMessage = {
            id: messageId,
            role: "assistant",
            content: "",
            timestamp: Date.now(),
            roleId,
            streaming: true,
            reasoningStreaming: false,
          };
          return {
            sessions: state.sessions.map((session) => {
              if (session.id !== activeSession.id) return session;
              const messages = [...session.messages, nextMessage];
              return {
                ...session,
                messages,
                title: session.title === DEFAULT_SESSION_TITLE ? formatSessionTitle(messages) : session.title,
                updatedAt: Date.now(),
              };
            }),
            activeSessionId: activeSession.id,
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
        return messageId;
      },

      appendMessageContent: (messageId, contentDelta) => {
        if (!contentDelta) return;
        set((state) => ({
          sessions: state.sessions.map((session) => ({
            ...session,
            messages: session.messages.map((message) =>
              message.id === messageId
                ? { ...message, content: message.content + contentDelta }
                : message,
            ),
            updatedAt: session.messages.some((message) => message.id === messageId)
              ? Date.now()
              : session.updatedAt,
          })),
        }));
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      appendMessageReasoning: (messageId, reasoningDelta) => {
        if (!reasoningDelta) return;
        set((state) => ({
          sessions: state.sessions.map((session) => ({
            ...session,
            messages: session.messages.map((message) =>
              message.id === messageId
                ? {
                    ...message,
                    reasoningContent: `${message.reasoningContent ?? ""}${reasoningDelta}`,
                    reasoningStartedAt: message.reasoningStartedAt ?? Date.now(),
                    reasoningEndedAt: undefined,
                    reasoningStreaming: true,
                  }
                : message,
            ),
            updatedAt: session.messages.some((message) => message.id === messageId)
              ? Date.now()
              : session.updatedAt,
          })),
        }));
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      upsertMessageToolActivity: (messageId, activity) => {
        const id = activity.id.trim();
        const label = activity.label.trim();
        if (!id || !label) return;
        set((state) => ({
          sessions: state.sessions.map((session) => ({
            ...session,
            messages: session.messages.map((message) => {
              if (message.id !== messageId) {
                return message;
              }
              const current = message.toolActivities ?? [];
              const nextActivity = {
                id,
                label,
                status: activity.status,
              } satisfies ChatToolActivity;
              const exists = current.some((item) => item.id === id);
              const toolActivities = exists
                ? current.map((item) => (item.id === id ? nextActivity : item))
                : [...current, nextActivity];
              return { ...message, toolActivities };
            }),
            updatedAt: session.messages.some((message) => message.id === messageId)
              ? Date.now()
              : session.updatedAt,
          })),
        }));
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      finalizeMessageReasoning: (messageId) => {
        set((state) => ({
          sessions: state.sessions.map((session) => ({
            ...session,
            messages: session.messages.map((message) => {
              if (message.id !== messageId || !message.reasoningStreaming) {
                return message;
              }
              return {
                ...message,
                reasoningStreaming: false,
                reasoningEndedAt: message.reasoningEndedAt ?? Date.now(),
              };
            }),
            updatedAt: session.messages.some((message) => message.id === messageId)
              ? Date.now()
              : session.updatedAt,
          })),
        }));
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      finalizeMessage: (messageId) => {
        set((state) => ({
          sessions: state.sessions.map((session) => ({
            ...session,
            messages: session.messages.map((message) =>
              message.id === messageId
                ? {
                    ...message,
                    streaming: false,
                    reasoningStreaming: false,
                    reasoningEndedAt:
                      message.reasoningEndedAt !== undefined
                        ? message.reasoningEndedAt
                        : message.reasoningStartedAt !== undefined ||
                            message.reasoningContent
                        ? Date.now()
                        : message.reasoningEndedAt,
                  }
                : message,
            ),
            updatedAt: session.messages.some((message) => message.id === messageId)
              ? Date.now()
              : session.updatedAt,
          })),
        }));
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      removeMessage: (messageId) => {
        set((state) => ({
          sessions: state.sessions.map((session) => {
            if (!session.messages.some((message) => message.id === messageId)) {
              return session;
            }
            return {
              ...session,
              messages: session.messages.filter((message) => message.id !== messageId),
              updatedAt: Date.now(),
            };
          }),
        }));
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      clearContext: () => {
        set((state) => {
          const activeSession = getActiveSessionFromState(state);
          if (!activeSession) {
            return {
              newSessionDraft: "",
              aiWorkTaskIds: [],
              isAiWorking: false,
            };
          }
          const replacement = {
            ...createEmptySession(DEFAULT_SESSION_TITLE),
            id: activeSession.id,
          };
          return {
            sessions: state.sessions.map((session) =>
              session.id === activeSession.id ? replacement : session,
            ),
            activeSessionId: activeSession.id,
            aiWorkTaskIds: [],
            isAiWorking: false,
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      setSummary: (summary) => {
        set((state) => {
          const activeSession = getActiveSessionFromState(state);
          if (!activeSession) {
            return state;
          }
          return {
            sessions: state.sessions.map((session) =>
              session.id === activeSession.id
                ? {
                    ...session,
                    summary,
                    updatedAt: Date.now(),
                  }
              : session,
            ),
          };
        });
        persistChatSnapshot(pickChatSnapshot(get()));
      },

      startAiWork: (source) => {
        const taskId = generateWorkId(source);
        set((state) => ({
          aiWorkTaskIds: [...state.aiWorkTaskIds, taskId],
          isAiWorking: true,
        }));
        return taskId;
      },

      finishAiWork: (taskId) =>
        set((state) => {
          const nextTaskIds = state.aiWorkTaskIds.filter((id) => id !== taskId);
          return {
            aiWorkTaskIds: nextTaskIds,
            isAiWorking: nextTaskIds.length > 0,
          };
        }),
      };
    },
);
