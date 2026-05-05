import React, { useEffect, useRef, useMemo } from 'react';
import * as d3 from 'd3';
import { TagData, InfoBoxData } from './types';
import {
  calculateConnection,
  createConnectors,
  LineEndpoint,
  updateConnectionEndpoints
} from './utils/ConnectorUtils';
import {
  flattenTagHierarchy,
  calculateMultiLevelLayout,
} from './utils/layoutUtils';
import { startSubTagsFloatingAnimation } from './utils/animationUtils';
import type { AnimatableDatum } from './utils/animationUtils';
import { startCoreTagFloatingAnimation } from './utils/coreTagAnimationUtils';
import { fetchKnowledgeTagDetail, mapApiToWebId } from '../../services/api';

type Position = { x: number; y: number };

interface SubTagsProps {
  gRef: React.RefObject<SVGGElement | null>;
  defsRef: React.RefObject<SVGDefsElement | null>;
  svgRef: React.RefObject<SVGSVGElement | null>;
  coreTag: TagData | null; // 传入选中的核心标签数据
  showSubTags: boolean;
  subTags: Record<string, TagData[]>; // 保持传入，但可能直接用coreTag.children
  transitionDuration: number;
  setInfoBox: React.Dispatch<React.SetStateAction<InfoBoxData>>;
  infoBoxTimeoutRef: React.RefObject<number | null>;
  studentId: string | null;
  masteryMap: Record<string, number>; // 掌握度映射
  weakPoints: string[]; // 薄弱知识点列表
}

/**
 * 子标签球组件 - 显示所有层级的子标签
 */
