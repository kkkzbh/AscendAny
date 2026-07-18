export interface LearningPathSnapshot {
  studentEntityId: number;
  studentEntityIds: number[];
  modelRunId?: number | null;
  generatedAt?: string | null;
  targets: string[];
  path: string[];
  explanations: Record<string, unknown>;
}

export interface LearningPathStatusItem {
  point: string;
  mastery: number;
  attempted: number;
  correct: number;
  lastTriedAt: string | null;
}

export interface LearningPathStatusSnapshot {
  studentEntityId: number;
  studentEntityIds: number[];
  items: LearningPathStatusItem[];
}

export interface KnowledgeNodeRecentDay {
  date: string;
  attempted: number;
  correct: number;
}

export interface KnowledgeNodeStats {
  attempted: number;
  correct: number;
  accuracy: number;
  lastTriedAt: string | null;
  recentSeries: KnowledgeNodeRecentDay[];
}

export interface KnowledgeNodeProblem {
  problemId: string;
  title: string | null;
  difficulty: number | null;
  knowledgePoints: string[];
  score: number | null;
  reason: string | null;
}

export interface KnowledgeNodeDetail {
  point: string;
  level: string | null;
  parents: string[];
  children: string[];
  prerequisites: string[];
  successors: string[];
  description: string | null;
  mastery: number;
  stats: KnowledgeNodeStats;
  problems: KnowledgeNodeProblem[];
}

export type NodeStatus = "locked" | "current" | "mastered";

export interface NodeViewModel {
  point: string;
  status: NodeStatus;
  mastery: number;
  attempted: number;
  correct: number;
  lastTriedAt: string | null;
  isTarget: boolean;
}
