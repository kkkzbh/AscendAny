import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

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
  logout: vi.fn(),
}));

vi.mock("./session/SessionContext", () => ({ useSession: () => sessionState }));
vi.mock("./components/AuthScreen", () => ({ AuthScreen: () => <p>auth-view</p> }));
vi.mock("./components/AnalyticsView", () => ({ AnalyticsView: () => <p>analytics-view</p> }));
vi.mock("./components/LeaderboardView", () => ({ LeaderboardView: () => <p>leaderboard-view</p> }));
vi.mock("./components/ChatView", () => ({ ChatView: () => <p>chat-view</p> }));
vi.mock("./components/OjView", () => ({ OjView: () => <p>oj-view</p> }));
vi.mock("./components/ExamCatalogView", () => ({ ExamCatalogView: () => <p>exams-view</p> }));
vi.mock("./components/AccountView", () => ({ AccountView: () => <p>account-view</p> }));

describe("mobile application navigation", () => {
  afterEach(cleanup);

  beforeEach(() => {
    sessionState.account.role = "student";
    sessionState.account.studentNumber = "20260001";
  });

  it("keeps student Analytics and Chat reachable while adding OJ", () => {
    render(<App />);
    expect(screen.getByText("analytics-view")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /助手/ }));
    expect(screen.getByText("chat-view")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /评测/ }));
    expect(screen.getByText("oj-view")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /画像/ }));
    expect(screen.getByText("analytics-view")).toBeTruthy();
  });

  it("makes OJ available to administrators without exposing student tabs", () => {
    sessionState.account.role = "admin";
    sessionState.account.studentNumber = null;
    render(<App />);

    expect(screen.getByText("exams-view")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /助手/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /评测/ }));
    expect(screen.getByText("oj-view")).toBeTruthy();
  });
});
