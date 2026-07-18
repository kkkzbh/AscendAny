import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { streamDataEvents } = vi.hoisted(() => ({
  streamDataEvents: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  streamDataEvents,
}));

import { useDataFreshnessWatcher } from "@/hooks/useDataFreshnessWatcher";
import { useAchievementsStore } from "@/stores/achievementsStore";
import { useAuthStore } from "@/stores/authStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useLeaderboardStore } from "@/stores/leaderboardStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { useRecommendationsStore } from "@/stores/recommendationsStore";
import type { DataFreshnessEvent } from "@/lib/api";

const originalLoadDashboard = useMetricsStore.getState().loadDashboard;
const originalLoadPath = useRecommendationsStore.getState().loadPath;
const originalClearNodeDetailCache = useRecommendationsStore.getState().clearNodeDetailCache;
const originalLoadAchievements = useAchievementsStore.getState().loadAchievements;

function WatcherHost() {
  useDataFreshnessWatcher();
  return null;
}

function pendingStream() {
  return new Promise<void>(() => {});
}

describe("useDataFreshnessWatcher", () => {
  const loadDashboard = vi.fn().mockResolvedValue(undefined);
  const loadPath = vi.fn().mockResolvedValue(undefined);
  const clearNodeDetailCache = vi.fn();
  const loadAchievements = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    streamDataEvents.mockReset();
    loadDashboard.mockClear();
    loadPath.mockClear();
    clearNodeDetailCache.mockClear();
    loadAchievements.mockClear();
    useAuthStore.setState({
      status: "authenticated",
      account: {
        accountId: "student-1",
        username: "student",
        displayName: "student",
        studentId: "20231202051",
        ptaNickname: "kkkzbh",
        provisionSource: "local",
        localPasswordEnabled: false,
      },
      accessToken: "token-1",
      initialized: true,
    });
    useMetricsStore.setState({ loadDashboard });
    useRecommendationsStore.setState({
      loadPath,
      clearNodeDetailCache,
      nodeDetailCache: {},
    });
    useAchievementsStore.setState({ loadAchievements });
    useLayoutStore.setState({ activeFullscreenView: "achievements" });
    useLeaderboardStore.setState({
      isOpen: true,
      refreshSeq: 0,
    });
  });

  afterEach(() => {
    cleanup();
    useMetricsStore.setState({ loadDashboard: originalLoadDashboard });
    useRecommendationsStore.setState({
      loadPath: originalLoadPath,
      clearNodeDetailCache: originalClearNodeDetailCache,
    });
    useAchievementsStore.setState({ loadAchievements: originalLoadAchievements });
    useLayoutStore.setState({ activeFullscreenView: "none" });
    useLeaderboardStore.setState({ isOpen: false, refreshSeq: 0 });
    vi.restoreAllMocks();
  });

  it("ignores the initial snapshot and refreshes on a later data change", async () => {
    streamDataEvents.mockImplementation(
      (onEvent: (event: DataFreshnessEvent) => void) => {
        onEvent({ type: "snapshot", latestExamImportedAt: "2026-02-13T09:30:00+00:00" });
        onEvent({ type: "data_changed", latestExamImportedAt: "2026-02-14T10:15:00+00:00" });
        return pendingStream();
      },
    );

    render(<WatcherHost />);

    await waitFor(() => expect(loadDashboard).toHaveBeenCalledTimes(1));
    expect(loadDashboard).toHaveBeenCalledWith({
      studentId: "20231202051",
      ptaNickname: "kkkzbh",
      authToken: "token-1",
    });
    expect(clearNodeDetailCache).toHaveBeenCalledTimes(1);
    expect(loadPath).toHaveBeenCalledWith("token-1");
    expect(loadAchievements).toHaveBeenCalledWith({
      studentId: "20231202051",
      ptaNickname: "kkkzbh",
      authToken: "token-1",
      force: true,
    });
    expect(useLeaderboardStore.getState().refreshSeq).toBe(1);
  });

  it("does not refresh for the first snapshot or repeated timestamps", async () => {
    streamDataEvents.mockImplementation(
      (onEvent: (event: DataFreshnessEvent) => void) => {
        onEvent({ type: "snapshot", latestExamImportedAt: "2026-02-13T09:30:00+00:00" });
        onEvent({ type: "data_changed", latestExamImportedAt: "2026-02-13T09:30:00+00:00" });
        return pendingStream();
      },
    );

    render(<WatcherHost />);

    await waitFor(() => expect(streamDataEvents).toHaveBeenCalledTimes(1));
    expect(loadDashboard).not.toHaveBeenCalled();
    expect(loadPath).not.toHaveBeenCalled();
    expect(loadAchievements).not.toHaveBeenCalled();
    expect(useLeaderboardStore.getState().refreshSeq).toBe(0);
  });
});
