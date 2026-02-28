export interface AchievementIdentity {
  studentId: string;
  ptaNickname: string | null;
  noSubmissionRecords: boolean;
}

export interface AchievementSummary {
  total: number;
  locked: number;
  bronze: number;
  silver: number;
  gold: number;
}

export interface AchievementItem {
  code: string;
  title: string;
  description: string;
  tier: number;
  progress: number;
  bronzeTarget: number;
  silverTarget: number;
  goldTarget: number;
  sortOrder: number;
}

export interface StudentAchievementsData {
  identity: AchievementIdentity;
  summary: AchievementSummary;
  items: AchievementItem[];
}
