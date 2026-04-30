import { useCallback, useEffect, useRef, useState } from "react";

import { LeaderboardTable } from "@/components/leaderboard/LeaderboardTable";
import { fetchStudentsLeaderboard, getApiErrorMessage } from "@/lib/api";
import { useAuthStore } from "@/stores/authStore";
import { useLeaderboardStore } from "@/stores/leaderboardStore";
import type { LeaderboardEntry } from "@/types/leaderboard";

const POLL_INTERVAL_MS = 10_000;

function WindowControls() {
  const api = window.electronAPI;

  return (
    <div className="student-window-controls" aria-label="窗口控制">
      <button
        type="button"
        onClick={() => api?.minimize()}
        className="ui-window-button ui-window-traffic ui-window-minimize student-titlebar-traffic"
        title="最小化"
        aria-label="最小化"
      >
        <span className="ui-window-dot-symbol" aria-hidden="true">−</span>
      </button>
      <button
        type="button"
        onClick={() => api?.maximize()}
        className="ui-window-button ui-window-traffic ui-window-maximize student-titlebar-traffic"
        title="最大化"
        aria-label="最大化"
      >
        <span className="ui-window-dot-symbol" aria-hidden="true">+</span>
      </button>
      <button
        type="button"
        onClick={() => api?.close()}
        className="ui-window-button ui-window-traffic ui-window-close student-titlebar-traffic"
        title="关闭"
        aria-label="关闭"
      >
        <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
      </button>
    </div>
  );
}

export function LeaderboardWorkspace() {
  const isOpen = useLeaderboardStore((s) => s.isOpen);
  const closeLeaderboard = useLeaderboardStore((s) => s.closeLeaderboard);
  const accessToken = useAuthStore((s) => s.accessToken);

  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const requestIdRef = useRef(0);

  const loadLeaderboard = useCallback(async () => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    setLoading(true);
    setError(null);
    try {
      const result = await fetchStudentsLeaderboard(accessToken ?? undefined);
      if (requestIdRef.current !== requestId) {
        return;
      }
      setEntries(result);
    } catch (fetchError) {
      if (requestIdRef.current !== requestId) {
        return;
      }
      setError(getApiErrorMessage(fetchError, "排行榜加载失败，请稍后重试。"));
    } finally {
      if (requestIdRef.current === requestId) {
        setLoading(false);
      }
    }
  }, [accessToken]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    void loadLeaderboard();
    const timer = window.setInterval(() => {
      void loadLeaderboard();
    }, POLL_INTERVAL_MS);
    return () => {
      window.clearInterval(timer);
    };
  }, [isOpen, loadLeaderboard]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) {
        return;
      }
      if (event.key === "Escape") {
        closeLeaderboard();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [isOpen, closeLeaderboard]);

  if (!isOpen) {
    return null;
  }

  return (
    <div className="leaderboard-workspace">
      <header className="leaderboard-workspace-header drag-region">
        <button
          type="button"
          onClick={closeLeaderboard}
          className="leaderboard-workspace-back no-drag"
          aria-label="返回应用"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="m15 18-6-6 6-6" />
            <path d="M21 12H9" />
          </svg>
          <span>返回应用</span>
        </button>
        <div className="leaderboard-workspace-header-spacer" />
        <div className="leaderboard-workspace-header-actions no-drag">
          <WindowControls />
        </div>
      </header>

      <main className="leaderboard-workspace-body">
        <div className="leaderboard-workspace-content">
          <LeaderboardTable
            entries={entries}
            loading={loading}
            error={error}
            onRetry={() => void loadLeaderboard()}
          />
          {error && entries.length > 0 && (
            <div className="leaderboard-inline-error">{error}</div>
          )}
        </div>
      </main>
    </div>
  );
}
