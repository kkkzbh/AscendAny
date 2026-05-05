// 星空动画相关工具函数
import * as d3 from 'd3';
import { Star, ShootingStar } from '../types';
import { Position } from './ConnectorUtils';

export type FloatingAnimationState = {
  baseX: number;
  baseY: number;
  phaseX: number;
  phaseY: number;
  frequencyX: number;
  frequencyY: number;
  amplitude: number;
  active: boolean;
};

export type AnimatableDatum = {
  animation?: FloatingAnimationState;
};

/**
 * 生成随机星星数据
 * @param width 画布宽度
 * @param height 画布高度
 * @param count 星星数量
 */
export const generateStars = (width: number, height: number, count: number): Star[] => {
  const stars: Star[] = [];
  if (width <= 0 || height <= 0) {
    return []; // 如果尺寸无效，返回空数组
  }
  for (let i = 0; i < count; i++) {
    stars.push({
      x: Math.random() * width,
      y: Math.random() * height,
      radius: Math.random() * 1.5 + 0.5,
      opacity: Math.random() * 0.5 + 0.3,
      twinkleSpeed: Math.random() * 0.02 + 0.005,
      twinkleOffset: Math.random() * Math.PI * 2
    });
  }
  return stars;
};

/**
 * 生成随机流星数据
 * @param width 画布宽度
 * @param height 画布高度
 * @param count 流星数量
 */
export const generateShootingStars = (width: number, height: number, count: number): ShootingStar[] => {
  const shootingStars: ShootingStar[] = [];
  for (let i = 0; i < count; i++) {
    const active = Math.random() > 0.7; // 只有部分流星是活跃的
    shootingStars.push({
      x: Math.random() * width * 3 - width,
      y: Math.random() * height * 3 - height,
      length: Math.random() * 100 + 50, // 流星长度
      speed: Math.random() * 15 + 10, // 流星速度
      angle: Math.random() * Math.PI / 4 + Math.PI / 4, // 流星角度，约45度左右
      alpha: active ? Math.random() * 0.5 + 0.3 : 0, // 随机透明度
      active: active // 是否处于活跃状态
    });
  }
  return shootingStars;
};

/**
 * 更新星星闪烁效果
 * @param starsGroup D3 selection对象
 * @param stars 星星数据
 * @param time 当前时间
 */
export const updateStars = (
  starsGroup: d3.Selection<SVGGElement, unknown, null, undefined>,
  stars: Star[],
  time: number
) => {
  starsGroup.selectAll<SVGCircleElement, Star>('.star')
    .data(stars)
    .join('circle')
    .attr('class', 'star')
    .attr('cx', d => d.x)
    .attr('cy', d => d.y)
    .attr('r', d => d.radius)
    .attr('fill', 'white')
    .attr('opacity', d => d.opacity * (0.5 + 0.5 * Math.sin(time * d.twinkleSpeed + d.twinkleOffset))); // 闪烁效果
};

/**
 * 更新流星动画
 * @param shootingStarsGroup D3 selection对象
 * @param shootingStars 流星数据
 * @param svgWidth 画布宽度
 * @param svgHeight 画布高度
 */
