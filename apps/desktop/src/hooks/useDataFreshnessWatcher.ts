import { useEffect, useRef } from "react";

import { streamDataEvents } from "@/lib/api";
import { useAchievementsStore } from "@/stores/achievementsStore";
import { useAuthStore } from "@/stores/authStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useLeaderboardStore } from "@/stores/leaderboardStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { useRecommendationsStore } from "@/stores/recommendationsStore";

const INITIAL_RECONNECT_DELAY_MS = 5_000;
const MAX_RECONNECT_DELAY_MS = 30_000;

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

export function useDataFreshnessWatcher() {
  const status = useAuthStore((s) => s.status);
  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);
  const loadDashboard = useMetricsStore((s) => s.loadDashboard);
  const loadPath = useRecommendationsStore((s) => s.loadPath);
  const clearNodeDetailCache = useRecommendationsStore((s) => s.clearNodeDetailCache);
  const loadAchievements = useAchievementsStore((s) => s.loadAchievements);
  const activeFullscreenView = useLayoutStore((s) => s.activeFullscreenView);
  const isLeaderboardOpen = useLeaderboardStore((s) => s.isOpen);
  const requestLeaderboardRefresh = useLeaderboardStore((s) => s.requestRefresh);
  const lastSeenImportedAtRef = useRef<string | null | undefined>(undefined);

  useEffect(() => {
    if (status !== "authenticated" || !account) {
      lastSeenImportedAtRef.current = undefined;
      return;
    }

    let disposed = false;
    let reconnectDelayMs = INITIAL_RECONNECT_DELAY_MS;
    let reconnectTimer: number | null = null;
    let controller: AbortController | null = null;

    const refreshClientData = (nextImportedAt: string | null) => {
      const previous = lastSeenImportedAtRef.current;
      if (previous === undefined) {
        lastSeenImportedAtRef.current = nextImportedAt;
        return;
      }
      if (previous === nextImportedAt) {
        return;
      }
      lastSeenImportedAtRef.current = nextImportedAt;

      const studentId = account.studentId?.trim() || undefined;
      const ptaNickname = account.ptaNickname?.trim() || undefined;
      const authToken = accessToken ?? undefined;
      void loadDashboard({ studentId, ptaNickname, authToken });
      clearNodeDetailCache();
      void loadPath(authToken);
      if (activeFullscreenView === "achievements") {
        void loadAchievements({ studentId, ptaNickname, authToken, force: true });
      }
      if (isLeaderboardOpen) {
        requestLeaderboardRefresh();
      }
    };

    const connect = () => {
      if (disposed) return;
      controller = new AbortController();
      void streamDataEvents(
        (event) => {
          if (event.type === "snapshot" || event.type === "data_changed") {
            refreshClientData(event.latestExamImportedAt);
          }
        },
        { signal: controller.signal },
      )
        .then(() => {
          if (disposed) return;
          reconnectTimer = window.setTimeout(connect, reconnectDelayMs);
          reconnectDelayMs = Math.min(reconnectDelayMs * 2, MAX_RECONNECT_DELAY_MS);
        })
        .catch((error: unknown) => {
          if (disposed || isAbortError(error)) return;
          reconnectTimer = window.setTimeout(connect, reconnectDelayMs);
          reconnectDelayMs = Math.min(reconnectDelayMs * 2, MAX_RECONNECT_DELAY_MS);
        });
    };

    connect();

    return () => {
      disposed = true;
      controller?.abort();
      if (reconnectTimer !== null) {
        window.clearTimeout(reconnectTimer);
      }
    };
  }, [
    status,
    account,
    accessToken,
    loadDashboard,
    loadPath,
    clearNodeDetailCache,
    loadAchievements,
    activeFullscreenView,
    isLeaderboardOpen,
    requestLeaderboardRefresh,
  ]);
}
