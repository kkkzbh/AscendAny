import React, { useEffect, useRef } from 'react';
import * as d3 from 'd3';
import { TagData } from './types';
import { coreTagsFixedLayout } from './utils/graphUtils';

type FloatingState = {
  baseX: number;
  baseY: number;
  phaseX: number;
  phaseY: number;
  frequencyX: number;
  frequencyY: number;
  amplitude: number;
  active: boolean;
};

type CoreTagDatum = TagData & {
  animation?: FloatingState;
};

// 添加浮动动画的工具函数
const startFloatingAnimation = (
  selection: d3.Selection<SVGGElement, TagData, null, undefined>,
  positionData: Map<string, { x: number, y: number }>,
  amplitude: number = 8,
  onFrame?: (positions: Map<string, { x: number; y: number }>) => void,
) => {
  // 为每个标签设置初始随机相位，使运动不同步
  selection.each(function(d) {
    const group = d3.select(this);
    const basePosition = positionData.get(d.id);
    if (!basePosition) return;

    // 随机相位偏移确保标签球运动不同步
    const phaseX = Math.random() * Math.PI * 2;
    const phaseY = Math.random() * Math.PI * 2;
    // 不同的频率使运动看起来更自然
    const frequencyX = 0.0015 + Math.random() * 0.0005;
    const frequencyY = 0.0015 + Math.random() * 0.0005;

    // 存储动画参数
    group.datum({
      ...d,
      animation: {
        baseX: basePosition.x,
        baseY: basePosition.y,
        phaseX,
        phaseY,
        frequencyX,
        frequencyY,
        amplitude: amplitude * (0.7 + Math.random() * 0.6),
        active: true
      }
    } satisfies CoreTagDatum);
  });

  // 启动动画
  let frameId: number | null = null;

  const animate = () => {
    const currentTime = Date.now();

    const updatedPositions = onFrame ? new Map<string, { x: number; y: number }>() : null;

    selection.each(function(d) {
      const datum = d as CoreTagDatum;
      if (!datum.animation || !datum.animation.active) return;

      const { baseX, baseY, phaseX, phaseY, frequencyX, frequencyY, amplitude } = datum.animation;

      // 使用正弦函数创建平滑浮动效果
      const offsetX = Math.sin(currentTime * frequencyX + phaseX) * amplitude;
      const offsetY = Math.sin(currentTime * frequencyY + phaseY) * amplitude;

      // 应用偏移
      const currentX = baseX + offsetX;
      const currentY = baseY + offsetY;
      d3.select(this).attr('transform', `translate(${currentX}, ${currentY})`);

      if (updatedPositions) {
        updatedPositions.set(datum.id, { x: currentX, y: currentY });
      }
    });

    if (updatedPositions) {
      onFrame?.(updatedPositions);
    }

    frameId = requestAnimationFrame(animate);
  };

  animate();

  // 返回停止动画的函数
  return () => {
    if (frameId !== null) {
      cancelAnimationFrame(frameId);
    }
  };
};

interface CoreTagsProps {
  gRef: React.RefObject<SVGGElement | null>;
  defsRef: React.RefObject<SVGDefsElement | null>;
  coreTags: TagData[];
  selectedTagId: string | null;
  setSelectedTagId: (id: string | null) => void;
  setHoveredTag: (tag: TagData | null) => void;
  setShowSubTags: (show: boolean) => void;
  centerX: number;
  centerY: number;
  transitionDuration: number;
  zoomToTag: (tagId: string, x: number, y: number) => void;
  // Ascend API 集成新增属性
  masteryMap?: Record<string, number>;      // 标签ID -> 掌握度 (0-1)
  weakPoints?: string[];                     // 薄弱知识点标签ID列表
  onPositionsUpdate?: (positions: Map<string, { x: number; y: number }>) => void; // 位置更新回调
  // 核心标签悬停 InfoBox 支持
  svgRef?: React.RefObject<SVGSVGElement | null>; // 用于计算鼠标位置
  onCoreTagHover?: (tag: TagData | null, x?: number, y?: number) => void; // 悬停回调
}

/**
 * 核心标签球组件，展示主要知识点
 */