export const updateShootingStars = (
  shootingStarsGroup: d3.Selection<SVGGElement, unknown, null, undefined>,
  shootingStars: ShootingStar[],
  svgWidth: number,
  svgHeight: number
): ShootingStar[] => {
  // 更新流星位置和状态
  const updatedShootingStars = shootingStars.map(star => {
    if (star.active) {
      // 移动流星
      star.x += Math.cos(star.angle) * star.speed;
      star.y += Math.sin(star.angle) * star.speed;

      // 随机改变流星透明度（闪烁效果）
      if (Math.random() > 0.97) {
        star.alpha = Math.random() * 0.5 + 0.3;
      }

      // 当流星移出视野后重置，考虑更大的背景范围
      if (star.x > svgWidth || star.y > svgHeight) {
        // 重新初始化流星位置，确保在背景矩形内
        star.x = Math.random() * svgWidth * 0.5 - svgWidth * 0.25; // 在左侧区域随机出现
        star.y = Math.random() * svgHeight * 0.5 - svgHeight * 0.25; // 在上方区域随机出现
        star.angle = Math.random() * Math.PI / 4 + Math.PI / 4; // 约45度角度
        star.speed = Math.random() * 15 + 10;
        star.active = Math.random() > 0.3; // 70%的概率保持活跃
        star.alpha = star.active ? Math.random() * 0.5 + 0.3 : 0;
        star.length = Math.random() * 100 + 50; // 重新随机流星长度
      }
    } else if (Math.random() > 0.995) { // 有小概率激活新的流星
      star.active = true;
      star.alpha = Math.random() * 0.5 + 0.3;
      // 流星出现在视野边缘
      const side = Math.floor(Math.random() * 4); // 0-3代表四个边
      switch(side) {
        case 0: // 顶部
          star.x = Math.random() * svgWidth;
          star.y = -100;
          star.angle = Math.PI / 4 + Math.random() * Math.PI / 2; // 向下方向
          break;
        case 1: // 右侧
          star.x = svgWidth + 100;
          star.y = Math.random() * svgHeight;
          star.angle = Math.PI * 0.75 + Math.random() * Math.PI / 2; // 向左方向
          break;
        case 2: // 底部
          star.x = Math.random() * svgWidth;
          star.y = svgHeight + 100;
          star.angle = Math.PI * 1.25 + Math.random() * Math.PI / 2; // 向上方向
          break;
        case 3: // 左侧
          star.x = -100;
          star.y = Math.random() * svgHeight;
          star.angle = Math.PI * 1.75 + Math.random() * Math.PI / 2; // 向右方向
          break;
      }
    }
    return star;
  });

  // 移除旧的流星元素
  shootingStarsGroup.selectAll('.shooting-star, .shooting-star-glow').remove();

  // 绘制流星主体
  shootingStarsGroup.selectAll<SVGLineElement, ShootingStar>('.shooting-star')
    .data(updatedShootingStars.filter(star => star.active && star.alpha > 0))
    .join('line')
    .attr('class', 'shooting-star')
    .attr('x1', d => d.x)
    .attr('y1', d => d.y)
    .attr('x2', d => d.x - Math.cos(d.angle) * d.length)
    .attr('y2', d => d.y - Math.sin(d.angle) * d.length)
    .attr('stroke', 'white')
    .attr('stroke-width', 1.5)
    .attr('stroke-opacity', d => d.alpha)
    .attr('stroke-linecap', 'round');

  // 添加流星尾部的发光效果
  shootingStarsGroup.selectAll<SVGLineElement, ShootingStar>('.shooting-star-glow')
    .data(updatedShootingStars.filter(star => star.active && star.alpha > 0))
    .join('line')
    .attr('class', 'shooting-star-glow')
    .attr('x1', d => d.x)
    .attr('y1', d => d.y)
    .attr('x2', d => d.x - Math.cos(d.angle) * (d.length * 0.7))
    .attr('y2', d => d.y - Math.sin(d.angle) * (d.length * 0.7))
    .attr('stroke', 'rgba(255, 255, 255, 0.3)')
    .attr('stroke-width', 4)
    .attr('stroke-opacity', d => d.alpha * 0.5)
    .attr('stroke-linecap', 'round')
    .attr('filter', 'url(#glowEffect)');

  return updatedShootingStars;
};

/**
 * 为子标签球添加浮动动画
 * @param selection 标签选择器
 * @param basePositions 基础位置数组
 * @param amplitude 振幅
 * @param connectorsUpdateCallback 更新连接线的回调函数 (可选)
 * @returns 停止动画的函数
 */
