import React, { useEffect, useRef } from 'react';
import * as d3 from 'd3';

interface PathConnectorProps {
  gRef: React.RefObject<SVGGElement | null>;
  learningPath: string[];                    // 学习路径标签ID数组
  tagPositions: Map<string, { x: number; y: number }>; // 标签位置映射
  visible: boolean;                          // 是否显示
}

interface Particle {
  progress: number;      // 0-1 沿路径的进度
  pathIndex: number;     // 所在路径段索引
  speed: number;         // 移动速度
  size: number;          // 粒子大小
  opacity: number;       // 透明度
  trailLength: number;   // 拖尾长度 (0-1)
}

type SegmentNodes = {
  fromId: string;
  toId: string;
  activated: boolean;
  pipe: SVGPathElement;
  outerGlow: SVGPathElement;
  energyFlow: SVGPathElement;
  core: SVGPathElement;
  arrow: SVGPathElement;
};

type ParticleDom = {
  group: SVGGElement;
  main: SVGCircleElement;
  trails: [SVGCircleElement, SVGCircleElement, SVGCircleElement, SVGCircleElement];
};

type PathMeasure = {
  el: SVGPathElement;
  len: number;
};

type NodeKind = 'core' | 'sub';

type NodeCacheEntry = {
  el: SVGGElement | null;
  kind: NodeKind | null;
  nextCheckAt: number;
};

const parseTranslate = (transform: string | null): { x: number; y: number } | null => {
  if (!transform) return null;
  const match = /translate\(\s*([-\d.]+)(?:\s*,\s*|\s+)([-\d.]+)\s*\)/.exec(transform);
  if (!match) return null;
  const x = parseFloat(match[1]);
  const y = parseFloat(match[2]);
  if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
  return { x, y };
};

const resolveNodeFromDom = (
  gNode: SVGGElement,
  tagId: string,
  cache: Map<string, NodeCacheEntry>,
  now: number,
): { pos: { x: number; y: number }; kind: NodeKind } | null => {
  const cached = cache.get(tagId);
  if (cached && now < cached.nextCheckAt) {
    if (!cached.el || !cached.kind) return null;
    const parsed = parseTranslate(cached.el.getAttribute('transform'));
    return parsed ? { pos: parsed, kind: cached.kind } : null;
  }

  // 主视图只会出现 core- 与 path-subtag- 两类 ID
  const coreEl = gNode.querySelector<SVGGElement>(`#${CSS.escape(`core-${tagId}`)}`);
  if (coreEl) {
    cache.set(tagId, { el: coreEl, kind: 'core', nextCheckAt: now + 1000 });
    const parsed = parseTranslate(coreEl.getAttribute('transform'));
    return parsed ? { pos: parsed, kind: 'core' } : null;
  }

  const subEl = gNode.querySelector<SVGGElement>(`#${CSS.escape(`path-subtag-${tagId}`)}`);
  if (subEl) {
    cache.set(tagId, { el: subEl, kind: 'sub', nextCheckAt: now + 1000 });
    const parsed = parseTranslate(subEl.getAttribute('transform'));
    return parsed ? { pos: parsed, kind: 'sub' } : null;
  }

  // 节点可能还没渲染出来，短时间后再检查
  cache.set(tagId, { el: null, kind: null, nextCheckAt: now + 200 });
  return null;
};

const computeCurvedPathD = (
  from: { x: number; y: number },
  to: { x: number; y: number },
  startTrim: number,
  endTrim: number,
): string | null => {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const dist = Math.hypot(dx, dy);
  if (!Number.isFinite(dist) || dist < 1) return null;

  const safeStartTrim = Math.max(0, startTrim);
  const safeEndTrim = Math.max(0, endTrim);
  const maxTrim = Math.max(0, dist - 1);
  const start = Math.min(safeStartTrim, maxTrim * 0.5);
  const end = Math.min(safeEndTrim, maxTrim * 0.5);

  const startRatio = start / dist;
  const endRatio = 1 - end / dist;
  const startX = from.x + dx * startRatio;
  const startY = from.y + dy * startRatio;
  const endX = from.x + dx * endRatio;
  const endY = from.y + dy * endRatio;

  // 控制点（曲线）
  const midX = (from.x + to.x) / 2;
  const midY = (from.y + to.y) / 2;
  const curveOffset = dist * 0.15;
  const perpX = (-dy / dist) * curveOffset;
  const perpY = (dx / dist) * curveOffset;
  const ctrlX = midX + perpX;
  const ctrlY = midY + perpY;

  return `M ${startX} ${startY} Q ${ctrlX} ${ctrlY} ${endX} ${endY}`;
};

