import React, { useEffect, useRef } from 'react';
import * as d3 from 'd3';
import { Star } from './types';
import { generateStars, updateStars } from './utils/animationUtils';
import {
  generateEnhancedShootingStars,
  updateEnhancedShootingStars,
  EnhancedShootingStar
} from './utils/enhancedShootingStars';
import { syncStarLayersWithMainGroup, adjustStarLayersSize, raiseStarLayers } from './utils/starRenderUtils';

interface StarBackgroundProps {
  mainGroupRef: React.RefObject<SVGGElement | null>;
  starsGroupRef: React.RefObject<SVGGElement | null>;
  shootingStarsGroupRef: React.RefObject<SVGGElement | null>;
}

/**
 * 星空背景组件，处理星星闪烁和流星动画效果
 */
const StarBackground: React.FC<StarBackgroundProps> = ({
  mainGroupRef,
  starsGroupRef,
  shootingStarsGroupRef
}) => {
  const starsRef = useRef<Star[]>([]);
  const shootingStarsRef = useRef<EnhancedShootingStar[]>([]);
  const animationFrameRef = useRef<number | null>(null);
  const cleanupFnRef = useRef<(() => void) | null>(null);

  // 初始化星空和动画
  useEffect(() => {
    const starsG = starsGroupRef.current;
    const shootingStarsG = shootingStarsGroupRef.current;
    const svgElement = starsG?.closest('svg') as SVGSVGElement | undefined;
    const mainGroup = mainGroupRef.current ?? undefined;

    console.log('[StarBackground] useEffect triggered.', { starsG, shootingStarsG, svgElement, mainGroup });

    if (!svgElement || !starsG || !shootingStarsG || !mainGroup) {
        console.warn('[StarBackground] Missing refs or required elements.');
        return;
    }

    // 1. 同步星星图层和主内容图层的变换
    cleanupFnRef.current = syncStarLayersWithMainGroup(mainGroup, starsG, shootingStarsG);

    // 2. 调整星星图层尺寸与背景一致
    adjustStarLayersSize(svgElement, starsG, shootingStarsG);

    // 3. 提升星星图层在视觉层次中的位置
    raiseStarLayers(starsG, shootingStarsG);

    // 获取SVG视口尺寸
    const width = svgElement.clientWidth;
    const height = svgElement.clientHeight;
    console.log(`[StarBackground] SVG dimensions: ${width}x${height}`);

    // 生成星星和增强版流星，尺寸与深蓝色背景匹配
    // 性能优化：减少星星数量从400到150
    const stars = generateStars(width * 4, height * 4, 150);
    // 使用增强版流星生成函数，减少流星数量
    const shootingStars = generateEnhancedShootingStars(width * 4, height * 4, 8);

    // 调整星星位置，使其覆盖整个深蓝色背景区域
    stars.forEach(star => {
      star.x = star.x - width * 1.5; // 与背景矩形偏移相同
      star.y = star.y - height * 1.5;
    });

    console.log(`[StarBackground] Generated ${stars.length} stars and ${shootingStars.length} shooting stars.`);

    starsRef.current = stars;
    shootingStarsRef.current = shootingStars;

    // 性能优化：使用节流控制更新频率
    let lastStarUpdateTime = 0;
    let lastShootingStarUpdateTime = 0;
    const STAR_UPDATE_INTERVAL = 100; // 星星闪烁每100ms更新一次
    const SHOOTING_STAR_UPDATE_INTERVAL = 33; // 流星每33ms更新一次 (~30fps)

    // 动画函数
    const animate = () => {
      if (!starsGroupRef.current || !shootingStarsGroupRef.current) return;

      const now = Date.now();
      const time = now / 1000;
      const starsGroup = d3.select(starsGroupRef.current);
      const shootingStarsGroup = d3.select(shootingStarsGroupRef.current);

      // 节流更新星星闪烁效果
      if (now - lastStarUpdateTime > STAR_UPDATE_INTERVAL) {
        updateStars(starsGroup, starsRef.current, time);
        lastStarUpdateTime = now;
      }

      // 节流更新流星
      if (now - lastShootingStarUpdateTime > SHOOTING_STAR_UPDATE_INTERVAL) {
        const updatedShootingStars = updateEnhancedShootingStars(
          shootingStarsGroup,
          shootingStarsRef.current,
          width * 4,
          height * 4,
          time
        );
        shootingStarsRef.current = updatedShootingStars;
        lastShootingStarUpdateTime = now;
      }

      // 继续下一帧动画
      animationFrameRef.current = requestAnimationFrame(animate);
    };

    // 开始动画
    animationFrameRef.current = requestAnimationFrame(animate);

    // 监听窗口大小变化，重新调整尺寸
    const handleResize = () => {
      adjustStarLayersSize(svgElement, starsG, shootingStarsG);
    };

    window.addEventListener('resize', handleResize);

    // 清理函数
    return () => {
      if (animationFrameRef.current) {
        cancelAnimationFrame(animationFrameRef.current);
        animationFrameRef.current = null;
      }

      if (cleanupFnRef.current) {
        cleanupFnRef.current();
        cleanupFnRef.current = null;
      }

      window.removeEventListener('resize', handleResize);
    };
  }, [mainGroupRef, starsGroupRef, shootingStarsGroupRef]);

  return null; // 这个组件不直接渲染内容，只处理动画逻辑
};

export default StarBackground;
