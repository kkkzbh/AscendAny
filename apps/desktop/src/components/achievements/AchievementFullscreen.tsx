import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";

import type {
  AchievementItem,
  StudentAchievementsData,
} from "@/types/achievements";

interface AchievementFullscreenProps {
  isOpen: boolean;
  onClose: () => void;
  data: StudentAchievementsData | null;
  loading: boolean;
  error: string | null;
  onRetry?: () => void;
}

type TierKey = "gold" | "silver" | "bronze" | "inprogress" | "locked";

type NavKey =
  | "all"
  | "earned"
  | "inprogress"
  | "locked"
  | "gold"
  | "silver"
  | "bronze";

const NAV_LABEL: Record<NavKey, string> = {
  all: "全部",
  earned: "已获得",
  inprogress: "进行中",
  locked: "已锁定",
  gold: "金牌",
  silver: "银牌",
  bronze: "铜牌",
};

const TIER_LABEL: Record<TierKey, string> = {
  gold: "金牌",
  silver: "银牌",
  bronze: "铜牌",
  inprogress: "进行中",
  locked: "锁定",
};

const TIER_GLYPH: Record<TierKey, string> = {
  gold: "金",
  silver: "银",
  bronze: "铜",
  inprogress: "·",
  locked: "锁",
};

function tierKeyOf(item: AchievementItem): TierKey {
  if (item.tier >= 3) return "gold";
  if (item.tier === 2) return "silver";
  if (item.tier === 1) return "bronze";
  if (item.progress > 0) return "inprogress";
  return "locked";
}

function nextTarget(item: AchievementItem): number {
  if (item.tier >= 3) return item.goldTarget;
  if (item.tier === 2) return item.silverTarget;
  if (item.tier === 1) return item.bronzeTarget;
  return item.bronzeTarget;
}

function progressPercent(item: AchievementItem): number {
  if (item.tier >= 3) return 100;
  const target = nextTarget(item);
  if (!target || target <= 0) return 0;
  return Math.min(100, Math.max(0, (item.progress / target) * 100));
}

function matchesNav(item: AchievementItem, nav: NavKey): boolean {
  if (nav === "all") return true;
  if (nav === "earned") return item.tier >= 1;
  return tierKeyOf(item) === nav;
}

