import { useEffect, useRef } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { useCustomRoleStore } from "@/stores/customRoleStore";
import { storage } from "@/lib/storage";
import { postAutoAnalysis } from "@/lib/api";
import { findRole } from "@/types/role";

/**
 * Hook that triggers auto-analysis once per latest exam (per account).
 * If the latest rating exam changes, it calls the auto-analysis endpoint
 * and forwards the assistant reply via callbacks.
 */
export function useAutoAnalysis(params: {
  onReply: (reply: string, roleId: string) => void;
  onWorkStart?: () => string;
  onWorkEnd?: (taskId: string | undefined) => void;
}) {
  const { onReply, onWorkStart, onWorkEnd } = params;
  const inFlightExamIdRef = useRef<string | null>(null);

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const status = useAuthStore((s) => s.status);
  const rating = useMetricsStore((s) => s.rating);
  const activeRole = useSettingsStore((s) => s.activeRole);
  const customRoles = useCustomRoleStore((s) => s.customRoles);

  useEffect(() => {
    if (status !== "authenticated" || !account || !accessToken) return;
    const latestExamId = rating?.history?.[0]?.examId?.trim();
    if (!latestExamId) return;

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
        taskId = onWorkStart?.();

        const response = await postAutoAnalysis(
          {
            studentId: account.studentId ?? undefined,
            ptaNickname: account.ptaNickname ?? undefined,
            roleId: roleIdAtRequest,
            roleName: roleAtRequest.name,
            roleSystemPrompt: roleAtRequest.systemPromptExtra || undefined,
            latestExamId,
          },
          accessToken,
        );

        storage.set(examStorageKey, latestExamId);

        const reply = response.reply.trim();
        if (reply) {
          onReply(reply, roleIdAtRequest);
        }
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
    onWorkStart,
    onWorkEnd,
  ]);
}
