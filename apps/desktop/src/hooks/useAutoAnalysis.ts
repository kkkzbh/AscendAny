import { useEffect, useRef } from "react";
import { useAuthStore } from "@/stores/authStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { storage } from "@/lib/storage";
import {
  fetchLatestExamImportedAt,
  postAutoAnalysis,
  type ClientProviderConfigPayload,
} from "@/lib/api";

/**
 * Hook that checks if new exams have been imported since the last
 * auto-analysis. If so, calls the auto-analysis endpoint and returns
 * the assistant reply via onTrigger.
 *
 * Runs at most once per calendar day per account.
 */
export function useAutoAnalysis(onTrigger: (reply: string) => void) {
  const triggered = useRef(false);

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const status = useAuthStore((s) => s.status);
  const activeProvider = useSettingsStore((s) => s.activeProvider);
  const providers = useSettingsStore((s) => s.providers);
  const activeRole = useSettingsStore((s) => s.activeRole);

  useEffect(() => {
    if (triggered.current) return;
    if (status !== "authenticated" || !account || !accessToken) return;

    const dateStorageKey = `last_auto_analysis_date_${account.accountId}`;
    const atStorageKey = `last_auto_analysis_at_${account.accountId}`;
    const today = new Date().toISOString().slice(0, 10);
    const lastAnalysisDate = storage.get<string>(dateStorageKey, "");

    if (lastAnalysisDate === today) return;

    triggered.current = true;

    (async () => {
      try {
        const meta = await fetchLatestExamImportedAt();
        const latestImported = meta.latestExamImportedAt;

        if (!latestImported) return;

        const lastKnown = storage.get<string>(atStorageKey, "");
        if (lastKnown && latestImported <= lastKnown) {
          // No new exams since last analysis — still mark today as checked
          storage.set(dateStorageKey, today);
          return;
        }

        // Build provider config for non-server-config providers
        const provider = providers[activeProvider];
        let providerConfig: ClientProviderConfigPayload | undefined;
        if (provider && !provider.usesServerConfig) {
          const baseUrl = provider.baseUrl.trim();
          const model = provider.model.trim();
          const apiKey = provider.apiKey.trim();
          if (!baseUrl || !model || !apiKey) {
            // Provider not configured — skip auto-analysis silently
            storage.set(dateStorageKey, today);
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
          },
          accessToken,
        );

        storage.set(dateStorageKey, today);
        storage.set(atStorageKey, latestImported);

        const reply = response.reply.trim();
        if (reply) {
          onTrigger(reply);
        }
      } catch {
        // Auto-analysis is best-effort; silently ignore errors.
        // Still mark today so we don't retry every render.
        storage.set(dateStorageKey, today);
      }
    })();
  }, [
    status,
    account,
    accessToken,
    activeProvider,
    providers,
    activeRole,
    onTrigger,
  ]);
}
