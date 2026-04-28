import { create } from "zustand";

interface LeaderboardState {
  isOpen: boolean;
  openLeaderboard: () => void;
  closeLeaderboard: () => void;
}

export const useLeaderboardStore = create<LeaderboardState>((set) => ({
  isOpen: false,
  openLeaderboard: () => set({ isOpen: true }),
  closeLeaderboard: () => set({ isOpen: false }),
}));
