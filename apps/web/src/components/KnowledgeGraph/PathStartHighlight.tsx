import React, { useEffect, useRef } from 'react';
import * as d3 from 'd3';

interface PathStartHighlightProps {
  gRef: React.RefObject<SVGGElement | null>;
  defsRef: React.RefObject<SVGDefsElement | null>;
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

const isCoreNode = (gNode: SVGGElement, tagId: string): boolean => {
  return Boolean(gNode.querySelector<SVGGElement>(`#${CSS.escape(`core-${tagId}`)}`));
};

/**
 * 在学习路径的第一个标签上添加一个非常明显的“起点”特效。
 * 只做视觉层，不影响交互。
 */
const PathStartHighlight: React.FC<PathStartHighlightProps> = ({
  gRef,
  defsRef,
  targetId,
  visible,
}) => {
  const groupRef = useRef<SVGGElement | null>(null);
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    if (!gRef.current || !defsRef.current || !visible || !targetId) {
      if (groupRef.current) {
        d3.select(groupRef.current).remove();
        groupRef.current = null;
      }
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      return;
    }

    const g = d3.select(gRef.current);

    let group = g.select<SVGGElement>('.path-start-highlight-group');
    if (group.empty()) {
      group = g.append('g')
        .attr('class', 'path-start-highlight-group')
        .style('pointer-events', 'none');
      groupRef.current = group.node();

      // 多层强烈红色环
      group.append('circle')
        .attr('class', 'path-start-outer-ring')
        .attr('r', 70)
        .attr('fill', 'none')
        .attr('stroke', 'rgba(255, 70, 70, 0.55)')
        .attr('stroke-width', 14)
        .attr('filter', 'url(#bloodGlow)');

      group.append('circle')
        .attr('class', 'path-start-inner-ring')
        .attr('r', 58)
        .attr('fill', 'none')
        .attr('stroke', 'rgba(255, 140, 140, 0.75)')
        .attr('stroke-width', 4)
        .attr('filter', 'url(#bloodGlow)');

      group.append('circle')
        .attr('class', 'path-start-rotate-ring')
        .attr('r', 84)
        .attr('fill', 'none')
        .attr('stroke', 'rgba(255, 190, 190, 0.75)')
        .attr('stroke-width', 3)
        .attr('stroke-dasharray', '7 10')
        .attr('filter', 'url(#bloodGlow)');

      // 十字准星
      group.append('line')
        .attr('class', 'path-start-crosshair path-start-crosshair-x')
        .attr('x1', -95).attr('y1', 0)
        .attr('x2', 95).attr('y2', 0)
        .attr('stroke', 'rgba(255, 140, 140, 0.55)')
        .attr('stroke-width', 2)
        .attr('filter', 'url(#bloodGlow)');

      group.append('line')
        .attr('class', 'path-start-crosshair path-start-crosshair-y')
        .attr('x1', 0).attr('y1', -95)
        .attr('x2', 0).attr('y2', 95)
        .attr('stroke', 'rgba(255, 140, 140, 0.55)')
        .attr('stroke-width', 2)
        .attr('filter', 'url(#bloodGlow)');

      // 冲击波（循环）
      group.append('circle')
        .attr('class', 'path-start-ping-ring')
        .attr('r', 50)
        .attr('fill', 'none')
        .attr('stroke', 'rgba(255, 80, 80, 0.7)')
        .attr('stroke-width', 3)
        .attr('filter', 'url(#bloodGlow)');
    } else {
      groupRef.current = group.node();
    }

    // 确保高亮层在最上面（但不影响交互）
    group.raise();

    // 动态更新位置和半径
    const update = () => {
      if (!gRef.current || !targetId) {
        rafRef.current = requestAnimationFrame(update);
        return;
      }

      const gNode = gRef.current;
      const pos = getNodePosFromDom(gNode, targetId);
      if (pos) {
        const core = isCoreNode(gNode, targetId);
        const base = core ? 72 : 58;

        group.attr('transform', `translate(${pos.x}, ${pos.y})`);
        group.select<SVGCircleElement>('.path-start-outer-ring').attr('r', base);
        group.select<SVGCircleElement>('.path-start-inner-ring').attr('r', base - 14);
        group.select<SVGCircleElement>('.path-start-rotate-ring').attr('r', base + 14);
        group.select<SVGLineElement>('line.path-start-crosshair-x')
          .attr('x1', -(base + 24)).attr('x2', (base + 24));
        group.select<SVGLineElement>('line.path-start-crosshair-y')
          .attr('y1', -(base + 24)).attr('y2', (base + 24));
        group.select<SVGCircleElement>('.path-start-ping-ring').attr('r', base - 22);
      }

      rafRef.current = requestAnimationFrame(update);
    };

    // 循环冲击波动画：用 d3 transition 避免每帧重算
    const ping = () => {
      if (!groupRef.current) return;
      const ring = d3.select(groupRef.current).select<SVGCircleElement>('.path-start-ping-ring');
      const baseR = parseFloat(ring.attr('r') || '50');

      ring
        .attr('opacity', 0.9)
        .attr('r', baseR)
        .transition()
        .duration(850)
        .ease(d3.easeCubicOut)
        .attr('r', baseR + 70)
        .attr('opacity', 0)
        .on('end', () => {
          // 继续循环
          if (groupRef.current) {
            ping();
          }
        });
    };

    // 启动
    update();
    ping();

    return () => {
      if (rafRef.current) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      if (groupRef.current) {
        d3.select(groupRef.current).remove();
        groupRef.current = null;
      }
    };
  }, [gRef, defsRef, targetId, visible]);

  return null;
};

export default PathStartHighlight;
