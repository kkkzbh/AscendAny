import React, { useEffect, useRef, useMemo } from 'react';
import * as d3 from 'd3';
import { TagData } from './types';
import {
  calculateConnection,
  createConnectors,
  LineEndpoint,
} from './utils/ConnectorUtils';
import {
  calculatePathSubTagLayout,
  extractPathSubTags,
} from './utils/layoutUtils';
import type { Position } from './utils/ConnectorUtils';
import { mapApiToWebId } from '../../services/api';

const parseTranslate = (transform: string | null): { x: number; y: number } | null => {
  if (!transform) return null;
  const match = /translate\(\s*([-\d.]+)(?:\s*,\s*|\s+)([-\d.]+)\s*\)/.exec(transform);
  if (!match) return null;
  const x = parseFloat(match[1]);
  const y = parseFloat(match[2]);
  if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
  return { x, y };
};

type CoreCacheEntry = {
  el: SVGGElement | null;
  nextCheckAt: number;
};

const getCorePosFromDomCached = (
  gNode: SVGGElement,
  coreTagId: string,
  cache: Map<string, CoreCacheEntry>,
  now: number,
): { x: number; y: number } | null => {
  const cached = cache.get(coreTagId);
  if (cached && now < cached.nextCheckAt) {
    if (!cached.el) return null;
    return parseTranslate(cached.el.getAttribute('transform'));
  }

  const el = gNode.querySelector<SVGGElement>(`#${CSS.escape(`core-${coreTagId}`)}`);
  if (!el) {
    cache.set(coreTagId, { el: null, nextCheckAt: now + 200 });
    return null;
  }
  cache.set(coreTagId, { el, nextCheckAt: now + 1000 });
  return parseTranslate(el.getAttribute('transform'));
};

interface PathSubTagsProps {
  gRef: React.RefObject<SVGGElement | null>;
  defsRef: React.RefObject<SVGDefsElement | null>;
  svgRef?: React.RefObject<SVGSVGElement | null>; // 用于计算鼠标位置
  learningPath: string[];                    // 路径规划的标签ID数组
  coreTagPositions: Map<string, { x: number; y: number }>;  // 核心标签位置
  subTags: Record<string, TagData[]>;        // 所有子标签数据
  visible: boolean;                          // 是否显示
  onSubTagPositionsUpdate: (positions: Map<string, { x: number; y: number }>) => void; // 位置更新回调
  masteryMap: Record<string, number>;        // 掌握度映射
  weakPoints: string[];                      // 薄弱知识点列表
  onPathSubTagHover?: (tag: TagData | null, x?: number, y?: number) => void; // 悬停回调（用于 InfoBox）
}

/**
 * 路径子标签组件
 * 在核心标签主视图中，渲染路径规划涉及的子标签
 * 并绘制子标签与核心标签之间的普通连线
 */
