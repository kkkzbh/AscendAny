import { useEffect, useRef } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { useNotesStore } from "@/stores/notesStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { storage } from "@/lib/storage";
import { streamAutoAnalysis } from "@/lib/api";
import type { ChatToolActivity } from "@/types/chat";
import { findRole } from "@/types/role";

/**
 * Hook that triggers auto-analysis once per latest exam (per account).
 * If the latest rating exam changes, it calls the auto-analysis endpoint
 * and forwards the assistant reply via callbacks.
 */
export function useAutoAnalysis(params: {
  onReply: (reply: string, roleId: string) => void;
  onStreamStart?: (roleId: string) => string;
  onStreamDelta?: (messageId: string, delta: string) => void;
  onStreamReasoning?: (messageId: string, delta: string) => void;
  onStreamReasoningDone?: (messageId: string) => void;
  onStreamToolActivity?: (messageId: string, activity: ChatToolActivity) => void;
  onStreamDone?: (messageId: string, reply: string) => void;
  onStreamEmpty?: (messageId: string) => void;
  onSessionStart?: (title: string) => void;
  onWorkStart?: () => string;
  onWorkEnd?: (taskId: string | undefined) => void;
}) {
  const { onReply, onStreamStart, onStreamDelta, onStreamReasoning, onStreamReasoningDone, onStreamToolActivity, onStreamDone, onStreamEmpty, onSessionStart, onWorkStart, onWorkEnd } = params;
  const inFlightExamIdRef = useRef<string | null>(null);

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const status = useAuthStore((s) => s.status);
  const rating = useMetricsStore((s) => s.rating);
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);

  useEffect(() => {
    if (status !== "authenticated" || !account || !accessToken) return;
    const latestExam = rating?.history?.[0];
    const latestExamId = latestExam?.examId?.trim();
    if (!latestExamId) return;
    const latestExamName = latestExam?.examName?.trim() || latestExamId;

    const examStorageKey = `last_auto_analysis_exam_${account.accountId}`;
    const lastExamId = storage.get<string>(examStorageKey, "");
    if (lastExamId === latestExamId) return;
    if (inFlightExamIdRef.current === latestExamId) return;

    inFlightExamIdRef.current = latestExamId;

    (async () => {
      const roleIdAtRequest = activeRole;
      const roleAtRequest = findRole(roleIdAtRequest, customRoles);
      let taskId: string | undefined;
      try {
        onSessionStart?.(`${latestExamName}考试分析报告`);
        taskId = onWorkStart?.();

        let draftMessageId: string | null = null;
        let bufferedText = "";
        let bufferedReasoning = "";
        let rafId = 0;
        const flush = () => {
          rafId = 0;
          if (!draftMessageId) return;
          if (bufferedReasoning) {
            const nextReasoning = bufferedReasoning;
            bufferedReasoning = "";
            onStreamReasoning?.(draftMessageId, nextReasoning);
          }
          if (bufferedText) {
            const next = bufferedText;
            bufferedText = "";
            onStreamDelta?.(draftMessageId, next);
          }
        };
        const flushReasoning = () => {
          if (!draftMessageId || !bufferedReasoning) return;
          const nextReasoning = bufferedReasoning;
          bufferedReasoning = "";
          onStreamReasoning?.(draftMessageId, nextReasoning);
        };
        const flushTextSync = () => {
          if (!draftMessageId || !bufferedText) return;
          const next = bufferedText;
          bufferedText = "";
          onStreamDelta?.(draftMessageId, next);
        };

        const notesState = useNotesStore.getState();
        const activeNote = notesState.activeId
          ? notesState.items[notesState.activeId] ?? null
          : null;
        const notesLocked = notesState.isEditingContent && notesState.isDirty;
        await streamAutoAnalysis(
          {
            studentId: account.studentId ?? undefined,
            ptaNickname: account.ptaNickname ?? undefined,
            roleId: roleIdAtRequest,
            roleName: roleAtRequest.name,
            roleSystemPrompt: roleAtRequest.systemPromptExtra || undefined,
            latestExamId,
            notes: activeNote?.content ?? "",
            notesTitle: activeNote?.title ?? "",
            notesLocked,
          },
          accessToken,
          (event) => {
            if (event.type === "delta" && event.text) {
              if (!draftMessageId) {
                draftMessageId = onStreamStart?.(roleIdAtRequest) ?? null;
                onWorkEnd?.(taskId);
                taskId = undefined;
              }
              if (draftMessageId) {
                flushReasoning();
                onStreamReasoningDone?.(draftMessageId);
                bufferedText += event.text;
                if (!rafId) {
                  rafId = window.requestAnimationFrame(flush);
                }
              }
              return;
            }
            if (event.type === "reasoning_delta" && event.text) {
              if (!draftMessageId) {
                draftMessageId = onStreamStart?.(roleIdAtRequest) ?? null;
                onWorkEnd?.(taskId);
                taskId = undefined;
              }
              if (draftMessageId) {
                bufferedReasoning += event.text;
                if (!rafId) {
                  rafId = window.requestAnimationFrame(flush);
                }
              }
              return;
            }
            if (
              event.type === "tool_activity_start" ||
              event.type === "tool_activity_done" ||
              event.type === "tool_activity_error"
            ) {
              if (!draftMessageId) {
                draftMessageId = onStreamStart?.(roleIdAtRequest) ?? null;
                onWorkEnd?.(taskId);
                taskId = undefined;
              }
              if (draftMessageId) {
                flushReasoning();
                onStreamReasoningDone?.(draftMessageId);
                flushTextSync();
                onStreamToolActivity?.(draftMessageId, {
                  id: event.activityId,
                  label: event.label,
                  status: event.status,
                });
              }
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
            if (event.type === "done") {
              if (rafId) {
                window.cancelAnimationFrame(rafId);
                flush();
              }
              if (typeof event.updatedNotes === "string") {
                useNotesStore.getState().reconcileRemoteUpdate(event.updatedNotes);
              }
              const reply = event.reply.trim();
              if (draftMessageId) {
                if (reply) {
                  onStreamDone?.(draftMessageId, reply);
                } else {
                  onStreamEmpty?.(draftMessageId);
                }
              } else if (reply) {
                onReply(reply, roleIdAtRequest);
              }
              return;
            }
            if (event.type === "error") {
              throw new Error(event.message);
            }
          },
        );

        storage.set(examStorageKey, latestExamId);
      } catch {
        // Auto-analysis is best-effort; silently ignore errors.
      } finally {
        inFlightExamIdRef.current = null;
        onWorkEnd?.(taskId);
      }
    })();
  }, [
    status,
    account,
    accessToken,
    rating,
    activeRole,
    customRoles,
    onReply,
    onStreamStart,
    onStreamDelta,
    onStreamReasoning,
    onStreamReasoningDone,
    onStreamToolActivity,
    onStreamDone,
    onStreamEmpty,
    onSessionStart,
    onWorkStart,
    onWorkEnd,
  ]);
}
