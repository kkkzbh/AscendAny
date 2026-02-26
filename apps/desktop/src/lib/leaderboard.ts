import type { LeaderboardEntry, RankedLeaderboardEntry } from "@/types/leaderboard";

const RANK_TIE_EPSILON = 1e-9;

export const LEADERBOARD_METRIC_KEYS = [
  "knowledge",
  "accuracy",
  "quality",
  "flexibility",
  "proficiency",
] as const;

export type LeaderboardMetricKey = (typeof LEADERBOARD_METRIC_KEYS)[number];

export type LeaderboardValueKey = "rating" | LeaderboardMetricKey;

export function sortLeaderboardEntries(
  entries: LeaderboardEntry[],
): LeaderboardEntry[] {
  return [...entries].sort((left, right) => {
    if (right.rating !== left.rating) {
      return right.rating - left.rating;
    }
    if (Math.abs(right.knowledge - left.knowledge) > RANK_TIE_EPSILON) {
      return right.knowledge - left.knowledge;
    }
    return left.username.localeCompare(right.username, "zh-Hans-CN");
  });
}

export function rankLeaderboardEntries(
  entries: LeaderboardEntry[],
): RankedLeaderboardEntry[] {
  const sorted = sortLeaderboardEntries(entries);
  const ranked: RankedLeaderboardEntry[] = [];
  let previousRating: number | null = null;
  let previousKnowledge: number | null = null;
  let previousRank = 0;

  sorted.forEach((entry, index) => {
    const sameAsPrevious =
      previousRating !== null &&
      previousKnowledge !== null &&
      previousRating === entry.rating &&
      Math.abs(previousKnowledge - entry.knowledge) <= RANK_TIE_EPSILON;
    const rank = sameAsPrevious ? previousRank : index + 1;
    ranked.push({
      ...entry,
      rank,
    });
    previousRating = entry.rating;
    previousKnowledge = entry.knowledge;
    previousRank = rank;
  });

  return ranked;
}

export function getLeaderboardColumnMax(
  entries: LeaderboardEntry[],
): Record<LeaderboardValueKey, number> {
  return {
    rating: Math.max(0, ...entries.map((item) => item.rating)),
    knowledge: Math.max(0, ...entries.map((item) => item.knowledge)),
    accuracy: Math.max(0, ...entries.map((item) => item.accuracy)),
    quality: Math.max(0, ...entries.map((item) => item.quality)),
    flexibility: Math.max(0, ...entries.map((item) => item.flexibility)),
    proficiency: Math.max(0, ...entries.map((item) => item.proficiency)),
  };
}
