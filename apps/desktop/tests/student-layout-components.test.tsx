import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { StudentSidebar } from "@/components/layout/StudentSidebar";
import { MetricsPanel } from "@/components/metrics/MetricsPanel";
import { useChatStore } from "@/stores/chatStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useMetricsStore } from "@/stores/metricsStore";

const STUDENT_CSS = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

function getCssRule(selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`).exec(STUDENT_CSS);
  return match?.[1] ?? "";
}

describe("student shell components", () => {
  beforeEach(async () => {
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
    useChatStore.persist.setOptions({ name: "ascendany_chat_guest" });
    useChatStore.getState().resetForAccount();
    await useChatStore.persist.rehydrate();
    useLayoutStore.getState().resetForAccount();
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
    expect(sidebarRule).toContain("width: 224px");
    const activeRule = getCssRule(".student-session-item.is-active");
    expect(activeRule).not.toContain("background");
    const hoverRule = getCssRule(".student-session-item:hover");
    expect(hoverRule).toContain("background: var(--student-control-hover)");
    const selectRule = getCssRule(".student-session-select");
    expect(selectRule).toContain("align-items: center");
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

    const collapsedRule = getCssRule(".student-sidebar.is-collapsed");
    expect(collapsedRule).toContain("width: 0");
    expect(collapsedRule).toContain("padding: 0");
    expect(collapsedRule).toContain("border-right: 0");
  });

  it("shows only ability and history tabs in the right panel", () => {
    render(<MetricsPanel />);

    expect(screen.getByRole("button", { name: "能力" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "历史" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "导入" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "历史" }));
    expect(useLayoutStore.getState().activeRightPanelTab).toBe("history");
    expect(screen.getByText("月测表现分析")).toBeTruthy();
  });
});
