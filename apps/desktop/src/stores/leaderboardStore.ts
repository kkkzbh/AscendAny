import { create } from "zustand";

interface LeaderboardState {
  isOpen: boolean;
  refreshSeq: number;
  openLeaderboard: () => void;
  closeLeaderboard: () => void;
  requestRefresh: () => void;
}

export const useLeaderboardStore = create<LeaderboardState>((set) => ({
  isOpen: false,
  refreshSeq: 0,
  openLeaderboard: () => set({ isOpen: true }),
  closeLeaderboard: () => set({ isOpen: false }),
  requestRefresh: () => set((state) => ({ refreshSeq: state.refreshSeq + 1 })),
}));
