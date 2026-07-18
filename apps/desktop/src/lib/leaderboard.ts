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
  // 单次遍历，避免为每列分别 map 产生 O(n*6) 临时数组
  const result: Record<LeaderboardValueKey, number> = {
    rating: 0,
    knowledge: 0,
    accuracy: 0,
    quality: 0,
    flexibility: 0,
    proficiency: 0,
  };
  for (const item of entries) {
    if (item.rating      > result.rating)      result.rating      = item.rating;
    if (item.knowledge   > result.knowledge)   result.knowledge   = item.knowledge;
    if (item.accuracy    > result.accuracy)    result.accuracy    = item.accuracy;
    if (item.quality     > result.quality)     result.quality     = item.quality;
    if (item.flexibility > result.flexibility) result.flexibility = item.flexibility;
    if (item.proficiency > result.proficiency) result.proficiency = item.proficiency;
  }
  return result;
}
