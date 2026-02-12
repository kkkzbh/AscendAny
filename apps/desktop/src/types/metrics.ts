export type MetricName =
  | "knowledge"
  | "accuracy"
  | "quality"
  | "flexibility"
  | "proficiency";

export const METRIC_LABELS: Record<MetricName, string> = {
  knowledge: "知识",
  accuracy: "准确",
  quality: "质量",
  flexibility: "灵活",
  proficiency: "熟练",
};

export const METRIC_COLORS: Record<MetricName, string> = {
  knowledge: "#6366f1",
  accuracy: "#22d3ee",
  quality: "#a78bfa",
  flexibility: "#f472b6",
  proficiency: "#34d399",
};

export interface StudentMetrics {
  knowledge: number;
  accuracy: number;
  quality: number;
  flexibility: number;
  proficiency: number;
}

export interface RatingInfo {
  current: number;
  lastDelta: number | null;
  history: RatingPoint[];
}

export interface RatingPoint {
  examId: string;
  examName: string;
  date: string;
  oldRating: number;
  delta: number;
  newRating: number;
}
