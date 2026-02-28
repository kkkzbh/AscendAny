import { useEffect } from "react";

import { AchievementFullscreen } from "@/components/achievements/AchievementFullscreen";
import { useAuthStore } from "@/stores/authStore";
import { useAchievementsStore } from "@/stores/achievementsStore";
import { TitleBar } from "./TitleBar";
import { SplitPanel } from "./SplitPanel";
import { ChatPanel } from "@/components/chat/ChatPanel";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";
import { useAvatarSync } from "@/hooks/useAvatar";
import { useLayoutStore } from "@/stores/layoutStore";

export function AppLayout() {
  useAvatarSync();
  const isMetricsPanelVisible = useLayoutStore((s) => s.isMetricsPanelVisible);
  const splitRatio = useLayoutStore((s) => s.splitRatio);
  const setSplitRatio = useLayoutStore((s) => s.setSplitRatio);
  const activeFullscreenView = useLayoutStore((s) => s.activeFullscreenView);
  const closeFullscreenView = useLayoutStore((s) => s.closeFullscreenView);
  const isAchievementOpen = activeFullscreenView === "achievements";

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);

  const achievementsData = useAchievementsStore((s) => s.data);
  const achievementsLoading = useAchievementsStore((s) => s.loading);
  const achievementsError = useAchievementsStore((s) => s.error);
  const loadAchievements = useAchievementsStore((s) => s.loadAchievements);

  useEffect(() => {
    if (!isAchievementOpen) {
      return;
    }
    const studentId = account?.studentId?.trim() || undefined;
    const ptaNickname = account?.ptaNickname?.trim() || undefined;
    const shouldUseAuthFallback = !studentId && !ptaNickname;
    void loadAchievements({
      studentId,
      ptaNickname,
      // Prefer non-auth header request to avoid unnecessary cross-origin preflight.
      // Only fall back to auth token when account identifiers are both missing.
      authToken: shouldUseAuthFallback ? accessToken ?? undefined : undefined,
    });
  }, [
    isAchievementOpen,
    account?.studentId,
    account?.ptaNickname,
    accessToken,
    loadAchievements,
  ]);

  return (
    <div className="app-shell flex h-screen w-screen flex-col overflow-hidden">
      <TitleBar />
      <main className="flex-1 overflow-hidden px-[var(--app-gutter-x)] pb-[var(--app-gutter-y)] pt-3 max-[960px]:pt-2">
        <div className="h-full">
          <SplitPanel
            left={<ChatPanel />}
            right={<MetricsPanel />}
            defaultRatio={0.55}
            minRatio={0.3}
            ratio={splitRatio}
            onRatioChange={setSplitRatio}
            showRightPanel={isMetricsPanelVisible}
          />
        </div>
      </main>
      <AchievementFullscreen
        isOpen={isAchievementOpen}
        onClose={closeFullscreenView}
        data={achievementsData}
        loading={achievementsLoading}
        error={achievementsError}
        onRetry={() =>
          void loadAchievements({
            studentId: account?.studentId ?? undefined,
            ptaNickname: account?.ptaNickname ?? undefined,
            authToken: accessToken ?? undefined,
            force: true,
          })
        }
      />
    </div>
  );
}