export const startSubTagsFloatingAnimation = (
  selection: d3.Selection<SVGGElement, AnimatableDatum, null, undefined>,
  basePositions: Position[],
  amplitude: number = 5,
  connectorsUpdateCallback?: (positions: Position[]) => void
): () => void => {
  // 为每个标签设置初始随机相位，使运动不同步
  let index = 0;
  selection.each(function(d) {
    const basePosition = basePositions[index];
    if (!basePosition) return;

    // 随机相位偏移确保标签球运动不同步
    const phaseX = Math.random() * Math.PI * 2;
    const phaseY = Math.random() * Math.PI * 2;
    // 不同的频率使运动看起来更自然
    const frequencyX = 0.0015 + Math.random() * 0.0005;
    const frequencyY = 0.0015 + Math.random() * 0.0005;

    // 存储动画参数
    d.animation = {
      baseX: basePosition.x,
      baseY: basePosition.y,
      phaseX,
      phaseY,
      frequencyX,
      frequencyY,
      amplitude: amplitude * (0.7 + Math.random() * 0.6), // 稍微随机化振幅
      active: true // 动画激活状态
    };

    index++;
  });

  // 启动动画
  let frameId: number | null = null;

  // 存储当前位置的数组，用于更新连线
  const currentPositions: Position[] = basePositions.map(p => ({...p}));

  // 性能优化：缓存计算值
  const cosCache = new Map<string, number>();
  const sinCache = new Map<string, number>();

  const animate = () => {
    const currentTime = Date.now();
    let index = 0;

    // 批量更新标签位置以提高性能
    const updateBatch: Array<{element: d3.Selection<SVGGElement, AnimatableDatum, null, undefined>, x: number, y: number}> = [];

    selection.each(function(d) {
      if (!d.animation || !d.animation.active) {
        index++;
        return;
      }

      const { baseX, baseY, phaseX, phaseY, frequencyX, frequencyY, amplitude } = d.animation;

      // 高性能计算：使用缓存减少三角函数计算
      const timePhaseX = currentTime * frequencyX + phaseX;
      const timePhaseY = currentTime * frequencyY + phaseY;

      // 为了避免频繁计算三角函数，使用缓存
      const keyX = Math.round(timePhaseX * 100) / 100;
      const keyY = Math.round(timePhaseY * 100) / 100;

      let sinX = sinCache.get(String(keyX));
      if (sinX === undefined) {
        sinX = Math.sin(timePhaseX);
        sinCache.set(String(keyX), sinX);
      }

      let sinY = sinCache.get(String(keyY));
      if (sinY === undefined) {
        sinY = Math.sin(timePhaseY);
        sinCache.set(String(keyY), sinY);
      }

      // 计算偏移
      const offsetX = sinX * amplitude;
      const offsetY = sinY * amplitude;

      // 应用偏移并存储当前位置
      const currentX = baseX + offsetX;
      const currentY = baseY + offsetY;

      // 收集更新而不是立即应用，提高性能
      updateBatch.push({
        element: d3.select<SVGGElement, AnimatableDatum>(this),
        x: currentX,
        y: currentY
      });

      // 检测位置是否有明显变化 - 更小的阈值提高响应性 - 直接更新位置
      if (currentPositions[index]) {
        currentPositions[index].x = currentX;
        currentPositions[index].y = currentY;
      }

      index++;
    });

    // 批量应用DOM更新提高性能
    updateBatch.forEach(item => {
      item.element.attr('transform', `translate(${item.x}, ${item.y})`);
    });

    // 大幅度减少缓存大小，防止内存泄漏
    if (sinCache.size > 300) {
      sinCache.clear();
      cosCache.clear();
    }

    // 调用传入的回调函数来更新连接线 - 移除节流和位置变化检查
    if (connectorsUpdateCallback) {
      connectorsUpdateCallback([...currentPositions]); // 传递副本避免引用问题
    }

    frameId = requestAnimationFrame(animate);
  };

  animate();

  // 返回停止动画的函数
  return () => {
    if (frameId !== null) {
      cancelAnimationFrame(frameId);
    }
    // 清理缓存
    sinCache.clear();
    cosCache.clear();
  };
};

/**
 * 为标签球添加鼠标悬停动画效果
 * @param group 标签组
 * @param nodeRadius 节点半径
 */
export const addHoverEffects = (
  group: d3.Selection<d3.BaseType, unknown, null, undefined>,
  nodeRadius: number
): void => {
  group.on('mouseenter.animation', function() {
    // 放大效果
    d3.select(this).select('.main-circle')
      .transition().duration(300).attr('r', nodeRadius * 1.2);

    d3.select(this).select('.glow-effect')
      .transition().duration(300).attr('r', nodeRadius * 1.4);
  })
  .on('mouseleave.animation', function() {
    // 恢复原始大小
    d3.select(this).select('.main-circle')
      .transition().duration(300).attr('r', nodeRadius);

    d3.select(this).select('.glow-effect')
      .transition().duration(300).attr('r', nodeRadius * 1.2);
  });
};

/**
 * 为标签球创建渐变效果
 * @param defs SVG定义元素
 * @param tag 标签数据
 * @param prefixId 渐变ID前缀
 * @returns 渐变ID
 */
export const createTagGradient = (
  defs: d3.Selection<SVGDefsElement, unknown, null, undefined>,
  tag: { id: string; color: string },
  prefixId: string
): string => {
  // 创建渐变ID
  const gradientId = `${prefixId}-${tag.id}-${Date.now()}`;

  // 创建渐变定义
  const gradient = defs.append('radialGradient')
    .attr('id', gradientId)
    .attr('cx', '30%')
    .attr('cy', '30%')
    .attr('r', '70%')
    .attr('fx', '20%')
    .attr('fy', '20%');

  // 渐变起始颜色（亮色）
  gradient.append('stop')
    .attr('offset', '0%')
    .attr('stop-color', d3.color(tag.color)!.brighter(1.5).formatHex())
    .attr('stop-opacity', '0.9');

  // 渐变中间颜色
  gradient.append('stop')
    .attr('offset', '50%')
    .attr('stop-color', tag.color)
    .attr('stop-opacity', '0.85');

  // 渐变末尾颜色（暗色）
  gradient.append('stop')
    .attr('offset', '100%')
    .attr('stop-color', d3.color(tag.color)!.darker(1).formatHex())
    .attr('stop-opacity', '0.8');

  return gradientId;
};
