import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ChatMessage, ChatSession } from "@/types/chat";

interface ChatState {
  session: ChatSession;
  aiWorkTaskIds: string[];
  isAiWorking: boolean;
  addMessage: (
    role: ChatMessage["role"],
    content: string,
    options?: { roleId?: string },
  ) => void;
  clearContext: () => void;
  setSummary: (summary: string) => void;
  startAiWork: (source: "manual" | "auto") => string;
  finishAiWork: (taskId: string) => void;
}

function createEmptySession(): ChatSession {
  return {
    messages: [],
    summary: "",
    createdAt: Date.now(),
    updatedAt: Date.now(),
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

export const useChatStore = create<ChatState>()(
  persist(
    (set) => ({
      session: createEmptySession(),
      aiWorkTaskIds: [],
      isAiWorking: false,

      addMessage: (role, content, options) =>
        set((state) => ({
          session: {
            ...state.session,
            messages: [
              ...state.session.messages,
              {
                id: generateId(),
                role,
                content,
                timestamp: Date.now(),
                roleId: role === "assistant" ? options?.roleId : undefined,
              },
            ],
            updatedAt: Date.now(),
          },
        })),

      clearContext: () =>
        set(() => ({
          session: createEmptySession(),
          aiWorkTaskIds: [],
          isAiWorking: false,
        })),

      setSummary: (summary) =>
        set((state) => ({
          session: {
            ...state.session,
            summary,
            updatedAt: Date.now(),
          },
        })),

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
    }),
    {
      name: "ascendany_chat_guest",
      partialize: (state) => ({
        session: state.session,
      }),
    },
  ),
);