const CoreTags: React.FC<CoreTagsProps> = ({
  gRef,
  defsRef,
  coreTags,
  selectedTagId,
  setSelectedTagId,
  setHoveredTag,
  setShowSubTags,
  centerX,
  centerY,
  transitionDuration,
  zoomToTag,
  masteryMap = {},
  weakPoints = [],
  onPositionsUpdate,
  svgRef,
  onCoreTagHover
}) => {
  // 在组件顶层创建 positionData useRef 存储标签位置
  const positionData = useRef<Map<string, { x: number, y: number }>>(new Map());

  // 添加一个标记来记录是否是首次渲染或从选中状态返回
  const isReturningFromSelection = useRef(false);

  // 保存之前选中的标签ID
  const prevSelectedTagId = useRef<string | null>(null);

  // 添加对浮动动画的引用
  const animationStopperRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    if (!gRef.current || !defsRef.current) return;

    const g = d3.select(gRef.current);
    const defs = d3.select(defsRef.current);

    const coreTagRadius = 35;

    // 检测是否是从选中状态返回到未选中状态
    if (selectedTagId === null && isReturningFromSelection.current) {
      isReturningFromSelection.current = false;

      // 恢复之前被点击的标签球大小
      if (prevSelectedTagId.current) {
        const prevSelectedTag = g.select(`#core-${prevSelectedTagId.current}`);
        if (!prevSelectedTag.empty()) {
          prevSelectedTag.select('circle.main-circle')
            .transition().duration(300).attr('r', coreTagRadius);
          prevSelectedTag.select('circle.glow-circle')
            .transition().duration(300).attr('r', coreTagRadius * 1.2);

          prevSelectedTag.select('circle.mastery-ring')
            .transition().duration(300).attr('r', coreTagRadius + 1);
          prevSelectedTag.selectAll<SVGCircleElement, TagData>('circle.mastery-effect-ring')
            .transition().duration(300)
            .attr('r', function() {
              const base = parseFloat(d3.select(this).attr('data-base-r') ?? `${coreTagRadius * 1.45}`);
              return Number.isFinite(base) ? base : coreTagRadius * 1.45;
            });
        }
        prevSelectedTagId.current = null;
      }
    }
    // 记录从未选中到选中的状态变化
    else if (selectedTagId !== null && !isReturningFromSelection.current) {
      isReturningFromSelection.current = true;
      prevSelectedTagId.current = selectedTagId;
    }
    // 计算位置
    else if (selectedTagId === null && !isReturningFromSelection.current || positionData.current.size === 0) {
      const width = centerX * 2;
      const height = centerY * 2;
      const fixedSpread = 2.7; // 增大核心标签间距，减少路径连线拥挤

      coreTags.forEach(tag => {
        const fixedPos = coreTagsFixedLayout[tag.id];
        let x, y;
        if (fixedPos) {
          // fixedPos 是 0~1 的归一化坐标；围绕中心放大“布局半径”
          x = (fixedPos.x - 0.5) * width * fixedSpread + width / 2;
          y = (fixedPos.y - 0.5) * height * fixedSpread + height / 2;
        } else {
          // 使用环形布局作为备选
          const index = coreTags.findIndex(t => t.id === tag.id);
          const angle = (index * (2 * Math.PI) / coreTags.length) - Math.PI / 2;
           const ringRadius = Math.min(centerX, centerY) * 1.3;
          x = centerX + ringRadius * Math.cos(angle);
          y = centerY + ringRadius * Math.sin(angle);
        }
        positionData.current.set(tag.id, { x, y });
      });
    }

    g.selectAll<SVGGElement, TagData>('.core-tag-group')
      .data(coreTags, (d) => d.id)
      .join(
        enter => enter.append('g')
          .attr('class', 'core-tag-group')
          .attr('id', d => `core-${d.id}`)
          .attr('transform', (d) => {
            const pos = positionData.current.get(d.id);
            if (pos) {
              return `translate(${pos.x}, ${pos.y})`;
            }
            return 'translate(0, 0)';
          })
          .style('cursor', 'pointer')
          .style('opacity', 0)
          .call(enterGroup => {
            // 为每个标签创建渐变
            enterGroup.each((d, i) => {
              const gradientId = `coreGradient-${i}`;

              const gradient = defs.append('radialGradient')
                .attr('id', gradientId)
                .attr('cx', '30%')
                .attr('cy', '30%')
                .attr('r', '70%')
                .attr('fx', '20%')
                .attr('fy', '20%');

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

              d.gradientId = gradientId;
            });

            // 外发光效果底层圆
            enterGroup.append('circle')
              .attr('class', 'glow-circle')
              .attr('r', coreTagRadius * 1.2)
              .attr('fill', d => d.color)
              .attr('opacity', 0.3)
              .attr('filter', 'url(#glowEffect)')
              .lower();

            // 主圆球
            enterGroup.append('circle')
              .attr('class', 'main-circle')
              .attr('r', coreTagRadius)
              .attr('fill', d => `url(#${d.gradientId})`)
              .attr('stroke', 'rgba(255, 255, 255, 0.35)')
              .attr('stroke-width', 1.5);

            // 掌握度边框（分层效果由 mastery effect 控制）
            enterGroup.insert('circle', 'circle.highlight')
              .attr('class', 'mastery-ring')
              .attr('r', coreTagRadius + 1)
              .attr('fill', 'none')
              .attr('stroke', '#ffffff')
              .attr('stroke-width', 2.2)
              .attr('stroke-opacity', 0.75)
              .attr('stroke-linecap', 'round')
              .attr('pointer-events', 'none');

            // 高光效果
            enterGroup.append('circle')
              .attr('class', 'highlight')
              .attr('r', coreTagRadius * 0.4)
              .attr('cx', -coreTagRadius * 0.3)
              .attr('cy', -coreTagRadius * 0.3)
              .attr('fill', 'rgba(255, 255, 255, 0.4)')
              .attr('pointer-events', 'none');

            // 标签文本
            enterGroup.append('text')
              .attr('text-anchor', 'middle')
              .attr('dy', '0.3em')
              .attr('fill', '#ffffff')
              .attr('font-size', '12px')
              .attr('font-weight', 'bold')
              .attr('pointer-events', 'none')
              .text(d => d.name);
          })
          // 初始淡入动画
          .call(enter => enter.transition('initial-fade')
            .duration(transitionDuration)
            .style('opacity', selectedTagId ? 0 : 1)
            .style('pointer-events', selectedTagId ? 'none' : 'all')
          ),
        update => update
          .attr('transform', (d) => {
            const pos = positionData.current.get(d.id);
            if (pos) {
              return `translate(${pos.x}, ${pos.y})`;
            }
            return 'translate(0, 0)';
          })
          .call(update => update.transition('update-fade')
            .duration(transitionDuration)
            .style('opacity', d => (selectedTagId === null || selectedTagId === d.id) ? 1 : 0)
            .style('pointer-events', selectedTagId === null ? 'all' : 'none')
          ),
        exit => exit.transition('exit-fade')
          .duration(transitionDuration)
          .style('opacity', 0)
          .remove()
      )
      // 事件处理 - 修复灰屏BUG
      .on('mouseenter', (event, d) => {
        if (selectedTagId) return; // 已选中状态不响应悬停

        setHoveredTag(d);

        // 获取鼠标位置并调用悬停回调
        if (onCoreTagHover && svgRef?.current) {
          const [mouseX, mouseY] = d3.pointer(event, svgRef.current);
          onCoreTagHover(d, mouseX, mouseY);
        }

        // 标签球放大动画
        d3.select(event.currentTarget).select('circle.main-circle')
          .transition().duration(300).attr('r', coreTagRadius * 1.2);
        d3.select(event.currentTarget).select('circle.glow-circle')
          .transition().duration(300).attr('r', coreTagRadius * 1.4);

        d3.select(event.currentTarget).select('circle.mastery-ring')
          .transition().duration(300).attr('r', (coreTagRadius + 1) * 1.2);
        d3.select(event.currentTarget).selectAll<SVGCircleElement, TagData>('circle.mastery-effect-ring')
          .transition().duration(300)
          .attr('r', function() {
            const base = parseFloat(d3.select(this).attr('data-base-r') ?? `${coreTagRadius * 1.45}`);
            return (Number.isFinite(base) ? base : coreTagRadius * 1.45) * 1.2;
          });

        // 将当前悬停的标签组提升到最上层
        d3.select(event.currentTarget).raise();

        // 将所有其他标签设为半透明
        g.selectAll('.core-tag-group')
          .filter(function() { return this !== event.currentTarget; })
          .transition().duration(300)
          .style('opacity', 0.3);
      })
      .on('mouseleave', (event) => {
        if (selectedTagId) return; // 已选中状态不响应

        // 清除悬停标签数据
        setHoveredTag(null);

        // 清除 InfoBox
        if (onCoreTagHover) {
          onCoreTagHover(null);
        }

        // 标签球恢复原始大小
        d3.select(event.currentTarget).select('circle.main-circle')
          .transition().duration(300).attr('r', coreTagRadius);
        d3.select(event.currentTarget).select('circle.glow-circle')
          .transition().duration(300).attr('r', coreTagRadius * 1.2);

        d3.select(event.currentTarget).select('circle.mastery-ring')
          .transition().duration(300).attr('r', coreTagRadius + 1);
        d3.select(event.currentTarget).selectAll<SVGCircleElement, TagData>('circle.mastery-effect-ring')
          .transition().duration(300)
          .attr('r', function() {
            const base = parseFloat(d3.select(this).attr('data-base-r') ?? `${coreTagRadius * 1.45}`);
            return Number.isFinite(base) ? base : coreTagRadius * 1.45;
          });

        // 恢复所有标签的不透明度
        g.selectAll('.core-tag-group')
          .transition().duration(300)
          .style('opacity', 1);
      })
      .on('click', (event, d) => {
        if (selectedTagId) return; // 防止在已缩放状态下点击

        // 清除任何悬停状态
        setHoveredTag(null);

        // 清除 InfoBox（避免点击后信息框残留）
        if (onCoreTagHover) {
          onCoreTagHover(null);
        }

        // 恢复所有标签的不透明度
        g.selectAll('.core-tag-group')
          .transition().duration(300)
          .style('opacity', 1);

        // 设置选中状态
        setSelectedTagId(d.id);
        setShowSubTags(false);

        // 获取点击的标签位置
        const clickedElement = event.currentTarget as SVGGElement;
        const currentTransformStr = d3.select(clickedElement).attr('transform');
        const match = /translate\(([^,]+),([^)]+)\)/.exec(currentTransformStr);
        if (!match) return;

        const clickedX = parseFloat(match[1]);
        const clickedY = parseFloat(match[2]);

        // 执行缩放
        zoomToTag(d.id, clickedX, clickedY);
      });

    // 通知父组件位置更新
    if (onPositionsUpdate && positionData.current.size > 0) {
      onPositionsUpdate(new Map(positionData.current));
    }

  }, [gRef, defsRef, coreTags, selectedTagId, centerX, centerY, transitionDuration,
      setSelectedTagId, setHoveredTag, setShowSubTags, zoomToTag, onPositionsUpdate, svgRef, onCoreTagHover]);

  // 掌握度/薄弱点视觉效果（独立 effect，避免重置主动画/事件处理）
  useEffect(() => {
    if (!gRef.current) return;

    const g = d3.select(gRef.current);
    const coreTagRadius = 35;

    g.selectAll<SVGGElement, TagData>('.core-tag-group').each(function(d) {
      const masteryRaw = masteryMap[d.id] ?? 0;
      const mastery = Math.max(0, Math.min(1, masteryRaw));
      const isWeak = weakPoints.includes(d.id);
      const group = d3.select(this);

      const brightness = Math.min(1, 0.25 + mastery * 0.95);
      const glowIntensity = Math.min(1, 0.08 + Math.pow(mastery, 1.8) * 0.85);

      const mainCircle = group.select<SVGCircleElement>('circle.main-circle');
      const glowCircle = group.select<SVGCircleElement>('circle.glow-circle');

      const masteryBrightnessFilter = mastery < 0.33
        ? 'url(#masteryBrightness-low)'
        : mastery < 0.66
          ? 'url(#masteryBrightness-medium)'
          : null;

      mainCircle
        .attr('stroke', 'rgba(255, 255, 255, 0.35)')
        .attr('stroke-width', 1.5)
        .attr('fill-opacity', brightness)
        .attr('filter', masteryBrightnessFilter ?? null);

      glowCircle.attr('opacity', glowIntensity);

      // 掌握度分层边框
      if (group.select('circle.mastery-ring').empty()) {
        group.insert('circle', 'circle.highlight')
          .attr('class', 'mastery-ring')
          .attr('r', coreTagRadius + 1)
          .attr('fill', 'none')
          .attr('stroke', '#ffffff')
          .attr('stroke-width', 2.2)
          .attr('stroke-opacity', 0.75)
          .attr('stroke-linecap', 'round')
          .attr('pointer-events', 'none');
      }

      const ring = group.select<SVGCircleElement>('circle.mastery-ring');

      const tier = mastery < 0.2 ? 0 : mastery < 0.4 ? 1 : mastery < 0.6 ? 2 : mastery < 0.8 ? 3 : 4;

      // 清理上一层效果
      ring
        .style('animation', null)
        .attr('stroke-dasharray', null)
        .attr('filter', null);

      group.selectAll('circle.mastery-effect-ring').remove();

      // 清理动漫文字
      group.selectAll('text.mastery-popup-text').remove();

      if (tier === 0) {
        // 白色：普通边框
        ring
          .attr('stroke', '#ffffff')
          .attr('stroke-width', 2.2)
          .attr('stroke-opacity', 0.6);
      } else if (tier === 1) {
        // 绿色：完整圆环 + 内部流动效果 + 偶发弹出"菜"

        // 底层圆环（完整圆环作为"管道"）
        ring
          .attr('stroke', '#2d8c4e')
          .attr('stroke-width', 5)
          .attr('stroke-opacity', 0.6);

        // 流动层（在圆环内流动的效果）
        group.insert('circle', 'circle.main-circle')
          .attr('class', 'mastery-effect-ring mastery-green-flow')
          .attr('data-base-r', `${coreTagRadius + 1}`)
          .attr('r', coreTagRadius + 1)
          .attr('fill', 'none')
          .attr('stroke', 'url(#greenFlowGradient)')
          .attr('stroke-width', 4)
          .attr('stroke-opacity', 0.9)
          .attr('filter', 'url(#greenGlow)')
          .style('animation', 'mastery-ring-rotate 4s linear infinite')
          .attr('pointer-events', 'none');

        // 偶发弹出"菜"文字
        const greenText = group.append('text')
          .attr('class', 'mastery-popup-text mastery-green-text')
          .attr('x', coreTagRadius + 8)
          .attr('y', -coreTagRadius - 5)
          .attr('fill', '#40ff73')
          .attr('font-size', '18px')
          .attr('font-weight', 'bold')
          .attr('font-family', '"Comic Sans MS", "Microsoft YaHei", cursive, sans-serif')
          .attr('opacity', 0)
          .attr('pointer-events', 'none')
          .attr('filter', 'url(#greenGlow)')
          .style('animation', 'mastery-text-popup 6s infinite')
          .text('菜');

        greenText.style('animation-delay', `${Math.random() * 5}s`);
      } else if (tier === 2) {
        // 蓝色：完整圆环 + 激光流动 + 偶发闪电

        // 底层圆环（完整圆环作为"管道"）
        ring
          .attr('stroke', '#0066aa')
          .attr('stroke-width', 5)
          .attr('stroke-opacity', 0.6);

        // 激光流动层
        group.insert('circle', 'circle.main-circle')
          .attr('class', 'mastery-effect-ring mastery-blue-flow')
          .attr('data-base-r', `${coreTagRadius + 1}`)
          .attr('r', coreTagRadius + 1)
          .attr('fill', 'none')
          .attr('stroke', 'url(#blueFlowGradient)')
          .attr('stroke-width', 4)
          .attr('stroke-opacity', 1)
          .attr('filter', 'url(#laserGlow)')
          .style('animation', 'mastery-ring-rotate 2s linear infinite')
          .attr('pointer-events', 'none');

        // 偶发闪电爆发
        const burstRing = group.insert('circle', 'circle.main-circle')
          .attr('class', 'mastery-effect-ring mastery-blue-burst')
          .attr('data-base-r', `${coreTagRadius + 6}`)
          .attr('r', coreTagRadius + 6)
          .attr('fill', 'none')
          .attr('stroke', '#7df9ff')
          .attr('stroke-width', 2.5)
          .attr('stroke-opacity', 0)
          .attr('stroke-dasharray', '4 12')
          .attr('stroke-linecap', 'round')
          .attr('filter', 'url(#lightningGlow)')
          .style('animation', 'mastery-lightning-burst 4s infinite')
          .attr('pointer-events', 'none');

        burstRing.style('animation-delay', `${Math.random() * 3}s`);
      } else if (tier === 3) {
        // 金色：无圆环，只有金光闪耀
        ring
          .attr('stroke', 'none')
          .attr('stroke-width', 0)
          .attr('stroke-opacity', 0);

        // 金色外发光（包裹球体）
        glowCircle
          .attr('fill', '#ffd54f')
          .attr('opacity', 0.35)
          .attr('filter', 'url(#goldGlow)')
          .style('animation', 'mastery-gold-flash 8s ease-in-out infinite');
      } else {
        // 红色：无圆环，淡血色雾气 + 偶发弹出"AC"
        ring
          .attr('stroke', 'none')
          .attr('stroke-width', 0)
          .attr('stroke-opacity', 0);

        // 血色雾气（淡，缠绕球体）
        const mistRing = group.insert('circle', 'circle.main-circle')
          .attr('class', 'mastery-effect-ring mastery-red-mist')
          .attr('data-base-r', `${coreTagRadius * 1.4}`)
          .attr('r', coreTagRadius * 1.4)
          .attr('fill', 'none')
          .attr('stroke', '#ff1744')
          .attr('stroke-width', 12)
          .attr('stroke-opacity', 0.1)
          .attr('stroke-linecap', 'round')
          .attr('filter', 'url(#bloodMist)')
          .style('animation', 'mastery-red-mist 2.8s ease-in-out infinite')
          .attr('pointer-events', 'none');

        mistRing.style('animation-delay', `${Math.random() * 1.8}s`);

        // 偶发弹出"AC"文字
        const redText = group.append('text')
          .attr('class', 'mastery-popup-text mastery-red-text')
          .attr('x', coreTagRadius + 6)
          .attr('y', -coreTagRadius - 3)
          .attr('fill', '#ff1744')
          .attr('font-size', '16px')
          .attr('font-weight', 'bold')
          .attr('font-family', '"Comic Sans MS", "Impact", cursive, sans-serif')
          .attr('opacity', 0)
          .attr('pointer-events', 'none')
          .attr('filter', 'url(#bloodGlow)')
          .style('animation', 'mastery-text-popup 5.5s infinite')
          .text('AC');

        redText.style('animation-delay', `${Math.random() * 4.5}s`);
      }

      if (isWeak) {
        if (group.select('.lightning-ring').empty()) {
          group.insert('circle', ':first-child')
            .attr('class', 'lightning-ring')
            .attr('r', coreTagRadius * 1.5)
            .attr('fill', 'none')
            .attr('stroke', '#4da6ff')
            .attr('stroke-width', 2)
            .attr('stroke-opacity', 0.6)
            .attr('filter', 'url(#lightningGlow)');
        }

        group.select('.lightning-ring')
          .style('animation', 'lightning-flicker 0.15s infinite');
      } else {
        group.select('.lightning-ring').remove();
      }
    });
  }, [gRef, coreTags, masteryMap, weakPoints]);

  // 浮动动画效果
  useEffect(() => {
    if (!gRef.current || selectedTagId !== null) return;

    const g = d3.select(gRef.current);
    const tagGroups = g.selectAll<SVGGElement, TagData>('.core-tag-group');

    // 当组件挂载或从选中状态返回到主视图时启动浮动动画
    const animationTimeout = setTimeout(() => {
      // 停止任何现有的动画
      if (animationStopperRef.current) {
        animationStopperRef.current();
        animationStopperRef.current = null;
      }

      // 启动新的浮动动画
      const stopAnimation = startFloatingAnimation(
        tagGroups as unknown as d3.Selection<SVGGElement, TagData, null, undefined>,
        positionData.current,
        6,
      );
      animationStopperRef.current = stopAnimation;
    }, 500);

  // 添加鼠标悬停时停止浮动的事件处理
  const handleMouseEnter = function(this: SVGGElement) {
    const group = d3.select(this);
    const d = group.datum() as CoreTagDatum;

      if (d && d.animation) {
        d.animation.active = false;
        group.attr('transform', `translate(${d.animation.baseX}, ${d.animation.baseY})`);
      }
    };

  const handleMouseLeave = function(this: SVGGElement) {
    if (selectedTagId !== null) return;

    const group = d3.select(this);
    const d = group.datum() as CoreTagDatum;

      if (d && d.animation) {
        d.animation.active = true;
      }
    };

    tagGroups.on('mouseenter.animation', handleMouseEnter);
    tagGroups.on('mouseleave.animation', handleMouseLeave);

    // 清理函数
    return () => {
      clearTimeout(animationTimeout);

      if (animationStopperRef.current) {
        animationStopperRef.current();
        animationStopperRef.current = null;
      }

      tagGroups
        .on('mouseenter.animation', null)
        .on('mouseleave.animation', null);
    };
  }, [gRef, selectedTagId, positionData, centerX, centerY]);

  return null;
};

export default CoreTags;
