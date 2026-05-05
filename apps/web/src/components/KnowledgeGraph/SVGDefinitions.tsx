import React from 'react';

/**
 * SVG定义组件，包含所有的渐变、滤镜和模式
 */
const SVGDefinitions: React.FC<{ defsRef: React.RefObject<SVGDefsElement | null> }> = ({ defsRef }) => {
  return (
    <defs ref={defsRef}>
      {/* 背景渐变 */}
      <radialGradient id="backgroundGradient" cx="50%" cy="50%" r="70%" fx="50%" fy="50%">
        <stop offset="0%" stopColor="#0a0a1a" /> {/* 更深的蓝紫色 */}
        <stop offset="50%" stopColor="#101035" /> {/* 深蓝色 */}
        <stop offset="100%" stopColor="#080830" /> {/* 深蓝紫色 */}
      </radialGradient>

      {/* 星空背景纹理 */}
      <pattern id="starNoisePattern" width="200" height="200" patternUnits="userSpaceOnUse">
        <image href="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgAAADICAMAAACahl6sAAAABGdBTUEAALGPC/xhBQAAAAFzUkdCAK7OHOkAAAMAUExURQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAP//"
          width="200" height="200" />
      </pattern>

      {/* 为核心标签球创建通用渐变模板 */}
      <radialGradient id="coreTagGradient" cx="30%" cy="30%" r="70%" fx="30%" fy="30%">
        <stop offset="0%" stopColor="white" stopOpacity="0.6" />
        <stop offset="100%" stopColor="white" stopOpacity="0" />
      </radialGradient>

      {/* 球体外发光效果 */}
      <filter id="glowEffect" x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="5" result="blur" />
        <feComposite in="SourceGraphic" in2="blur" operator="over" />
      </filter>

      {/* 流星发光效果 - 原版 */}
      <filter id="shootingStarGlow" x="-20%" y="-20%" width="140%" height="140%">
        <feGaussianBlur stdDeviation="3" result="blur" />
        <feComposite in="SourceGraphic" in2="blur" operator="over" />
      </filter>

      {/* 增强版流星发光效果 - 更强烈的发光 */}
      <filter id="enhancedShootingStarGlow" x="-30%" y="-30%" width="160%" height="160%">
        <feGaussianBlur stdDeviation="6" result="blur1" />
        <feGaussianBlur in="SourceGraphic" stdDeviation="3" result="blur2" />
        <feComposite in="blur2" in2="blur1" operator="over" result="blurCombined" />
        <feComponentTransfer in="blurCombined" result="brighter">
          <feFuncA type="linear" slope="1.5" />
        </feComponentTransfer>
        <feComposite in="SourceGraphic" in2="brighter" operator="over" />
      </filter>

      {/* 流星头部强光效果 */}
      <filter id="starHeadGlow" x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="2" result="blur" />
        <feColorMatrix
          type="matrix"
          values="1 0 0 0 1
                  0 1 0 0 1
                  0 0 1 0 1
                  0 0 0 3 0"
          result="brightBlur" />
        <feComposite in="SourceGraphic" in2="brightBlur" operator="over" />
      </filter>

      {/* 流星尾部粒子效果 */}
      <filter id="particleGlow" x="-100%" y="-100%" width="300%" height="300%">
        <feGaussianBlur stdDeviation="1.5" result="blur" />
        <feColorMatrix
          type="matrix"
          values="1 0 0 0 0.2
                  0 1 0 0 0.2
                  0 0 1 0 0.5
                  0 0 0 1 0"
          result="coloredBlur" />
        <feComposite in="SourceGraphic" in2="coloredBlur" operator="over" />
      </filter>

      {/* 流星颜色渐变 - 蓝色系 */}
      <linearGradient id="blueShootingStarGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#ffffff" />
        <stop offset="40%" stopColor="#a3dfff" />
        <stop offset="100%" stopColor="#56a5db" />
      </linearGradient>

      {/* 流星颜色渐变 - 黄色系 */}
      <linearGradient id="yellowShootingStarGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#ffffff" />
        <stop offset="40%" stopColor="#fff9c4" />
        <stop offset="100%" stopColor="#ffd54f" />
      </linearGradient>

      {/* 流星颜色渐变 - 粉色系 */}
      <linearGradient id="pinkShootingStarGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#ffffff" />
        <stop offset="40%" stopColor="#fce4ec" />
        <stop offset="100%" stopColor="#f48fb1" />
      </linearGradient>

      {/* 高亮路径渐变 */}
      <linearGradient id="highlightedPathGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#64ffda" />
        <stop offset="50%" stopColor="#1de9b6" />
        <stop offset="100%" stopColor="#00bfa5" />
      </linearGradient>

      {/* 高亮路径发光效果 */}
      <filter id="highlightedPathGlow" x="-30%" y="-30%" width="160%" height="160%">
        <feGaussianBlur stdDeviation="3" result="blur" />
        <feColorMatrix
          type="matrix"
          values="0 0 0 0 0.2
                  0 1 0 0 0.8
                  0 0 1 0 0.6
                  0 0 0 1 0"
          result="coloredBlur" />
        <feComposite in="SourceGraphic" in2="coloredBlur" operator="over" />
      </filter>

      {/* 高亮路径粒子效果 */}
      <filter id="highlightedPathParticles" x="-20%" y="-20%" width="140%" height="140%">
        <feTurbulence type="fractalNoise" baseFrequency="0.05" numOctaves="2" seed="5" />
        <feDisplacementMap in="SourceGraphic" scale="5" />
        <feGaussianBlur stdDeviation="1" />
        <feComposite in="SourceGraphic" operator="over" />
      </filter>

      {/* 高亮路径流动效果 */}
      <linearGradient id="flowingGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#64ffda" stopOpacity="0.1">
          <animate attributeName="offset" values="0;1" dur="3s" repeatCount="indefinite" />
        </stop>
        <stop offset="20%" stopColor="#64ffda" stopOpacity="0.8">
          <animate attributeName="offset" values="0.2;1.2" dur="3s" repeatCount="indefinite" />
        </stop>
        <stop offset="40%" stopColor="#1de9b6" stopOpacity="0.8">
          <animate attributeName="offset" values="0.4;1.4" dur="3s" repeatCount="indefinite" />
        </stop>
        <stop offset="60%" stopColor="#00bfa5" stopOpacity="0.6">
          <animate attributeName="offset" values="0.6;1.6" dur="3s" repeatCount="indefinite" />
        </stop>
        <stop offset="80%" stopColor="#64ffda" stopOpacity="0.2">
          <animate attributeName="offset" values="0.8;1.8" dur="3s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#64ffda" stopOpacity="0">
          <animate attributeName="offset" values="1;2" dur="3s" repeatCount="indefinite" />
        </stop>
      </linearGradient>

      {/* ===== Ascend API 集成 - 新增效果 ===== */}

      {/* 雷电环绕效果滤镜 - 用于薄弱知识点 */}
      <filter id="lightningEffect" x="-100%" y="-100%" width="300%" height="300%">
        <feTurbulence type="fractalNoise" baseFrequency="0.02" numOctaves="3" result="noise">
          <animate attributeName="baseFrequency" values="0.02;0.03;0.02" dur="0.5s" repeatCount="indefinite" />
        </feTurbulence>
        <feDisplacementMap in="SourceGraphic" in2="noise" scale="8" xChannelSelector="R" yChannelSelector="G" result="displaced" />
        <feColorMatrix in="displaced" type="matrix"
          values="0 0 0 0 0.4
                  0 0 0 0 0.7
                  0 0 0 0 1
                  0 0 0 1 0"
          result="colored" />
        <feGaussianBlur in="colored" stdDeviation="2" result="blurred" />
        <feMerge>
          <feMergeNode in="blurred" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 雷电外发光效果 */}
      <filter id="lightningGlow" x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="4" result="blur" />
        <feColorMatrix type="matrix"
          values="0 0 0 0 0.3
                  0 0 0 0 0.6
                  0 0 0 0 1
                  0 0 0 1.5 0"
          result="coloredGlow" />
        <feMerge>
          <feMergeNode in="coloredGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 激光流渐变 - 用于学习路径连接线 */}
      <linearGradient id="laserFlowGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#00ffff" stopOpacity="0">
          <animate attributeName="offset" values="0;1" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="15%" stopColor="#00ffff" stopOpacity="0.8">
          <animate attributeName="offset" values="0.15;1.15" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="30%" stopColor="#00ffff" stopOpacity="1">
          <animate attributeName="offset" values="0.3;1.3" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="50%" stopColor="#00bfff" stopOpacity="0.8">
          <animate attributeName="offset" values="0.5;1.5" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="70%" stopColor="#0080ff" stopOpacity="0.4">
          <animate attributeName="offset" values="0.7;1.7" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#0040ff" stopOpacity="0">
          <animate attributeName="offset" values="1;2" dur="2s" repeatCount="indefinite" />
        </stop>
      </linearGradient>

      {/* 激光流发光效果 */}
      <filter id="laserGlow" x="-20%" y="-20%" width="140%" height="140%">
        <feGaussianBlur stdDeviation="3" result="blur" />
        <feColorMatrix type="matrix"
          values="0 0 0 0 0
                  0 0 0 0 0.8
                  0 0 0 0 1
                  0 0 0 1.2 0"
          result="coloredGlow" />
        <feMerge>
          <feMergeNode in="coloredGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 更强的激光流渐变 - 用于掌握度蓝边 */}
      <linearGradient id="laserFlowGradientStrong" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#00ffff" stopOpacity="0">
          <animate attributeName="offset" values="0;1" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="10%" stopColor="#00ffff" stopOpacity="0.95">
          <animate attributeName="offset" values="0.1;1.1" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="22%" stopColor="#7df9ff" stopOpacity="1">
          <animate attributeName="offset" values="0.22;1.22" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="40%" stopColor="#00bfff" stopOpacity="0.9">
          <animate attributeName="offset" values="0.4;1.4" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="65%" stopColor="#0080ff" stopOpacity="0.55">
          <animate attributeName="offset" values="0.65;1.65" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#0040ff" stopOpacity="0">
          <animate attributeName="offset" values="1;2" dur="1s" repeatCount="indefinite" />
        </stop>
      </linearGradient>

      {/* 更强的激光流发光效果 */}
      <filter id="laserGlowStrong" x="-30%" y="-30%" width="160%" height="160%">
        <feGaussianBlur stdDeviation="5" result="blur" />
        <feColorMatrix type="matrix"
          values="0 0 0 0 0
                  0 0 0 0 0.9
                  0 0 0 0 1
                  0 0 0 1.6 0"
          result="coloredGlow" />
        <feMerge>
          <feMergeNode in="coloredGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 金色闪耀渐变 */}
      <linearGradient id="goldShimmerGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#fff7cc" stopOpacity="0">
          <animate attributeName="offset" values="0;1" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="18%" stopColor="#ffe08a" stopOpacity="0.9">
          <animate attributeName="offset" values="0.18;1.18" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="30%" stopColor="#fff2b2" stopOpacity="1">
          <animate attributeName="offset" values="0.3;1.3" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="45%" stopColor="#ffd54f" stopOpacity="0.95">
          <animate attributeName="offset" values="0.45;1.45" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="70%" stopColor="#ffb300" stopOpacity="0.4">
          <animate attributeName="offset" values="0.7;1.7" dur="2s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#fff7cc" stopOpacity="0">
          <animate attributeName="offset" values="1;2" dur="2s" repeatCount="indefinite" />
        </stop>
      </linearGradient>

      {/* 金光滤镜 */}

      {/* ===== 圆环内流动效果 ===== */}

      {/* 绿色流动渐变（沿圆周方向） */}
      <linearGradient id="greenFlowGradient" gradientUnits="userSpaceOnUse" x1="-50" y1="0" x2="50" y2="0">
        <stop offset="0%" stopColor="#40ff73" stopOpacity="0.3" />
        <stop offset="40%" stopColor="#7dff9e" stopOpacity="0.9">
          <animate attributeName="offset" values="0;0.4;0.8;0.4;0" dur="3s" repeatCount="indefinite" />
        </stop>
        <stop offset="60%" stopColor="#40ff73" stopOpacity="1">
          <animate attributeName="offset" values="0.2;0.6;1;0.6;0.2" dur="3s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#40ff73" stopOpacity="0.3" />
      </linearGradient>

      {/* 蓝色激光流动渐变 */}
      <linearGradient id="blueFlowGradient" gradientUnits="userSpaceOnUse" x1="-50" y1="0" x2="50" y2="0">
        <stop offset="0%" stopColor="#00e5ff" stopOpacity="0.4" />
        <stop offset="30%" stopColor="#7df9ff" stopOpacity="1">
          <animate attributeName="offset" values="0;0.3;0.6;0.3;0" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="50%" stopColor="#00e5ff" stopOpacity="1">
          <animate attributeName="offset" values="0.1;0.5;0.9;0.5;0.1" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="70%" stopColor="#0080ff" stopOpacity="0.8">
          <animate attributeName="offset" values="0.3;0.7;1;0.7;0.3" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#00e5ff" stopOpacity="0.4" />
      </linearGradient>

      {/* 绿色发光滤镜 */}
      <filter id="greenGlow" x="-30%" y="-30%" width="160%" height="160%">
        <feGaussianBlur stdDeviation="2.5" result="blur" />
        <feColorMatrix type="matrix"
          values="0 0 0 0 0.25
                  0 0 0 0 1
                  0 0 0 0 0.45
                  0 0 0 1.2 0"
          result="coloredGlow" />
        <feMerge>
          <feMergeNode in="coloredGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>
      <filter id="goldGlow" x="-40%" y="-40%" width="180%" height="180%">
        <feGaussianBlur stdDeviation="4" result="blur" />
        <feColorMatrix type="matrix"
          values="1 0 0 0 0.6
                  0 1 0 0 0.45
                  0 0 1 0 0
                  0 0 0 1.3 0"
          result="golden" />
        <feMerge>
          <feMergeNode in="golden" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 红色血气流渐变 */}
      <linearGradient id="bloodFlowGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#ff5252" stopOpacity="0">
          <animate attributeName="offset" values="0;1" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="12%" stopColor="#ff5252" stopOpacity="0.95">
          <animate attributeName="offset" values="0.12;1.12" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="28%" stopColor="#ff1744" stopOpacity="1">
          <animate attributeName="offset" values="0.28;1.28" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="45%" stopColor="#ff9100" stopOpacity="0.75">
          <animate attributeName="offset" values="0.45;1.45" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="70%" stopColor="#b71c1c" stopOpacity="0.5">
          <animate attributeName="offset" values="0.7;1.7" dur="1s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#ff5252" stopOpacity="0">
          <animate attributeName="offset" values="1;2" dur="1s" repeatCount="indefinite" />
        </stop>
      </linearGradient>

      {/* 红色发光滤镜 */}
      <filter id="bloodGlow" x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="5" result="blur" />
        <feColorMatrix type="matrix"
          values="1 0 0 0 0.9
                  0 1 0 0 0.05
                  0 0 1 0 0.05
                  0 0 0 1.6 0"
          result="redGlow" />
        <feMerge>
          <feMergeNode in="redGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 红色雾气/血气滤镜 */}
      <filter id="bloodMist" x="-120%" y="-120%" width="340%" height="340%">
        <feTurbulence type="fractalNoise" baseFrequency="0.015" numOctaves="3" seed="9" result="noise">
          <animate attributeName="baseFrequency" values="0.012;0.02;0.012" dur="0.8s" repeatCount="indefinite" />
        </feTurbulence>
        <feDisplacementMap in="SourceGraphic" in2="noise" scale="18" xChannelSelector="R" yChannelSelector="G" result="displaced" />
        <feColorMatrix in="displaced" type="matrix"
          values="1 0 0 0 0.9
                  0 0.2 0 0 0
                  0 0 0.2 0 0
                  0 0 0 1 0"
          result="colored" />
        <feGaussianBlur in="colored" stdDeviation="3" result="blurred" />
        <feMerge>
          <feMergeNode in="blurred" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 路径箭头标记 - 增强版 */}
      <marker id="pathArrow" markerWidth="12" markerHeight="12" refX="10" refY="4" orient="auto" markerUnits="strokeWidth">
        <path d="M0,0 L0,8 L12,4 z" fill="#00ffff" opacity="0.9" filter="url(#laserGlow)" />
      </marker>

      {/* ===== 激光连线增强效果 ===== */}

      {/* 激光核心发光 - 高亮白色细线效果 */}
      <filter id="laserCoreGlow" x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="1" result="blur1" />
        <feColorMatrix type="matrix"
          values="1 1 1 0 0.5
                  1 1 1 0 0.5
                  1 1 1 0 0.5
                  0 0 0 2 0"
          result="brightCore" />
        <feMerge>
          <feMergeNode in="brightCore" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 激光外发光层 - 大范围模糊光晕 */}
      <filter id="laserOuterGlow" x="-100%" y="-100%" width="300%" height="300%">
        <feGaussianBlur stdDeviation="8" result="blur" />
        <feColorMatrix type="matrix"
          values="0 0 0 0 0
                  0 0.8 0 0 0.9
                  0 0 1 0 1
                  0 0 0 1.5 0"
          result="coloredGlow" />
        <feMerge>
          <feMergeNode in="coloredGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 能量脉冲渐变 - 单向流动动画 */}
      <linearGradient id="energyPulseGradient" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stopColor="#0040ff" stopOpacity="0.1">
          <animate attributeName="offset" values="0;1" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="8%" stopColor="#00bfff" stopOpacity="0.6">
          <animate attributeName="offset" values="0.08;1.08" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="15%" stopColor="#00ffff" stopOpacity="1">
          <animate attributeName="offset" values="0.15;1.15" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="25%" stopColor="#7df9ff" stopOpacity="1">
          <animate attributeName="offset" values="0.25;1.25" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="35%" stopColor="#00ffff" stopOpacity="0.8">
          <animate attributeName="offset" values="0.35;1.35" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="50%" stopColor="#00bfff" stopOpacity="0.4">
          <animate attributeName="offset" values="0.5;1.5" dur="1.5s" repeatCount="indefinite" />
        </stop>
        <stop offset="100%" stopColor="#0040ff" stopOpacity="0">
          <animate attributeName="offset" values="1;2" dur="1.5s" repeatCount="indefinite" />
        </stop>
      </linearGradient>

      {/* 粒子拖尾渐变 */}
      <radialGradient id="particleTrailGradient" cx="50%" cy="50%" r="50%">
        <stop offset="0%" stopColor="#ffffff" stopOpacity="1" />
        <stop offset="30%" stopColor="#7df9ff" stopOpacity="0.9" />
        <stop offset="60%" stopColor="#00ffff" stopOpacity="0.5" />
        <stop offset="100%" stopColor="#00bfff" stopOpacity="0" />
      </radialGradient>

      {/* 粒子强发光效果 */}
      <filter id="particleIntenseGlow" x="-200%" y="-200%" width="500%" height="500%">
        <feGaussianBlur stdDeviation="3" result="blur1" />
        <feGaussianBlur in="SourceGraphic" stdDeviation="1" result="blur2" />
        <feColorMatrix in="blur1" type="matrix"
          values="0 0 0 0 0.3
                  0 0 0 0 0.9
                  0 0 0 0 1
                  0 0 0 2 0"
          result="coloredBlur" />
        <feMerge>
          <feMergeNode in="coloredBlur" />
          <feMergeNode in="blur2" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* ===== 薄弱知识点高亮效果（子标签页面） ===== */}

      {/* 薄弱点外层雾气滤镜 - 红色淡雾气 */}
      <filter id="weakPointMist" x="-150%" y="-150%" width="400%" height="400%">
        <feTurbulence type="fractalNoise" baseFrequency="0.02" numOctaves="3" seed="12" result="noise">
          <animate attributeName="baseFrequency" values="0.018;0.025;0.018" dur="3s" repeatCount="indefinite" />
        </feTurbulence>
        <feDisplacementMap in="SourceGraphic" in2="noise" scale="12" xChannelSelector="R" yChannelSelector="G" result="displaced" />
        <feColorMatrix in="displaced" type="matrix"
          values="1 0 0 0 0.8
                  0 0.15 0 0 0
                  0 0 0.15 0 0
                  0 0 0 0.6 0"
          result="colored" />
        <feGaussianBlur in="colored" stdDeviation="6" result="blurred" />
        <feMerge>
          <feMergeNode in="blurred" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 薄弱点呼吸灯发光滤镜 */}
      <filter id="weakPointBreathGlow" x="-80%" y="-80%" width="260%" height="260%">
        <feGaussianBlur stdDeviation="4" result="blur" />
        <feColorMatrix type="matrix"
          values="1 0 0 0 0.85
                  0 0.1 0 0 0
                  0 0 0.1 0 0
                  0 0 0 1.3 0"
          result="redGlow" />
        <feMerge>
          <feMergeNode in="redGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 薄弱点内层紧急呼吸发光 */}
      <filter id="weakPointInnerGlow" x="-50%" y="-50%" width="200%" height="200%">
        <feGaussianBlur stdDeviation="2" result="blur" />
        <feColorMatrix type="matrix"
          values="1 0 0 0 0.9
                  0 0.2 0 0 0.05
                  0 0 0.2 0 0.05
                  0 0 0 1.5 0"
          result="innerGlow" />
        <feMerge>
          <feMergeNode in="innerGlow" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>

      {/* 掌握度亮度调节滤镜 - 通过 feComponentTransfer 调整亮度 */}
      <filter id="masteryBrightness-low" x="0" y="0" width="100%" height="100%">
        <feComponentTransfer>
          <feFuncR type="linear" slope="0.4" />
          <feFuncG type="linear" slope="0.4" />
          <feFuncB type="linear" slope="0.4" />
        </feComponentTransfer>
      </filter>

      <filter id="masteryBrightness-medium" x="0" y="0" width="100%" height="100%">
        <feComponentTransfer>
          <feFuncR type="linear" slope="0.7" />
          <feFuncG type="linear" slope="0.7" />
          <feFuncB type="linear" slope="0.7" />
        </feComponentTransfer>
      </filter>

      <filter id="masteryBrightness-high" x="0" y="0" width="100%" height="100%">
        <feComponentTransfer>
          <feFuncR type="linear" slope="1.0" />
          <feFuncG type="linear" slope="1.0" />
          <feFuncB type="linear" slope="1.0" />
        </feComponentTransfer>
      </filter>
    </defs>
  );
};

export default SVGDefinitions;