function formatTarget(value: number): string {
  if (!Number.isFinite(value)) return "";
  return Number.isInteger(value) ? String(value) : String(value);
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function disclosedDescription(item: AchievementItem): string {
  if (item.tier <= 0) return "";
  if (item.tier >= 3) return item.description;

  const bronze = formatTarget(item.bronzeTarget);
  const silver = formatTarget(item.silverTarget);
  const gold = formatTarget(item.goldTarget);
  if (!bronze || !silver || !gold) return "";

  const exposedTargets = item.tier === 1 ? bronze : `${bronze} / ${silver}`;
  const targetSequence = new RegExp(
    `${escapeRegExp(bronze)}\\s*/\\s*${escapeRegExp(silver)}\\s*/\\s*${escapeRegExp(gold)}`,
  );
  return item.description.replace(targetSequence, exposedTargets);
}

function searchMatches(item: AchievementItem, queryLower: string): boolean {
  if (!queryLower) return true;
  if (item.title.toLowerCase().includes(queryLower)) return true;
  const description = disclosedDescription(item);
  if (!description) return false;
  return description.toLowerCase().includes(queryLower);
}

function highlight(text: string, queryLower: string): ReactNode {
  if (!queryLower) return text;
  const lower = text.toLowerCase();
  const nodes: ReactNode[] = [];
  let cursor = 0;
  let key = 0;
  while (cursor < text.length) {
    const idx = lower.indexOf(queryLower, cursor);
    if (idx < 0) {
      nodes.push(<Fragment key={key++}>{text.slice(cursor)}</Fragment>);
      break;
    }
    if (idx > cursor) {
      nodes.push(<Fragment key={key++}>{text.slice(cursor, idx)}</Fragment>);
    }
    nodes.push(
      <mark key={key++} className="achievement-mark">
        {text.slice(idx, idx + queryLower.length)}
      </mark>,
    );
    cursor = idx + queryLower.length;
  }
  return <>{nodes}</>;
}

function WindowControls() {
  const api = window.electronAPI;
  return (
    <div className="student-window-controls" aria-label="窗口控制">
      <button
        type="button"
        onClick={() => api?.minimize()}
        className="ui-window-button ui-window-traffic ui-window-minimize student-titlebar-traffic"
        title="最小化"
        aria-label="最小化"
      >
        <span className="ui-window-dot-symbol" aria-hidden="true">−</span>
      </button>
      <button
        type="button"
        onClick={() => api?.maximize()}
        className="ui-window-button ui-window-traffic ui-window-maximize student-titlebar-traffic"
        title="最大化"
        aria-label="最大化"
      >
        <span className="ui-window-dot-symbol" aria-hidden="true">+</span>
      </button>
      <button
        type="button"
        onClick={() => api?.close()}
        className="ui-window-button ui-window-traffic ui-window-close student-titlebar-traffic"
        title="关闭"
        aria-label="关闭"
      >
        <span className="ui-window-dot-symbol" aria-hidden="true">×</span>
      </button>
    </div>
  );
}

interface NavItemProps {
  nav: NavKey;
  active: NavKey;
  count: number;
  onSelect: (nav: NavKey) => void;
}

function NavItem({ nav, active, count, onSelect }: NavItemProps) {
  const isActive = active === nav;
  return (
    <button
      type="button"
      onClick={() => onSelect(nav)}
      className={`achievement-nav-item achievement-nav-item--${nav}${isActive ? " is-active" : ""}`}
    >
      <span className="achievement-nav-dot" aria-hidden="true" />
      <span className="achievement-nav-name">{NAV_LABEL[nav]}</span>
      <span className="achievement-nav-count">{count}</span>
    </button>
  );
}

interface AchievementRowProps {
  item: AchievementItem;
  queryLower: string;
}

function AchievementRow({ item, queryLower }: AchievementRowProps) {
  const tierKey = tierKeyOf(item);
  const locked = tierKey === "locked";
  const description = disclosedDescription(item);
  const percent = progressPercent(item);
  const current = Math.floor(item.progress);
  const target = Math.floor(nextTarget(item));

  return (
    <li className={`achievement-row achievement-row--${tierKey}`}>
      <div
        className={`achievement-row-icon achievement-row-icon--${tierKey}`}
        aria-hidden="true"
      >
        {TIER_GLYPH[tierKey]}
      </div>
      <div className="achievement-row-body">
        <div className="achievement-row-title">
          {highlight(item.title, queryLower)}
        </div>
        <div className="achievement-row-description">
          {description
            ? highlight(description, queryLower)
            : locked
              ? "达成后解锁"
              : ""}
        </div>
      </div>
      <div className="achievement-row-meta">
        <span className={`achievement-tier-chip achievement-tier-chip--${tierKey}`}>
          {TIER_LABEL[tierKey]}
        </span>
        {!locked && target > 0 && (
          <div className="achievement-row-progress">
            <div className="achievement-progress">
              <div
                className={`achievement-progress-fill achievement-progress-fill--${tierKey}`}
                style={{ width: `${percent}%` }}
              />
            </div>
            <span className="achievement-row-progress-text">
              {current} / {target}
            </span>
          </div>
        )}
      </div>
    </li>
  );
}

export function AchievementFullscreen({
  isOpen,
  onClose,
  data,
  loading,
  error,
  onRetry,
}: AchievementFullscreenProps) {
  const [query, setQuery] = useState("");
  const [activeNav, setActiveNav] = useState<NavKey>("all");

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    setQuery("");
    setActiveNav("all");
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [isOpen, onClose]);

  const items = useMemo(() => {
    const raw = data?.items ?? [];
    return [...raw].sort((a, b) => {
      if (a.sortOrder !== b.sortOrder) {
        return a.sortOrder - b.sortOrder;
      }
      return a.code.localeCompare(b.code);
    });
  }, [data]);

  const queryLower = query.trim().toLowerCase();

  const navCounts = useMemo(() => {
    const counts: Record<NavKey, number> = {
      all: 0,
      earned: 0,
      inprogress: 0,
      locked: 0,
      gold: 0,
      silver: 0,
      bronze: 0,
    };
    for (const item of items) {
      counts.all += 1;
      if (item.tier >= 1) counts.earned += 1;
      const key = tierKeyOf(item);
      if (key === "inprogress") counts.inprogress += 1;
      else if (key === "locked") counts.locked += 1;
      else if (key === "gold") counts.gold += 1;
      else if (key === "silver") counts.silver += 1;
      else if (key === "bronze") counts.bronze += 1;
    }
    return counts;
  }, [items]);

  const autoNav = useMemo<NavKey | null>(() => {
    if (!queryLower) return null;
    const tierSet = new Set<TierKey>();
    for (const item of items) {
      if (searchMatches(item, queryLower)) {
        tierSet.add(tierKeyOf(item));
      }
    }
    if (tierSet.size === 0) return "all";
    if (tierSet.size === 1) {
      const [only] = [...tierSet];
      return only ?? "all";
    }
    return "all";
  }, [items, queryLower]);

  useEffect(() => {
    if (autoNav) {
      setActiveNav(autoNav);
    }
  }, [autoNav]);

  const filteredItems = useMemo(() => {
    return items.filter(
      (item) => matchesNav(item, activeNav) && searchMatches(item, queryLower),
    );
  }, [items, activeNav, queryLower]);

  const totalAll = navCounts.all;
  const totalEarned = navCounts.earned;
  const overallPercent =
    totalAll > 0 ? Math.round((totalEarned / totalAll) * 100) : 0;

  if (!isOpen) {
    return null;
  }

  const showInitialLoading = loading && items.length === 0;
  const showErrorState = !loading && Boolean(error) && items.length === 0;
  const showEmptyData = !loading && !error && items.length === 0;
  const showSearchEmpty =
    !showInitialLoading
    && !showErrorState
    && !showEmptyData
    && filteredItems.length === 0;

  return (
    <div className="achievement-workspace" role="dialog" aria-modal="true">
      <aside className="achievement-sidebar">
        <div className="achievement-sidebar-top drag-region">
          <button
            type="button"
            onClick={onClose}
            className="achievement-return-button no-drag"
            aria-label="返回应用"
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="m15 18-6-6 6-6" />
              <path d="M21 12H9" />
            </svg>
            <span>返回应用</span>
          </button>
        </div>

        <div className="achievement-sidebar-scroll no-drag">
          <label className="achievement-search">
            <svg
              className="achievement-search-icon"
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <circle cx="11" cy="11" r="7" />
              <path d="m21 21-4.3-4.3" />
            </svg>
            <input
              type="text"
              className="achievement-search-input"
              placeholder="搜索成就…"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              aria-label="搜索成就"
            />
            {query && (
              <button
                type="button"
                className="achievement-search-clear"
                onClick={() => {
                  setQuery("");
                  setActiveNav("all");
                }}
                aria-label="清除搜索"
              >
                ×
              </button>
            )}
          </label>

          <div className="achievement-summary-card">
            <div className="achievement-summary-head">
              <span className="achievement-summary-label">总进度</span>
              <span className="achievement-summary-percent">
                {overallPercent}%
              </span>
            </div>
            <div className="achievement-progress achievement-progress--summary">
              <div
                className="achievement-progress-fill achievement-progress-fill--accent"
                style={{ width: `${overallPercent}%` }}
              />
            </div>
            <div className="achievement-summary-foot">
              已获得 {totalEarned} / {totalAll}
            </div>
          </div>

          <nav className="achievement-nav">
            <div className="achievement-nav-label">按状态</div>
            <NavItem
              nav="all"
              active={activeNav}
              count={navCounts.all}
              onSelect={setActiveNav}
            />
            <NavItem
              nav="earned"
              active={activeNav}
              count={navCounts.earned}
              onSelect={setActiveNav}
            />
            <NavItem
              nav="inprogress"
              active={activeNav}
              count={navCounts.inprogress}
              onSelect={setActiveNav}
            />
            <NavItem
              nav="locked"
              active={activeNav}
              count={navCounts.locked}
              onSelect={setActiveNav}
            />

            <div className="achievement-nav-label achievement-nav-label--spaced">
              按层级
            </div>
            <NavItem
              nav="gold"
              active={activeNav}
              count={navCounts.gold}
              onSelect={setActiveNav}
            />
            <NavItem
              nav="silver"
              active={activeNav}
              count={navCounts.silver}
              onSelect={setActiveNav}
            />
            <NavItem
              nav="bronze"
              active={activeNav}
              count={navCounts.bronze}
              onSelect={setActiveNav}
            />
          </nav>
        </div>
      </aside>

      <main className="achievement-main">
        <header className="achievement-titlebar drag-region">
          <div className="achievement-titlebar-spacer" />
          <div className="achievement-titlebar-actions no-drag">
            <WindowControls />
          </div>
        </header>

        <div className="achievement-content">
          <div className="achievement-content-inner">
            <div className="achievement-content-head">
              <h2 className="achievement-content-title">
                {NAV_LABEL[activeNav]}成就
              </h2>
              <p className="achievement-content-meta">
                {queryLower
                  ? `匹配 ${filteredItems.length} 条结果`
                  : totalAll > 0
                    ? `共 ${filteredItems.length} 条 · 整体已获得 ${totalEarned} / ${totalAll} (${overallPercent}%)`
                    : "尚无成就数据"}
              </p>
            </div>

            {showInitialLoading && (
              <div className="achievement-empty">加载成就中...</div>
            )}
            {showErrorState && (
              <div className="achievement-empty achievement-empty--error">
                <span>{error}</span>
                {onRetry && (
                  <button
                    type="button"
                    className="ui-icon-button px-3 py-1 text-xs"
                    onClick={onRetry}
                  >
                    重试
                  </button>
                )}
              </div>
            )}
            {showEmptyData && (
              <div className="achievement-empty">暂无成就数据</div>
            )}
            {showSearchEmpty && (
              <div className="achievement-empty">
                {queryLower ? "没有匹配的成就" : "该分类下暂无成就"}
              </div>
            )}

            {filteredItems.length > 0 && (
              <ul className="achievement-list">
                {filteredItems.map((item) => (
                  <AchievementRow
                    key={item.code}
                    item={item}
                    queryLower={queryLower}
                  />
                ))}
              </ul>
            )}

            {error && items.length > 0 && (
              <div className="achievement-inline-error">{error}</div>
            )}
          </div>
        </div>
      </main>
    </div>
  );
}
