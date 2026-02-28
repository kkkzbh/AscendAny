import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type WheelEvent as ReactWheelEvent,
} from "react";

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

interface Point {
  x: number;
  y: number;
}

const CANVAS_WIDTH = 1500;
const CANVAS_HEIGHT = 920;
const CARD_WIDTH = 252;
const CARD_HEIGHT = 136;
const DEFAULT_OFFSET = { x: 120, y: 96 };
const DEFAULT_SCALE = 1;
const MIN_SCALE = 0.65;
const MAX_SCALE = 1.85;

const PRESET_POSITIONS: Point[] = [
  { x: 80, y: 60 },
  { x: 360, y: 40 },
  { x: 640, y: 80 },
  { x: 920, y: 50 },
  { x: 1180, y: 90 },
  { x: 120, y: 230 },
  { x: 400, y: 210 },
  { x: 680, y: 250 },
  { x: 960, y: 220 },
  { x: 1220, y: 260 },
  { x: 70, y: 420 },
  { x: 350, y: 390 },
  { x: 640, y: 430 },
  { x: 920, y: 400 },
  { x: 1210, y: 440 },
  { x: 220, y: 600 },
  { x: 640, y: 610 },
  { x: 1060, y: 590 },
];

const FALLBACK_COLUMNS = 6;
const FALLBACK_GAP_X = 240;
const FALLBACK_GAP_Y = 150;

function fallbackPosition(index: number): Point {
  const row = Math.floor(index / FALLBACK_COLUMNS);
  const col = index % FALLBACK_COLUMNS;
  return {
    x: 90 + col * FALLBACK_GAP_X,
    y: 70 + row * FALLBACK_GAP_Y,
  };
}

export function getAchievementTierClass(tier: number): string {
  if (tier >= 3) {
    return "achievement-card achievement-tier-gold";
  }
  if (tier === 2) {
    return "achievement-card achievement-tier-silver";
  }
  if (tier === 1) {
    return "achievement-card achievement-tier-bronze";
  }
  return "achievement-card achievement-tier-locked";
}

function formatProgressText(item: AchievementItem): string {
  const current = Math.floor(item.progress);
  const target =
    item.tier >= 3
      ? item.goldTarget
      : item.tier === 2
        ? item.goldTarget
        : item.tier === 1
          ? item.silverTarget
          : item.bronzeTarget;
  return `${current} / ${Math.floor(target)}`;
}

export function AchievementFullscreen({
  isOpen,
  onClose,
  data,
  loading,
  error,
  onRetry,
}: AchievementFullscreenProps) {
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const [offset, setOffset] = useState<Point>(DEFAULT_OFFSET);
  const [scale, setScale] = useState(DEFAULT_SCALE);
  const dragStateRef = useRef<{
    pointerId: number;
    origin: Point;
    start: Point;
  } | null>(null);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    setOffset(DEFAULT_OFFSET);
    setScale(DEFAULT_SCALE);
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
    const sorted = [...raw].sort((a, b) => {
      if (a.sortOrder !== b.sortOrder) {
        return a.sortOrder - b.sortOrder;
      }
      return a.code.localeCompare(b.code);
    });
    return sorted.map((item, index) => ({
      item,
      point: PRESET_POSITIONS[index] ?? fallbackPosition(index),
    }));
  }, [data]);

  if (!isOpen) {
    return null;
  }

  const handlePointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) {
      return;
    }
    dragStateRef.current = {
      pointerId: event.pointerId,
      origin: offset,
      start: { x: event.clientX, y: event.clientY },
    };
    if (typeof event.currentTarget.setPointerCapture === "function") {
      event.currentTarget.setPointerCapture(event.pointerId);
    }
  };

  const handlePointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const dragState = dragStateRef.current;
    if (!dragState) {
      return;
    }
    const dx = event.clientX - dragState.start.x;
    const dy = event.clientY - dragState.start.y;
    setOffset({
      x: dragState.origin.x + dx,
      y: dragState.origin.y + dy,
    });
  };

  const handlePointerEnd = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (!dragStateRef.current) {
      return;
    }
    dragStateRef.current = null;
    if (
      typeof event.currentTarget.hasPointerCapture === "function"
      && typeof event.currentTarget.releasePointerCapture === "function"
      && event.currentTarget.hasPointerCapture(event.pointerId)
    ) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  };

  const handleWheel = (event: ReactWheelEvent<HTMLDivElement>) => {
    event.preventDefault();
    if (event.ctrlKey) {
      const zoomFactor = Math.exp(-event.deltaY * 0.0024);
      setScale((prev) => Math.min(MAX_SCALE, Math.max(MIN_SCALE, prev * zoomFactor)));
      return;
    }
    setOffset((prev) => ({
      x: prev.x - event.deltaX,
      y: prev.y - event.deltaY,
    }));
  };

  return (
    <section className="achievement-overlay fixed inset-0 z-[70]">
      <div className="achievement-overlay-inner no-drag relative flex h-full w-full flex-col p-0">
        <button
          onClick={onClose}
          className="achievement-close-button ui-window-button ui-window-traffic ui-window-close absolute right-3 top-3 z-30"
          title="关闭成就页"
          aria-label="关闭成就页"
        >
          <span className="ui-window-dot-symbol" aria-hidden="true">
            ×
          </span>
        </button>
        <div
          ref={viewportRef}
          data-testid="achievement-viewport"
          className="achievement-viewport-shell relative h-full w-full flex-1 overflow-hidden"
          style={{ touchAction: "none" }}
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={handlePointerEnd}
          onPointerCancel={handlePointerEnd}
          onWheel={handleWheel}
        >
          {loading && (
            <div className="absolute inset-0 z-20 flex items-center justify-center text-sm text-[var(--text-soft)]">
              加载成就中...
            </div>
          )}
          {error && !loading && (
            <div className="absolute inset-0 z-20 flex flex-col items-center justify-center gap-3">
              <p className="text-sm text-[var(--rating-negative)]">{error}</p>
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
          {!loading && !error && items.length === 0 && (
            <div className="absolute inset-0 z-20 flex items-center justify-center text-sm text-[var(--text-soft)]">
              暂无成就数据
            </div>
          )}

          <div
            className="absolute left-0 top-0"
            data-testid="achievement-canvas"
            style={{
              width: `${CANVAS_WIDTH}px`,
              height: `${CANVAS_HEIGHT}px`,
              transformOrigin: "0 0",
              transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})`,
            }}
          >
            {items.map(({ item, point }) => (
              <article
                key={item.code}
                className={getAchievementTierClass(item.tier)}
                style={{
                  left: `${point.x}px`,
                  top: `${point.y}px`,
                  width: `${CARD_WIDTH}px`,
                  minHeight: `${CARD_HEIGHT}px`,
                }}
              >
                <h3 className="achievement-card-title">
                  {item.title}
                </h3>
                <p className="achievement-card-description">
                  {item.description}
                </p>
                <span className="achievement-card-progress">
                  进度 {formatProgressText(item)}
                </span>
              </article>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
