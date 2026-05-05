import * as d3 from 'd3';
import { ShootingStar } from '../types';

/**
 * 增强版流星类型
 */
export interface EnhancedShootingStar extends ShootingStar {
  // 基本属性继承自ShootingStar
  // 新增属性
  color: string;           // 流星颜色
  gradientId: string;      // 渐变ID
  width: number;          // 流星宽度
  trailLength: number;    // 尾迹长度比例
  pulseSpeed: number;     // 脉冲速度
  pulseOffset: number;    // 脉冲偏移
  particles: ShootingStarParticle[]; // 尾迹粒子
  lifeTime: number;       // 生命周期
  currentLife: number;    // 当前生命值
  hasTrail: boolean;      // 是否有尾迹粒子
  shimmerEffect: boolean; // 是否有闪烁效果
  exitSide: number;       // 预期的退出边界 (0-3)
  fadeOutState: number;   // 淡出状态 (0表示不淡出, 1表示开始淡出, 2表示已淡出)
}

/**
 * 流星尾迹粒子
 */
interface ShootingStarParticle {
  x: number;
  y: number;
  size: number;
  alpha: number;
  speed: number;
  angle: number;
  rotationSpeed: number;
  life: number;
  maxLife: number;
  color: string;
}

// 记录上次激活流星的时间，用于控制频率
let lastActivationTime = Date.now();
// 设定激活间隔(毫秒)
const MIN_ACTIVATION_INTERVAL = 10000; // 最小10秒
const MAX_ACTIVATION_INTERVAL = 20000; // 最大20秒
let currentActivationInterval = Math.random() * (MAX_ACTIVATION_INTERVAL - MIN_ACTIVATION_INTERVAL) + MIN_ACTIVATION_INTERVAL;

// 深蓝色主题相关的颜色
const THEME_COLORS = {
  primary: '#1a365d',  // 深蓝色
  secondary: '#a3dfff', // 浅蓝色
  accent: '#e6fffa',   // 青色
};

/**
 * 生成增强版流星数据
 */
export const generateEnhancedShootingStars = (
  width: number,
  height: number,
  count: number
): EnhancedShootingStar[] => {
  // 减少流星数量，只生成传入数量的20%
  const actualCount = Math.floor(count * 0.2);
  const shootingStars: EnhancedShootingStar[] = [];

  // 流星颜色集合 - 只使用深蓝色主题相关的颜色
  const colors = [
    THEME_COLORS.secondary, // 浅蓝色
    THEME_COLORS.accent,    // 青色
    '#ffffff',             // 白色用于点缀
  ];

  // 流星渐变ID - 只使用蓝色系渐变
  const gradients = [
    'blueShootingStarGradient',
    'url(#blueShootingStarGradient)'
  ];

  // 初始化激活时间
  lastActivationTime = Date.now();

  for (let i = 0; i < actualCount; i++) {
    // 降低初始活跃流星的比例，最多只有两个活跃
    const active = i < 2 && Math.random() > 0.5;
    const hasTrail = Math.random() > 0.3; // 70%的流星有尾迹粒子
    const shimmerEffect = Math.random() > 0.5; // 50%的流星有闪烁效果
    const colorIndex = Math.floor(Math.random() * colors.length);
    const gradientIndex = Math.floor(Math.random() * gradients.length);
    // 延长生命周期，使流星出现频率降低
    const lifeTime = Math.random() * 800 + 500;

    // 确保初始位置在背景外围
    let initialX, initialY, initialAngle;
    const entrySide: 1 | 3 = Math.random() > 0.5 ? 1 : 3; // 1为右侧，3为左侧

    // 预期退出边（与入口边相对）
    const exitSide: 1 | 3 = entrySide === 1 ? 3 : 1;

    // 基于入口边设置初始位置和角度
    switch(entrySide) {
      case 1: // 右侧
        initialX = width + 200;
        initialY = Math.random() * height;
        initialAngle = Math.PI * 0.75 + Math.random() * Math.PI / 2; // 向左方向
        break;
      case 3: // 左侧
      default:
        initialX = -200;
        initialY = Math.random() * height;
        initialAngle = Math.PI * 1.75 + Math.random() * Math.PI / 2; // 向右方向
        break;
    }

    // 基本流星数据
    const star: EnhancedShootingStar = {
      x: initialX,
      y: initialY,
      length: Math.random() * 150 + 80, // 保持原有长度
      // 降低流星速度
      speed: Math.random() * 5 + 3,
      angle: initialAngle,
      alpha: active ? Math.random() * 0.7 + 0.3 : 0, // 保持原有透明度
      active: active,
      color: colors[colorIndex], // 随机选择颜色
      gradientId: gradients[gradientIndex], // 随机选择渐变
      width: Math.random() * 2 + 1, // 保持原有宽度
      trailLength: Math.random() * 0.3 + 0.7, // 保持原有尾迹长度比例
      pulseSpeed: Math.random() * 0.03 + 0.01, // 保持原有脉冲速度
      pulseOffset: Math.random() * Math.PI * 2, // 保持原有脉冲偏移
      particles: [], // 尾迹粒子
      lifeTime: lifeTime,
      currentLife: lifeTime,
      hasTrail: hasTrail,
      shimmerEffect: shimmerEffect,
      exitSide: exitSide, // 预期退出边
      fadeOutState: 0 // 初始不淡出
    };

    // 如果有尾迹，生成粒子
    if (hasTrail) {
      const particleCount = Math.floor(Math.random() * 15) + 5;
      for (let j = 0; j < particleCount; j++) {
        star.particles.push(createParticle(star));
      }
    }

    shootingStars.push(star);
  }

  return shootingStars;
};

