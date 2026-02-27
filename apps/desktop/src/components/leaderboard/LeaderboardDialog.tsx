import { useCallback, useEffect, useMemo, useRef, useState } from "react";

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

/** 前三名奖牌 emoji */
function RankBadge({ rank }: { rank: number }) {
  if (rank === 1) {
    return <span className="leaderboard-rank-medal leaderboard-rank-medal--1">🥇</span>;
  }
  if (rank === 2) {
    return <span className="leaderboard-rank-medal leaderboard-rank-medal--2">🥈</span>;
  }
  if (rank === 3) {
    return <span className="leaderboard-rank-medal leaderboard-rank-medal--3">🥉</span>;
  }
  return <span className="leaderboard-rank-num">#{rank}</span>;
}

export function LeaderboardDialog({ isOpen, onClose }: LeaderboardDialogProps) {
  const accessToken = useAuthStore((s) => s.accessToken);
  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [mounted, setMounted] = useState(false);
  const requestIdRef = useRef(0);
  const rafRef = useRef<number | null>(null);

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

  // 挂载动画
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

  // 初次加载
  useEffect(() => {
    if (!isOpen) {
      return;
    }
    void loadLeaderboard();
  }, [isOpen, loadLeaderboard]);

  // 轮询刷新（10 秒）
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
  }, [isOpen, loadLeaderboard]);

  // ESC 关闭
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

  /**
   * 预计算每列的"最优值"条目 ID 集合，避免在渲染时对每行每列重复比较。
   * key → Set<studentId>
   */
  const topValueSets = useMemo<Partial<Record<LeaderboardValueKey, Set<string>>>>(() => {
    const keys: LeaderboardValueKey[] = [
      "rating",
      "knowledge",
      "accuracy",
      "quality",
      "flexibility",
      "proficiency",
    ];
    const result: Partial<Record<LeaderboardValueKey, Set<string>>> = {};
    for (const key of keys) {
      const maxVal = maxValues[key];
      const ids = new Set<string>();
      for (const row of rankedEntries) {
        if (Math.abs(row[key] - maxVal) <= TOP_VALUE_EPSILON) {
          ids.add(row.studentId);
        }
      }
      result[key] = ids;
    }
    return result;
  }, [rankedEntries, maxValues]);

  function isTopValue(studentId: string, key: LeaderboardValueKey): boolean {
    return topValueSets[key]?.has(studentId) ?? false;
  }

  if (!isOpen) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-6 max-[960px]:p-4">
      {/* 背景遮罩 */}
      <div
        className={`absolute inset-0 bg-black/35 backdrop-blur-sm transition-opacity duration-250 ${mounted ? "opacity-100" : "opacity-0"}`}
        onClick={onClose}
      />

      {/* 弹窗主体 */}
      <section
        className={`leaderboard-dialog relative z-10 flex h-[600px] w-[1020px] max-h-[88vh] max-w-[94vw] flex-col overflow-hidden rounded-2xl transition-all duration-300 ${mounted ? "scale-100 opacity-100" : "scale-95 opacity-0"}`}
        style={{ transitionTimingFunction: "var(--ease-spring)" }}
      >
        {/* 标题栏 */}
        <div className="leaderboard-header">
          <div className="leaderboard-header-left">
            <span className="leaderboard-header-icon" aria-hidden="true">🏆</span>
            <span className="leaderboard-header-title">排行榜</span>
            {loading && (
              <span className="leaderboard-loading-dot" aria-label="加载中" />
            )}
          </div>
          <button
            type="button"
            onClick={onClose}
            className="ui-window-button ui-window-traffic ui-window-close dialog-close-traffic"
            aria-label="关闭排行榜"
          >
            <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
          </button>
        </div>

        {/* 主体内容 */}
        <div className="leaderboard-dialog-body min-h-0 flex-1 overflow-hidden px-5 pb-5">
          {loading && rankedEntries.length === 0 && (
            <div className="flex h-full items-center justify-center">
              <div className="leaderboard-skeleton-wrap">
                {Array.from({ length: 8 }, (_, i) => (
                  <div key={i} className="leaderboard-skeleton-row" style={{ opacity: 1 - i * 0.1 }} />
                ))}
              </div>
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
            <div className="leaderboard-table-wrap h-full overflow-auto rounded-xl border border-[var(--border-subtle)] bg-[var(--surface-raised)]/70">
              <table className="leaderboard-table min-w-[940px] border-collapse text-[12px]">
                <thead>
                  <tr>
                    <th className="leaderboard-th leaderboard-th--rank">排名</th>
                    <th className="leaderboard-th">年级</th>
                    <th className="leaderboard-th leaderboard-th--name">用户名</th>
                    <th className="leaderboard-th leaderboard-th--rating">RATING</th>
                    <th className="leaderboard-th">知识</th>
                    <th className="leaderboard-th">准确</th>
                    <th className="leaderboard-th">质量</th>
                    <th className="leaderboard-th">灵活</th>
                    <th className="leaderboard-th">熟练</th>
                  </tr>
                </thead>
                <tbody>
                  {rankedEntries.map((row) => (
                    <tr
                      key={`${row.studentId}:${row.username}`}
                      className={`leaderboard-row${row.rank <= 3 ? ` leaderboard-row--top${row.rank}` : ""}`}
                    >
                      <td className="leaderboard-cell leaderboard-cell--rank">
                        <RankBadge rank={row.rank} />
                      </td>
                      <td className="leaderboard-cell leaderboard-cell--muted">{row.grade}</td>
                      <td className="leaderboard-cell leaderboard-cell--name">{row.username}</td>
                      <td className={`leaderboard-cell leaderboard-cell--rating${isTopValue(row.studentId, "rating") ? " leaderboard-cell--top" : ""}`}>
                        {Math.round(row.rating)}
                      </td>
                      <td className={`leaderboard-cell${isTopValue(row.studentId, "knowledge") ? " leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.knowledge)}
                      </td>
                      <td className={`leaderboard-cell${isTopValue(row.studentId, "accuracy") ? " leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.accuracy)}
                      </td>
                      <td className={`leaderboard-cell${isTopValue(row.studentId, "quality") ? " leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.quality)}
                      </td>
                      <td className={`leaderboard-cell${isTopValue(row.studentId, "flexibility") ? " leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.flexibility)}
                      </td>
                      <td className={`leaderboard-cell${isTopValue(row.studentId, "proficiency") ? " leaderboard-cell--top" : ""}`}>
                        {formatMetric(row.proficiency)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* 错误提示条（有数据时的次要错误） */}
        {error && rankedEntries.length > 0 && (
          <div className="border-t border-[var(--border-subtle)] px-5 py-2 text-[11px] text-[var(--rating-negative)]">
            {error}
          </div>
        )}
      </section>
    </div>
  );
}
