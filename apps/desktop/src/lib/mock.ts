import type { StudentMetrics, RatingInfo, RatingPoint } from "@/types/metrics";

export const MOCK_METRICS: StudentMetrics = {
  knowledge: 72,
  accuracy: 65,
  quality: 58,
  flexibility: 44,
  proficiency: 81,
};

export const MOCK_RATING_HISTORY: RatingPoint[] = [
  {
    examId: "1",
    examName: "2025第一次月考",
    date: "2025-03-26",
    oldRating: 800,
    delta: 45,
    newRating: 845,
  },
  {
    examId: "2",
    examName: "ICPC训练赛 d3",
    date: "2025-04-02",
    oldRating: 845,
    delta: -12,
    newRating: 833,
  },
  {
    examId: "3",
    examName: "IOI模拟赛 d1",
    date: "2025-04-10",
    oldRating: 833,
    delta: 28,
    newRating: 861,
  },
  {
    examId: "4",
    examName: "2025第二次月考",
    date: "2025-04-20",
    oldRating: 861,
    delta: 35,
    newRating: 896,
  },
  {
    examId: "5",
    examName: "ICPC训练赛 d5",
    date: "2025-05-01",
    oldRating: 896,
    delta: -8,
    newRating: 888,
  },
];

export const MOCK_RATING: RatingInfo = {
  current: 888,
  lastDelta: -8,
  history: MOCK_RATING_HISTORY,
};