/**
 * 为流星创建尾迹粒子
 */
const createParticle = (star: EnhancedShootingStar): ShootingStarParticle => {
  // 粒子位置在流星尾部随机偏移
  const trailPortion = Math.random() * 0.9; // 粒子在尾迹0-90%位置间随机分布
  const offsetX = -Math.cos(star.angle) * star.length * trailPortion;
  const offsetY = -Math.sin(star.angle) * star.length * trailPortion;

  // 从流星轨迹线偏移一定距离
  const perpAngle = star.angle + Math.PI / 2;
  const perpOffset = (Math.random() - 0.5) * 20;

  // 粒子颜色变体
  const particleColors = [
    star.color, // 原始颜色
    '#ffffff',  // 白色
    '#a3dfff',  // 浅蓝色
    '#fff9c4',  // 淡黄色
  ];

  const colorIndex = Math.floor(Math.random() * particleColors.length);

  return {
    x: star.x + offsetX + Math.cos(perpAngle) * perpOffset,
    y: star.y + offsetY + Math.sin(perpAngle) * perpOffset,
    size: Math.random() * 2 + 0.5,
    alpha: Math.random() * 0.7 + 0.3,
    // 降低粒子速度
    speed: Math.random() * 1.5 + 0.3,
    angle: star.angle + (Math.random() - 0.5) * 1.5, // 粒子角度与流星略有偏差
    rotationSpeed: (Math.random() - 0.5) * 0.1,
    life: Math.random() * 30 + 20,
    maxLife: Math.random() * 30 + 20,
    color: particleColors[colorIndex]
  };
};

/**
 * 检查流星是否已经接近或超过指定的边界
 * @param star 流星对象
 * @param width 画布宽度
 * @param height 画布高度
 * @param boundaryOffset 边界偏移量
 * @returns 是否接近或超过边界
 */
const isNearOrBeyondBoundary = (
  star: EnhancedShootingStar,
  width: number,
  height: number,
  boundaryOffset: number = 50
): boolean => {
  // 根据预期的退出边判断是否接近边界
  switch(star.exitSide) {
    case 0: // 检查是否接近顶部边界
      return star.y <= boundaryOffset;
    case 1: // 检查是否接近右侧边界
      return star.x >= width - boundaryOffset;
    case 2: // 检查是否接近底部边界
      return star.y >= height - boundaryOffset;
    case 3: // 检查是否接近左侧边界
      return star.x <= boundaryOffset;
    default:
      return false;
  }
};

/**
 * 检查流星是否完全离开屏幕
 * @param star 流星对象
 * @param width 画布宽度
 * @param height 画布高度
 * @param margin 边缘外的安全距离
 * @returns 是否完全离开屏幕
 */
const isCompletelyOffScreen = (
  star: EnhancedShootingStar,
  width: number,
  height: number,
  margin: number = 300
): boolean => {
  const tailEndX = star.x - Math.cos(star.angle) * (star.length + margin);
  const tailEndY = star.y - Math.sin(star.angle) * (star.length + margin);

  // 检查流星的头部和尾部是否都在屏幕外
  return (
    (star.x < -margin && tailEndX < -margin) || // 左侧边界外
    (star.x > width + margin && tailEndX > width + margin) || // 右侧边界外
    (star.y < -margin && tailEndY < -margin) || // 顶部边界外
    (star.y > height + margin && tailEndY > height + margin) // 底部边界外
  );
};

/**
 * 更新增强版流星动画
 * 性能优化版本：使用 D3 的 join 模式而不是每帧删除重建
 */
