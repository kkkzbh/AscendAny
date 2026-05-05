import React, { useEffect, useRef } from 'react';

interface LightningEffectProps {
  cx: number;           // 中心X坐标
  cy: number;           // 中心Y坐标
  radius: number;       // 环绕半径
  color?: string;       // 雷电颜色
  intensity?: number;   // 强度 (0-1)
}

/**
 * 雷电环绕效果组件
 * 在薄弱知识点标签周围显示动态雷电效果
 */
const LightningEffect: React.FC<LightningEffectProps> = ({
  cx,
  cy,
  radius,
  color = '#4da6ff',
  intensity = 1,
}) => {
  const pathRef = useRef<SVGPathElement>(null);
  const animationRef = useRef<number | null>(null);

  useEffect(() => {
    if (!pathRef.current) return;

    // 生成雷电路径点
    const generateLightningPath = (time: number): string => {
      const points: [number, number][] = [];
      const segments = 24;
      const angleStep = (Math.PI * 2) / segments;

      for (let i = 0; i <= segments; i++) {
        const angle = i * angleStep + time * 0.002;
        // 添加随机偏移模拟雷电效果
        const jitter = (Math.sin(time * 0.01 + i * 2) * 0.3 + Math.random() * 0.2) * radius * 0.15;
        const r = radius + jitter;
        const x = cx + Math.cos(angle) * r;
        const y = cy + Math.sin(angle) * r;
        points.push([x, y]);
      }

      // 生成SVG路径
      if (points.length === 0) return '';
      let d = `M ${points[0][0]} ${points[0][1]}`;
      for (let i = 1; i < points.length; i++) {
        // 使用贝塞尔曲线使雷电更平滑
        const prev = points[i - 1];
        const curr = points[i];
        const midX = (prev[0] + curr[0]) / 2;
        const midY = (prev[1] + curr[1]) / 2;
        d += ` Q ${prev[0]} ${prev[1]} ${midX} ${midY}`;
      }
      d += ' Z';
      return d;
    };

    // 动画循环
    const animate = () => {
      const time = Date.now();
      if (pathRef.current) {
        pathRef.current.setAttribute('d', generateLightningPath(time));
      }
      animationRef.current = requestAnimationFrame(animate);
    };

    animate();

    return () => {
      if (animationRef.current) {
        cancelAnimationFrame(animationRef.current);
      }
    };
  }, [cx, cy, radius]);

  return (
    <g className="lightning-effect" opacity={intensity}>
      {/* 外层发光 */}
      <path
        ref={pathRef}
        fill="none"
        stroke={color}
        strokeWidth={2}
        strokeOpacity={0.6}
        filter="url(#lightningGlow)"
      />
      {/* 内层锐利线条 */}
      <path
        fill="none"
        stroke={color}
        strokeWidth={1}
        strokeOpacity={0.9}
        style={{ pointerEvents: 'none' }}
      >
        <animate
          attributeName="stroke-opacity"
          values="0.9;0.5;0.9"
          dur="0.15s"
          repeatCount="indefinite"
        />
      </path>
    </g>
  );
};

export default LightningEffect;
