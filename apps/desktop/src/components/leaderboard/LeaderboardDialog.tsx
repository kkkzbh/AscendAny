import { useEffect, useMemo, useRef, useState } from "react";

import { fetchStudentsLeaderboard, getApiErrorMessage } from "@/lib/api";
import {
  getLeaderboardColumnMax,
  rankLeaderboardEntries,
  type LeaderboardValueKey,
} from "@/lib/leaderboard";
import { useAuthStore } from "@/stores/authStore";
import type { LeaderboardEntry } from "@/types/leaderboard";

interface LeaderboardDialogProps {
  isOpen: boolean;
  onClose: () => void;
}

const TOP_VALUE_EPSILON = 1e-9;

function formatMetric(value: number): string {
  if (!Number.isFinite(value)) {
    return "0";
  }
  return String(Math.round(value));
}

export function LeaderboardDialog({ isOpen, onClose }: LeaderboardDialogProps) {
  const accessToken = useAuthStore((s) => s.accessToken);
  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [mounted, setMounted] = useState(false);
  const requestIdRef = useRef(0);
  const rafRef = useRef<number | null>(null);

  async function loadLeaderboard() {
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
  }

  useEffect(() => {
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }
    if (isOpen) {
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = null;
        setMounted(true);
      });
      return () => {
        if (rafRef.current !== null) {
          cancelAnimationFrame(rafRef.current);
          rafRef.current = null;
        }
      };
    }
    setMounted(false);
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    void loadLeaderboard();
  }, [isOpen, accessToken]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const timer = window.setInterval(() => {
      void loadLeaderboard();
    }, 10_000);
    return () => {
      window.clearInterval(timer);
    };
  }, [isOpen, accessToken]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) {
        return;
      }
      if (event.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [isOpen, onClose]);

  const rankedEntries = useMemo(
    () => rankLeaderboardEntries(entries),
    [entries],
  );

  const maxValues = useMemo(
    () => getLeaderboardColumnMax(rankedEntries),
    [rankedEntries],
  );

  function isTopValue(
    row: (typeof rankedEntries)[number],
    key: LeaderboardValueKey,
  ): boolean {
    return Math.abs(row[key] - maxValues[key]) <= TOP_VALUE_EPSILON;
  }

  if (!isOpen) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-6 max-[960px]:p-4">
      <div
        className={`absolute inset-0 bg-black/35 backdrop-blur-sm transition-opacity duration-250 ${mounted ? "opacity-100" : "opacity-0"}`}
        onClick={onClose}
      />
      <section
        className={`leaderboard-dialog relative z-10 flex h-[560px] w-[980px] max-h-[84vh] max-w-[92vw] flex-col overflow-hidden rounded-2xl transition-all duration-300 ${mounted ? "scale-100 opacity-100" : "scale-95 opacity-0"}`}
        style={{ transitionTimingFunction: "var(--ease-spring)" }}
      >
        <div className="leaderboard-dialog-body min-h-0 flex-1 px-6 py-6">
          {loading && rankedEntries.length === 0 && (
            <div className="flex h-full items-center justify-center text-sm text-[var(--text-soft)]">
              正在加载排行榜...
            </div>
          )}

          {!loading && error && rankedEntries.length === 0 && (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
              <p className="text-sm text-[var(--rating-negative)]">{error}</p>
              <button
                type="button"
                onClick={() => void loadLeaderboard()}
                className="h-8 rounded-full border border-[var(--accent-600)]/20 bg-[var(--accent-600)] px-4 text-[12px] font-medium text-white transition-opacity hover:opacity-90"
              >
                重试
              </button>
            </div>
          )}

          {rankedEntries.length > 0 && (
            <div className="leaderboard-table-wrap relative h-full overflow-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-raised)]/70">
              <button
                type="button"
                onClick={onClose}
                className="ui-window-button ui-window-traffic ui-window-close dialog-close-traffic leaderboard-table-close"
                aria-label="关闭排行榜"
              >
                <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
              </button>
              <table className="leaderboard-table min-w-[920px] border-collapse text-[12px]">
                <thead>
                  <tr>
                    <th>排名</th>
                    <th>年级</th>
                    <th>用户名</th>
                    <th>RATING</th>
                    <th>知识</th>
                    <th>准确</th>
                    <th>质量</th>
                    <th>灵活</th>
                    <th>熟练</th>
                  </tr>
                </thead>
                <tbody>
                  {rankedEntries.map((row) => (
                    <tr key={`${row.studentId}:${row.username}`}>
                      <td className="leaderboard-cell text-[var(--text-soft)]">#{row.rank}</td>
                      <td className="leaderboard-cell text-[var(--text-soft)]">{row.grade}</td>
                      <td className="leaderboard-cell">{row.username}</td>
                      <td className={`leaderboard-cell ${isTopValue(row, "rating") ? "leaderboard-cell--top" : ""}`}>
                        {Math.round(row.rating)}
                      </td>
                      <td className={`leaderboard-cell ${isTopValue(row, "knowledge") ? "leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.knowledge)}
                      </td>
                      <td className={`leaderboard-cell ${isTopValue(row, "accuracy") ? "leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.accuracy)}
                      </td>
                      <td className={`leaderboard-cell ${isTopValue(row, "quality") ? "leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.quality)}
                      </td>
                      <td className={`leaderboard-cell ${isTopValue(row, "flexibility") ? "leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.flexibility)}
                      </td>
                      <td className={`leaderboard-cell ${isTopValue(row, "proficiency") ? "leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.proficiency)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {error && rankedEntries.length > 0 && (
          <div className="border-t border-[var(--border-subtle)] px-5 py-2 text-[11px] text-[var(--rating-negative)]">
            {error}
          </div>
        )}
      </section>
    </div>
  );
}
