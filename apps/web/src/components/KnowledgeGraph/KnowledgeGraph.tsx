import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import * as d3 from 'd3';
import styles from './KnowledgeGraph.module.css';
import CoreTags from './CoreTags';
import SubTags from './SubTags';
import PathSubTags from './PathSubTags';
import PathConnectors from './PathConnectors';
import InfoBox from './InfoBox';
import { TagData, InfoBoxData } from './types';
import { loadCoreTags, loadSubTags } from './utils/dataUtils';
import { FaArrowLeft } from 'react-icons/fa';
import StarBackground from './StarBackground';
import SVGDefinitions from './SVGDefinitions';
import { useStudent } from '../../contexts/StudentContext';

const KnowledgeGraph: React.FC = () => {
  // 状态管理
  const [coreTags, setCoreTags] = useState<TagData[]>([]);
  const [subTags, setSubTags] = useState<Record<string, TagData[]>>({});
  const [selectedTagId, setSelectedTagId] = useState<string | null>(null);
  const [hoveredTag, setHoveredTag] = useState<TagData | null>(null);
  const [showSubTags, setShowSubTags] = useState(false);
  const [infoBox, setInfoBox] = useState<InfoBoxData>({ visible: false, x: 0, y: 0, content: '', opacity: 0 });
  const infoBoxTimeoutRef = useRef<number | null>(null);
  const [dimensions, setDimensions] = useState({ width: window.innerWidth, height: window.innerHeight });

  // Ascend API 集成：标签位置状态
  const [tagPositions, setTagPositions] = useState<Map<string, { x: number; y: number }>>(new Map());

  // 路径子标签位置状态
  const [pathSubTagPositions, setPathSubTagPositions] = useState<Map<string, { x: number; y: number }>>(new Map());

  // 从 StudentContext 获取学生数据
  const { studentId, masteryMap, weakPoints, learningPath, fullLearningPath } = useStudent();

  // 调试日志
  console.log('[KnowledgeGraph] render state:', {
    selectedTagId,
    fullLearningPathLength: fullLearningPath.length,
    fullLearningPath,
    shouldRenderPathSubTags: !selectedTagId && fullLearningPath.length > 0,
  });

  // 合并核心标签位置和路径子标签位置，供 PathConnectors 使用
  const allTagPositions = useMemo(() => {
    const merged = new Map(tagPositions);
    pathSubTagPositions.forEach((pos, id) => merged.set(id, pos));
    return merged;
  }, [tagPositions, pathSubTagPositions]);

  // DOM引用
  const svgRef = useRef<SVGSVGElement | null>(null);
  const gRef = useRef<SVGGElement | null>(null);
  const defsRef = useRef<SVGDefsElement | null>(null);
  const starsGroupRef = useRef<SVGGElement | null>(null);
  const shootingStarsGroupRef = useRef<SVGGElement | null>(null);

  // 常量
  const transitionDuration = 800;

  // 计算中心点
  const centerX = dimensions.width / 2;
  const centerY = dimensions.height / 2;

  // 初始化缩放行为
  const zoom = useRef<d3.ZoomBehavior<SVGSVGElement, unknown> | null>(null);

  // 获取当前选中的核心标签数据
  const selectedCoreTag = selectedTagId ? coreTags.find(tag => tag.id === selectedTagId) || null : null;

  // 初始化数据
  useEffect(() => {
    const coreTagsData = loadCoreTags();
    const subTagsData = loadSubTags();

    console.log("Loaded tags data:", { coreTagsData, subTagsData });
    setCoreTags(coreTagsData);
    setSubTags(subTagsData);
  }, []);

  // 初始化SVG和缩放行为
  useEffect(() => {
    if (!svgRef.current || !gRef.current) return;

    const svg = d3.select(svgRef.current);
    const g = d3.select(gRef.current);
    const width = svgRef.current.clientWidth;
    const height = svgRef.current.clientHeight;

    // 设置尺寸
    setDimensions({
      width,
      height
    });

    // 初始化缩放
    if (!zoom.current) {
      console.log('[KnowledgeGraph] Initializing D3 zoom behavior.');
      zoom.current = d3.zoom<SVGSVGElement, unknown>()
        .scaleExtent([0.5, 4])
        .on('zoom', (event) => {
          g.attr('transform', event.transform.toString());

          // 直接在缩放事件中同步星星图层的变换
          if (starsGroupRef.current && shootingStarsGroupRef.current) {
            d3.select(starsGroupRef.current).attr('transform', event.transform.toString());
            d3.select(shootingStarsGroupRef.current).attr('transform', event.transform.toString());
          }
        });

      svg.call(zoom.current);
      svg.on("dblclick.zoom", null); // 禁用双击缩放
    }

    // 创建背景
    if (g.select('rect.background').empty()) {
      // 渐变背景
      g.append('rect')
        .attr('class', 'background')
        .attr('width', width * 4)
        .attr('height', height * 4)
        .attr('x', -width * 1.5)
        .attr('y', -height * 1.5)
        .attr('fill', 'url(#backgroundGradient)')
        .lower();

      // 星云纹理层
      g.append('rect')
        .attr('class', 'star-texture')
        .attr('width', width * 4)
        .attr('height', height * 4)
        .attr('x', -width * 1.5)
        .attr('y', -height * 1.5)
        .attr('fill', 'url(#starNoisePattern)')
        .attr('opacity', 0.2)
        .lower();
    }

    // 确保背景显示在其他元素下方
    g.selectAll('rect.background, rect.star-texture').lower();
  }, []);

  // 窗口大小变化监听
  useEffect(() => {
    const handleResize = () => {
      if (!svgRef.current || !gRef.current) return;

      const width = svgRef.current.clientWidth;
      const height = svgRef.current.clientHeight;

      // 更新尺寸状态
      setDimensions({
        width,
        height
      });

      // 更新背景尺寸
      const g = d3.select(gRef.current);
      g.select('rect.background')
        .attr('width', width * 4)
        .attr('height', height * 4)
        .attr('x', -width * 1.5)
        .attr('y', -height * 1.5);

      g.select('rect.star-texture')
        .attr('width', width * 4)
        .attr('height', height * 4)
        .attr('x', -width * 1.5)
        .attr('y', -height * 1.5);
    };

    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  // 处理背景点击
  const handleBackgroundClick = useCallback(() => {
    if (!selectedTagId || !zoom.current || !svgRef.current) return;

    console.log('[KnowledgeGraph] Background clicked, returning to main view.');

    // 重置状态
    setSelectedTagId(null);
    setShowSubTags(false);

    // 重置缩放
    d3.select(svgRef.current)
      .transition()
      .duration(transitionDuration)
      .call(zoom.current.transform, d3.zoomIdentity);
  }, [selectedTagId, transitionDuration]);

  // 更新背景交互状态
  useEffect(() => {
    if (!gRef.current) return;

    const g = d3.select(gRef.current);
    g.select('rect.background')
      .style('cursor', selectedTagId && showSubTags ? 'pointer' : 'default')
      .attr('pointer-events', selectedTagId && showSubTags ? 'all' : 'none')
      .on('click', event => {
        if (selectedTagId && showSubTags) {
          event.stopPropagation();
          handleBackgroundClick();
        }
      });
  }, [selectedTagId, showSubTags, handleBackgroundClick]);

  // 缩放到选中的标签
  const zoomToTag = useCallback((_tagId: string, x: number, y: number) => {
    if (!svgRef.current || !zoom.current) return;

    const svg = d3.select(svgRef.current);
    const width = dimensions.width;
    const height = dimensions.height;

    // 计算缩放因子和位移
    const scale = 2;
    const tx = width / 2 - x * scale;
    const ty = height / 2 - y * scale;

    // 应用缩放
    svg.transition()
      .duration(transitionDuration)
      .call(zoom.current.transform, d3.zoomIdentity.translate(tx, ty).scale(scale))
      .on('end', () => {
        setShowSubTags(true);
      });

    // 立即显示子标签
    setTimeout(() => {
      setShowSubTags(true);
    }, 100);
  }, [dimensions, transitionDuration]);

  // 处理核心标签悬停的回调
  const handleCoreTagHover = useCallback((tag: TagData | null, x?: number, y?: number) => {
    if (tag && x !== undefined && y !== undefined) {
      setInfoBox({
        visible: true,
        x: x + 15,
        y: y + 15,
        content: tag.name,
        opacity: 1,
        tagId: tag.id,
        tagName: tag.name,
        tagScore: masteryMap[tag.id] ?? 0,
        isCoreTag: true, // 标记为核心标签
        isWeakPoint: weakPoints.includes(tag.id),
        isOnLearningPath: learningPath.includes(tag.id),
      });
    } else {
      setInfoBox(prev => ({ ...prev, opacity: 0 }));
      // 延迟隐藏，让淡出动画完成
      setTimeout(() => {
        setInfoBox(prev => {
          if (prev.opacity === 0) {
            return { ...prev, visible: false };
          }
          return prev;
        });
      }, 350);
    }
    setHoveredTag(tag);
  }, [masteryMap, weakPoints, learningPath]);

  return (
    <div className={styles.knowledgeGraphContainer}>
      {/* 返回按钮 */}
      {selectedTagId && (
        <button
          className={styles.backButton}
          onClick={handleBackgroundClick}
          aria-label="返回"
        >
          <FaArrowLeft />
        </button>
      )}

      <svg
        ref={svgRef}
        className={styles.graphSvg}
      >
        {/* SVG定义 */}
        <SVGDefinitions defsRef={defsRef} />

        {/* 星星和流星组 - 移到gRef之前确保它们在主内容组下方但在背景上方 */}
        <g ref={starsGroupRef} className="stars-layer"></g>
        <g ref={shootingStarsGroupRef} className="shooting-stars-layer"></g>

        {/* 主要内容组 - 包含背景和标签 */}
        <g ref={gRef}></g>
      </svg>

      {/* 核心标签 */}
      <CoreTags
        gRef={gRef}
        defsRef={defsRef}
        coreTags={coreTags}
        selectedTagId={selectedTagId}
        setSelectedTagId={setSelectedTagId}
        setHoveredTag={setHoveredTag}
        setShowSubTags={setShowSubTags}
        centerX={centerX}
        centerY={centerY}
        transitionDuration={transitionDuration}
        zoomToTag={zoomToTag}
        masteryMap={masteryMap}
        weakPoints={weakPoints}
        onPositionsUpdate={setTagPositions}
        svgRef={svgRef}
        onCoreTagHover={handleCoreTagHover}
      />

      {/* 学习路径连接线 */}
      <PathConnectors
        gRef={gRef}
        learningPath={learningPath}
        tagPositions={allTagPositions}
        visible={!selectedTagId && learningPath.length > 1}
      />

      {/* 路径规划子标签 - 在核心标签视图中显示 */}
      {!selectedTagId && fullLearningPath.length > 0 && (
        <PathSubTags
          gRef={gRef}
          defsRef={defsRef}
          learningPath={fullLearningPath}
          coreTagPositions={tagPositions}
          subTags={subTags}
          visible={!selectedTagId}
          onSubTagPositionsUpdate={setPathSubTagPositions}
          masteryMap={masteryMap}
          weakPoints={weakPoints}
        />
      )}

      {/* 子标签 */}
      {selectedTagId && showSubTags && selectedCoreTag && (
        <SubTags
          svgRef={svgRef}
          gRef={gRef}
          defsRef={defsRef}
          coreTag={selectedCoreTag}
          showSubTags={showSubTags}
          subTags={subTags}
          transitionDuration={transitionDuration}
          setInfoBox={setInfoBox}
          infoBoxTimeoutRef={infoBoxTimeoutRef}
          studentId={studentId}
          masteryMap={masteryMap}
          weakPoints={weakPoints}
        />
      )}

      {/* 星空背景动画 */}
      <StarBackground
        mainGroupRef={gRef}
        starsGroupRef={starsGroupRef}
        shootingStarsGroupRef={shootingStarsGroupRef}
      />

      {/* 信息框组件 */}
      <InfoBox
        hoveredTag={hoveredTag}
        selectedTagId={selectedTagId}
        infoBox={infoBox}
        setInfoBox={setInfoBox}
      />
    </div>
  );
};

export default KnowledgeGraph;
