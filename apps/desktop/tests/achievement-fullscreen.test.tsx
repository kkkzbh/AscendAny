import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AchievementFullscreen } from "@/components/achievements/AchievementFullscreen";
import type { StudentAchievementsData } from "@/types/achievements";

const SAMPLE_DATA: StudentAchievementsData = {
  identity: {
    studentId: "20230001",
    ptaNickname: "Alice",
    noSubmissionRecords: false,
  },
  summary: {
    total: 5,
    locked: 1,
    bronze: 1,
    silver: 1,
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
      description: "金色目标达到 10 / 50 / 90 次。",
      tier: 3,
      progress: 99,
      bronzeTarget: 10,
      silverTarget: 50,
      goldTarget: 90,
      sortOrder: 2,
    },
    {
      code: "bronze_item",
      title: "铜色起步",
      description: "累计参赛次数达到 1 / 10 / 50 场。",
      tier: 1,
      progress: 5,
      bronzeTarget: 1,
      silverTarget: 10,
      goldTarget: 50,
      sortOrder: 3,
    },
    {
      code: "silver_item",
      title: "银色进阶",
      description: "稳定连涨达到 2 / 4 / 6 场。",
      tier: 2,
      progress: 5,
      bronzeTarget: 2,
      silverTarget: 4,
      goldTarget: 6,
      sortOrder: 4,
    },
    {
      code: "inprogress_item",
      title: "稳步前进",
      description: "持续提交达到 5 / 20 / 100 次。",
      tier: 0,
      progress: 3,
      bronzeTarget: 5,
      silverTarget: 20,
      goldTarget: 100,
      sortOrder: 5,
    },
  ],
};

function rowForTitle(title: string): HTMLElement {
  const titleEl = screen.getByText(title);
  const row = titleEl.closest(".achievement-row");
  expect(row).toBeTruthy();
  return row as HTMLElement;
}

describe("AchievementFullscreen", () => {
  afterEach(() => {
    cleanup();
  });

  it("renders nothing when closed", () => {
    const { container } = render(
      <AchievementFullscreen
        isOpen={false}
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("supports close via Esc and back button", () => {
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

    fireEvent.click(screen.getByRole("button", { name: "返回应用" }));
    fireEvent.keyDown(window, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("renders all achievements with tier chips", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    expect(screen.getByText("金色成就")).toBeTruthy();
    expect(screen.getByText("铜色起步")).toBeTruthy();
    expect(screen.getByText("银色进阶")).toBeTruthy();
    expect(screen.getByText("稳步前进")).toBeTruthy();
    expect(screen.getByText("未解锁")).toBeTruthy();
  });

  it("hides locked description and shows placeholder", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    expect(screen.queryByText("未达成条件")).toBeNull();
    expect(screen.getByText("达成后解锁")).toBeTruthy();
  });

  it("discloses descriptions only up to the earned tier", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    const inprogress = rowForTitle("稳步前进");
    expect(inprogress.textContent).not.toContain("持续提交达到");
    expect(inprogress.textContent).toContain("3 / 5");

    const bronze = rowForTitle("铜色起步");
    expect(bronze.textContent).toContain("累计参赛次数达到 1 场。");
    expect(bronze.textContent).not.toContain("1 / 10");
    expect(bronze.textContent).toContain("5 / 1");

    const silver = rowForTitle("银色进阶");
    expect(silver.textContent).toContain("稳定连涨达到 2 / 4 场。");
    expect(silver.textContent).not.toContain("2 / 4 / 6");
    expect(silver.textContent).toContain("5 / 4");

    const gold = rowForTitle("金色成就");
    expect(gold.textContent).toContain("金色目标达到 10 / 50 / 90 次。");
    expect(gold.textContent).toContain("99 / 90");
  });

  it("filters by category when nav item is clicked", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /金牌\s*1/ }));
    expect(screen.getByText("金色成就")).toBeTruthy();
    expect(screen.queryByText("铜色起步")).toBeNull();
    expect(screen.queryByText("稳步前进")).toBeNull();
  });

  it("keeps in-progress items in progress category without exposing hidden targets", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /进行中\s*1/ }));
    expect(screen.getByText("稳步前进")).toBeTruthy();
    expect(screen.queryByText("持续提交达到 5 / 20 / 100 次。")).toBeNull();
    expect(screen.getByText("3 / 5")).toBeTruthy();
  });

  it("filters list by search query and auto-selects matching tier nav", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    fireEvent.change(screen.getByLabelText("搜索成就"), {
      target: { value: "金色" },
    });

    const titleByContent = (text: string) =>
      screen.getByText((_, el) =>
        el?.classList?.contains("achievement-row-title") === true
        && (el.textContent ?? "").trim() === text,
      );

    expect(titleByContent("金色成就")).toBeTruthy();
    expect(screen.queryByText("铜色起步")).toBeNull();

    const goldNav = screen.getByRole("button", { name: /金牌\s*1/ });
    expect(goldNav.className).toContain("is-active");
  });

  it("does not search hidden description targets", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    fireEvent.change(screen.getByLabelText("搜索成就"), {
      target: { value: "100" },
    });

    expect(screen.queryByText("稳步前进")).toBeNull();
    expect(screen.getByText("没有匹配的成就")).toBeTruthy();
  });

  it("clears search via the clear button", () => {
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={SAMPLE_DATA}
        loading={false}
        error={null}
      />,
    );

    const input = screen.getByLabelText("搜索成就") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "金色" } });
    expect(input.value).toBe("金色");

    fireEvent.click(screen.getByRole("button", { name: "清除搜索" }));
    expect(input.value).toBe("");
    expect(screen.getByText("铜色起步")).toBeTruthy();
  });

  it("shows error state with retry", () => {
    const onRetry = vi.fn();
    render(
      <AchievementFullscreen
        isOpen
        onClose={() => {}}
        data={null}
        loading={false}
        error="加载失败"
        onRetry={onRetry}
      />,
    );

    expect(screen.getByText("加载失败")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