const PathSubTags: React.FC<PathSubTagsProps> = ({
  gRef,
  defsRef,
  svgRef,
  learningPath,
  coreTagPositions,
  subTags,
  visible,
  onSubTagPositionsUpdate,
  masteryMap,
  weakPoints,
  onPathSubTagHover,
}) => {
  const pathSubTagsGroupRef = useRef<SVGGElement | null>(null);
  const animationStopperRef = useRef<(() => void) | null>(null);
  const coreDomCacheRef = useRef<Map<string, CoreCacheEntry>>(new Map());

  // 常量
  const CORE_TAG_RADIUS = 35;
  const PATH_SUB_TAG_RADIUS = CORE_TAG_RADIUS * 0.6; // 子标签半径约为核心标签的60%

  // 提取路径中的子标签并按核心标签分组
  const pathSubTagsMap = useMemo(() => {
    if (!visible || learningPath.length === 0) {
      return new Map<string, string[]>();
    }
    return extractPathSubTags(learningPath, subTags);
  }, [learningPath, subTags, visible]);

  // 查找子标签数据的辅助函数
  const findSubTagData = useMemo(() => {
    const subTagMap = new Map<string, TagData>();

    const processSubTags = (tags: TagData[], parentColor: string) => {
      tags.forEach(tag => {
        // 如果标签没有颜色，继承父级颜色
        const tagWithColor = { ...tag, color: tag.color || parentColor };
        subTagMap.set(tag.id, tagWithColor);
        if (tag.children && tag.children.length > 0) {
          processSubTags(tag.children, tagWithColor.color);
        }
      });
    };

    Object.entries(subTags).forEach(([coreTagId, tags]) => {
      // 获取核心标签的颜色作为默认颜色
      const corePos = coreTagPositions.get(coreTagId);
      void corePos; // 这里实际上不需要位置，只是为了检查核心标签是否存在
      processSubTags(tags, '#6366f1'); // 默认紫色
    });

    return (tagId: string): TagData | undefined => subTagMap.get(tagId);
  }, [subTags, coreTagPositions]);

  // 计算所有路径子标签的位置
  const allSubTagPositions = useMemo(() => {
    const positions = new Map<string, { x: number; y: number }>();

    if (!visible || pathSubTagsMap.size === 0) {
      return positions;
    }

    pathSubTagsMap.forEach((subTagIds, coreTagId) => {
      const corePos = coreTagPositions.get(coreTagId);
      if (corePos) {
        const subPositions = calculatePathSubTagLayout(
          coreTagId,
          corePos,
          subTagIds,
          CORE_TAG_RADIUS,
          PATH_SUB_TAG_RADIUS
        );
        subPositions.forEach((pos, id) => positions.set(id, pos));
      }
    });

    return positions;
  }, [pathSubTagsMap, coreTagPositions, visible, CORE_TAG_RADIUS, PATH_SUB_TAG_RADIUS]);

  // 当位置变化时，通知父组件
  useEffect(() => {
    onSubTagPositionsUpdate(allSubTagPositions);
  }, [allSubTagPositions, onSubTagPositionsUpdate]);

  // 渲染路径子标签和连线
  useEffect(() => {
    if (!gRef.current || !defsRef.current || !visible || pathSubTagsMap.size === 0) {
      // 清理
      if (gRef.current) {
        d3.select(gRef.current).selectAll('.path-subtags-group').remove();
      }
      if (animationStopperRef.current) {
        animationStopperRef.current();
        animationStopperRef.current = null;
      }

      coreDomCacheRef.current = new Map();

      // 隐藏 InfoBox
      if (onPathSubTagHover) {
        onPathSubTagHover(null);
      }
      return;
    }

    const g = d3.select(gRef.current);
    const defs = d3.select(defsRef.current);

    // 创建或获取路径子标签组（放在核心标签之前，避免被背景遮挡）
    let pathSubTagsGroup = g.select<SVGGElement>('.path-subtags-group');
    if (pathSubTagsGroup.empty()) {
      pathSubTagsGroup = g.insert('g', '.core-tag-group')
        .attr('class', 'path-subtags-group');
      pathSubTagsGroupRef.current = pathSubTagsGroup.node();
    } else {
      // 每次更新都确保层级在核心标签之前（但在背景之后）
      const firstCore = g.select<SVGGElement>('.core-tag-group').node();
      const groupNode = pathSubTagsGroup.node();
      if (firstCore && groupNode && groupNode.parentNode) {
        groupNode.parentNode.insertBefore(groupNode, firstCore);
      }
    }

    // 清空现有内容
    pathSubTagsGroup.selectAll('*').remove();

    // 收集所有需要渲染的子标签数据
    const subTagsToRender: Array<{
      tag: TagData;
      pos: { x: number; y: number };
      corePos: { x: number; y: number };
      coreTagId: string;
    }> = [];

    pathSubTagsMap.forEach((subTagIds, coreTagId) => {
      const corePos = coreTagPositions.get(coreTagId);
      if (!corePos) return;

      subTagIds.forEach(subTagId => {
        const tagData = findSubTagData(subTagId);
        const subPos = allSubTagPositions.get(subTagId);
        if (tagData && subPos) {
          subTagsToRender.push({
            tag: tagData,
            pos: subPos,
            corePos: corePos,
            coreTagId: coreTagId,
          });
        }
      });
    });

    if (subTagsToRender.length === 0) return;

    // 1. 先绘制连线（子标签与核心标签之间）
    const connectorsGroup = pathSubTagsGroup.append('g')
      .attr('class', 'path-subtag-connectors');

    const connections: LineEndpoint[] = [];
    subTagsToRender.forEach(({ tag, pos, corePos, coreTagId }) => {
      const connection = calculateConnection(
        { x: corePos.x, y: corePos.y, radius: CORE_TAG_RADIUS, id: coreTagId },
        { x: pos.x, y: pos.y, radius: PATH_SUB_TAG_RADIUS, id: tag.id }
      );
      if (connection) {
        connections.push(connection);
      }
    });

    // 创建普通连线
    createConnectors(
      connectorsGroup as unknown as d3.Selection<SVGGElement, unknown, null, undefined>,
      connections,
      'path-subtag-connector',
      'rgba(255, 255, 255, 0.5)', // 白色半透明连线
      1.5, // 线宽
      undefined,
      400, // 动画时长
      50, // 动画延迟
      true // 播放动画
    );

    // 2. 渲染子标签球
    const subTagGroups = pathSubTagsGroup.selectAll<SVGGElement, typeof subTagsToRender[0]>('.path-subtag-group')
      .data(subTagsToRender, d => d.tag.id)
      .join(
        enter => enter.append('g')
          .attr('class', 'path-subtag-group')
          .attr('id', d => `path-subtag-${d.tag.id}`)
          .attr('transform', d => `translate(${d.pos.x}, ${d.pos.y})`)
          .style('cursor', 'default')
          .style('opacity', 0)
          .call(enterGroup => {
            // 为每个子标签创建渐变
            enterGroup.each(function(d) {
              const gradientId = `pathSubGradient-${d.tag.id}-${Date.now()}`;
              d.tag.gradientId = gradientId;

              const tagColor = d.tag.color || '#6366f1';

              const gradient = defs.append('radialGradient')
                .attr('id', gradientId)
                .attr('cx', '30%').attr('cy', '30%')
                .attr('r', '70%').attr('fx', '20%').attr('fy', '20%');

              gradient.append('stop')
                .attr('offset', '0%')
                .attr('stop-color', d3.color(tagColor)!.brighter(1.5).formatHex())
                .attr('stop-opacity', '0.9');
              gradient.append('stop')
                .attr('offset', '50%')
                .attr('stop-color', tagColor)
                .attr('stop-opacity', '0.85');
              gradient.append('stop')
                .attr('offset', '100%')
                .attr('stop-color', d3.color(tagColor)!.darker(1).formatHex())
                .attr('stop-opacity', '0.8');
            });

            // 检查是否为薄弱知识点
            const isWeakPoint = (tag: TagData): boolean => {
              const mappedId = mapApiToWebId(tag.name);
              const inWeakPointsList = weakPoints.includes(tag.id) ||
                (mappedId ? weakPoints.includes(mappedId) : false) ||
                weakPoints.includes(tag.name) ||
                weakPoints.includes(tag.name.trim());
              const mastery = masteryMap[tag.id]
                ?? (mappedId ? masteryMap[mappedId] : undefined)
                ?? masteryMap[tag.name]
                ?? masteryMap[tag.name.trim()]
                ?? 0;
              const hasZeroMastery = mastery === 0;
              return inWeakPointsList || hasZeroMastery;
            };

            // 获取标签的掌握度分数
            const getMasteryScore = (tag: TagData): number => {
              const mappedId = mapApiToWebId(tag.name);
              return masteryMap[tag.id]
                ?? (mappedId ? masteryMap[mappedId] : undefined)
                ?? masteryMap[tag.name]
                ?? masteryMap[tag.name.trim()]
                ?? 0;
            };

            // 根据掌握度计算动画速度
            const getAnimationDuration = (mastery: number): number => {
              const clamped = Math.max(0, Math.min(mastery, 0.6));
              return 0.3 + (clamped / 0.6) * 0.5;
            };

            // 薄弱知识点效果
            enterGroup.filter(d => isWeakPoint(d.tag))
              .append('circle')
              .attr('class', 'weak-point-effect weak-point-wrap-mist')
              .attr('r', PATH_SUB_TAG_RADIUS * 1.08)
              .attr('fill', 'none')
              .attr('stroke', 'rgba(255, 70, 70, 0.7)')
              .attr('stroke-width', PATH_SUB_TAG_RADIUS * 0.35)
              .attr('filter', 'url(#weakPointMist)')
              .attr('pointer-events', 'none')
              .style('transform-origin', 'center center')
              .style('animation-duration', d => `${getAnimationDuration(getMasteryScore(d.tag)) * 1.2}s`);

            enterGroup.filter(d => isWeakPoint(d.tag))
              .append('circle')
              .attr('class', 'weak-point-effect weak-point-inner-breathe')
              .attr('r', PATH_SUB_TAG_RADIUS * 1.02)
              .attr('fill', 'none')
              .attr('stroke', 'rgba(255, 50, 50, 0.85)')
              .attr('stroke-width', PATH_SUB_TAG_RADIUS * 0.15)
              .attr('filter', 'url(#weakPointInnerGlow)')
              .attr('pointer-events', 'none')
              .style('transform-origin', 'center center')
              .style('animation-duration', d => `${getAnimationDuration(getMasteryScore(d.tag))}s`);

            enterGroup.filter(d => isWeakPoint(d.tag))
              .append('circle')
              .attr('class', 'weak-point-effect weak-point-outer-breathe')
              .attr('r', PATH_SUB_TAG_RADIUS * 1.18)
              .attr('fill', 'none')
              .attr('stroke', 'rgba(255, 80, 80, 0.5)')
              .attr('stroke-width', PATH_SUB_TAG_RADIUS * 0.25)
              .attr('filter', 'url(#weakPointBreathGlow)')
              .attr('pointer-events', 'none')
              .style('transform-origin', 'center center')
              .style('animation-duration', d => `${getAnimationDuration(getMasteryScore(d.tag)) * 1.1}s`)
              .lower();

            // 外发光效果
            enterGroup.append('circle')
              .attr('class', 'glow-circle')
              .attr('r', PATH_SUB_TAG_RADIUS * 1.2)
              .attr('fill', d => d.tag.color || '#6366f1')
              .attr('opacity', 0.3)
              .attr('filter', 'url(#glowEffect)')
              .lower();

            // 主圆
            enterGroup.append('circle')
              .attr('class', 'main-circle')
              .attr('r', PATH_SUB_TAG_RADIUS)
              .attr('fill', d => `url(#${d.tag.gradientId})`)
              .attr('stroke', '#ffffff')
              .attr('stroke-width', 1.5);

            // 高光
            enterGroup.append('circle')
              .attr('class', 'highlight')
              .attr('r', PATH_SUB_TAG_RADIUS * 0.4)
              .attr('cx', -PATH_SUB_TAG_RADIUS * 0.3)
              .attr('cy', -PATH_SUB_TAG_RADIUS * 0.3)
              .attr('fill', 'rgba(255, 255, 255, 0.4)')
              .attr('pointer-events', 'none');

            // 文本标签
            enterGroup.append('text')
              .attr('text-anchor', 'middle')
              .attr('dy', '0.3em')
              .attr('fill', '#ffffff')
              .attr('font-size', `${Math.max(8, 11)}px`) // 较小的字体
              .attr('font-weight', 'bold')
              .attr('pointer-events', 'none')
              .text(d => {
                // 如果名称过长，截断显示
                const name = d.tag.name;
                return name.length > 4 ? name.substring(0, 4) + '..' : name;
              });
          })
          // 淡入动画
          .transition()
          .duration(400)
          .delay((_, i) => 100 + i * 50)
          .style('opacity', 1),

        update => update
          .transition()
          .duration(400)
          .attr('transform', d => `translate(${d.pos.x}, ${d.pos.y})`),

        exit => exit
          .transition()
          .duration(200)
          .style('opacity', 0)
          .remove()
      );

    // 2.1 交互：悬停显示 InfoBox（核心标签主视图）
    if (onPathSubTagHover && svgRef?.current) {
      subTagGroups
        .on('mouseenter', (event, d) => {
          const [mx, my] = d3.pointer(event, svgRef.current as SVGSVGElement);
          onPathSubTagHover(d.tag, mx, my);
        })
        .on('mousemove', (event, d) => {
          const [mx, my] = d3.pointer(event, svgRef.current as SVGSVGElement);
          onPathSubTagHover(d.tag, mx, my);
        })
        .on('mouseleave', () => {
          onPathSubTagHover(null);
        });
    } else {
      subTagGroups
        .on('mouseenter', null)
        .on('mousemove', null)
        .on('mouseleave', null);
    }

    // 3. 子标签轻微浮动（只影响子标签与普通连线；激光连线保持基准位置）
    // 采用“绕核心的小角度摆动”而不是 xy 随机漂移，避免子标签穿过核心标签。
    if (animationStopperRef.current) {
      animationStopperRef.current();
      animationStopperRef.current = null;
    }

    const basePositions: Position[] = subTagsToRender.map(d => ({ x: d.pos.x, y: d.pos.y }));

    const updateConnectors = (currentPositions: Position[]) => {
      const posById = new Map<string, Position>();
      currentPositions.forEach((pos, i) => {
        const data = subTagsToRender[i];
        if (!data) return;
        posById.set(data.tag.id, pos);
      });

      connectorsGroup
        .selectAll<SVGLineElement, LineEndpoint>('.path-subtag-connector')
        .each(function(d) {
          const now = performance.now();
          const coreId = d.source?.id;
          const subId = d.target?.id;
          if (!coreId || !subId) return;

          const corePos = gRef.current
            ? (getCorePosFromDomCached(gRef.current, coreId, coreDomCacheRef.current, now) ?? coreTagPositions.get(coreId))
            : coreTagPositions.get(coreId);
          const subPos = posById.get(subId);
          if (!corePos || !subPos) return;

          const updated = calculateConnection(
            { x: corePos.x, y: corePos.y, radius: CORE_TAG_RADIUS, id: coreId },
            { x: subPos.x, y: subPos.y, radius: PATH_SUB_TAG_RADIUS, id: subId }
          );
          if (!updated) return;

          d3.select(this)
            .attr('x1', updated.x1)
            .attr('y1', updated.y1)
            .attr('x2', updated.x2)
            .attr('y2', updated.y2);
        });
    };

    const nodes = subTagGroups.nodes();
    const orbitData = subTagsToRender.map((d, i) => {
      const base = basePositions[i];
      const dx = base.x - d.corePos.x;
      const dy = base.y - d.corePos.y;
      const baseRadius = Math.max(1, Math.hypot(dx, dy));
      const baseAngle = Math.atan2(dy, dx);

      // 小角度摆动 + 仅向外的轻微呼吸，避免“穿过核心”
      const angleAmp = 0.06 + Math.random() * 0.04; // ~3.4-5.7deg
      const radialAmp = 2 + Math.random() * 1.5; // px
      const freqA = 0.0009 + Math.random() * 0.0005;
      const freqR = 0.0010 + Math.random() * 0.0006;
      const phaseA = Math.random() * Math.PI * 2;
      const phaseR = Math.random() * Math.PI * 2;

      return {
        node: nodes[i],
        coreTagId: d.coreTagId,
        fallbackCorePos: d.corePos,
        baseRadius,
        baseAngle,
        angleAmp,
        radialAmp,
        freqA,
        freqR,
        phaseA,
        phaseR,
      };
    });

    let frameId: number | null = null;

    const animate = () => {
      const t = performance.now();
      const currentPositions: Position[] = [];

      const gNode = gRef.current;

      orbitData.forEach((o, i) => {
        if (!o.node) return;

        const corePos = gNode
          ? (getCorePosFromDomCached(gNode, o.coreTagId, coreDomCacheRef.current, t) ?? o.fallbackCorePos)
          : o.fallbackCorePos;

        const angle = o.baseAngle + Math.sin(t * o.freqA + o.phaseA) * o.angleAmp;
        const outward = Math.max(0, Math.sin(t * o.freqR + o.phaseR));
        const r = o.baseRadius + outward * o.radialAmp;

        const x = corePos.x + Math.cos(angle) * r;
        const y = corePos.y + Math.sin(angle) * r;
        currentPositions[i] = { x, y };

        d3.select(o.node).attr('transform', `translate(${x}, ${y})`);
      });

      updateConnectors(currentPositions);
      frameId = requestAnimationFrame(animate);
    };

    animate();

    animationStopperRef.current = () => {
      if (frameId !== null) {
        cancelAnimationFrame(frameId);
      }
    };

    // 注意：不要 lower()，否则可能被 background rect 覆盖

    // 清理函数
    return () => {
      if (animationStopperRef.current) {
        animationStopperRef.current();
        animationStopperRef.current = null;
      }
      // 清理创建的渐变
      subTagsToRender.forEach(({ tag }) => {
        if (tag.gradientId) {
          defs.select(`#${tag.gradientId}`).remove();
        }
      });
    };

  }, [gRef, defsRef, visible, pathSubTagsMap, allSubTagPositions, coreTagPositions,
      findSubTagData, masteryMap, weakPoints, CORE_TAG_RADIUS, PATH_SUB_TAG_RADIUS, svgRef, onPathSubTagHover]);

  // 清理函数
  useEffect(() => {
    return () => {
      if (animationStopperRef.current) {
        animationStopperRef.current();
        animationStopperRef.current = null;
      }
      if (pathSubTagsGroupRef.current) {
        d3.select(pathSubTagsGroupRef.current).remove();
        pathSubTagsGroupRef.current = null;
      }
    };
  }, []);

  return null; // 组件本身不渲染DOM，完全由D3控制
};

export default PathSubTags;
