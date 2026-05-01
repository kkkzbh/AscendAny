import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppLayout } from "@/components/layout/AppLayout";
import { ChatInput } from "@/components/chat/ChatInput";
import { MessageList } from "@/components/chat/MessageList";
import { StudentSidebar } from "@/components/layout/StudentSidebar";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";
import { useAuthStore } from "@/stores/authStore";
import { useChatStore } from "@/stores/chatStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useMetricsStore } from "@/stores/metricsStore";
import { useSettingsStore } from "@/stores/settingsStore";

const STUDENT_CSS = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

function getCssRule(selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`).exec(STUDENT_CSS);
  return match?.[1] ?? "";
}

function dispatchPointerEvent(target: EventTarget, type: string, clientX: number) {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clientX", { value: clientX });
  target.dispatchEvent(event);
}

describe("student shell components", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
    localStorage.clear();
    useChatStore.getState().resetForAccount();
    useLayoutStore.getState().resetForAccount();
    useSettingsStore.getState().resetForAccount();
    useAuthStore.setState({
      status: "authenticated",
      account: {
        accountId: "student-1",
        username: "student",
        displayName: "student",
        studentId: "2023001",
        ptaNickname: "student",
        provisionSource: "local",
        localPasswordEnabled: false,
      },
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
      updaterGetState: vi.fn().mockResolvedValue({
        status: "idle",
        currentVersion: "0.1.0",
        latestVersion: null,
        progressPercent: null,
        lastCheckedAt: null,
        message: null,
      }),
      updaterOnStateChanged: vi.fn().mockImplementation(() => () => {}),
    };
    useMetricsStore.setState({
      metrics: {
        knowledge: 84,
        accuracy: 83,
        quality: 61,
        flexibility: 94,
        proficiency: 97,
      },
      metricMissing: {
        knowledge: false,
        accuracy: false,
        quality: false,
        flexibility: false,
        proficiency: false,
      },
      rating: {
        current: 1002,
        lastDelta: 202,
        history: [
          {
            examId: "11",
            examName: "月测表现分析",
            date: "2026-04-20",
            oldRating: 800,
            delta: 202,
            newRating: 1002,
          },
        ],
      },
      metricDelta: {
        latestExamId: "11",
        latestExamName: "月测表现分析",
        latestExamDate: "2026-04-20",
        baseline: "previous_exam",
        values: {
          knowledge: 84,
          accuracy: 83,
          quality: 61,
          flexibility: 94,
          proficiency: 97,
        },
      },
      progressExplanation: null,
      milestoneStreak: null,
      peerComparison: null,
      postExamSupport: null,
      identity: {
        studentId: "20230001",
        ptaNickname: "demo",
        noSubmissionRecords: false,
      },
      loading: false,
      error: null,
    });
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("keeps the sidebar scoped to student conversations and settings", () => {
    useChatStore.getState().addMessage("user", "分析最近一次考试");

    render(<StudentSidebar />);

    const collapseButton = screen.getByRole("button", { name: "折叠左侧栏" });
    expect(collapseButton).toBeTruthy();
    expect(collapseButton.classList.contains("student-titlebar-button")).toBe(true);
    expect(collapseButton.classList.contains("student-icon-button")).toBe(false);
    expect(screen.getByRole("button", { name: "新对话" })).toBeTruthy();
    expect(screen.getByRole("textbox")).toBeTruthy();
    expect(screen.getByRole("button", { name: "设置" })).toBeTruthy();
    expect(screen.queryByText("插件")).toBeNull();
    expect(screen.queryByText("自动化")).toBeNull();
    expect(screen.queryByText("项目")).toBeNull();

    const sessionButton = screen
      .getAllByRole("button", { name: /分析最近一次考试/ })
      .find((button) => button.classList.contains("student-session-select"));
    expect(sessionButton).toBeTruthy();
    expect(sessionButton.querySelector("svg")).toBeNull();
    expect(sessionButton.querySelector(".student-session-title")).toBeTruthy();
    expect(sessionButton.querySelector(".student-session-time")).toBeTruthy();

    const sidebarRule = getCssRule(".student-sidebar");
    expect(sidebarRule).toContain("width: calc(100vw * var(--student-left-sidebar-ratio, 0.22))");
    expect(screen.getByRole("separator", { name: "调整左侧栏宽度" })).toBeTruthy();
    const activeRule = getCssRule(".student-session-item.is-active");
    expect(activeRule).not.toContain("background");
    const hoverRule = getCssRule(".student-session-item:hover");
    expect(hoverRule).toContain("background: var(--student-control-hover)");
    const transparentAppRule = getCssRule(':root[data-opaque-sidebar="false"] .student-app');
    expect(transparentAppRule).toContain("--student-control-hover: var(--student-transparent-control-hover)");
    const selectRule = getCssRule(".student-session-select");
    expect(selectRule).toContain("align-items: center");
  });

  it("returns to the initial chat surface without creating another session", () => {
    useChatStore.getState().addMessage("user", "分析最近一次考试");

    render(
      <>
        <StudentSidebar />
        <MessageList />
      </>,
    );

    fireEvent.click(screen.getByRole("button", { name: "新对话" }));

    expect(useChatStore.getState().sessions).toHaveLength(1);
    expect(useChatStore.getState().activeSessionId).toBeNull();
    expect(screen.getByText("开始对话")).toBeTruthy();
  });

  it("restores separate drafts for the blank composer and real sessions", () => {
    useChatStore.getState().addMessage("user", "分析最近一次考试");
    useChatStore.getState().setCurrentDraft("会话草稿");
    useChatStore.getState().startNewSessionDraft();
    useChatStore.getState().setCurrentDraft("初始界面草稿");

    render(
      <>
        <StudentSidebar />
        <ChatInput />
      </>,
    );

    const messageInput = () => screen.getByRole("textbox", { name: "消息输入" });

    expect(messageInput()).toHaveProperty("value", "初始界面草稿");

    const sessionButton = screen
      .getAllByRole("button", { name: /分析最近一次考试/ })
      .find((button) => button.classList.contains("student-session-select"));
    expect(sessionButton).toBeTruthy();
    fireEvent.click(sessionButton!);
    expect(messageInput()).toHaveProperty("value", "会话草稿");

    fireEvent.click(screen.getByRole("button", { name: "新对话" }));
    expect(messageInput()).toHaveProperty("value", "初始界面草稿");
  });

  it("fully hides the sidebar content when collapsed", () => {
    useLayoutStore.getState().setLeftSidebarCollapsed(true);

    render(<StudentSidebar />);

    const sidebar = document.querySelector(".student-sidebar");
    expect(sidebar?.classList.contains("is-collapsed")).toBe(true);
    expect(sidebar?.textContent).toBe("");
    expect(screen.queryByRole("button", { name: "展开左侧栏" })).toBeNull();
    expect(screen.queryByRole("button", { name: "新对话" })).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "设置" })).toBeNull();
    expect(screen.queryByRole("separator", { name: "调整左侧栏宽度" })).toBeNull();

    const collapsedRule = getCssRule(".student-sidebar.is-collapsed");
    expect(collapsedRule).toContain("width: 0");
    expect(collapsedRule).toContain("padding: 0");
    expect(collapsedRule).toContain("border-right: 0");
    expect(collapsedRule).toContain("box-shadow: none");
  });

  it("resizes the student sidebar with pointer and keyboard controls", () => {
    render(<StudentSidebar />);

    const resizer = screen.getByRole("separator", { name: "调整左侧栏宽度" });
    dispatchPointerEvent(resizer, "pointerdown", 260);
    dispatchPointerEvent(window, "pointermove", 320);
    dispatchPointerEvent(window, "pointerup", 320);
    expect(useLayoutStore.getState().leftSidebarRatio).toBe(0.3125);
    expect(document.body.style.cursor).toBe("");
    expect(document.body.style.userSelect).toBe("");

    fireEvent.keyDown(resizer, { key: "ArrowLeft" });
    expect(useLayoutStore.getState().leftSidebarRatio).toBe(0.3025);

    fireEvent.keyDown(resizer, { key: "ArrowRight", shiftKey: true });
    expect(useLayoutStore.getState().leftSidebarRatio).toBe(0.32);
  });

  it("shows the ability, history, and notes tabs in the right panel", () => {
    render(<MetricsPanel />);

    expect(screen.getByRole("button", { name: "能力" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "历史" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "笔记" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "导入" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "历史" }));
    expect(useLayoutStore.getState().activeRightPanelTab).toBe("history");
    expect(screen.getByText("月测表现分析")).toBeTruthy();
  });

  it("switches the main app shell to the settings workspace", () => {
    useSettingsStore.getState().openSettings();

    render(<AppLayout />);

    expect(screen.getByRole("button", { name: "返回应用" })).toBeTruthy();
    expect(screen.getByText("通用设置")).toBeTruthy();
    expect(screen.queryByPlaceholderText("输入消息，按 Enter 发送...")).toBeNull();
    expect(screen.queryByRole("button", { name: "能力" })).toBeNull();
    expect(screen.queryByRole("separator", { name: "调整左侧栏宽度" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "返回应用" }));
    expect(useSettingsStore.getState().isOpen).toBe(false);
  });
});
