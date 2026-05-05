import React, { useEffect, useRef } from 'react';
import * as d3 from 'd3';

interface PathStartScreenArrowsProps {
  svgRef: React.RefObject<SVGSVGElement | null>;
  gRef: React.RefObject<SVGGElement | null>;
  targetId: string | null;
  visible: boolean;
}

const parseTranslate = (transform: string | null): { x: number; y: number } | null => {
  if (!transform) return null;
  const match = /translate\(\s*([-\d.]+)(?:\s*,\s*|\s+)([-\d.]+)\s*\)/.exec(transform);
  if (!match) return null;
  const x = parseFloat(match[1]);
  const y = parseFloat(match[2]);
  if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
  return { x, y };
};

const getNodePosFromDom = (gNode: SVGGElement, tagId: string): { x: number; y: number } | null => {
  const candidates = [`core-${tagId}`, `path-subtag-${tagId}`];
  for (const id of candidates) {
    const el = gNode.querySelector<SVGGElement>(`#${CSS.escape(id)}`);
    if (!el) continue;
    const parsed = parseTranslate(el.getAttribute('transform'));
    if (parsed) return parsed;
  }
  return null;
};

const clamp = (v: number, min: number, max: number) => Math.max(min, Math.min(max, v));

/**
 * FPS 受击提示风格：当“起点”跑到屏幕外时，在屏幕边缘显示淡红色箭头指向它。
 * 仅当目标不在视口内时显示。
 */
const PathStartScreenArrows: React.FC<PathStartScreenArrowsProps> = ({
  svgRef,
  gRef,
  targetId,
  visible,
}) => {
  const arrowGroupRef = useRef<SVGGElement | null>(null);
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    if (!visible || !targetId) {
      if (arrowGroupRef.current) {
        arrowGroupRef.current.style.opacity = '0';
      }
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      return;
    }

    const tick = () => {
      const svg = svgRef.current;
      const gNode = gRef.current;
      const arrowGroup = arrowGroupRef.current;
      if (!svg || !gNode || !arrowGroup) {
        rafRef.current = requestAnimationFrame(tick);
        return;
      }

      const worldPos = getNodePosFromDom(gNode, targetId);
      if (!worldPos) {
        arrowGroup.style.opacity = '0';
        rafRef.current = requestAnimationFrame(tick);
        return;
      }

      const width = svg.clientWidth;
      const height = svg.clientHeight;
      if (width <= 0 || height <= 0) {
        arrowGroup.style.opacity = '0';
        rafRef.current = requestAnimationFrame(tick);
        return;
      }

      const zt = d3.zoomTransform(svg);
      const sx = worldPos.x * zt.k + zt.x;
      const sy = worldPos.y * zt.k + zt.y;

      // 视口范围判断（给一点 padding，避免刚好贴边时闪烁）
      const pad = 26;
      const inView = sx >= pad && sx <= width - pad && sy >= pad && sy <= height - pad;
      if (inView) {
        arrowGroup.style.opacity = '0';
        rafRef.current = requestAnimationFrame(tick);
        return;
      }

      const cx = width / 2;
      const cy = height / 2;
      const dx = sx - cx;
      const dy = sy - cy;

      // 计算射线与屏幕边界的交点
      const edgePad = 18;
      const tX = dx === 0 ? Number.POSITIVE_INFINITY : ((dx > 0 ? (width - edgePad - cx) : (edgePad - cx)) / dx);
      const tY = dy === 0 ? Number.POSITIVE_INFINITY : ((dy > 0 ? (height - edgePad - cy) : (edgePad - cy)) / dy);
      const t = Math.min(tX, tY);

      let ix = cx + dx * t;
      let iy = cy + dy * t;
      ix = clamp(ix, edgePad, width - edgePad);
      iy = clamp(iy, edgePad, height - edgePad);

      const angleDeg = Math.atan2(dy, dx) * 180 / Math.PI;
      arrowGroup.setAttribute('transform', `translate(${ix}, ${iy}) rotate(${angleDeg})`);
      arrowGroup.style.opacity = '1';

      rafRef.current = requestAnimationFrame(tick);
    };

    tick();

    return () => {
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
    };
  }, [visible, targetId, svgRef, gRef]);

  return (
    <div
      className="path-start-screen-arrows"
      style={{
        position: 'absolute',
        inset: 0,
        pointerEvents: 'none',
        zIndex: 900,
      }}
    >
      <svg width="100%" height="100%" style={{ display: 'block' }}>
        <g
          ref={arrowGroupRef}
          className="path-start-edge-arrow"
          style={{ opacity: 0 }}
        >
          {/* 轻微外发光底片 */}
          <path
            d="M 28 0 L -10 14 L -6 0 L -10 -14 Z"
            fill="rgba(255, 60, 60, 0.22)"
            stroke="rgba(255, 160, 160, 0.18)"
            strokeWidth={2}
            className="path-start-edge-arrow-back"
          />
          {/* 主箭头 */}
          <path
            d="M 24 0 L -12 16 L -7 0 L -12 -16 Z"
            fill="rgba(255, 70, 70, 0.55)"
            stroke="rgba(255, 210, 210, 0.55)"
            strokeWidth={1.5}
            className="path-start-edge-arrow-main"
          />
        </g>
      </svg>
    </div>
  );
};

export default PathStartScreenArrows;
