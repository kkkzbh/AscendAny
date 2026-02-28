import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  AchievementFullscreen,
  getAchievementTierClass,
} from "@/components/achievements/AchievementFullscreen";
import type { StudentAchievementsData } from "@/types/achievements";

const SAMPLE_DATA: StudentAchievementsData = {
  identity: {
    studentId: "20230001",
    ptaNickname: "Alice",
    noSubmissionRecords: false,
  },
  summary: {
    total: 2,
    locked: 1,
    bronze: 0,
    silver: 0,
    gold: 1,
  },
  items: [
    {
      code: "locked_item",
      title: "未解锁",
      description: "未达成条件",
      tier: 0,
      progress: 0,
      bronzeTarget: 1,
      silverTarget: 2,
      goldTarget: 3,
      sortOrder: 1,
    },
    {
      code: "gold_item",
      title: "金色成就",
      description: "达成最高等级",
      tier: 3,
      progress: 99,
      bronzeTarget: 10,
      silverTarget: 50,
      goldTarget: 90,
      sortOrder: 2,
    },
  ],
};

describe("AchievementFullscreen", () => {
  afterEach(() => {
    cleanup();
  });

  it("maps tier classes correctly", () => {
    expect(getAchievementTierClass(0)).toContain("achievement-tier-locked");
    expect(getAchievementTierClass(1)).toContain("achievement-tier-bronze");
    expect(getAchievementTierClass(2)).toContain("achievement-tier-silver");
    expect(getAchievementTierClass(3)).toContain("achievement-tier-gold");
  });

  it("supports close by red button and Esc", () => {
    const onClose = vi.fn();
    render(
      <AchievementFullscreen
        isOpen
        onClose={onClose}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "关闭成就页" }));
    fireEvent.keyDown(window, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("renders locked and gold cards", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    const locked = screen.getByText("未解锁").closest("article");
    const gold = screen.getByText("金色成就").closest("article");
    expect(locked?.className).toContain("achievement-tier-locked");
    expect(gold?.className).toContain("achievement-tier-gold");
  });

  it("supports wheel panning and pointer drag hooks", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    const viewport = screen.getByTestId("achievement-viewport");
    const canvas = screen.getByTestId("achievement-canvas");
    expect(canvas.getAttribute("style")).toContain("translate(120px, 96px)");

    fireEvent.wheel(viewport, { deltaX: 20, deltaY: 10 });
    expect(canvas.getAttribute("style")).toContain("translate(100px, 86px) scale(1)");

    fireEvent.pointerDown(viewport, {
      pointerId: 1,
      button: 0,
      clientX: 120,
      clientY: 120,
    });
    fireEvent.pointerMove(viewport, {
      pointerId: 1,
      clientX: 150,
      clientY: 160,
    });
    fireEvent.pointerUp(viewport, { pointerId: 1 });

    expect(canvas.getAttribute("style")).toContain("translate(");
  });

  it("supports Ctrl + wheel zoom with min/max limits", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    const viewport = screen.getByTestId("achievement-viewport");
    const canvas = screen.getByTestId("achievement-canvas");
    expect(canvas.getAttribute("style")).toContain("scale(1)");

    fireEvent.wheel(viewport, { deltaY: -100, ctrlKey: true });
    const styleAfterZoomIn = canvas.getAttribute("style") ?? "";
    expect(styleAfterZoomIn).toContain("translate(120px, 96px)");
    expect(styleAfterZoomIn).toMatch(/scale\(1\.[0-9]+\)/);

    fireEvent.wheel(viewport, { deltaY: 30000, ctrlKey: true });
    expect(canvas.getAttribute("style")).toContain("scale(0.65)");

    fireEvent.wheel(viewport, { deltaY: -30000, ctrlKey: true });
    expect(canvas.getAttribute("style")).toContain("scale(1.85)");
  });
});
