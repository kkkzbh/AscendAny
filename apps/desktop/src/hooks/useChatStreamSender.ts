import { useCallback, useState } from "react";
import {
  getApiErrorMessage,
  streamChatReply,
  type ChatMessagePayload,
} from "@/lib/api";
import { useAuthStore } from "@/stores/authStore";
import { useChatStore } from "@/stores/chatStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useNotesStore } from "@/stores/notesStore";
import { useRecommendationsStore } from "@/stores/recommendationsStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { findRole } from "@/types/role";
import type { ChatBlock, ChatMessage } from "@/types/chat";

export interface ChoiceAnswerRequest {
  assistantMessageId: string;
  blockIndex: number;
  optionIndex: number;
}

function normalizeIdentifier(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

export function toOutboundChatMessage(message: ChatMessage): ChatMessagePayload | null {
  if (message.role === "system") return null;
  const content = message.content.trim();
  if (!content) return null;
  const payload: ChatMessagePayload = {
    role: message.role,
    content,
  };
  const reasoningContent = message.reasoningContent?.trim();
  if (message.role === "assistant" && reasoningContent) {
    payload.reasoningContent = reasoningContent;
  }
  return payload;
}

function hasActiveAssistantStream(messages: ChatMessage[]): boolean {
  return messages.some(
    (message) =>
      message.role === "assistant" &&
      (message.streaming || message.reasoningStreaming),
  );
}

function getChoiceBlock(
  message: ChatMessage | undefined,
  blockIndex: number,
): Extract<ChatBlock, { kind: "choice" }> | null {
  const block = message?.blocks?.[blockIndex];
  return block?.kind === "choice" ? block : null;
}

export function buildChoiceAnswerPrompt(
  block: Extract<ChatBlock, { kind: "choice" }>,
  optionIndex: number,
): string {
  const selected = block.options[optionIndex] ?? block.options[0];
  if (!selected) {
    return "用户刚刚在你上一条选择题卡片中选择了一个选项，但前端没有取到选项内容。请提示用户重新选择。";
  }
  const optionLines = block.options
    .map((option) => `${option.id}. ${option.label}`)
    .join("\n");
  const lines = [
    "用户刚刚在你上一条选择题卡片中选择了一个选项。请基于这个选择继续回复，不要重复出同一道题。",
    "",
    `题目：${block.question}`,
    "选项：",
    optionLines,
    `用户选择：${selected.id}. ${selected.label}`,
  ];
  if (
    typeof block.answerIdx === "number" &&
    block.answerIdx >= 0 &&
    block.answerIdx < block.options.length
  ) {
    const answer = block.options[block.answerIdx];
    if (answer) {
      lines.push(`正确答案：${answer.id}. ${answer.label}`);
    }
  }
  if (block.explanation?.trim()) {
    lines.push(`原解析：${block.explanation.trim()}`);
  }
  return lines.join("\n");
}

export function useChatStreamSender() {
  const [isSending, setIsSending] = useState(false);

  const addMessage = useChatStore((s) => s.addMessage);
  const createAssistantDraft = useChatStore((s) => s.createAssistantDraft);
  const appendMessageContent = useChatStore((s) => s.appendMessageContent);
  const appendMessageReasoning = useChatStore((s) => s.appendMessageReasoning);
  const upsertMessageToolActivity = useChatStore((s) => s.upsertMessageToolActivity);
  const appendMessageBlock = useChatStore((s) => s.appendMessageBlock);
  const setMessageChoiceAnswer = useChatStore((s) => s.setMessageChoiceAnswer);
  const finalizeMessageReasoning = useChatStore((s) => s.finalizeMessageReasoning);
  const finalizeMessage = useChatStore((s) => s.finalizeMessage);
  const removeMessage = useChatStore((s) => s.removeMessage);
  const setSummary = useChatStore((s) => s.setSummary);
  const isAiWorking = useChatStore((s) => s.isAiWorking);
  const startAiWork = useChatStore((s) => s.startAiWork);
  const finishAiWork = useChatStore((s) => s.finishAiWork);

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);

  const runStream = useCallback(
    async (messages: ChatMessagePayload[], summary: string, roleIdAtSend: string) => {
      const roleAtSend = findRole(roleIdAtSend, customRoles);
      const workTaskId = startAiWork("manual");
      let draftMessageId: string | null = null;
      let workFinishedForOutput = false;
      let bufferedText = "";
      let bufferedReasoning = "";
      let rafId = 0;

      const flush = () => {
        rafId = 0;
        if (!draftMessageId) return;
        if (bufferedReasoning) {
          const nextReasoning = bufferedReasoning;
          bufferedReasoning = "";
          appendMessageReasoning(draftMessageId, nextReasoning);
        }
        if (bufferedText) {
          const next = bufferedText;
          bufferedText = "";
          appendMessageContent(draftMessageId, next);
        }
      };
      const flushReasoning = () => {
        if (!draftMessageId || !bufferedReasoning) return;
        const nextReasoning = bufferedReasoning;
        bufferedReasoning = "";
        appendMessageReasoning(draftMessageId, nextReasoning);
      };
      const flushTextSync = () => {
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

      try {
        const notesState = useNotesStore.getState();
        const activeNote = notesState.activeId
          ? notesState.items[notesState.activeId] ?? null
          : null;
        const notesLocked = notesState.isEditingContent && notesState.isDirty;
        await streamChatReply(
          {
            studentId: normalizeIdentifier(account?.studentId ?? ""),
            ptaNickname: normalizeIdentifier(account?.ptaNickname ?? ""),
            messages,
            summary,
            roleId: roleIdAtSend,
            roleName: roleAtSend.name,
            roleSystemPrompt: roleAtSend.systemPromptExtra || undefined,
            notes: activeNote?.content ?? "",
            notesTitle: activeNote?.title ?? "",
            notesLocked,
          },
          accessToken ?? undefined,
          (event) => {
            if (event.type === "delta" && event.text) {
              const activeDraftId = ensureDraft();
              flushReasoning();
              finalizeMessageReasoning(activeDraftId);
              bufferedText += event.text;
              if (!rafId) {
                rafId = window.requestAnimationFrame(flush);
              }
              return;
            }
            if (event.type === "reasoning_delta" && event.text) {
              ensureDraft();
              bufferedReasoning += event.text;
              if (!rafId) {
                rafId = window.requestAnimationFrame(flush);
              }
              return;
            }
            if (
              event.type === "tool_activity_start" ||
              event.type === "tool_activity_done" ||
              event.type === "tool_activity_error"
            ) {
              const activeDraftId = ensureDraft();
              flushReasoning();
              finalizeMessageReasoning(activeDraftId);
              flushTextSync();
              upsertMessageToolActivity(activeDraftId, {
                id: event.activityId,
                label: event.label,
                status: event.status,
              });
              return;
            }
            if (event.type === "notes_update") {
              useNotesStore.getState().streamRemoteUpdate({
                mode: event.mode,
                previous: event.previous,
                next: event.next,
              });
              return;
            }
            if (event.type === "path_update") {
              useRecommendationsStore.getState().applyPathUpdate({
                mode: event.mode,
                previous: event.previous,
                next: event.next,
              });
              return;
            }
            if (event.type === "node_focus") {
              useLayoutStore.getState().setActiveRightPanelTab("path");
              void useRecommendationsStore
                .getState()
                .openNodeDetail(event.point, {
                  authToken: accessToken ?? undefined,
                });
              return;
            }
            if (event.type === "node_status") {
              useRecommendationsStore
                .getState()
                .applyNodeStatus(event.point, event.mastery);
              return;
            }
            if (event.type === "block_append") {
              const activeDraftId = ensureDraft();
              flushReasoning();
              finalizeMessageReasoning(activeDraftId);
              flushTextSync();
              appendMessageBlock(activeDraftId, event.block);
              return;
            }
            if (event.type === "done") {
              if (rafId) {
                window.cancelAnimationFrame(rafId);
                flush();
              }
              if (event.summary !== undefined && event.summary !== summary) {
                setSummary(event.summary);
              }
              if (typeof event.updatedNotes === "string") {
                useNotesStore.getState().reconcileRemoteUpdate(event.updatedNotes);
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
        if (rafId) {
          window.cancelAnimationFrame(rafId);
        }
        setIsSending(false);
        finishAiWork(workTaskId);
      }
    },
    [
      account?.studentId,
      account?.ptaNickname,
      accessToken,
      addMessage,
      appendMessageBlock,
      appendMessageContent,
      appendMessageReasoning,
      createAssistantDraft,
      customRoles,
      finalizeMessage,
      finalizeMessageReasoning,
      finishAiWork,
      removeMessage,
      setSummary,
      startAiWork,
      upsertMessageToolActivity,
    ],
  );

  const sendManual = useCallback(
    async (text: string) => {
      const trimmed = text.trim();
      if (!trimmed || isSending || isAiWorking) return false;
      const roleIdAtSend = activeRole;
      setIsSending(true);
      addMessage("user", trimmed);

      const latestSession = useChatStore.getState().getActiveSession();
      if (!latestSession) {
        addMessage("system", "未能创建对话，请重试。");
        setIsSending(false);
        return false;
      }
      const messages = latestSession.messages
        .map(toOutboundChatMessage)
        .filter((message): message is ChatMessagePayload => message !== null);
      await runStream(messages, latestSession.summary, roleIdAtSend);
      return true;
    },
    [activeRole, addMessage, isAiWorking, isSending, runStream],
  );

  const sendChoiceAnswer = useCallback(
    async ({ assistantMessageId, blockIndex, optionIndex }: ChoiceAnswerRequest) => {
      if (isSending || isAiWorking) return false;
      const state = useChatStore.getState();
      const activeSession = state.getActiveSession();
      if (!activeSession || hasActiveAssistantStream(activeSession.messages)) {
        return false;
      }
      const assistantMessage = activeSession.messages.find(
        (message) => message.id === assistantMessageId,
      );
      const choice = getChoiceBlock(assistantMessage, blockIndex);
      if (!choice || typeof choice.answerIdx === "number") return false;
      if (optionIndex < 0 || optionIndex >= choice.options.length) return false;

      const hiddenChoiceMessage = buildChoiceAnswerPrompt(choice, optionIndex);
      const roleIdAtSend = activeRole;
      setIsSending(true);
      setMessageChoiceAnswer(assistantMessageId, blockIndex, optionIndex);

      const latestSession = useChatStore.getState().getActiveSession();
      if (!latestSession) {
        addMessage("system", "未能找到当前对话，请重试。");
        setIsSending(false);
        return false;
      }
      const messages = latestSession.messages
        .map(toOutboundChatMessage)
        .filter((message): message is ChatMessagePayload => message !== null);
      messages.push({ role: "user", content: hiddenChoiceMessage });
      await runStream(messages, latestSession.summary, roleIdAtSend);
      return true;
    },
    [
      activeRole,
      addMessage,
      isAiWorking,
      isSending,
      runStream,
      setMessageChoiceAnswer,
    ],
  );

  return {
    isSending,
    isBlocked: isSending || isAiWorking,
    sendManual,
    sendChoiceAnswer,
  };
}
