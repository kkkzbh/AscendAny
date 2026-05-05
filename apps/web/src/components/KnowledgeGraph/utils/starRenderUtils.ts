import * as d3 from 'd3';

/**
 * 同步星星层和主内容组的变换
 * 确保星星层跟随主内容组的缩放和平移
 *
 * @param mainGroup 主内容组元素引用
 * @param starsGroup 星星层元素引用
 * @param shootingStarsGroup 流星层元素引用
 */
export const syncStarLayersWithMainGroup = (
  mainGroup: SVGGElement | null,
  starsGroup: SVGGElement | null,
  shootingStarsGroup: SVGGElement | null
): (() => void) => {
  if (!mainGroup || !starsGroup || !shootingStarsGroup) {
    return () => {}; // 返回空函数
  }

  // 获取主内容组的当前变换
  const currentTransform = mainGroup.getAttribute('transform');

  // 设置初始变换
  if (currentTransform) {
    starsGroup.setAttribute('transform', currentTransform);
    shootingStarsGroup.setAttribute('transform', currentTransform);
  }

  // 设置观察器监听主内容组的变换变化
  const observer = new MutationObserver((mutations) => {
    mutations.forEach((mutation) => {
      if (mutation.type === 'attributes' && mutation.attributeName === 'transform') {
        const newTransform = (mutation.target as Element).getAttribute('transform');
        if (newTransform) {
          // 更新星星层的变换
          starsGroup.setAttribute('transform', newTransform);
          shootingStarsGroup.setAttribute('transform', newTransform);
        }
      }
    });
  });

  // 开始观察变换变化
  observer.observe(mainGroup, { attributes: true, attributeFilter: ['transform'] });

  // 返回清理函数
  return () => {
    observer.disconnect();
  };
};

/**
 * 设置星星层与背景尺寸一致
 * 确保星星层覆盖整个背景区域
 *
 * @param svgElement SVG元素
 * @param starsGroup 星星层组元素
 * @param shootingStarsGroup 流星层组元素
 */
export const adjustStarLayersSize = (
  svgElement: SVGSVGElement | null,
  starsGroup: SVGGElement | null,
  shootingStarsGroup: SVGGElement | null
): void => {
  if (!svgElement || !starsGroup || !shootingStarsGroup) {
    return;
  }

  // 查找背景矩形元素
  const backgroundRect = svgElement.querySelector('rect.background');
  if (!backgroundRect) {
    return;
  }

  // 获取背景尺寸和位置
  const bgWidth = backgroundRect.getAttribute('width');
  const bgHeight = backgroundRect.getAttribute('height');
  const bgX = backgroundRect.getAttribute('x');
  const bgY = backgroundRect.getAttribute('y');

  // 确保星星层的内容区域与背景一致
  d3.select(starsGroup)
    .attr('data-width', bgWidth)
    .attr('data-height', bgHeight)
    .attr('data-x', bgX)
    .attr('data-y', bgY);

  d3.select(shootingStarsGroup)
    .attr('data-width', bgWidth)
    .attr('data-height', bgHeight)
    .attr('data-x', bgX)
    .attr('data-y', bgY);
};

/**
 * 提升星星层的视觉层级
 * 确保星星显示在背景上方但在内容下方
 *
 * @param starsGroup 星星层引用
 * @param shootingStarsGroup 流星层引用
 */
export const raiseStarLayers = (
  starsGroup: SVGGElement | null,
  shootingStarsGroup: SVGGElement | null
): void => {
  if (!starsGroup || !shootingStarsGroup) return;

  // 使用 d3 的 raise 方法提升图层
  const starsSelection = d3.select(starsGroup);
  const shootingStarsSelection = d3.select(shootingStarsGroup);

  // 提升星星和流星层到适当位置
  starsSelection.raise();
  shootingStarsSelection.raise();
};
