export interface LeaderboardEntry {
  studentId: string;
  grade: string;
  username: string;
  rating: number;
  knowledge: number;
  accuracy: number;
  quality: number;
  flexibility: number;
  proficiency: number;
}

export interface RankedLeaderboardEntry extends LeaderboardEntry {
  rank: number;
}
