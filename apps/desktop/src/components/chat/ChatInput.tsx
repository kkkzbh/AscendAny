import { useState, useRef, useCallback } from "react";
import {
  getApiErrorMessage,
  streamChatReply,
  type ChatMessagePayload,
} from "@/lib/api";
import { useAuthStore } from "@/stores/authStore";
import { useChatStore } from "@/stores/chatStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { findRole } from "@/types/role";

export interface ChatInputProps {
  showClearButton?: boolean;
  sendVariant?: "icon" | "pill";
  sendLabel?: string;
}

function normalizeIdentifier(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

export function ChatInput({
  showClearButton = false,
  sendVariant = "icon",
  sendLabel = "发送",
}: ChatInputProps = {}) {
  const [text, setText] = useState("");
  const [isSending, setIsSending] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const addMessage = useChatStore((s) => s.addMessage);
  const createAssistantDraft = useChatStore((s) => s.createAssistantDraft);
  const appendMessageContent = useChatStore((s) => s.appendMessageContent);
  const finalizeMessage = useChatStore((s) => s.finalizeMessage);
  const removeMessage = useChatStore((s) => s.removeMessage);
  const clearContext = useChatStore((s) => s.clearContext);
  const setSummary = useChatStore((s) => s.setSummary);
  const isAiWorking = useChatStore((s) => s.isAiWorking);
  const startAiWork = useChatStore((s) => s.startAiWork);
  const finishAiWork = useChatStore((s) => s.finishAiWork);

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);

  const handleSend = useCallback(async () => {
    const trimmed = text.trim();
    if (!trimmed || isSending || isAiWorking) return;
    const roleIdAtSend = activeRole;
    const roleAtSend = findRole(roleIdAtSend, customRoles);

    setIsSending(true);
    const workTaskId = startAiWork("manual");

    addMessage("user", trimmed);
    setText("");

    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }

    try {
      const latestSession = useChatStore.getState().getActiveSession();
      const messages: ChatMessagePayload[] = latestSession.messages
        .filter((message) => message.role !== "system")
        .map((message) => ({
          role: message.role,
          content: message.content.trim(),
        }))
        .filter((message) => message.content.length > 0);

      let draftMessageId: string | null = null;
      let workFinishedForOutput = false;
      let bufferedText = "";
      let rafId = 0;
      const flush = () => {
        rafId = 0;
        if (!draftMessageId || !bufferedText) return;
        const next = bufferedText;
        bufferedText = "";
        appendMessageContent(draftMessageId, next);
      };
      const ensureDraft = () => {
        if (!draftMessageId) {
          draftMessageId = createAssistantDraft(roleIdAtSend);
        }
        if (!workFinishedForOutput) {
          finishAiWork(workTaskId);
          workFinishedForOutput = true;
        }
        return draftMessageId;
      };

      await streamChatReply({
          studentId: normalizeIdentifier(account?.studentId ?? ""),
          ptaNickname: normalizeIdentifier(account?.ptaNickname ?? ""),
          messages,
          summary: latestSession.summary,
          roleId: roleIdAtSend,
          roleName: roleAtSend.name,
          roleSystemPrompt: roleAtSend.systemPromptExtra || undefined,
        },
        accessToken ?? undefined,
        (event) => {
          if (event.type === "delta" && event.text) {
            ensureDraft();
            bufferedText += event.text;
            if (!rafId) {
              rafId = window.requestAnimationFrame(flush);
            }
            return;
          }
          if (event.type === "done") {
            if (rafId) {
              window.cancelAnimationFrame(rafId);
              flush();
            }
            if (event.summary !== undefined && event.summary !== latestSession.summary) {
              setSummary(event.summary);
            }
            if (draftMessageId) {
              if (!event.reply.trim()) {
                removeMessage(draftMessageId);
              } else {
                finalizeMessage(draftMessageId);
              }
            } else if (event.reply.trim()) {
              const id = createAssistantDraft(roleIdAtSend);
              appendMessageContent(id, event.reply);
              finalizeMessage(id);
            }
            return;
          }
          if (event.type === "error") {
            throw new Error(event.message);
          }
        },
      );
    } catch (error) {
      addMessage(
        "system",
        getApiErrorMessage(error, "请求失败，请检查后端服务后重试。"),
      );
    } finally {
      setIsSending(false);
      finishAiWork(workTaskId);
    }
  }, [
    text,
    isSending,
    isAiWorking,
    activeRole,
    customRoles,
    addMessage,
    createAssistantDraft,
    appendMessageContent,
    finalizeMessage,
    removeMessage,
    account?.studentId,
    account?.ptaNickname,
    accessToken,
    setSummary,
    startAiWork,
    finishAiWork,
  ]);

  const handleClear = useCallback(() => {
    if (isSending || isAiWorking) return;
    clearContext();
  }, [clearContext, isSending, isAiWorking]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleInput = () => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  };

  const sendButtonClassName = sendVariant === "pill"
    ? "send-button flex h-10 min-w-[146px] items-center justify-center rounded-full px-8 text-[15px] font-semibold tracking-[0.02em] text-white shadow-[0_6px_14px_rgba(43,83,255,0.18)] disabled:opacity-40 disabled:shadow-none"
    : "send-button flex h-8 w-8 items-center justify-center rounded-lg text-white disabled:opacity-30 disabled:shadow-none";

  return (
    <div className="chat-input-wrap shrink-0">
      <div className="mb-3 h-px bg-gradient-to-r from-transparent via-[var(--border)] to-transparent" />

      <div className="input-shell chat-input-shell flex items-end gap-2 rounded-[18px]">
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            handleInput();
          }}
          onKeyDown={handleKeyDown}
          aria-label="消息输入"
          rows={1}
          className="chat-textarea max-h-[120px] min-h-[40px] flex-1 resize-none bg-transparent text-[13px] leading-relaxed text-[var(--text-strong)] outline-none placeholder:text-[var(--text-soft)]"
        />
        <div className="flex shrink-0 items-center gap-1.5 pb-0.5">
          {showClearButton && (
            <button
              onClick={handleClear}
              disabled={isSending || isAiWorking}
              className="ui-icon-button"
              title="清空上下文"
            >
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
              </svg>
            </button>
          )}
          <button
            onClick={handleSend}
            disabled={!text.trim() || isSending || isAiWorking}
            className={sendButtonClassName}
            title={isAiWorking ? "助手处理中" : sendLabel}
          >
            {sendVariant === "pill" ? (
              <span>{sendLabel}</span>
            ) : (
              <svg
                width="15"
                height="15"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <line x1="22" y1="2" x2="11" y2="13" />
                <polygon points="22 2 15 22 11 13 2 9 22 2" />
              </svg>
            )}
          </button>
        </div>
      </div>

    </div>
  );
}
