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
  knowledge: "#0284c7",
  accuracy: "#0f766e",
  quality: "#ea580c",
  flexibility: "#d97706",
  proficiency: "#65a30d",
};

export interface StudentMetrics {
  knowledge: number;
  accuracy: number;
  quality: number;
  flexibility: number;
  proficiency: number;
}

export interface MetricMissingValues {
  knowledge: boolean;
  accuracy: boolean;
  quality: boolean;
  flexibility: boolean;
  proficiency: boolean;
}

export interface MetricDeltaValues {
  knowledge: number;
  accuracy: number;
  quality: number;
  flexibility: number;
  proficiency: number;
}

export interface MetricDeltaInfo {
  latestExamId: string | null;
  latestExamName: string | null;
  latestExamDate: string | null;
  baseline: "zero" | "previous_exam";
  values: MetricDeltaValues;
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

export interface StudentIdentity {
  studentId: string;
  ptaNickname: string | null;
  noSubmissionRecords: boolean;
}

export interface StudentDashboardData {
  metrics: StudentMetrics;
  metricMissing: MetricMissingValues;
  rating: RatingInfo;
  metricDelta: MetricDeltaInfo;
  identity: StudentIdentity;
}
