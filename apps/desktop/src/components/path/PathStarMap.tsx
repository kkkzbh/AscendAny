import { useEffect, useMemo, useRef, useState } from "react";

import { PathNode } from "./PathNode";
import { getPathIconSrc } from "./pathIconAssets";
import type { NodeViewModel } from "@/types/path";

const NODE_SIZE = 52;
const VERTICAL_GAP = 92;
const HORIZONTAL_AMPLITUDE_FRACTION = 0.34;
const TOP_PADDING = 56;
const BOTTOM_PADDING = 56;
const FOCUSED_CX = 58;
const FOCUSED_CY = 56;

interface PathStarMapProps {
  nodes: NodeViewModel[];
  focusedPoint: string | null;
  onSelectNode: (point: string) => void;
  recentlyAdded?: string[];
  recentlyRemoved?: string[];
}

interface PositionedNode {
  vm: NodeViewModel;
  cx: number;
  cy: number;
}

export function PathStarMap({
  nodes,
  focusedPoint,
  onSelectNode,
  recentlyAdded,
  recentlyRemoved,
}: PathStarMapProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState<number>(0);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return undefined;
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      const next = Math.max(0, entry.contentRect.width);
      setWidth(next);
    });
    observer.observe(element);
    setWidth(element.clientWidth);
    return () => observer.disconnect();
  }, []);

  const positioned = useMemo<PositionedNode[]>(() => {
    if (!width || nodes.length === 0) return [];
    const centerX = width / 2;
    const amplitude = Math.min(
      width * HORIZONTAL_AMPLITUDE_FRACTION,
      width / 2 - NODE_SIZE / 2 - 20,
    );
    const totalCount = nodes.length;
    return nodes.map((vm, index) => {
      const cy = TOP_PADDING + index * VERTICAL_GAP;
      const phase = (index / Math.max(totalCount - 1, 1)) * Math.PI * 1.6;
      const cx = centerX + Math.sin(phase) * amplitude;
      return { vm, cx, cy };
    });
  }, [width, nodes]);

  const totalHeight =
    positioned.length === 0
      ? 0
      : (positioned[positioned.length - 1]?.cy ?? 0) + BOTTOM_PADDING;
  const focusedCx = Math.min(
    Math.max(FOCUSED_CX, NODE_SIZE / 2 + 12),
    Math.max(NODE_SIZE / 2 + 12, width - NODE_SIZE / 2 - 12),
  );

  const removedSet = useMemo(
    () => new Set(recentlyRemoved ?? []),
    [recentlyRemoved],
  );
  const addedSet = useMemo(() => new Set(recentlyAdded ?? []), [recentlyAdded]);

  const linkPath = useMemo(() => {
    if (positioned.length < 2) return "";
    let d = "";
    positioned.forEach((node, index) => {
      if (index === 0) {
        d += `M ${node.cx} ${node.cy} `;
        return;
      }
      const prev = positioned[index - 1];
      if (!prev) return;
      const midY = (prev.cy + node.cy) / 2;
      d += `C ${prev.cx} ${midY}, ${node.cx} ${midY}, ${node.cx} ${node.cy} `;
    });
    return d.trim();
  }, [positioned]);

  if (nodes.length === 0) {
    return (
      <div className="path-starmap-empty">
        暂未生成学习路径，先做几道题让模型为你规划吧。
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className={`path-starmap ${focusedPoint ? "is-focusing" : ""}`}
      style={{ height: totalHeight }}
    >
      <div className="path-starmap__sky" aria-hidden />
      {linkPath ? (
        <svg
          className="path-starmap__links"
          width="100%"
          height={totalHeight}
          viewBox={`0 0 ${Math.max(width, 1)} ${Math.max(totalHeight, 1)}`}
          preserveAspectRatio="none"
        >
          <defs>
            <linearGradient id="path-link-gradient" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--star-mastered)" stopOpacity={0.65} />
              <stop offset="100%" stopColor="var(--star-locked)" stopOpacity={0.45} />
            </linearGradient>
          </defs>
          <path
            className="path-starmap__link path-starmap__link--base"
            d={linkPath}
          />
          <path
            className="path-starmap__link path-starmap__link--flow"
            d={linkPath}
          />
        </svg>
      ) : null}
      {positioned.map((node) => {
        const isFocused = focusedPoint === node.vm.point;
        return (
          <PathNode
            key={node.vm.point}
            vm={node.vm}
            cx={isFocused ? focusedCx : node.cx}
            cy={isFocused ? FOCUSED_CY : node.cy}
            size={NODE_SIZE}
            isFocused={isFocused}
            isGhosted={focusedPoint !== null && !isFocused}
            isAdded={addedSet.has(node.vm.point)}
            isLeaving={removedSet.has(node.vm.point)}
            iconSrc={getPathIconSrc(node.vm.point)}
            labelSide={node.cx > width * 0.62 ? "left" : "right"}
            onSelect={onSelectNode}
          />
        );
      })}
    </div>
  );
}
