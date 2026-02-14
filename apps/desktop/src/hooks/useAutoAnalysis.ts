import { useEffect, useRef } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { storage } from "@/lib/storage";
import {
  postAutoAnalysis,
  type ClientProviderConfigPayload,
} from "@/lib/api";

/**
 * Hook that triggers auto-analysis once per latest exam (per account).
 * If the latest rating exam changes, it calls the auto-analysis endpoint
 * and forwards the assistant reply via onTrigger.
 */
export function useAutoAnalysis(onTrigger: (reply: string) => void) {
  const inFlightExamIdRef = useRef<string | null>(null);

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const status = useAuthStore((s) => s.status);
  const rating = useMetricsStore((s) => s.rating);
  const activeProvider = useSettingsStore((s) => s.activeProvider);
  const providers = useSettingsStore((s) => s.providers);
  const activeRole = useSettingsStore((s) => s.activeRole);

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
      try {
        // Build provider config for non-server-config providers
        const provider = providers[activeProvider];
        let providerConfig: ClientProviderConfigPayload | undefined;
        if (provider && !provider.usesServerConfig) {
          const baseUrl = provider.baseUrl.trim();
          const model = provider.model.trim();
          const apiKey = provider.apiKey.trim();
          if (!baseUrl || !model || !apiKey) {
            return;
          }
          providerConfig = {
            baseUrl,
            model,
            apiKey,
            mode: activeProvider === "anthropic" ? "anthropic" : "openai_compatible",
          };
        }

        const response = await postAutoAnalysis(
          {
            studentId: account.studentId ?? undefined,
            ptaNickname: account.ptaNickname ?? undefined,
            providerType: activeProvider,
            providerConfig,
            roleId: activeRole,
            latestExamId,
          },
          accessToken,
        );

        storage.set(examStorageKey, latestExamId);

        const reply = response.reply.trim();
        if (reply) {
          onTrigger(reply);
        }
      } catch {
        // Auto-analysis is best-effort; silently ignore errors.
      } finally {
        inFlightExamIdRef.current = null;
      }
    })();
  }, [
    status,
    account,
    accessToken,
    rating,
    activeProvider,
    providers,
    activeRole,
    onTrigger,
  ]);
}
