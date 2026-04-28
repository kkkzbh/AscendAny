import { useMemo, type ReactNode } from "react";

import {
  getLeaderboardColumnMax,
  rankLeaderboardEntries,
  type LeaderboardValueKey,
} from "@/lib/leaderboard";
import type { LeaderboardEntry } from "@/types/leaderboard";

interface LeaderboardTableProps {
  entries: LeaderboardEntry[];
  loading: boolean;
  error: string | null;
  onRetry: () => void;
}

const TOP_VALUE_EPSILON = 1e-9;

const TOP_VALUE_KEYS: LeaderboardValueKey[] = [
  "rating",
  "knowledge",
  "accuracy",
  "quality",
  "flexibility",
  "proficiency",
];

function formatMetric(value: number): string {
  if (!Number.isFinite(value)) {
    return "0";
  }
  return String(Math.round(value));
}

function TopValue({ top, children }: { top: boolean; children: ReactNode }) {
  if (top) {
    return <span className="leaderboard-top-pill">{children}</span>;
  }
  return <>{children}</>;
}

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

export function LeaderboardTable({ entries, loading, error, onRetry }: LeaderboardTableProps) {
  const rankedEntries = useMemo(
    () => rankLeaderboardEntries(entries),
    [entries],
  );

  const maxValues = useMemo(
    () => getLeaderboardColumnMax(rankedEntries),
    [rankedEntries],
  );

  const topValueSets = useMemo<Partial<Record<LeaderboardValueKey, Set<string>>>>(() => {
    const result: Partial<Record<LeaderboardValueKey, Set<string>>> = {};
    for (const key of TOP_VALUE_KEYS) {
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

  if (loading && rankedEntries.length === 0) {
    return (
      <div className="leaderboard-skeleton-wrap">
        {Array.from({ length: 8 }, (_, i) => (
          <div key={i} className="leaderboard-skeleton-row" />
        ))}
      </div>
    );
  }

  if (!loading && error && rankedEntries.length === 0) {
    return (
      <div className="leaderboard-empty-state">
        <p className="leaderboard-empty-message">{error}</p>
        <button type="button" onClick={onRetry} className="leaderboard-retry-button">
          重试
        </button>
      </div>
    );
  }

  if (rankedEntries.length === 0) {
    return (
      <div className="leaderboard-empty-state">
        <p className="leaderboard-empty-message">暂无排行数据</p>
      </div>
    );
  }

  return (
    <div className="leaderboard-table-wrap">
      <table className="leaderboard-table">
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
              <td className="leaderboard-cell leaderboard-cell--rating">
                <TopValue top={isTopValue(row.studentId, "rating")}>{Math.round(row.rating)}</TopValue>
              </td>
              <td className="leaderboard-cell">
                <TopValue top={isTopValue(row.studentId, "knowledge")}>{formatMetric(row.knowledge)}</TopValue>
              </td>
              <td className="leaderboard-cell">
                <TopValue top={isTopValue(row.studentId, "accuracy")}>{formatMetric(row.accuracy)}</TopValue>
              </td>
              <td className="leaderboard-cell">
                <TopValue top={isTopValue(row.studentId, "quality")}>{formatMetric(row.quality)}</TopValue>
              </td>
              <td className="leaderboard-cell">
                <TopValue top={isTopValue(row.studentId, "flexibility")}>{formatMetric(row.flexibility)}</TopValue>
              </td>
              <td className="leaderboard-cell">
                <TopValue top={isTopValue(row.studentId, "proficiency")}>{formatMetric(row.proficiency)}</TopValue>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