const SubTags: React.FC<SubTagsProps> = ({
  gRef,
  defsRef,
  svgRef,
  coreTag, // 使用coreTag代替selectedTagId
  showSubTags,
  subTags,
  transitionDuration,
  setInfoBox,
  infoBoxTimeoutRef,
  studentId,
  masteryMap,
  weakPoints
}) => {
  const TAG_DETAIL_CACHE_TTL_MS = 60 * 1000;
  const animationStopperRef = useRef<(() => void) | null>(null);
  const coreAnimationStopperRef = useRef<(() => void) | null>(null);
  const studentIdRef = useRef<string | null>(studentId ?? null);
  const tagDetailCacheRef = useRef(
    new Map<
      string,
      { ts: number; acCount: number; recommendedProblems: InfoBoxData['recommendedProblems'] }
    >()
  );

  useEffect(() => {
    studentIdRef.current = studentId ?? null;
  }, [studentId]);
  // 添加一个ref来跟踪连线是否已经初始化
  const linesInitializedRef = useRef<boolean>(false);
  // 跟踪核心标签的当前位置
  const coreTagCurrentPosRef = useRef<{x: number, y: number} | null>(null);

  // 使用 useMemo 缓存扁平化的标签列表和布局计算结果，避免重复计算
  const { flatTags, tagPositions } = useMemo(() => {
    if (!coreTag || !showSubTags || !svgRef.current) {
      return { flatTags: [], tagPositions: new Map<string, Position>() };
    }

    const rootChildren = subTags[coreTag.id] || [];
    if (rootChildren.length === 0) {
      return { flatTags: [], tagPositions: new Map<string, Position>() };
    }

    const svg = d3.select(svgRef.current);
    const finalTransform = d3.zoomTransform(svg.node()!);
    const width = svgRef.current.clientWidth;
    const height = svgRef.current.clientHeight;
    const coreX = (width / 2 - finalTransform.x) / finalTransform.k;
    const coreY = (height / 2 - finalTransform.y) / finalTransform.k;

    // 核心标签和子标签的基础半径
    const CORE_TAG_BASE_RADIUS = 35;
    const SUB_TAG_BASE_RADIUS = 30;

    const positions = calculateMultiLevelLayout(
      coreTag,
      rootChildren,
      coreX,
      coreY,
      CORE_TAG_BASE_RADIUS,
      SUB_TAG_BASE_RADIUS,
      finalTransform.k
    );

    const flattened = flattenTagHierarchy(rootChildren);

    // 当计算新的位置时，重置连线初始化状态
    linesInitializedRef.current = false;

    return { flatTags: flattened, tagPositions: positions };

  }, [coreTag, showSubTags, subTags, svgRef]); // 依赖项包含可能影响布局的因素

  useEffect(() => {
    if (!gRef.current || !svgRef.current || !defsRef.current || !coreTag || !showSubTags || flatTags.length === 0) {
      // 如果没有核心标签、不显示子标签或没有子标签数据，则清理并返回
      if (gRef.current) {
        d3.select(gRef.current).selectAll('.subtag-group, .connectors-container').remove();
      }
      if (defsRef.current) {
        // 不再需要清理渐变元素，因为我们没有创建新的核心标签渐变
      }
      if (animationStopperRef.current) {
        animationStopperRef.current();
        animationStopperRef.current = null;
      }
      if (coreAnimationStopperRef.current) {
        coreAnimationStopperRef.current();
        coreAnimationStopperRef.current = null;
      }
      // 重置连线初始化状态
      linesInitializedRef.current = false;
      return;
    }

    const g = d3.select(gRef.current);
    const svg = d3.select(svgRef.current);
    const defs = d3.select(defsRef.current);
    const finalTransform = d3.zoomTransform(svg.node()!);
    const scaleK = finalTransform.k;

    // 核心标签和子标签的基础半径
    const CORE_TAG_BASE_RADIUS = 35;
    const SUB_TAG_BASE_RADIUS = 30;
    const CORE_TAG_ZOOM_FACTOR = 2.0; // 与 KnowledgeGraph 中 zoomToTag 一致

    const coreTagRadius = (CORE_TAG_BASE_RADIUS * CORE_TAG_ZOOM_FACTOR) / scaleK; // 计算放大后的核心半径

    // --- 清理旧元素 ---
    g.selectAll('.subtag-group').remove();
    g.selectAll('.connectors-container').remove();

    // --- 找到并使用已有的核心标签 ---
    // 选择选中的核心标签组
    const coreTagGroup = g.select<SVGGElement>(`.core-tag-group#core-${coreTag.id}`);

    if (coreTagGroup.empty()) {
      console.error('找不到核心标签元素:', coreTag.id);
      return;
    }

    // --- 渲染标签球 ---
    const subTagGroups = g.selectAll<SVGGElement, TagData>('.subtag-group')
      .data(flatTags, (d: TagData) => d.id)
      .join(
        enter => enter.append('g')
          .attr('class', 'subtag-group')
          .attr('id', d => `subtag-${d.id}`)
          .style('cursor', 'default') // 默认不可点击，除非需要进一步交互
          .attr('transform', d => {
            const pos = tagPositions.get(d.id);
            return pos ? `translate(${pos.x}, ${pos.y})` : 'translate(0,0)';
          })
          .style('opacity', 0) // 初始透明
          .call(enterGroup => {
            // 为每个标签创建渐变
            enterGroup.each((d) => {
              const gradientId = `subGradient-${d.id}-${Date.now()}`;
              d.gradientId = gradientId;

      const gradient = defs.append('radialGradient')
        .attr('id', gradientId)
        .attr('cx', '30%').attr('cy', '30%')
        .attr('r', '70%').attr('fx', '20%').attr('fy', '20%');

      gradient.append('stop')
        .attr('offset', '0%')
        .attr('stop-color', d3.color(d.color)!.brighter(1.5).formatHex())
        .attr('stop-opacity', '0.9');
      gradient.append('stop')
        .attr('offset', '50%')
        .attr('stop-color', d.color)
        .attr('stop-opacity', '0.85');
      gradient.append('stop')
        .attr('offset', '100%')
        .attr('stop-color', d3.color(d.color)!.darker(1).formatHex())
        .attr('stop-opacity', '0.8');
            });

            // 计算当前层级的半径
            const calculateRadius = (level: number = 1) => {
              return (SUB_TAG_BASE_RADIUS / Math.pow(1.2, level - 1)) / scaleK;
            };

            // 检查标签是否为薄弱知识点
            const isWeakPoint = (tag: TagData): boolean => {
              // 检查标签ID、名称是否在薄弱点列表中
              const mappedId = mapApiToWebId(tag.name);
              const inWeakPointsList = weakPoints.includes(tag.id) ||
                     weakPoints.includes(tag.name) ||
                     weakPoints.includes(tag.name.trim()) ||
                     (mappedId ? weakPoints.includes(mappedId) : false);

              // 掌握度为0的标签也视为薄弱知识点
              const mastery = masteryMap[tag.id]
                ?? (mappedId ? masteryMap[mappedId] : undefined)
                ?? masteryMap[tag.name]
                ?? masteryMap[tag.name.trim()];
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
            // mastery 0~0.6 映射到 0.3s~0.8s (掌握度越低，闪烁越快)
            const getAnimationDuration = (mastery: number): number => {
              const clamped = Math.max(0, Math.min(mastery, 0.6));
              return 0.3 + (clamped / 0.6) * 0.5;
            };

            // ===== 薄弱知识点红色薄雾包围 + 紧急呼吸灯效果 =====
            // 紧贴球体的红色薄雾层，完全包围子标签球
            // 动画速度根据掌握度动态调整：掌握度越低，闪烁越快（更紧急）
            enterGroup.filter(d => isWeakPoint(d))
              .append('circle')
              .attr('class', 'weak-point-effect weak-point-wrap-mist')
              .attr('r', d => calculateRadius(d.level) * 1.08) // 紧贴球体
              .attr('fill', 'none')
              .attr('stroke', 'rgba(255, 70, 70, 0.7)')
              .attr('stroke-width', d => calculateRadius(d.level) * 0.35) // 厚实的雾气边缘
              .attr('filter', 'url(#weakPointMist)')
              .attr('pointer-events', 'none')
              .style('transform-origin', 'center center')
              // 中层雾气：动画时长 = 基础时长 * 1.2（比内层稍慢）
              .style('animation-duration', d => `${getAnimationDuration(getMasteryScore(d)) * 1.2}s`);

            // 内层紧急呼吸发光（贴合球体表面）
            enterGroup.filter(d => isWeakPoint(d))
              .append('circle')
              .attr('class', 'weak-point-effect weak-point-inner-breathe')
              .attr('r', d => calculateRadius(d.level) * 1.02) // 几乎贴合球体
              .attr('fill', 'none')
              .attr('stroke', 'rgba(255, 50, 50, 0.85)')
              .attr('stroke-width', d => calculateRadius(d.level) * 0.15)
              .attr('filter', 'url(#weakPointInnerGlow)')
              .attr('pointer-events', 'none')
              .style('transform-origin', 'center center')
              // 内层最快：直接使用计算出的动画时长
              .style('animation-duration', d => `${getAnimationDuration(getMasteryScore(d))}s`);

            // 外层柔和呼吸光晕（稍微扩散但不离开球体太远）
            enterGroup.filter(d => isWeakPoint(d))
              .append('circle')
              .attr('class', 'weak-point-effect weak-point-outer-breathe')
              .attr('r', d => calculateRadius(d.level) * 1.18)
              .attr('fill', 'none')
              .attr('stroke', 'rgba(255, 80, 80, 0.5)')
              .attr('stroke-width', d => calculateRadius(d.level) * 0.25)
              .attr('filter', 'url(#weakPointBreathGlow)')
              .attr('pointer-events', 'none')
              .style('transform-origin', 'center center')
              // 外层稍慢：动画时长 = 基础时长 * 1.1
              .style('animation-duration', d => `${getAnimationDuration(getMasteryScore(d)) * 1.1}s`)
              .lower();

            // 外发光效果
            enterGroup.append('circle')
              .attr('class', 'glow-circle')
              .attr('r', d => calculateRadius(d.level) * 1.2)
      .attr('fill', d => d.color)
      .attr('opacity', 0.3)
      .attr('filter', 'url(#glowEffect)')
      .lower();

            // 主圆
            enterGroup.append('circle')
      .attr('class', 'main-circle')
              .attr('r', d => calculateRadius(d.level))
      .attr('fill', d => `url(#${d.gradientId})`)
      .attr('stroke', '#ffffff')
              .attr('stroke-width', 1.5 / scaleK);

            // 高光
            enterGroup.append('circle')
      .attr('class', 'highlight')
              .attr('r', d => calculateRadius(d.level) * 0.4)
              .attr('cx', d => -calculateRadius(d.level) * 0.3)
              .attr('cy', d => -calculateRadius(d.level) * 0.3)
      .attr('fill', 'rgba(255, 255, 255, 0.4)')
      .attr('pointer-events', 'none');

            // 文本标签
            enterGroup.append('text')
      .attr('text-anchor', 'middle')
      .attr('dy', '0.3em')
      .attr('fill', '#ffffff')
              .attr('font-size', d => `${Math.max(8, 14 / Math.pow(1.1, d.level || 1)) / scaleK}px`) // 字体随层级缩小
      .attr('font-weight', 'bold')
      .attr('pointer-events', 'none')
              .text(d => d.name);
          })
          // 淡入动画
          .transition()
          .duration(transitionDuration)
          .delay((d, i) => 100 + (d.level || 1) * 50 + i * 10) // 根据层级和索引错开动画
          .style('opacity', 1)
          // 在标签淡入完成后延迟一小段时间再绘制连线
          .on('end', (d, i) => {
            // 仅对一级标签的第一个元素触发连线绘制
            if (d.level === 1 && i === 0) {
              // 等待一小段延迟，确保所有标签都已经完全显示
              setTimeout(() => drawConnections(), 100);
            }
          }),

        update => update // 更新现有标签（如果需要的话，例如位置变化）
          .transition()
          .duration(transitionDuration)
          .attr('transform', d => {
            const pos = tagPositions.get(d.id);
            return pos ? `translate(${pos.x}, ${pos.y})` : 'translate(0,0)';
          }),

        exit => exit // 处理消失的标签
          .transition()
          .duration(transitionDuration / 2)
          .style('opacity', 0)
          .remove()
      );

    // --- 连线准备但不立即创建 ---
    const connectorGroup = g.append<SVGGElement>('g')
      .attr('class', 'connectors-container')
      .attr('opacity', 0) // 初始设置为透明
      .raise(); // 修改为raise()使连线显示在上层

    const allConnections: LineEndpoint[] = [];

    // 1. 核心标签到第一级子标签的连接
    const level1Tags = flatTags.filter(tag => tag.level === 1);

    // 获取核心标签位置（从实际DOM中获取）
    const coreTagPos = {
      x: svgRef.current ? (svgRef.current.clientWidth / 2 - finalTransform.x) / scaleK : 0,
      y: svgRef.current ? (svgRef.current.clientHeight / 2 - finalTransform.y) / scaleK : 0
    };

    // 存储核心标签初始位置
    coreTagCurrentPosRef.current = { ...coreTagPos };

    level1Tags.forEach(child => {
      const childPos = tagPositions.get(child.id);
      if (childPos) {
        const childRadius = (SUB_TAG_BASE_RADIUS / Math.pow(1.2, (child.level || 1) - 1)) / scaleK;
        const connection = calculateConnection(
          { ...coreTag, x: coreTagPos.x, y: coreTagPos.y, radius: coreTagRadius },
          { ...child, x: childPos.x, y: childPos.y, radius: childRadius }
        );
        if (connection) {
          allConnections.push(connection);
        }
      }
    });

    // 2. 父子标签之间的连接 (1级到2级, 2级到3级, ...)
    flatTags.forEach(parent => {
      if (parent.children && parent.children.length > 0) {
        const parentPos = tagPositions.get(parent.id);
        if (parentPos) {
          const parentRadius = (SUB_TAG_BASE_RADIUS / Math.pow(1.2, (parent.level || 1) - 1)) / scaleK;

          parent.children.forEach(child => {
            const childPos = tagPositions.get(child.id);
            if (childPos) {
              const childRadius = (SUB_TAG_BASE_RADIUS / Math.pow(1.2, (child.level || 1) - 1)) / scaleK;
              const connection = calculateConnection(
                { ...parent, x: parentPos.x, y: parentPos.y, radius: parentRadius },
                { ...child, x: childPos.x, y: childPos.y, radius: childRadius }
              );
              if (connection) {
                allConnections.push(connection);
              }
            }
          });
        }
      }
    });

    // 函数：等所有标签显示后再绘制连线
    const drawConnections = () => {
      // 连线容器淡入
      connectorGroup.transition()
        .duration(300)
        .attr('opacity', 1);

      // 标记连线已初始化
      linesInitializedRef.current = true;

      // 分离一级和多级连线，创建层次动画效果
      const level1Connections: LineEndpoint[] = []; // 核心到一级标签
      const level2PlusConnections: LineEndpoint[] = []; // 一级到更多级别

      // 将连线分类
      allConnections.forEach(conn => {
        // 通过判断起点位置是否与核心位置接近来识别一级连线
        const distToCoreX = Math.abs(conn.x1 - coreTagPos.x);
        const distToCoreY = Math.abs(conn.y1 - coreTagPos.y);
        const isFromCore = distToCoreX < coreTagRadius * 1.5 && distToCoreY < coreTagRadius * 1.5;

        // 确保连线存储了完整的标签引用
        if (isFromCore && conn.source) {
          // 更新核心标签引用
          conn.source.id = coreTag.id;
        }

        if (isFromCore) {
          level1Connections.push(conn);
        } else {
          level2PlusConnections.push(conn);
        }
      });

      // 先创建从核心到一级标签的连线
      setTimeout(() => {
        createConnectors(
          connectorGroup,
          level1Connections,
          'subtag-connector-level1',
          'rgba(255, 255, 255, 0.8)', // 增加不透明度
          1.8 / scaleK, // 增加线宽
          undefined,
          600, // 增加动画时间使效果更明显
          50, // 较短延迟
          true // 总是播放动画
        );

        // 然后创建一级标签到更多级别标签的连线
        setTimeout(() => {
          createConnectors(
            connectorGroup,
            level2PlusConnections,
            'subtag-connector-level2plus',
            'rgba(255, 255, 255, 0.8)', // 增加不透明度
            1.8 / scaleK, // 增加线宽
            undefined,
            500, // 略短的动画时间
            30, // 较短延迟
            true // 总是播放动画
          );
        }, 250); // 在一级连线开始后再创建
      }, 100); // 等待一小段时间再开始连线动画
    };

    // 定义 stroke width 常量
    const CORE_TAG_STROKE_WIDTH = 2;
    const SUB_TAG_STROKE_WIDTH = 1.5;

    // 创建用于更新连接线的回调函数
    const updateConnectors = (currentPositions: Position[]) => {
      // 将当前位置数组转换为映射，以便通过ID访问
      const posMap = new Map<string, Position>();
      currentPositions.forEach((pos, i) => {
        if (i < flatTags.length) {
          const tag = flatTags[i];
          posMap.set(tag.id, pos);
        }
      });

      // 获取核心标签的当前位置（如果有浮动动画）
      const currentCorePos = coreTagCurrentPosRef.current || coreTagPos;

      // 计算当前核心标签和子标签的基础半径 (考虑缩放)
      const currentCoreBaseRadius = (CORE_TAG_BASE_RADIUS * CORE_TAG_ZOOM_FACTOR) / scaleK;
      const calculateCurrentSubBaseRadius = (level: number = 1) => {
        return (SUB_TAG_BASE_RADIUS / Math.pow(1.2, level - 1)) / scaleK;
      };

      // 获取现有的所有连线
      const existingConnectors = g.selectAll<SVGLineElement, unknown>('.subtag-connector, .subtag-connector-level1, .subtag-connector-level2plus');

      // 逐一更新每条连线
      existingConnectors.each(function() {
        // 获取连线元素和数据
        const connector = d3.select<SVGLineElement, unknown>(this);
        const rawData = connector.datum();

        // 确保数据符合LineEndpoint接口
        if (!rawData || typeof rawData !== 'object') return;

        const connData = rawData as LineEndpoint;
        if (!connData.source || !connData.target || !connData.source.id || !connData.target.id) return;

        let sourcePos: Position;
        let targetPos: Position;
        let sourceRadiusWithStroke: number; // 改为包含 stroke 调整的半径
        let targetRadiusWithStroke: number; // 改为包含 stroke 调整的半径

        if (connData.source.id === coreTag.id) {
          sourcePos = currentCorePos;
          targetPos = posMap.get(connData.target.id) || { x: connData.target.x, y: connData.target.y };
          // 计算包含 stroke 调整的核心半径
          sourceRadiusWithStroke = currentCoreBaseRadius + CORE_TAG_STROKE_WIDTH / 2;
          // 计算子标签半径和调整
          const targetLevel = flatTags.find(t => t.id === connData.target?.id)?.level || 1;
          const targetBaseRadius = calculateCurrentSubBaseRadius(targetLevel);
          targetRadiusWithStroke = targetBaseRadius + SUB_TAG_STROKE_WIDTH / 2;
        } else {
          sourcePos = posMap.get(connData.source.id) || { x: connData.source.x, y: connData.source.y };
          targetPos = posMap.get(connData.target.id) || { x: connData.target.x, y: connData.target.y };
          // 计算源子标签半径和调整
          const sourceLevel = flatTags.find(t => t.id === connData.source?.id)?.level || 1;
          const sourceBaseRadius = calculateCurrentSubBaseRadius(sourceLevel);
          sourceRadiusWithStroke = sourceBaseRadius + SUB_TAG_STROKE_WIDTH / 2;
          // 计算目标子标签半径和调整
          const targetLevel = flatTags.find(t => t.id === connData.target?.id)?.level || 1;
          const targetBaseRadius = calculateCurrentSubBaseRadius(targetLevel);
          targetRadiusWithStroke = targetBaseRadius + SUB_TAG_STROKE_WIDTH / 2;
        }

        // 使用updateConnectionEndpoints，传递已调整好的半径
        const updatedConn = updateConnectionEndpoints(
          connData as LineEndpoint,
          sourcePos,
          targetPos,
          sourceRadiusWithStroke,
          targetRadiusWithStroke
        );

        // 更新连线位置
        connector
          .datum(updatedConn)
          .attr('x1', updatedConn.x1)
          .attr('y1', updatedConn.y1)
          .attr('x2', updatedConn.x2)
          .attr('y2', updatedConn.y2);
      });
    };

    // 启动子标签的浮动动画
    const basePositions = flatTags.map(tag => {
      const pos = tagPositions.get(tag.id);
      return pos ? { ...pos } : { x: 0, y: 0 };
    });

    // 优化：减少延迟时间，加快动画开始
    setTimeout(() => {
      if (animationStopperRef.current) {
        animationStopperRef.current();
      }

      if (coreAnimationStopperRef.current) {
        coreAnimationStopperRef.current();
      }

      // 启动核心标签的浮动动画
      coreAnimationStopperRef.current = startCoreTagFloatingAnimation(
        coreTagGroup,
        coreTagPos,
        2, // 比子标签更小的振幅，移动更缓慢
        (newPosition) => { // 传递一个只更新 Ref 的回调
          coreTagCurrentPosRef.current = newPosition;
        }
      );

      // 启动子标签动画，传入 updateConnectors
      animationStopperRef.current = startSubTagsFloatingAnimation(
        subTagGroups as unknown as d3.Selection<SVGGElement, AnimatableDatum, null, undefined>,
        basePositions,
        3, // 较小的振幅
        updateConnectors // 直接传入 updateConnectors
      );
    }, 300); // 从500减到300，加快响应

    // --- 鼠标事件 --- (可以简化，因为不再需要层级导航)
    // 跟踪当前悬停的标签ID，用于防止动画导致的误触发
    let currentHoveredTagId: string | null = null;

    subTagGroups.on('mouseenter', (event, d) => {
      const currentRadius = (SUB_TAG_BASE_RADIUS / Math.pow(1.2, (d.level || 1) - 1)) / scaleK;

      // 记录当前悬停的标签
      currentHoveredTagId = d.id;

      // 暂停这个标签的浮动动画，防止动画导致mouseleave
      if (d.animation) {
        d.animation.active = false;
      }

      // 标签放大效果，添加淡入动画
      d3.select(event.currentTarget).select('circle.main-circle')
        .transition()
        .duration(300)
        .attr('r', currentRadius * 1.1);

      d3.select(event.currentTarget).select('circle.glow-circle')
        .transition()
        .duration(300)
        .attr('r', currentRadius * 1.3);

      // 清除任何待处理的隐藏定时器
      if (infoBoxTimeoutRef.current) {
        clearTimeout(infoBoxTimeoutRef.current);
        infoBoxTimeoutRef.current = null;
      }

      // 显示信息框并添加淡入动画
      const [svgX, svgY] = d3.pointer(event, svgRef.current);

      // 从 masteryMap 获取该标签的掌握度
      // 后端可能返回映射后的 webId（例如“动态规划” -> “dp”），也可能返回原始中文名
      const mappedId = mapApiToWebId(d.name);
      const tagScore = masteryMap[d.id]
        ?? (mappedId ? masteryMap[mappedId] : undefined)
        ?? masteryMap[d.name]
        ?? masteryMap[d.name.trim()]
        ?? 0;

      // 先设置为可见并且透明度为0，确保渲染但不可见
      setInfoBox({
        visible: true,
        x: svgX + 10,
        y: svgY + 10,
        content: d.name,
        tagName: d.name,
        tagId: d.id,
        tagScore: tagScore, // 传递掌握度数据
        isCoreTag: false, // 标记为子标签，显示详细信息框
        acCount: undefined,
        recommendedProblems: undefined,
        opacity: 0 // 初始完全透明
      });

      // 异步补齐：已解题数 + 题目推荐（缓存 + 防串数据）
      const sid = studentIdRef.current;
      const kpName = (d.name || '').trim();
      if (sid && kpName) {
        const cacheKey = `${sid}::${kpName}`;
        const cached = tagDetailCacheRef.current.get(cacheKey);

        const applyDetail = (detail: { acCount: number; recommendedProblems: InfoBoxData['recommendedProblems'] }) => {
          if (currentHoveredTagId !== d.id) return;
          setInfoBox(prev => {
            if (!prev.visible) return prev;
            if (prev.tagId !== d.id) return prev;
            return {
              ...prev,
              acCount: detail.acCount,
              recommendedProblems: detail.recommendedProblems,
            };
          });
        };

        const now = Date.now();
        const isFresh = cached ? now - cached.ts < TAG_DETAIL_CACHE_TTL_MS : false;

        if (cached) {
          applyDetail(cached);
        }

        if (!cached || !isFresh) {
          fetchKnowledgeTagDetail(sid, kpName, 6)
            .then((data) => {
              const problems = (data.recommendations || []).map((p) => ({
                id: p.problem_id,
                title: p.title || p.problem_id,
                tags: p.knowledge_points || [],
                difficulty: typeof p.difficulty === 'number' ? p.difficulty : 3,
                link: (p.url || `/oj/${p.problem_id}`),
              }));
              const detail = { ts: Date.now(), acCount: data.solved ?? 0, recommendedProblems: problems };
              tagDetailCacheRef.current.set(cacheKey, detail);
              applyDetail(detail);
            })
            .catch(() => {
              // keep silent; InfoBox already shows placeholders
            });
        }
      }

      // 使用setTimeout而不是requestAnimationFrame，确保DOM已更新后再设置不透明度
      setTimeout(() => {
        setInfoBox(prev => ({
          ...prev,
          opacity: 1 // 淡入到完全不透明
        }));
      }, 30); // 短暂延迟以确保DOM更新
    })
    .on('mousemove', (_event, d) => {
      // 鼠标在标签上移动时，更新 InfoBox 位置（可选，保持位置稳定也可以注释掉）
      // 同时确保标签保持高亮状态
      if (d.animation) {
        d.animation.active = false;
      }
    })
    .on('mouseleave', (event, d) => {
      const currentRadius = (SUB_TAG_BASE_RADIUS / Math.pow(1.2, (d.level || 1) - 1)) / scaleK;

      // 检查是否移动到了 InfoBox 上
      const relatedTarget = event.relatedTarget as Element;
      const isMovingToInfoBox = relatedTarget && (
        relatedTarget.closest('.enhanced-info-box') !== null ||
        relatedTarget.closest('[class*="info-box"]') !== null
      );

      // 如果移动到 InfoBox，不要隐藏
      if (isMovingToInfoBox) {
        return;
      }

      // 清除当前悬停标记
      currentHoveredTagId = null;

      // 延迟恢复动画，给用户反应时间
      setTimeout(() => {
        // 只有当没有其他标签被悬停时才恢复动画
        if (d.animation && currentHoveredTagId !== d.id) {
          d.animation.active = true;
        }
      }, 200);

      // 标签恢复原始大小
      d3.select(event.currentTarget).select('circle.main-circle')
        .transition()
        .duration(300)
        .attr('r', currentRadius);

      d3.select(event.currentTarget).select('circle.glow-circle')
        .transition()
        .duration(300)
        .attr('r', currentRadius * 1.2);

      // 延迟隐藏信息框，给用户足够时间移动到 InfoBox
      infoBoxTimeoutRef.current = window.setTimeout(() => {
        // 再次检查是否悬停在 InfoBox 上
        const infoBoxElement = document.querySelector('.enhanced-info-box');
        const isHoveringInfoBox = infoBoxElement && infoBoxElement.matches(':hover');

        // 检查是否悬停在任何子标签上
        const isHoveringAnyTag = currentHoveredTagId !== null;

        if (!isHoveringInfoBox && !isHoveringAnyTag) {
          setInfoBox(prev => ({ ...prev, opacity: 0 }));
          // 等待淡出动画完成后再隐藏
          setTimeout(() => {
            setInfoBox(prev => {
              // 最终检查，确保没有新的悬停
              if (prev.opacity === 0) {
                return { ...prev, visible: false };
              }
              return prev;
            });
          }, 350);
        }
      }, 300); // 增加延迟时间从100ms到300ms
    });

    // --- 动画清理 ---
    return () => {
      // 停止浮动动画等清理工作
      if (animationStopperRef.current) {
        animationStopperRef.current();
        animationStopperRef.current = null;
      }
      if (coreAnimationStopperRef.current) {
        coreAnimationStopperRef.current();
        coreAnimationStopperRef.current = null;
      }

      // 确保清理DOM元素，但不再清理核心标签
      if (gRef.current) {
        d3.select(gRef.current).selectAll('.connectors-container').remove();
      }
      // 不再需要清理渐变元素
    };

  }, [gRef, defsRef, svgRef, coreTag, showSubTags, flatTags, tagPositions, transitionDuration, infoBoxTimeoutRef, setInfoBox]);

  return null; // 组件本身不渲染DOM，完全由D3控制
};

export default SubTags;
