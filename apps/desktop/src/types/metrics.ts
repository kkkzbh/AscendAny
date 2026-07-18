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

export interface ProgressExplanation {
  available: boolean;
  latestExamId: string | null;
  latestExamName: string | null;
  latestExamDate: string | null;
  ratingDelta: number | null;
  keyImprovements: string[];
  keySetbacks: string[];
  summary: string;
}

export interface MilestoneItem {
  code: string;
  label: string;
  detail: string;
  examId: string | null;
  examDate: string | null;
}

export interface MilestoneStreak {
  available: boolean;
  currentPositiveStreak: number;
  bestPositiveStreak: number;
  newMilestones: MilestoneItem[];
  recentMilestones: MilestoneItem[];
  nextTargets: string[];
}

export interface PeerMetricGap {
  score: number | null;
  solved: number | null;
  knowledge: number | null;
  accuracy: number | null;
  quality: number | null;
  flexibility: number | null;
  proficiency: number | null;
}

export interface PercentileBandComparison {
  totalParticipants: number;
  myRank: number | null;
  myPercentile: number | null;
  bandCode: string | null;
  bandLabel: string;
  gapVsBandMedian: PeerMetricGap;
}

export interface PreviousRankerComparison {
  available: boolean;
  rankGap: number | null;
  scoreGap: number | null;
  solvedGap: number | null;
  metricGapVsPrevious: PeerMetricGap;
}

export interface PeerComparison {
  available: boolean;
  defaultMode: "percentile_band" | "previous_ranker";
  percentileBand: PercentileBandComparison;
  previousRanker: PreviousRankerComparison;
}

export interface PostExamSupport {
  available: boolean;
  mode: "recovery" | "steady" | "reinforce";
  headline: string;
  message: string;
  actionPlan: string[];
  checkInQuestion: string;
}

export interface StudentDashboardData {
  metrics: StudentMetrics;
  metricMissing: MetricMissingValues;
  rating: RatingInfo;
  metricDelta: MetricDeltaInfo;
  identity: StudentIdentity;
  progressExplanation: ProgressExplanation;
  milestoneStreak: MilestoneStreak;
  peerComparison: PeerComparison;
  postExamSupport: PostExamSupport;
}

export function createEmptyPeerMetricGap(): PeerMetricGap {
  return {
    score: null,
    solved: null,
    knowledge: null,
    accuracy: null,
    quality: null,
    flexibility: null,
    proficiency: null,
  };
}

export function createEmptyProgressExplanation(): ProgressExplanation {
  return {
    available: false,
    latestExamId: null,
    latestExamName: null,
    latestExamDate: null,
    ratingDelta: null,
    keyImprovements: [],
    keySetbacks: [],
    summary: "暂无可比较数据",
  };
}

export function createEmptyMilestoneStreak(): MilestoneStreak {
  return {
    available: false,
    currentPositiveStreak: 0,
    bestPositiveStreak: 0,
    newMilestones: [],
    recentMilestones: [],
    nextTargets: [],
  };
}

export function createEmptyPeerComparison(): PeerComparison {
  return {
    available: false,
    defaultMode: "percentile_band",
    percentileBand: {
      totalParticipants: 0,
      myRank: null,
      myPercentile: null,
      bandCode: null,
      bandLabel: "暂无可比较数据",
      gapVsBandMedian: createEmptyPeerMetricGap(),
    },
    previousRanker: {
      available: false,
      rankGap: null,
      scoreGap: null,
      solvedGap: null,
      metricGapVsPrevious: createEmptyPeerMetricGap(),
    },
  };
}

export function createEmptyPostExamSupport(): PostExamSupport {
  return {
    available: false,
    mode: "steady",
    headline: "先稳住节奏",
    message: "暂无可比较数据",
    actionPlan: [],
    checkInQuestion: "",
  };
}
