import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ChatMessage, ChatSession } from "@/types/chat";

interface ChatState {
  session: ChatSession;
  addMessage: (role: ChatMessage["role"], content: string) => void;
  clearContext: () => void;
  setSummary: (summary: string) => void;
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

export const useChatStore = create<ChatState>()(
  persist(
    (set) => ({
      session: createEmptySession(),

      addMessage: (role, content) =>
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
              },
            ],
            updatedAt: Date.now(),
          },
        })),

      clearContext: () =>
        set(() => ({
          session: createEmptySession(),
        })),

      setSummary: (summary) =>
        set((state) => ({
          session: {
            ...state.session,
            summary,
            updatedAt: Date.now(),
          },
        })),
    }),
    {
      name: "ascendany_chat",
    },
  ),
);
