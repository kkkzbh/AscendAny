import { useEffect, useMemo, useRef, useState } from "react";

import { useAuthStore } from "@/stores/authStore";
import { useRecommendationsStore } from "@/stores/recommendationsStore";

import { NodeDetailCard } from "./NodeDetailCard";
import { PathStarMap } from "./PathStarMap";

const DETAIL_REVEAL_DELAY_MS = 430;

export function PathPanel() {
  const accessToken = useAuthStore((s) => s.accessToken);
  const path = useRecommendationsStore((s) => s.path);
  const status = useRecommendationsStore((s) => s.status);
  const loadingPath = useRecommendationsStore((s) => s.loadingPath);
  const pathError = useRecommendationsStore((s) => s.pathError);
  const loadPath = useRecommendationsStore((s) => s.loadPath);
  const activeDetailPoint = useRecommendationsStore((s) => s.activeDetailPoint);
  const nodeDetailCache = useRecommendationsStore((s) => s.nodeDetailCache);
  const loadingDetail = useRecommendationsStore((s) => s.loadingDetail);
  const detailError = useRecommendationsStore((s) => s.detailError);
  const openNodeDetail = useRecommendationsStore((s) => s.openNodeDetail);
  const closeNodeDetail = useRecommendationsStore((s) => s.closeNodeDetail);
  const recentlyAdded = useRecommendationsStore((s) => s.recentlyAdded);
  const recentlyRemoved = useRecommendationsStore((s) => s.recentlyRemoved);
  const [detailRevealed, setDetailRevealed] = useState(false);

  const mapRef = useRef<HTMLDivElement | null>(null);
  const loadPathRef = useRef(loadPath);
  loadPathRef.current = loadPath;

  useEffect(() => {
    void loadPathRef.current(accessToken ?? undefined);
  }, [accessToken]);

  useEffect(() => {
    if (!activeDetailPoint) {
      setDetailRevealed(false);
      return undefined;
    }

    const reduceMotion =
      window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;

    mapRef.current?.scrollTo({
      top: 0,
      behavior: reduceMotion ? "auto" : "smooth",
    });

    if (reduceMotion) {
      setDetailRevealed(true);
      return undefined;
    }

    setDetailRevealed(false);
    const timer = window.setTimeout(() => {
      setDetailRevealed(true);
    }, DETAIL_REVEAL_DELAY_MS);

    return () => window.clearTimeout(timer);
  }, [activeDetailPoint]);

  const nodes = useMemo(() => {
    if (!path) return [];
    const targets = new Set(path.targets);
    let frontier = path.path.findIndex((point) => {
      const item = status[point];
      return !item || item.mastery < 0.8;
    });
    if (frontier === -1) {
      frontier = path.path.length === 0 ? -1 : path.path.length - 1;
    }
    return path.path.map((point, index) => {
      const item = status[point];
      let nodeStatus: "locked" | "current" | "mastered" = "locked";
      const mastery = item?.mastery ?? 0;
      if (mastery >= 0.8) {
        nodeStatus = "mastered";
      } else if (index <= frontier || (item?.attempted ?? 0) > 0) {
        nodeStatus = "current";
      }
      return {
        point,
        mastery,
        attempted: item?.attempted ?? 0,
        correct: item?.correct ?? 0,
        lastTriedAt: item?.lastTriedAt ?? null,
        isTarget: targets.has(point),
        status: nodeStatus,
      };
    });
  }, [path, status]);

  const handleSelect = (point: string) => {
    void openNodeDetail(point, { authToken: accessToken ?? undefined });
  };

  const handleJumpTo = (point: string) => {
    void openNodeDetail(point, { authToken: accessToken ?? undefined });
  };

  const handleClose = () => {
    closeNodeDetail();
  };

  const detail = activeDetailPoint
    ? nodeDetailCache[activeDetailPoint] ?? null
    : null;

  return (
    <div
      className={`path-panel ${
        activeDetailPoint ? "is-detail-open" : "is-map-open"
      } ${detailRevealed ? "is-detail-revealed" : ""}`}
    >
      {loadingPath && !path ? (
        <div className="path-panel__placeholder">学习路径加载中…</div>
      ) : pathError ? (
        <div className="path-panel__placeholder is-error">{pathError}</div>
      ) : (
        <>
          <div
            ref={mapRef}
            className="path-panel__map"
            aria-hidden={Boolean(activeDetailPoint)}
          >
            <PathStarMap
              nodes={nodes}
              focusedPoint={activeDetailPoint}
              onSelectNode={handleSelect}
              recentlyAdded={recentlyAdded}
              recentlyRemoved={recentlyRemoved}
            />
          </div>
          {activeDetailPoint ? (
            <div className="path-panel__detail">
              <NodeDetailCard
                point={activeDetailPoint}
                detail={detail}
                loading={loadingDetail}
                error={detailError}
                onClose={handleClose}
                onJumpTo={handleJumpTo}
              />
            </div>
          ) : null}
        </>
      )}
    </div>
  );
}
