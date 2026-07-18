import { create } from "zustand";

import {
  fetchStudentAchievements,
  getApiErrorMessage,
} from "@/lib/api";
import type { StudentAchievementsData } from "@/types/achievements";

interface AchievementsQuery {
  studentId?: string;
  ptaNickname?: string;
  authToken?: string;
  force?: boolean;
}

interface AchievementsState {
  data: StudentAchievementsData | null;
  loading: boolean;
  error: string | null;
  cacheKey: string | null;
  loadAchievements: (query: AchievementsQuery) => Promise<void>;
  clear: () => void;
}

function normalize(value?: string): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

function buildCacheKey(query: AchievementsQuery): string {
  const studentId = normalize(query.studentId) ?? "";
  const ptaNickname = normalize(query.ptaNickname) ?? "";
  return `${studentId}::${ptaNickname}`;
}

export const useAchievementsStore = create<AchievementsState>()((set, get) => ({
  data: null,
  loading: false,
  error: null,
  cacheKey: null,

  loadAchievements: async (query) => {
    const studentId = normalize(query.studentId);
    const ptaNickname = normalize(query.ptaNickname);
    const nextKey = buildCacheKey({ studentId, ptaNickname });
    const force = Boolean(query.force);

    if (!force && get().cacheKey === nextKey && get().data !== null) {
      return;
    }

    set({ loading: true, error: null });
    try {
      const data = await fetchStudentAchievements({
        studentId,
        ptaNickname,
        authToken: query.authToken,
      });
      set({
        data,
        loading: false,
        error: null,
        cacheKey: nextKey,
      });
    } catch (error) {
      set({
        data: null,
        loading: false,
        error: getApiErrorMessage(error, "加载成就失败，请稍后重试。"),
      });
    }
  },

  clear: () =>
    set({
      data: null,
      loading: false,
      error: null,
      cacheKey: null,
    }),
}));
