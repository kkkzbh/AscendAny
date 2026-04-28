import { useEffect } from "react";

import { AchievementFullscreen } from "@/components/achievements/AchievementFullscreen";
import { useAuthStore } from "@/stores/authStore";
import { useAchievementsStore } from "@/stores/achievementsStore";
import { TitleBar } from "./TitleBar";
import { StudentSidebar } from "./StudentSidebar";
import { ChatPanel } from "@/components/chat/ChatPanel";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";
import { SettingsWorkspace } from "@/components/settings/SettingsDialog";
import { useAvatarSync } from "@/hooks/useAvatar";
import { useLayoutStore } from "@/stores/layoutStore";
import { useSettingsStore } from "@/stores/settingsStore";

export function AppLayout() {
  useAvatarSync();
  const isMetricsPanelVisible = useLayoutStore((s) => s.isMetricsPanelVisible);
  const activeFullscreenView = useLayoutStore((s) => s.activeFullscreenView);
  const closeFullscreenView = useLayoutStore((s) => s.closeFullscreenView);
  const isSettingsOpen = useSettingsStore((s) => s.isOpen);
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
    <div className="app-shell student-app h-screen w-screen overflow-hidden">
      {isSettingsOpen ? (
        <SettingsWorkspace />
      ) : (
        <>
          <StudentSidebar />
          <main className="student-main">
            <TitleBar />
            <div className={`student-workspace ${isMetricsPanelVisible ? "" : "is-right-collapsed"}`}>
              <section className="student-chat-surface">
                <ChatPanel showClearButton={false} />
              </section>
              {isMetricsPanelVisible ? (
                <aside className="student-right-surface">
                  <MetricsPanel />
                </aside>
              ) : null}
            </div>
          </main>
        </>
      )}
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
