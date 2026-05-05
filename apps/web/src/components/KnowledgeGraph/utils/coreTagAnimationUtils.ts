import * as d3 from 'd3';

/**
 * 为核心标签添加浮动动画
 * 当进入子界面时，核心标签也应该有轻微的浮动效果
 * @param coreTagGroup 核心标签的D3选择器
 * @param basePosition 核心标签的基础位置
 * @param amplitude 浮动振幅，默认较小以保持稳定感
 * @param connectorsUpdateCallback 更新连接线的回调函数
 * @param positionUpdateCallback 回调函数，用于将当前位置传递出去
 * @returns 停止动画的函数
 */
export const startCoreTagFloatingAnimation = (
  coreTagGroup: d3.Selection<SVGGElement, unknown, null, undefined>,
  basePosition: { x: number, y: number },
  amplitude: number = 3,
  // connectorsUpdateCallback?: (position: { x: number, y: number }) => void, // 移除连接线更新回调
  positionUpdateCallback?: (position: { x: number, y: number }) => void // 新增位置更新回调
): (() => void) => {
  // 创建随机的动画参数
  const animationParams = {
    baseX: basePosition.x,
    baseY: basePosition.y,
    phaseX: Math.random() * Math.PI * 2, // 随机初始相位
    phaseY: Math.random() * Math.PI * 2, // 随机初始相位
    frequencyX: 0.0008 + Math.random() * 0.0004, // 较低频率比子标签慢
    frequencyY: 0.0008 + Math.random() * 0.0004, // 较低频率比子标签慢
    amplitude: 0, // 初始振幅为0，逐渐增加到目标值
    targetAmplitude: amplitude, // 目标振幅
    active: true // 动画激活状态
  };

  // 缓存计算值以提高性能
  const sinCache = new Map<string, number>();

  // 启动动画
  let frameId: number | null = null;

  // 随机延迟开始动画，防止所有元素同时启动
  const startDelay = Math.random() * 200; // 0-200ms的随机延迟

  // 振幅渐变参数
  const amplitudeStartTime = Date.now() + startDelay;
  const amplitudeFadeInDuration = 1000; // 1秒内从0渐变到目标振幅

  const animate = () => {
    if (!animationParams.active) return;

    const currentTime = Date.now();

    // 计算当前应用的振幅（渐变效果）
    if (animationParams.amplitude < animationParams.targetAmplitude) {
      if (currentTime > amplitudeStartTime) {
        const progress = Math.min(1, (currentTime - amplitudeStartTime) / amplitudeFadeInDuration);
        animationParams.amplitude = animationParams.targetAmplitude * progress;
      }
    }

    const { baseX, baseY, phaseX, phaseY, frequencyX, frequencyY, amplitude } = animationParams;

    // 计算时间相位
    const timePhaseX = currentTime * frequencyX + phaseX;
    const timePhaseY = currentTime * frequencyY + phaseY;

    // 使用缓存减少三角函数计算
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

    // 应用偏移
    const currentX = baseX + offsetX;
    const currentY = baseY + offsetY;

    // 更新标签位置
    coreTagGroup
      .attr('transform', `translate(${currentX}, ${currentY})`);

    // 调用连接线更新回调 - 改为调用位置更新回调
    // if (connectorsUpdateCallback && amplitude > 0) {
    //   connectorsUpdateCallback({ x: currentX, y: currentY });
    // }
    if (positionUpdateCallback && amplitude > 0) {
      positionUpdateCallback({ x: currentX, y: currentY });
    }

    // 持续动画
    frameId = requestAnimationFrame(animate);
  };

  // 启动动画循环
  setTimeout(() => {
    frameId = requestAnimationFrame(animate);
  }, startDelay);

  // 返回停止函数
  return () => {
    if (frameId !== null) {
      cancelAnimationFrame(frameId);
      frameId = null;
    }
    animationParams.active = false;
  };
};