export const updateEnhancedShootingStars = (
  shootingStarsGroup: d3.Selection<SVGGElement, unknown, null, undefined>,
  shootingStars: EnhancedShootingStar[],
  svgWidth: number,
  svgHeight: number,
  time: number
): EnhancedShootingStar[] => {
  const currentTime = Date.now();
  void time;
  // 基于时间间隔控制流星激活概率
  const timeSinceLastActivation = currentTime - lastActivationTime;

  // 只有当时间间隔超过最小间隔时才可能激活
  const canActivate = timeSinceLastActivation >= currentActivationInterval;

  // 更新流星位置和状态
  const updatedShootingStars = shootingStars.map(star => {
    if (star.active) {
      // 更新生命周期
      star.currentLife -= 1;

      // 生命周期结束，重置流星
      if (star.currentLife <= 0) {
        resetShootingStar(star, svgWidth, svgHeight);
        return star;
      }

      // 动态检查是否需要开始淡出
      if (star.fadeOutState === 0 && isNearOrBeyondBoundary(star, svgWidth, svgHeight, 100)) {
        // 标记为开始淡出
        star.fadeOutState = 1;
      }

      // 已经标记为开始淡出，逐渐降低透明度
      if (star.fadeOutState === 1) {
        star.alpha *= 0.95; // 逐渐降低透明度

        // 当透明度很低时，标记为已淡出
        if (star.alpha < 0.1) {
          star.fadeOutState = 2;
        }
      }

      // 当已淡出且完全离开屏幕，重置流星
      if (star.fadeOutState === 2 && isCompletelyOffScreen(star, svgWidth, svgHeight, 200)) {
        resetShootingStar(star, svgWidth, svgHeight);
        return star;
      }

      // 移动流星
      star.x += Math.cos(star.angle) * star.speed;
      star.y += Math.sin(star.angle) * star.speed;

      // 更新尾迹粒子 - 简化粒子系统
      if (star.hasTrail && star.particles.length < 10) {
        // 减少粒子生成频率
        if (Math.random() > 0.85 && star.fadeOutState === 0) {
          star.particles.push(createParticle(star));
        }
      }

      // 更新现有粒子
      if (star.hasTrail) {
        star.particles = star.particles.filter(p => {
          p.x += Math.cos(p.angle) * p.speed;
          p.y += Math.sin(p.angle) * p.speed;
          p.life -= 1;
          p.alpha = (p.life / p.maxLife) * 0.7;
          return p.life > 0;
        });
      }
    } else if (canActivate) {
      // 当可以激活时，只激活一个流星
      const shouldActivate = !shootingStars.some(s => s.active);

      if (shouldActivate) {
        activateShootingStar(star, svgWidth, svgHeight);
        // 更新最后激活时间并重新计算下一个激活间隔
        lastActivationTime = currentTime;
        currentActivationInterval = Math.random() * (MAX_ACTIVATION_INTERVAL - MIN_ACTIVATION_INTERVAL) + MIN_ACTIVATION_INTERVAL;
      }
    }

    return star;
  });

  // 筛选活跃的流星
  const activeStars = updatedShootingStars.filter(star => star.active && star.alpha > 0.1);

  // 性能优化：使用 D3 join 模式更新 DOM，而不是每帧删除重建
  const starGroups = shootingStarsGroup.selectAll<SVGGElement, EnhancedShootingStar>('.shooting-star-group')
    .data(activeStars, d => `star-${activeStars.indexOf(d)}`);

  // 移除不再活跃的流星
  starGroups.exit().remove();

  // 添加新流星
  const enterGroups = starGroups.enter()
    .append('g')
    .attr('class', 'shooting-star-group');

  // 为新流星添加元素（简化为2层而不是5层）
  enterGroups.append('line').attr('class', 'shooting-star-glow');
  enterGroups.append('line').attr('class', 'shooting-star-core');
  enterGroups.append('circle').attr('class', 'shooting-star-head');

  // 合并并更新所有流星
  const allGroups = enterGroups.merge(starGroups);

  // 更新发光效果
  allGroups.select('.shooting-star-glow')
    .attr('x1', d => d.x)
    .attr('y1', d => d.y)
    .attr('x2', d => d.x - Math.cos(d.angle) * (d.length * d.trailLength))
    .attr('y2', d => d.y - Math.sin(d.angle) * (d.length * d.trailLength))
    .attr('stroke', d => d.color)
    .attr('stroke-width', d => d.width * 4)
    .attr('stroke-opacity', d => d.alpha * 0.3)
    .attr('stroke-linecap', 'round');

  // 更新流星核心
  allGroups.select('.shooting-star-core')
    .attr('x1', d => d.x)
    .attr('y1', d => d.y)
    .attr('x2', d => d.x - Math.cos(d.angle) * d.length)
    .attr('y2', d => d.y - Math.sin(d.angle) * d.length)
    .attr('stroke', 'white')
    .attr('stroke-width', d => d.width + 1)
    .attr('stroke-opacity', d => d.alpha)
    .attr('stroke-linecap', 'round');

  // 更新流星头部
  allGroups.select('.shooting-star-head')
    .attr('cx', d => d.x)
    .attr('cy', d => d.y)
    .attr('r', d => d.width * 1.5 + 1)
    .attr('fill', 'white')
    .attr('fill-opacity', d => d.alpha);

  // 简化粒子渲染 - 只渲染少量粒子
  const allParticles = activeStars.flatMap(star => star.hasTrail ? star.particles.slice(0, 5) : []);

  shootingStarsGroup.selectAll<SVGCircleElement, ShootingStarParticle>('.star-particle')
    .data(allParticles)
    .join('circle')
    .attr('class', 'star-particle')
    .attr('cx', d => d.x)
    .attr('cy', d => d.y)
    .attr('r', d => d.size)
    .attr('fill', d => d.color)
    .attr('fill-opacity', d => d.alpha);

  return updatedShootingStars;
};