/**
 * 学习路径连接线组件 - 增强版
 * 多层激光流形式连接学习路径上的标签，带有向性表达
 */
const PathConnectors: React.FC<PathConnectorProps> = ({
  gRef,
  learningPath,
  tagPositions,
  visible,
}) => {
  const connectorsGroupRef = useRef<SVGGElement | null>(null);
  const particlesRef = useRef<Particle[]>([]);
  const animationRef = useRef<number | null>(null);
  const pathElementsRef = useRef<SVGPathElement[]>([]);
  const segmentsRef = useRef<SegmentNodes[]>([]);
  const tagPositionsRef = useRef(tagPositions);
  const particleDomRef = useRef<ParticleDom[]>([]);
  const pathMeasureRef = useRef<PathMeasure[]>([]);
  const nodeCacheRef = useRef<Map<string, NodeCacheEntry>>(new Map());

  useEffect(() => {
    tagPositionsRef.current = tagPositions;
  }, [tagPositions]);

  useEffect(() => {
    if (!gRef.current || !visible || learningPath.length < 2) {
      // 清理现有连接线
      if (connectorsGroupRef.current) {
        d3.select(connectorsGroupRef.current).remove();
        connectorsGroupRef.current = null;
      }
      if (animationRef.current) {
        cancelAnimationFrame(animationRef.current);
        animationRef.current = null;
      }
      pathElementsRef.current = [];
      segmentsRef.current = [];
      particleDomRef.current = [];
      pathMeasureRef.current = [];
      nodeCacheRef.current = new Map();
      return;
    }

    const g = d3.select(gRef.current);

    // 创建或获取连接线组
    let connectorsGroup = g.select<SVGGElement>('.path-connectors-group');
    if (connectorsGroup.empty()) {
      // 插入到核心标签之前，避免被背景覆盖
      connectorsGroup = g.insert('g', '.core-tag-group')
        .attr('class', 'path-connectors-group');
      connectorsGroupRef.current = connectorsGroup.node();
    } else {
      // 每次更新都确保层级在核心标签之前（但在背景之后）
      const firstCore = g.select<SVGGElement>('.core-tag-group').node();
      const groupNode = connectorsGroup.node();
      if (firstCore && groupNode && groupNode.parentNode) {
        groupNode.parentNode.insertBefore(groupNode, firstCore);
      }
    }

    // 清空现有内容
    connectorsGroup.selectAll('*').remove();
    pathElementsRef.current = [];
    particleDomRef.current = [];
    pathMeasureRef.current = [];
    nodeCacheRef.current = new Map();

    // 预创建每段的所有图层 path，后续每帧只更新 d
    const segments: SegmentNodes[] = [];
    for (let i = 0; i < learningPath.length - 1; i++) {
      const fromId = learningPath[i];
      const toId = learningPath[i + 1];

      const pipe = connectorsGroup.append('path')
        .attr('class', 'path-connector-pipe')
        .attr('d', 'M 0 0 L 0 0')
        .attr('fill', 'none')
        .attr('stroke', '#0a1628')
        .attr('stroke-width', 8)
        .attr('stroke-opacity', 0.6)
        .attr('stroke-linecap', 'round')
        .style('opacity', 0)
        .node();

      const outerGlow = connectorsGroup.append('path')
        .attr('class', 'path-connector-outer-glow')
        .attr('d', 'M 0 0 L 0 0')
        .attr('fill', 'none')
        .attr('stroke', '#00bfff')
        .attr('stroke-width', 6)
        .attr('stroke-opacity', 0.3)
        .attr('stroke-linecap', 'round')
        .attr('filter', 'url(#laserOuterGlow)')
        .style('opacity', 0)
        .node();

      const energyFlow = connectorsGroup.append('path')
        .attr('class', 'path-connector-energy-flow')
        .attr('d', 'M 0 0 L 0 0')
        .attr('fill', 'none')
        .attr('stroke', 'url(#energyPulseGradient)')
        .attr('stroke-width', 4)
        .attr('stroke-linecap', 'round')
        .attr('filter', 'url(#laserGlow)')
        .style('opacity', 0)
        .node();

      const core = connectorsGroup.append('path')
        .attr('class', 'path-connector-core')
        .attr('id', `laser-core-path-${i}`)
        .attr('d', 'M 0 0 L 0 0')
        .attr('fill', 'none')
        .attr('stroke', '#ffffff')
        .attr('stroke-width', 1.5)
        .attr('stroke-opacity', 0.9)
        .attr('stroke-linecap', 'round')
        .attr('filter', 'url(#laserCoreGlow)')
        .style('opacity', 0)
        .node();

      const arrow = connectorsGroup.append('path')
        .attr('class', 'path-arrow-marker')
        .attr('d', 'M 0 0 L 0 0')
        .attr('fill', 'none')
        .attr('stroke', 'transparent')
        .attr('stroke-width', 2)
        .attr('marker-end', 'url(#pathArrow)')
        .style('opacity', 0)
        .node();

      if (pipe && outerGlow && energyFlow && core && arrow) {
        segments.push({ fromId, toId, activated: false, pipe, outerGlow, energyFlow, core, arrow });
        pathElementsRef.current.push(core);
      }
    }

    segmentsRef.current = segments;

    // ===== Layer 4: 能量粒子（单向沿路径移动 + 拖尾）=====
    // 初始化粒子 - 每条路径3个粒子，错开分布
    particlesRef.current = [];
    segments.forEach((_, pathIdx) => {
      for (let i = 0; i < 3; i++) {
        particlesRef.current.push({
          progress: i / 3, // 均匀分布
          pathIndex: pathIdx,
          speed: 0.003 + Math.random() * 0.002, // 随机速度
          size: 2.5 + Math.random() * 1.5,
          opacity: 0.8 + Math.random() * 0.2,
          trailLength: 0.08 + Math.random() * 0.04,
        });
      }
    });

    // 粒子容器
    const particleContainer = connectorsGroup.append('g')
      .attr('class', 'particle-container');

    // 一次性创建粒子 DOM，后续每帧只更新属性（避免每帧 data/join 开销）
    const particleDoms: ParticleDom[] = [];
    particlesRef.current.forEach(() => {
      const groupNode = particleContainer.append('g')
        .attr('class', 'particle-group')
        .node();
      if (!groupNode) return;

      const trails: SVGCircleElement[] = [];
      for (let i = 4; i >= 1; i--) {
        const trailNode = d3.select(groupNode).append('circle')
          .attr('class', `particle-trail-${i}`)
          .attr('fill', '#00ffff')
          .attr('filter', i === 1 ? 'url(#particleIntenseGlow)' : 'url(#particleGlow)')
          .node();
        if (trailNode) trails.push(trailNode);
      }

      const mainNode = d3.select(groupNode).append('circle')
        .attr('class', 'particle-main')
        .attr('fill', '#ffffff')
        .attr('filter', 'url(#particleIntenseGlow)')
        .node();

      if (mainNode && trails.length === 4) {
        // trails 当前是 [4,3,2,1] 顺序；转成 [1..4] 使用更直观
        const t1 = trails[3];
        const t2 = trails[2];
        const t3 = trails[1];
        const t4 = trails[0];
        particleDoms.push({ group: groupNode, main: mainNode, trails: [t1, t2, t3, t4] });
      }
    });
    particleDomRef.current = particleDoms;

    // 预缓存路径长度（后续几何更新时刷新；避免每帧 getTotalLength）
    pathMeasureRef.current = pathElementsRef.current.map((el) => ({
      el,
      len: 0,
    }));

    const resolvePos = (gNode: SVGGElement, tagId: string, now: number): { pos: { x: number; y: number }; kind: NodeKind } | null => {
      const fromDom = resolveNodeFromDom(gNode, tagId, nodeCacheRef.current, now);
      if (fromDom) return fromDom;
      const fromMap = tagPositionsRef.current.get(tagId);
      if (!fromMap) return null;
      return { pos: fromMap, kind: 'core' };
    };

    const updateSegmentPaths = () => {
      if (!gRef.current) return;
      const gNode = gRef.current;
      const segmentsNow = segmentsRef.current;
      const now = performance.now();

      segmentsNow.forEach((seg, idx) => {
        const from = resolvePos(gNode, seg.fromId, now);
        const to = resolvePos(gNode, seg.toId, now);
        if (!from || !to) return;

        const startTrim = from.kind === 'sub' ? 28 : 42;
        const endTrim = to.kind === 'sub' ? 28 : 42;
        const d = computeCurvedPathD(from.pos, to.pos, startTrim, endTrim);
        if (!d) return;

         seg.pipe.setAttribute('d', d);
         seg.outerGlow.setAttribute('d', d);
         seg.energyFlow.setAttribute('d', d);
         seg.core.setAttribute('d', d);
         seg.arrow.setAttribute('d', d);

         const pm = pathMeasureRef.current[idx];
         if (pm && pm.el === seg.core) {
           // 更新段长度缓存
           const len = seg.core.getTotalLength();
           pm.len = Number.isFinite(len) ? len : 0;
         }

        if (!seg.activated) {
          seg.activated = true;
          d3.select(seg.pipe)
            .transition().delay(idx * 150).duration(400)
            .style('opacity', 1);
          d3.select(seg.outerGlow)
            .transition().delay(idx * 150 + 100).duration(400)
            .style('opacity', 1);
          d3.select(seg.energyFlow)
            .transition().delay(idx * 150 + 200).duration(500)
            .style('opacity', 1);
          d3.select(seg.core)
            .transition().delay(idx * 150 + 300).duration(500)
            .style('opacity', 1);
          d3.select(seg.arrow)
            .transition().delay(idx * 150 + 400).duration(400)
            .style('opacity', 1);
        }
      });
    };

    // 粒子动画 - 使用 getPointAtLength 沿曲线移动（同时每帧更新路径几何）
    const animateFrame = () => {
      updateSegmentPaths();

      const particles = particlesRef.current;
      const pathMeasures = pathMeasureRef.current;
      const particleDomsNow = particleDomRef.current;

      particles.forEach((particle) => {
        // 单向移动：始终从起点流向终点
        particle.progress += particle.speed;
        if (particle.progress > 1) {
          particle.progress = 0; // 重置到起点
        }
      });

      // 更新粒子和拖尾位置（直接写 DOM 属性，避免 d3 select/data/join）
      for (let i = 0; i < particles.length; i++) {
        const d = particles[i];
        const dom = particleDomsNow[i];
        if (!dom) continue;

        const pm = pathMeasures[d.pathIndex];
        const pathElement = pm?.el;
        const pathLength = pm?.len ?? 0;
        if (!pathElement || !Number.isFinite(pathLength) || pathLength <= 0) {
          dom.group.setAttribute('opacity', '0');
          continue;
        }
        dom.group.removeAttribute('opacity');

        const mainPoint = pathElement.getPointAtLength(d.progress * pathLength);

        const fadeRange = 0.12;
        let currentOpacity = d.opacity;
        if (d.progress < fadeRange) {
          currentOpacity = d.opacity * (d.progress / fadeRange);
        } else if (d.progress > 1 - fadeRange) {
          currentOpacity = d.opacity * ((1 - d.progress) / fadeRange);
        }

        dom.main.setAttribute('cx', `${mainPoint.x}`);
        dom.main.setAttribute('cy', `${mainPoint.y}`);
        dom.main.setAttribute('r', `${d.size}`);
        dom.main.setAttribute('opacity', `${currentOpacity}`);

        for (let t = 1; t <= 4; t++) {
          const trailProgress = Math.max(0, d.progress - (d.trailLength * t / 4));
          const trailPoint = pathElement.getPointAtLength(trailProgress * pathLength);
          const trailOpacity = currentOpacity * (1 - t * 0.2);
          const trailSize = d.size * (1 - t * 0.15);
          const circle = dom.trails[t - 1];
          circle.setAttribute('cx', `${trailPoint.x}`);
          circle.setAttribute('cy', `${trailPoint.y}`);
          circle.setAttribute('r', `${trailSize}`);
          circle.setAttribute('opacity', `${Math.max(0, trailOpacity)}`);
        }
      }

      animationRef.current = requestAnimationFrame(animateFrame);
    };

    // 延迟启动粒子动画
    setTimeout(() => {
      animateFrame();
    }, 500);

    return () => {
      if (animationRef.current) {
        cancelAnimationFrame(animationRef.current);
      }
    };
  }, [gRef, learningPath, visible]);

  // 清理函数
  useEffect(() => {
    return () => {
      if (connectorsGroupRef.current) {
        d3.select(connectorsGroupRef.current).remove();
      }
      if (animationRef.current) {
        cancelAnimationFrame(animationRef.current);
      }
    };
  }, []);

  return null;
};

export default PathConnectors;
