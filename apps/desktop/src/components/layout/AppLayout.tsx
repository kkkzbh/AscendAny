import {
  useCallback,
  useEffect,
  useRef,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";

import { AchievementFullscreen } from "@/components/achievements/AchievementFullscreen";
import { useAuthStore } from "@/stores/authStore";
import { useAchievementsStore } from "@/stores/achievementsStore";
import { TitleBar } from "./TitleBar";
import { StudentSidebar } from "./StudentSidebar";
import { ChatPanel } from "@/components/chat/ChatPanel";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";
import { LeaderboardWorkspace } from "@/components/leaderboard/LeaderboardWorkspace";
import { SettingsWorkspace } from "@/components/settings/SettingsDialog";
import { useAvatarSync } from "@/hooks/useAvatar";
import { useDataFreshnessWatcher } from "@/hooks/useDataFreshnessWatcher";
import {
  DEFAULT_RIGHT_PANEL_RATIO,
  MAX_RIGHT_PANEL_RATIO,
  MIN_RIGHT_PANEL_RATIO,
  RIGHT_PANEL_MAX_WIDTH,
  RIGHT_PANEL_MIN_WIDTH,
  useLayoutStore,
} from "@/stores/layoutStore";
import { useLeaderboardStore } from "@/stores/leaderboardStore";
import { useSettingsStore } from "@/stores/settingsStore";

interface RightPanelDragRect {
  right: number;
  width: number;
}

function clampRightPanelRatioForWidth(value: number, workspaceWidth: number): number {
  const ratio = Number.isFinite(value) ? value : DEFAULT_RIGHT_PANEL_RATIO;
  if (!Number.isFinite(workspaceWidth) || workspaceWidth <= 0) {
    return Math.max(MIN_RIGHT_PANEL_RATIO, Math.min(MAX_RIGHT_PANEL_RATIO, ratio));
  }

  const minRatio = Math.min(
    MAX_RIGHT_PANEL_RATIO,
    Math.max(MIN_RIGHT_PANEL_RATIO, RIGHT_PANEL_MIN_WIDTH / workspaceWidth),
  );
  const maxRatio = Math.max(
    minRatio,
    Math.min(MAX_RIGHT_PANEL_RATIO, RIGHT_PANEL_MAX_WIDTH / workspaceWidth),
  );
  return Math.max(minRatio, Math.min(maxRatio, ratio));
}

export function AppLayout() {
  useAvatarSync();
  useDataFreshnessWatcher();
  const isMetricsPanelVisible = useLayoutStore((s) => s.isMetricsPanelVisible);
  const rightPanelRatio = useLayoutStore((s) => s.rightPanelRatio);
  const setRightPanelRatio = useLayoutStore((s) => s.setRightPanelRatio);
  const activeFullscreenView = useLayoutStore((s) => s.activeFullscreenView);
  const closeFullscreenView = useLayoutStore((s) => s.closeFullscreenView);
  const isSettingsOpen = useSettingsStore((s) => s.isOpen);
  const isLeaderboardOpen = useLeaderboardStore((s) => s.isOpen);
  const isAchievementOpen = activeFullscreenView === "achievements";
  const workspaceRef = useRef<HTMLDivElement>(null);
  const rightPanelDraggingRef = useRef(false);
  const rightPanelDragRectRef = useRef<RightPanelDragRect | null>(null);
  const pendingRightPanelRatioRef = useRef(rightPanelRatio);
  const rightPanelRafRef = useRef<number | null>(null);

  const account = useAuthStore((s) => s.account);
  const accessToken = useAuthStore((s) => s.accessToken);

  const achievementsData = useAchievementsStore((s) => s.data);
  const achievementsLoading = useAchievementsStore((s) => s.loading);
  const achievementsError = useAchievementsStore((s) => s.error);
  const loadAchievements = useAchievementsStore((s) => s.loadAchievements);

  useEffect(() => {
    pendingRightPanelRatioRef.current = rightPanelRatio;
  }, [rightPanelRatio]);

  useEffect(
    () => () => {
      if (rightPanelRafRef.current !== null) {
        window.cancelAnimationFrame(rightPanelRafRef.current);
      }
    },
    [],
  );

  const scheduleRightPanelPreview = useCallback((ratio: number) => {
    pendingRightPanelRatioRef.current = ratio;
    if (rightPanelRafRef.current !== null) return;

    rightPanelRafRef.current = window.requestAnimationFrame(() => {
      rightPanelRafRef.current = null;
      workspaceRef.current?.style.setProperty(
        "--student-right-panel-ratio",
        String(pendingRightPanelRatioRef.current),
      );
    });
  }, []);

  const previewRightPanelRatioFromClientX = useCallback(
    (clientX: number) => {
      const rect = rightPanelDragRectRef.current ?? workspaceRef.current?.getBoundingClientRect();
      if (!rect || rect.width <= 0) return;
      const nextRatio = (rect.right - clientX) / rect.width;
      scheduleRightPanelPreview(clampRightPanelRatioForWidth(nextRatio, rect.width));
    },
    [scheduleRightPanelPreview],
  );

  const onRightPanelResizePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      if (!isMetricsPanelVisible) return;
      const workspace = workspaceRef.current;
      const rect = workspace?.getBoundingClientRect();
      if (!workspace || !rect || rect.width <= 0) return;

      event.preventDefault();
      rightPanelDraggingRef.current = true;
      rightPanelDragRectRef.current = {
        right: rect.right,
        width: rect.width,
      };
      pendingRightPanelRatioRef.current = rightPanelRatio;
      workspace.classList.add("is-right-resizing");
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      previewRightPanelRatioFromClientX(event.clientX);

      const onPointerMove = (moveEvent: PointerEvent) => {
        if (!rightPanelDraggingRef.current) return;
        previewRightPanelRatioFromClientX(moveEvent.clientX);
      };

      const onPointerUp = () => {
        rightPanelDraggingRef.current = false;
        rightPanelDragRectRef.current = null;
        if (rightPanelRafRef.current !== null) {
          window.cancelAnimationFrame(rightPanelRafRef.current);
          rightPanelRafRef.current = null;
        }
        workspaceRef.current?.style.setProperty(
          "--student-right-panel-ratio",
          String(pendingRightPanelRatioRef.current),
        );
        workspaceRef.current?.classList.remove("is-right-resizing");
        window.removeEventListener("pointermove", onPointerMove);
        window.removeEventListener("pointerup", onPointerUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        setRightPanelRatio(pendingRightPanelRatioRef.current);
      };

      window.addEventListener("pointermove", onPointerMove);
      window.addEventListener("pointerup", onPointerUp);
    },
    [isMetricsPanelVisible, previewRightPanelRatioFromClientX, rightPanelRatio, setRightPanelRatio],
  );

  const onRightPanelResizeKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLDivElement>) => {
      if (!isMetricsPanelVisible) return;
      if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
      event.preventDefault();
      const width = workspaceRef.current?.getBoundingClientRect().width ?? 0;
      const step = event.shiftKey ? 0.03 : 0.01;
      const delta = event.key === "ArrowLeft" ? step : -step;
      const nextRatio = useLayoutStore.getState().rightPanelRatio + delta;
      setRightPanelRatio(clampRightPanelRatioForWidth(nextRatio, width));
    },
    [isMetricsPanelVisible, setRightPanelRatio],
  );

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
      ) : isLeaderboardOpen ? (
        <LeaderboardWorkspace />
      ) : (
        <>
          <StudentSidebar />
          <main className="student-main">
            <TitleBar />
            <div
              ref={workspaceRef}
              className={`student-workspace ${isMetricsPanelVisible ? "" : "is-right-collapsed"}`}
              style={
                {
                  "--student-right-panel-ratio": String(rightPanelRatio),
                } as CSSProperties
              }
            >
              <section className="student-chat-surface">
                <ChatPanel showClearButton={false} />
              </section>
              {isMetricsPanelVisible ? (
                <div
                  role="separator"
                  aria-orientation="vertical"
                  aria-label="调整右侧栏宽度"
                  aria-valuemin={MIN_RIGHT_PANEL_RATIO}
                  aria-valuemax={MAX_RIGHT_PANEL_RATIO}
                  aria-valuenow={Number(rightPanelRatio.toFixed(2))}
                  tabIndex={0}
                  className="student-right-resizer no-drag"
                  onPointerDown={onRightPanelResizePointerDown}
                  onKeyDown={onRightPanelResizeKeyDown}
                >
                  <div className="student-right-resizer-bar" />
                </div>
              ) : null}
              <aside
                className="student-right-surface"
                aria-hidden={!isMetricsPanelVisible}
              >
                <MetricsPanel />
              </aside>
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
