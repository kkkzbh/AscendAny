import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "../src/App";

const sessionState = vi.hoisted(() => ({
  status: "authenticated" as const,
  account: {
    id: "123e4567-e89b-42d3-a456-426614174000",
    username: "student-1",
    displayName: "学生一",
    studentNumber: "20260001" as string | null,
    role: "student" as "student" | "admin",
    authRevision: 1,
  },
  error: null,
  session: { marker: "desktop-browser-session" },
  logout: vi.fn(),
}));

vi.mock("../src/session/context", () => ({ useSession: () => sessionState }));
vi.mock("../src/components/WindowTitleBar", () => ({ WindowTitleBar: () => null }));
vi.mock("../src/components/AuthScreen", () => ({ AuthScreen: () => <p>auth-view</p> }));
vi.mock("../src/components/AnalyticsView", () => ({ AnalyticsView: () => <p>analytics-view</p> }));
vi.mock("../src/components/LeaderboardView", () => ({ LeaderboardView: () => <p>leaderboard-view</p> }));
vi.mock("../src/components/ChatView", () => ({ ChatView: () => <p>chat-view</p> }));
vi.mock("../src/components/ExamCatalogView", () => ({ ExamCatalogView: () => <p>exams-view</p> }));
vi.mock("../src/components/OjView", () => ({ OjView: () => <p>oj-view</p> }));
vi.mock("../src/components/AccountView", () => ({ AccountView: () => <p>account-view</p> }));
vi.mock("../src/components/UpdaterView", () => ({ UpdaterView: () => <p>updates-view</p> }));

describe("desktop application navigation", () => {
  afterEach(cleanup);

  beforeEach(() => {
    sessionState.account.role = "student";
    sessionState.account.studentNumber = "20260001";
  });

  it("keeps student Analytics and Chat reachable while adding exams and OJ", () => {
    render(<App />);
    expect(screen.getByText("analytics-view")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /学习助手/ }));
    expect(screen.getByText("chat-view")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /考试分析/ }));
    expect(screen.getByText("exams-view")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /在线评测/ }));
    expect(screen.getByText("oj-view")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /能力画像/ }));
    expect(screen.getByText("analytics-view")).toBeTruthy();
  });

  it("makes exams and OJ available to administrators without exposing student tabs", () => {
    sessionState.account.role = "admin";
    sessionState.account.studentNumber = null;
    render(<App />);

    expect(screen.getByText("account-view")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /学习助手/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /考试分析/ }));
    expect(screen.getByText("exams-view")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /在线评测/ }));
    expect(screen.getByText("oj-view")).toBeTruthy();
  });
});
