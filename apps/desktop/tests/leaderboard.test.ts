import { describe, expect, it } from "vitest";

import {
  getLeaderboardColumnMax,
  rankLeaderboardEntries,
} from "@/lib/leaderboard";
import type { LeaderboardEntry } from "@/types/leaderboard";

const BASE_ROW: Omit<LeaderboardEntry, "studentId" | "grade" | "username"> = {
  rating: 0,
  knowledge: 0,
  accuracy: 0,
  quality: 0,
  flexibility: 0,
  proficiency: 0,
};

describe("leaderboard helpers", () => {
  it("sorts by rating then knowledge and supports tie ranks", () => {
    const rows: LeaderboardEntry[] = [
      {
        ...BASE_ROW,
        studentId: "20230001",
        grade: "2023",
        username: "carol",
        rating: 980,
        knowledge: 99,
      },
      {
        ...BASE_ROW,
        studentId: "20230002",
        grade: "2023",
        username: "alice",
        rating: 1000,
        knowledge: 80,
      },
      {
        ...BASE_ROW,
        studentId: "20230003",
        grade: "2023",
        username: "bob",
        rating: 1000,
        knowledge: 80,
      },
      {
        ...BASE_ROW,
        studentId: "20230004",
        grade: "2023",
        username: "dora",
        rating: 1000,
        knowledge: 78,
      },
    ];

    const ranked = rankLeaderboardEntries(rows);
    expect(ranked.map((item) => item.username)).toEqual([
      "alice",
      "bob",
      "dora",
      "carol",
    ]);
    expect(ranked.map((item) => item.rank)).toEqual([1, 1, 3, 4]);
  });

  it("returns column max values", () => {
    const rows: LeaderboardEntry[] = [
      {
        ...BASE_ROW,
        studentId: "20230001",
        grade: "2023",
        username: "alice",
        rating: 1010,
        knowledge: 88,
        accuracy: 70,
        quality: 60,
        flexibility: 50,
        proficiency: 72,
      },
      {
        ...BASE_ROW,
        studentId: "20230002",
        grade: "2023",
        username: "bob",
        rating: 999,
        knowledge: 90,
        accuracy: 82,
        quality: 75,
        flexibility: 56,
        proficiency: 80,
      },
    ];

    const max = getLeaderboardColumnMax(rows);
    expect(max.rating).toBe(1010);
    expect(max.knowledge).toBe(90);
    expect(max.accuracy).toBe(82);
    expect(max.quality).toBe(75);
    expect(max.flexibility).toBe(56);
    expect(max.proficiency).toBe(80);
  });
});
