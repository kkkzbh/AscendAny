import { cleanup, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { LeaderboardWorkspace } from "@/components/leaderboard/LeaderboardWorkspace";
import { useAuthStore } from "@/stores/authStore";
import { useLeaderboardStore } from "@/stores/leaderboardStore";

const STUDENT_CSS = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

function getCssRule(selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`).exec(STUDENT_CSS);
  return match?.[1] ?? "";
}

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    fetchStudentsLeaderboard: vi.fn(() => new Promise(() => {})),
  };
});

describe("LeaderboardWorkspace", () => {
  beforeEach(() => {
    useLeaderboardStore.getState().openLeaderboard();
    useAuthStore.setState({
      status: "authenticated",
      account: null,
      accessToken: null,
      refreshToken: null,
      initialized: true,
      rememberPassword: true,
    });
    window.electronAPI = {
      platform: "linux",
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
    };
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    useLeaderboardStore.getState().closeLeaderboard();
  });

  it("shows the loading skeleton on the first open frame", () => {
    render(<LeaderboardWorkspace />);

    expect(document.querySelectorAll(".leaderboard-skeleton-row")).toHaveLength(8);
    expect(screen.queryByText("暂无排行数据")).toBeNull();
  });

  it("does not fade in the full workspace", () => {
    expect(getCssRule(".leaderboard-workspace")).not.toContain("animation");
    expect(STUDENT_CSS).not.toContain("@keyframes leaderboard-fade-in");
  });
});
