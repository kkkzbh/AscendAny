import { create } from "zustand";

import { diffPaths, type PathDiffEntry } from "@/lib/pathDiff";
import {
  fetchKnowledgeNodeDetail,
  fetchLearningPath,
  fetchLearningPathStatus,
} from "@/lib/api";
import type {
  KnowledgeNodeDetail,
  LearningPathSnapshot,
  LearningPathStatusItem,
  NodeStatus,
  NodeViewModel,
} from "@/types/path";

const MASTERED_THRESHOLD = 0.8;
const ENTRY_HINT_TTL_MS = 1500;

export interface PathUpdateEvent {
  mode: "patch" | "replace";
  previous: LearningPathSnapshot | null;
  next: LearningPathSnapshot;
}

interface RecommendationsState {
  path: LearningPathSnapshot | null;
  status: Record<string, LearningPathStatusItem>;
  nodeDetailCache: Record<string, KnowledgeNodeDetail>;
  loadingPath: boolean;
  pathError: string | null;
  loadingDetail: boolean;
  detailError: string | null;
  activeDetailPoint: string | null;
  recentlyAdded: string[];
  recentlyRemoved: string[];
  recentlyTouchedAt: number | null;

  loadPath: (authToken?: string) => Promise<void>;
  loadNodeDetail: (point: string, options?: { topK?: number; authToken?: string }) => Promise<KnowledgeNodeDetail | null>;
  openNodeDetail: (point: string, options?: { topK?: number; authToken?: string }) => Promise<void>;
  closeNodeDetail: () => void;
  applyPathUpdate: (event: PathUpdateEvent) => void;
  applyNodeStatus: (point: string, mastery: number) => void;
  selectNodeViewModels: () => NodeViewModel[];
}

function deriveStatus(
  mastery: number,
  attempted: number,
  pathIndex: number,
  currentIndex: number,
): NodeStatus {
  if (mastery >= MASTERED_THRESHOLD) return "mastered";
  if (pathIndex === currentIndex) return "current";
  if (pathIndex < currentIndex) return "current";
  if (attempted > 0) return "current";
  return "locked";
}

function buildViewModels(
  path: LearningPathSnapshot | null,
  status: Record<string, LearningPathStatusItem>,
): NodeViewModel[] {
  if (!path) return [];
  const targets = new Set(path.targets);
  // The "current" frontier is the first node that is not yet mastered.
  let currentIndex = path.path.findIndex((point) => {
    const item = status[point];
    return !item || item.mastery < MASTERED_THRESHOLD;
  });
  if (currentIndex === -1) {
    currentIndex = path.path.length === 0 ? -1 : path.path.length - 1;
  }
  return path.path.map((point, index) => {
    const item = status[point];
    return {
      point,
      mastery: item?.mastery ?? 0,
      attempted: item?.attempted ?? 0,
      correct: item?.correct ?? 0,
      lastTriedAt: item?.lastTriedAt ?? null,
      isTarget: targets.has(point),
      status: deriveStatus(
        item?.mastery ?? 0,
        item?.attempted ?? 0,
        index,
        currentIndex,
      ),
    };
  });
}

export const useRecommendationsStore = create<RecommendationsState>(
  (set, get) => ({
    path: null,
    status: {},
    nodeDetailCache: {},
    loadingPath: false,
    pathError: null,
    loadingDetail: false,
    detailError: null,
    activeDetailPoint: null,
    recentlyAdded: [],
    recentlyRemoved: [],
    recentlyTouchedAt: null,

    loadPath: async (authToken) => {
      if (get().loadingPath) return;
      set({ loadingPath: true, pathError: null });
      try {
        const [snapshot, statusSnapshot] = await Promise.all([
          fetchLearningPath(authToken),
          fetchLearningPathStatus(authToken),
        ]);
        const statusByPoint: Record<string, LearningPathStatusItem> = {};
        for (const item of statusSnapshot.items) {
          statusByPoint[item.point] = item;
        }
        set({
          path: snapshot,
          status: statusByPoint,
          loadingPath: false,
          pathError: null,
        });
      } catch (error) {
        set({
          loadingPath: false,
          pathError:
            error instanceof Error ? error.message : "无法加载学习路径",
        });
      }
    },

    loadNodeDetail: async (point, options) => {
      const trimmed = point?.trim();
      if (!trimmed) return null;
      set({ loadingDetail: true, detailError: null });
      try {
        const detail = await fetchKnowledgeNodeDetail(trimmed, {
          topK: options?.topK,
          authToken: options?.authToken,
        });
        set((state) => ({
          loadingDetail: false,
          nodeDetailCache: {
            ...state.nodeDetailCache,
            [trimmed]: detail,
          },
        }));
        return detail;
      } catch (error) {
        set({
          loadingDetail: false,
          detailError:
            error instanceof Error ? error.message : "无法加载知识点详情",
        });
        return null;
      }
    },

    openNodeDetail: async (point, options) => {
      set({ activeDetailPoint: point });
      const cached = get().nodeDetailCache[point];
      if (!cached) {
        await get().loadNodeDetail(point, options);
      } else {
        // Refresh in background to keep stats fresh, but keep cached display.
        void get().loadNodeDetail(point, options);
      }
    },

    closeNodeDetail: () => {
      set({ activeDetailPoint: null, detailError: null });
    },

    applyPathUpdate: (event) => {
      const previousPath = event.previous?.path ?? get().path?.path ?? [];
      const nextPath = event.next.path;
      const diff: PathDiffEntry[] = diffPaths(previousPath, nextPath);
      const added = diff.filter((entry) => entry.kind === "added").map((entry) => entry.point);
      const removed = diff.filter((entry) => entry.kind === "removed").map((entry) => entry.point);
      set({
        path: event.next,
        recentlyAdded: added,
        recentlyRemoved: removed,
        recentlyTouchedAt: Date.now(),
      });
      // Schedule a clear-out for the entrance hint so animation effects don't
      // re-fire on subsequent renders.
      window.setTimeout(() => {
        const state = get();
        if (state.recentlyTouchedAt && Date.now() - state.recentlyTouchedAt >= ENTRY_HINT_TTL_MS) {
          set({ recentlyAdded: [], recentlyRemoved: [], recentlyTouchedAt: null });
        }
      }, ENTRY_HINT_TTL_MS + 50);
    },

    applyNodeStatus: (point, mastery) => {
      const trimmed = point?.trim();
      if (!trimmed) return;
      set((state) => {
        const existing = state.status[trimmed];
        const next: LearningPathStatusItem = {
          point: trimmed,
          mastery: Math.max(0, Math.min(1, mastery)),
          attempted: existing?.attempted ?? 0,
          correct: existing?.correct ?? 0,
          lastTriedAt: existing?.lastTriedAt ?? null,
        };
        return {
          status: {
            ...state.status,
            [trimmed]: next,
          },
        };
      });
    },

    selectNodeViewModels: () => buildViewModels(get().path, get().status),
  }),
);
