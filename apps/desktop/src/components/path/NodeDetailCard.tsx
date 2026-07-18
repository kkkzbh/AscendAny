import { useMemo } from "react";

import { useChatStore } from "@/stores/chatStore";
import { useRecommendationsStore } from "@/stores/recommendationsStore";
import type { KnowledgeNodeDetail } from "@/types/path";

import { MiniSeriesChart } from "./MiniSeriesChart";

interface NodeDetailCardProps {
  point: string;
  detail: KnowledgeNodeDetail | null;
  loading: boolean;
  error: string | null;
  onClose: () => void;
  onJumpTo: (point: string) => void;
}

export function NodeDetailCard({
  point,
  detail,
  loading,
  error,
  onClose,
  onJumpTo,
}: NodeDetailCardProps) {
  const setCurrentDraft = useChatStore((s) => s.setCurrentDraft);

  const masteryPercent = detail
    ? Math.round(Math.max(0, Math.min(1, detail.mastery)) * 100)
    : 0;
  const masteryLabel = detail ? `${masteryPercent}%` : "—";
  const accuracyPercent = detail
    ? Math.round(Math.max(0, Math.min(1, detail.stats.accuracy)) * 100)
    : 0;

  const lastTried = useMemo(() => {
    const value = detail?.stats.lastTriedAt;
    if (!value) return null;
    const time = new Date(value);
    if (Number.isNaN(time.getTime())) return null;
    return formatRelativeTime(time);
  }, [detail?.stats.lastTriedAt]);
  const breadcrumb =
    detail && detail.parents.length > 0
      ? detail.parents.slice().reverse().concat(point).join(" / ")
      : detail?.level?.trim() || null;
  const hasStats = Boolean(detail && detail.stats.attempted > 0);
  const hasRecentSeries = Boolean(
    detail?.stats.recentSeries.some((item) => item.attempted > 0 || item.correct > 0),
  );

  return (
    <div className="path-detail-card" key={point}>
      <header className="path-detail-card__header">
        <div className="path-detail-card__title">
          <button
            type="button"
            className="path-detail-card__back"
            onClick={onClose}
            aria-label="返回地图"
          >
            ← 返回地图
          </button>
          <h3 className="path-detail-card__name">{point}</h3>
          {breadcrumb ? (
            <p className="path-detail-card__breadcrumb">{breadcrumb}</p>
          ) : null}
        </div>
        <div className="path-detail-card__mastery">
          <div className="path-detail-card__mastery-meter" aria-hidden>
            <div
              className="path-detail-card__mastery-fill"
              style={{ width: `${masteryPercent}%` }}
            />
          </div>
          <span className="path-detail-card__mastery-label">{masteryLabel}</span>
          {lastTried ? (
            <span className="path-detail-card__last-tried">{lastTried}</span>
          ) : null}
        </div>
      </header>

      {loading && !detail ? (
        <div className="path-detail-card__placeholder">加载中…</div>
      ) : error ? (
        <div className="path-detail-card__placeholder is-error">{error}</div>
      ) : !detail ? (
        null
      ) : (
        <div className="path-detail-card__body">
          {detail.description ||
          detail.prerequisites.length > 0 ||
          detail.successors.length > 0 ? (
            <div className="path-detail-card__narrative">
              {detail.description ? (
              <p className="path-detail-card__description">
                {detail.description}
              </p>
              ) : null}
              {(detail.prerequisites.length > 0 ||
                detail.successors.length > 0) && (
                <div className="path-detail-card__chips">
                  {detail.prerequisites.length > 0 && (
                    <div className="path-detail-card__chip-group">
                      <span className="path-detail-card__chip-label">前置</span>
                      {detail.prerequisites.map((value) => (
                        <button
                          key={`prereq-${value}`}
                          type="button"
                          className="path-detail-card__chip path-detail-card__chip--prereq"
                          onClick={() => onJumpTo(value)}
                        >
                          {value}
                        </button>
                      ))}
                    </div>
                  )}
                  {detail.successors.length > 0 && (
                    <div className="path-detail-card__chip-group">
                      <span className="path-detail-card__chip-label">后续</span>
                      {detail.successors.map((value) => (
                        <button
                          key={`next-${value}`}
                          type="button"
                          className="path-detail-card__chip path-detail-card__chip--next"
                          onClick={() => onJumpTo(value)}
                        >
                          {value}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          ) : null}
          {hasStats ? (
            <div className="path-detail-card__stat-row">
              <Stat label="尝试题数" value={detail.stats.attempted} />
              <Stat label="正确题数" value={detail.stats.correct} />
              <Stat label="正确率" value={`${accuracyPercent}%`} />
            </div>
          ) : null}
          {hasRecentSeries ? (
            <MiniSeriesChart data={detail.stats.recentSeries} />
          ) : null}
          {detail.problems.length > 0 ? (
            <ul className="path-detail-card__problem-list">
              {detail.problems.map((problem) => (
                <li
                  key={problem.problemId}
                  className="path-detail-card__problem"
                >
                  <div className="path-detail-card__problem-head">
                    <span className="path-detail-card__problem-title">
                      {problem.title ?? problem.problemId}
                    </span>
                    {typeof problem.difficulty === "number" ? (
                      <span className="path-detail-card__problem-difficulty">
                        难度 {problem.difficulty.toFixed(1)}
                      </span>
                    ) : null}
                  </div>
                  {problem.knowledgePoints.length > 0 ? (
                    <div className="path-detail-card__problem-tags">
                      {problem.knowledgePoints.map((tag) => (
                        <span
                          key={`${problem.problemId}-${tag}`}
                          className="path-detail-card__problem-tag"
                        >
                          {tag}
                        </span>
                      ))}
                    </div>
                  ) : null}
                  {problem.reason ? (
                    <p className="path-detail-card__problem-reason">
                      {problem.reason}
                    </p>
                  ) : null}
                  <button
                    type="button"
                    className="path-detail-card__problem-action"
                    onClick={() => {
                      const message = `我想做这道：${
                        problem.title ?? problem.problemId
                      }（${problem.problemId}）。请帮我讲讲解题思路。`;
                      setCurrentDraft(message);
                    }}
                  >
                    在聊天里讨论
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      )}
    </div>
  );
}

interface StatProps {
  label: string;
  value: string | number;
}

function Stat({ label, value }: StatProps) {
  return (
    <div className="path-detail-card__stat">
      <div className="path-detail-card__stat-value">{value}</div>
      <div className="path-detail-card__stat-label">{label}</div>
    </div>
  );
}

function formatRelativeTime(time: Date): string {
  const diffMs = Date.now() - time.getTime();
  if (diffMs < 0) return time.toLocaleString();
  const diffMin = Math.floor(diffMs / 60_000);
  if (diffMin < 1) return "刚刚练习";
  if (diffMin < 60) return `${diffMin} 分钟前练习`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour} 小时前练习`;
  const diffDay = Math.floor(diffHour / 24);
  if (diffDay < 30) return `${diffDay} 天前练习`;
  return time.toLocaleDateString();
}

export function useNodeDetailJump() {
  const openNodeDetail = useRecommendationsStore((s) => s.openNodeDetail);
  return openNodeDetail;
}