/**
 * 重置流星状态 - 确保从深蓝色边缘开始
 */
const resetShootingStar = (star: EnhancedShootingStar, width: number, height: number): void => {
  // 流星颜色集合 - 只使用深蓝色主题相关的颜色
  const colors = [
    THEME_COLORS.secondary, // 浅蓝色
    THEME_COLORS.accent,    // 青色
    '#ffffff',             // 白色用于点缀
  ];

  // 流星渐变ID - 只使用蓝色系渐变
  const gradients = [
    'blueShootingStarGradient',
    'url(#blueShootingStarGradient)'
  ];

  const colorIndex = Math.floor(Math.random() * colors.length);
  const gradientIndex = Math.floor(Math.random() * gradients.length);

  // 更新流星属性
  star.currentLife = star.lifeTime;
  star.color = colors[colorIndex];
  star.gradientId = gradients[gradientIndex];
  star.particles = [];
  star.shimmerEffect = Math.random() > 0.5;
  star.fadeOutState = 0; // 重置淡出状态

  // 将流星设为非活跃，等待重新激活
  star.active = false;
  star.alpha = 0;

  // 只从左侧或右侧进入
  const entrySide = Math.random() > 0.5 ? 1 : 3; // 1为右侧，3为左侧

  // 计算预期的退出边界 - 从右侧进入则从左侧退出，反之亦然
  star.exitSide = entrySide === 1 ? 3 : 1;

  // 根据入口边设置初始位置和角度
  if (entrySide === 1) { // 右侧
    star.x = width + 200;
    star.y = Math.random() * height;
    star.angle = Math.PI * 0.75 + Math.random() * Math.PI / 4; // 向左方向，角度范围更小
  } else { // 左侧
    star.x = -200;
    star.y = Math.random() * height;
    star.angle = Math.PI * 1.75 + Math.random() * Math.PI / 4; // 向右方向，角度范围更小
  }
};

/**
 * 激活一个非活跃的流星 - 确保从深蓝色边缘激活
 */
const activateShootingStar = (star: EnhancedShootingStar, width: number, height: number): void => {
  // 流星颜色集合 - 只使用深蓝色主题相关的颜色
  const colors = [
    THEME_COLORS.secondary, // 浅蓝色
    THEME_COLORS.accent,    // 青色
    '#ffffff',             // 白色用于点缀
  ];

  // 流星渐变ID - 只使用蓝色系渐变
  const gradients = [
    'blueShootingStarGradient',
    'url(#blueShootingStarGradient)'
  ];

  const colorIndex = Math.floor(Math.random() * colors.length);
  const gradientIndex = Math.floor(Math.random() * gradients.length);

  star.active = true;
  star.alpha = Math.random() * 0.7 + 0.3;
  star.currentLife = star.lifeTime;
  star.color = colors[colorIndex];
  star.gradientId = gradients[gradientIndex];
  star.shimmerEffect = Math.random() > 0.5;
  star.fadeOutState = 0; // 重置淡出状态

  // 降低速度
  star.speed = Math.random() * 4 + 2;

  // 只从左侧或右侧进入
  const entrySide = Math.random() > 0.5 ? 1 : 3; // 1为右侧，3为左侧

  // 计算预期的退出边界 - 从右侧进入则从左侧退出，反之亦然
  star.exitSide = entrySide === 1 ? 3 : 1;

  // 设置初始位置和角度
  if (entrySide === 1) { // 右侧
    star.x = width + 200;
    star.y = Math.random() * height;
    star.angle = Math.PI * 0.75 + Math.random() * Math.PI / 4; // 向左方向，角度范围更小
  } else { // 左侧
    star.x = -200;
    star.y = Math.random() * height;
    star.angle = Math.PI * 1.75 + Math.random() * Math.PI / 4; // 向右方向，角度范围更小
  }

  // 清空并重新生成尾迹粒子
  star.particles = [];
  if (star.hasTrail) {
    const particleCount = Math.floor(Math.random() * 15) + 5;
    for (let j = 0; j < particleCount; j++) {
      star.particles.push(createParticle(star));
    }
  }
};
