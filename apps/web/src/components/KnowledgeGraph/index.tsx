import React, { useRef, useState, useCallback, useEffect, useMemo } from 'react';
import * as d3 from 'd3';
import { TagData, InfoBoxData } from './types';
import { loadCoreTags, loadSubTags } from './utils/dataUtils';
import SVGDefinitions from './SVGDefinitions';
import StarBackground from './StarBackground';
import CoreTags from './CoreTags';
import SubTags from './SubTags';
import PathSubTags from './PathSubTags';
import BackButton from './BackButton';
import InfoBox from './InfoBox';
import PathConnectors from './PathConnectors';
import PathStartHighlight from './PathStartHighlight';
import PathStartScreenArrows from './PathStartScreenArrows';
import { useStudent } from '../../contexts/StudentContext';
import { fetchKnowledgeTagDetail, mapApiToWebId } from '../../services/api';

// 加载标签数据
const coreTags = loadCoreTags();
const subTags = loadSubTags();

/**
 * 知识图谱组件 - 主组件
 */
const KnowledgeGraph: React.FC = () => {
  const TAG_DETAIL_CACHE_TTL_MS = 60 * 1000;
  // 引用
  const svgRef = useRef<SVGSVGElement>(null);
  const gRef = useRef<SVGGElement>(null);
  const staticGroupRef = useRef<SVGGElement>(null);
  const starsGroupRef = useRef<SVGGElement>(null);
  const shootingStarsGroupRef = useRef<SVGGElement>(null);
  const defsRef = useRef<SVGDefsElement>(null);
  const zoomRef = useRef<d3.ZoomBehavior<SVGSVGElement, unknown> | null>(null);
  const infoBoxTimeoutRef = useRef<number | null>(null);
  const coreInfoBoxHideTimeoutRef = useRef<number | null>(null);
  const pathSubInfoBoxHideTimeoutRef = useRef<number | null>(null);
  const tagDetailCacheRef = useRef(
    new Map<
      string,
      { ts: number; acCount: number; recommendedProblems: InfoBoxData['recommendedProblems'] }
    >()
  );

  // 状态
  const [selectedTagId, setSelectedTagId] = useState<string | null>(null);
  const [hoveredTag, setHoveredTag] = useState<TagData | null>(null);
  const [showSubTags, setShowSubTags] = useState<boolean>(false);
  const [infoBox, setInfoBox] = useState<InfoBoxData>({
    visible: false,
    x: 0,
    y: 0,
    content: '',
    opacity: 1
  });

  // 屏幕尺寸状态
  const [dimensions, setDimensions] = useState({
    width: window.innerWidth,
    height: window.innerHeight
  });

  // Ascend API 集成
  const { studentId, masteryMap, weakPoints, learningPath, fullLearningPath } = useStudent();
  const [tagPositions, setTagPositions] = useState<Map<string, { x: number; y: number }>>(new Map());
  const [pathSubTagPositions, setPathSubTagPositions] = useState<Map<string, { x: number; y: number }>>(new Map());

  // 合并核心标签位置和路径子标签位置，供 PathConnectors 使用
  const allTagPositions = useMemo(() => {
    const merged = new Map(tagPositions);
    pathSubTagPositions.forEach((pos, id) => merged.set(id, pos));
    return merged;
  }, [tagPositions, pathSubTagPositions]);

  // 构建“名称 -> 标签ID”的映射（核心标签 + 子标签），用于把 fullLearningPath 映射成可连线的 tagId 序列
  const nameToTagId = useMemo(() => {
    const map = new Map<string, string>();

    // 核心标签：中文名称 -> coreId
    coreTags.forEach((t) => {
      map.set(t.name.trim(), t.id);
    });

    // 子标签：中文名称 -> subTagId（递归）
    const walk = (tags: TagData[]) => {
      tags.forEach((t) => {
        map.set(t.name.trim(), t.id);
        if (t.children && t.children.length > 0) {
          walk(t.children);
        }
      });
    };

    Object.values(subTags).forEach(walk);
    return map;
  }, []);

  // 学习路径（用于激光连线）：按顺序映射为前端 tagId（核心 + 子标签）
  const laserLearningPath = useMemo(() => {
    const ids: string[] = [];

    for (const name of fullLearningPath) {
      const trimmed = name.trim();
      const mapped = nameToTagId.get(trimmed);
      if (!mapped) continue;
      if (ids.length === 0 || ids[ids.length - 1] !== mapped) {
        ids.push(mapped);
      }
    }

    return ids;
  }, [fullLearningPath, nameToTagId]);

  // 核心标签悬停 InfoBox
  const handleCoreTagHover = useCallback((tag: TagData | null, x?: number, y?: number) => {
    if (coreInfoBoxHideTimeoutRef.current) {
      window.clearTimeout(coreInfoBoxHideTimeoutRef.current);
      coreInfoBoxHideTimeoutRef.current = null;
    }

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
        isCoreTag: true,
        isWeakPoint: weakPoints.includes(tag.id),
        isOnLearningPath: learningPath.includes(tag.id),
      });
      return;
    }

    setInfoBox(prev => ({ ...prev, opacity: 0 }));
    coreInfoBoxHideTimeoutRef.current = window.setTimeout(() => {
      setInfoBox(prev => {
        if (prev.opacity === 0) {
          return { ...prev, visible: false };
        }
        return prev;
      });
    }, 350);
  }, [learningPath, masteryMap, weakPoints]);

  // 路径子标签悬停 InfoBox（核心标签主视图）
  const handlePathSubTagHover = useCallback((tag: TagData | null, x?: number, y?: number) => {
    if (pathSubInfoBoxHideTimeoutRef.current) {
      window.clearTimeout(pathSubInfoBoxHideTimeoutRef.current);
      pathSubInfoBoxHideTimeoutRef.current = null;
    }

    if (tag && x !== undefined && y !== undefined) {
      const mappedId = mapApiToWebId(tag.name);
      const mastery = masteryMap[tag.id]
        ?? (mappedId ? masteryMap[mappedId] : undefined)
        ?? masteryMap[tag.name]
        ?? masteryMap[tag.name.trim()]
        ?? 0;
      const inWeakPointsList = weakPoints.includes(tag.id) ||
        (mappedId ? weakPoints.includes(mappedId) : false) ||
        weakPoints.includes(tag.name) ||
        weakPoints.includes(tag.name.trim());

      setInfoBox({
        visible: true,
        x: x + 15,
        y: y + 15,
        content: tag.name,
        opacity: 1,
        tagId: tag.id,
        tagName: tag.name,
        tagScore: mastery,
        isCoreTag: false,
        isWeakPoint: inWeakPointsList || mastery === 0,
        isOnLearningPath: laserLearningPath.includes(tag.id),
        acCount: undefined,
        recommendedProblems: undefined,
      });

      // 异步补齐：已解题数 + 题目推荐（缓存 + 防串数据）
      const sid = (studentId || '').trim();
      const kpName = (tag.name || '').trim();
      if (sid && kpName) {
        const cacheKey = `${sid}::${kpName}`;
        const cached = tagDetailCacheRef.current.get(cacheKey);

        const applyDetail = (detail: { acCount: number; recommendedProblems: InfoBoxData['recommendedProblems'] }) => {
          setInfoBox(prev => {
            if (!prev.visible) return prev;
            if (prev.tagId !== tag.id) return prev;
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
              // keep silent
            });
        }
      }
      return;
    }

    setInfoBox(prev => ({ ...prev, opacity: 0 }));
    pathSubInfoBoxHideTimeoutRef.current = window.setTimeout(() => {
      setInfoBox(prev => {
        if (prev.opacity === 0) {
          return { ...prev, visible: false };
        }
        return prev;
      });
    }, 350);
  }, [laserLearningPath, masteryMap, studentId, weakPoints]);

  // 常量
  const transitionDuration = 750; // 动画持续时间

  // 使用 useMemo 查找选中的核心标签数据
  const selectedCoreTag = useMemo(() => {
    if (!selectedTagId) return null;
    return coreTags.find(tag => tag.id === selectedTagId) || null;
  }, [selectedTagId]);

  // 返回主视图
  const returnToMainView = useCallback(() => {
    if (!selectedTagId || !zoomRef.current || !svgRef.current || !gRef.current || !staticGroupRef.current) return;

    const zoom = zoomRef.current;
    const svg = d3.select(svgRef.current);
    const g = d3.select(gRef.current);
    const staticGroup = d3.select(staticGroupRef.current);

    // 重置遮罩层透明度，确保背景不再变暗
    g.select<SVGRectElement>('.dim-overlay')
      .transition().duration(300)
      .attr('opacity', 0);

    // 动画缩放回主视图
    svg.interrupt()
       .transition('zoom-out').duration(transitionDuration)
       .call(zoom.transform, d3.zoomIdentity);

    // 动画显示核心标签球
    g.selectAll<SVGGElement, TagData>('.core-tag-group')
      .transition('core-fade-in').duration(transitionDuration).delay(transitionDuration * 0.3)
      .style('opacity', 1)
      .style('pointer-events', 'all');

    // 动画隐藏子标签球
    g.selectAll('.subtag-group')
      .transition('sub-fade-out').duration(transitionDuration / 2)
      .style('opacity', 0)
      .style('pointer-events', 'none')
      .remove();

    // 彻底清除连接线和连接线容器
    g.selectAll('.subtag-connector')
      .transition().duration(transitionDuration / 3)
      .style('opacity', 0)
      .remove();

    g.selectAll('.connectors-container')
      .transition().duration(transitionDuration / 3)
      .style('opacity', 0)
      .remove();

    // 动画隐藏返回按钮
    staticGroup.select('.back-button-group')
      .style('pointer-events', 'none')
      .transition('back-fade-out').duration(transitionDuration / 2)
      .style('opacity', 0)
      .remove();

    // 清除悬停状态
    setHoveredTag(null);

    // 清除选中状态
    setSelectedTagId(null);
    setShowSubTags(false);
  }, [selectedTagId, setHoveredTag]);

  // 缩放到指定标签
  const zoomToTag = useCallback((tagId: string, clickedX: number, clickedY: number) => {
    if (!zoomRef.current || !svgRef.current || !gRef.current) return;

    const zoom = zoomRef.current;
    const svg = d3.select(svgRef.current);
    const g = d3.select(gRef.current);

    const width = svgRef.current.clientWidth;
    const height = svgRef.current.clientHeight;
    const centerX = width / 2;
    const centerY = height / 2;

    // 计算缩放目标
    const targetScale = 1.8;
    const targetX = centerX - clickedX * targetScale;
    const targetY = centerY - clickedY * targetScale;
    const targetTransform = d3.zoomIdentity.translate(targetX, targetY).scale(targetScale);

    // 清除现有的子标签球和连接线
    g.selectAll('.subtag-group, .subtag-connector').remove();

    // 动画缩放
    svg.interrupt()
       .transition('zoom-in').duration(transitionDuration)
       .call(zoom.transform, targetTransform);

    // 仅显示选中的核心标签球，隐藏其他
    g.selectAll<SVGGElement, TagData>('.core-tag-group')
      .transition('core-fade-out').duration(transitionDuration)
      .style('opacity', tagData => (tagData.id === tagId ? 1 : 0))
      .style('pointer-events', 'none');

    // 缩放完成后显示子标签球和返回按钮
    setTimeout(() => {
      setShowSubTags(true);
    }, transitionDuration);
  }, [transitionDuration]);

  // 初始化缩放行为和背景
  useEffect(() => {
    if (!svgRef.current || !gRef.current) return;

    const svg = d3.select(svgRef.current);
    const g = d3.select(gRef.current);

    // 初始化缩放
    if (!zoomRef.current) {
      const zoom = d3.zoom<SVGSVGElement, unknown>()
        .scaleExtent([0.5, 5])
        .on('zoom', (event) => {
          g.attr('transform', event.transform.toString());
        });

      zoomRef.current = zoom;
      svg.call(zoom).call(zoom.transform, d3.zoomIdentity);
      svg.on("dblclick.zoom", null); // 禁用双击缩放
    }

    // 创建背景
    if (g.select('rect.background').empty()) {
      const width = svgRef.current.clientWidth;
      const height = svgRef.current.clientHeight;

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

      // 为背景添加点击事件
      g.select('rect.background')
        .style('cursor', selectedTagId && showSubTags ? 'pointer' : 'default')
        .on('click', event => {
          // 只有在子标签视图时才响应点击
          if (selectedTagId && showSubTags) {
            event.stopPropagation();
            returnToMainView();
          }
        });
    }

    // 更新背景的交互状态
    g.select('rect.background')
      .style('cursor', selectedTagId && showSubTags ? 'pointer' : 'default')
      .attr('pointer-events', selectedTagId && showSubTags ? 'all' : 'none');
  }, [selectedTagId, showSubTags, returnToMainView]);

  // 添加全局点击事件来处理返回
  useEffect(() => {
    if (!svgRef.current) return;

    const svg = d3.select(svgRef.current);

    // 添加SVG层级的点击事件（作为空白区域点击的备用方案）
    const handleGlobalClick = (event: MouseEvent) => {
      // 确保点击事件不是来自标签或返回按钮
      const target = event.target as Element;
      const isTagOrButton = target.closest('.core-tag-group') ||
                           target.closest('.subtag-group') ||
                           target.closest('.back-button-group');

      // 如果不是点击在特定元素上且处于子标签视图，则返回主视图
      if (!isTagOrButton && selectedTagId && showSubTags) {
        returnToMainView();
      }
    };

    // 添加事件监听器
    svg.node()?.addEventListener('click', handleGlobalClick);

    // 清理函数
    return () => {
      svg.node()?.removeEventListener('click', handleGlobalClick);
    };
  }, [selectedTagId, showSubTags, returnToMainView]);

  // 窗口大小变化监听
  useEffect(() => {
    const handleResize = () => {
      // 更新尺寸状态
      setDimensions({
        width: window.innerWidth,
        height: window.innerHeight
      });

      // 重新创建背景
      if (svgRef.current && gRef.current) {
        const svg = d3.select(svgRef.current);
        const g = d3.select(gRef.current);

        const width = svgRef.current.clientWidth;
        const height = svgRef.current.clientHeight;

        // 更新背景尺寸
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

        // 如果在主视图，重置变换以确保居中
        if (!selectedTagId) {
          if (zoomRef.current) {
            svg.call(zoomRef.current.transform, d3.zoomIdentity);
          }
        }
      }
    };

    // 添加窗口大小变化监听器
    window.addEventListener('resize', handleResize);

    // 组件卸载时移除监听器
    return () => {
      window.removeEventListener('resize', handleResize);
    };
  }, [selectedTagId]);

  return (
    <div style={{ position: 'relative', width: '100%', height: '100%', overflow: 'hidden' }}>
      <svg ref={svgRef} style={{ width: '100%', height: '100%' }}>
        {/* SVG定义（渐变、滤镜等） */}
        <SVGDefinitions defsRef={defsRef} />

        {/* 星空背景层 */}
        <g ref={starsGroupRef} className="stars-layer"></g>

        {/* 流星层 */}
        <g ref={shootingStarsGroupRef} className="shooting-stars-layer"></g>

        {/* 可缩放内容组 */}
        <g ref={gRef}></g>

        {/* 静态内容组（如返回按钮） */}
        <g ref={staticGroupRef}></g>
      </svg>

      {/* 学习路径起点：屏幕外指示箭头（仅当目标不在视口内时显示） */}
      <PathStartScreenArrows
        svgRef={svgRef}
        gRef={gRef}
        targetId={!selectedTagId && laserLearningPath.length > 0 ? laserLearningPath[0] : null}
        visible={!selectedTagId && laserLearningPath.length > 0}
      />

      {/* 信息框 */}
      <InfoBox
        hoveredTag={hoveredTag}
        selectedTagId={selectedTagId}
        infoBox={infoBox}
        setInfoBox={setInfoBox}
      />

      {/* 星空背景动画 */}
      <StarBackground
        mainGroupRef={gRef}
        starsGroupRef={starsGroupRef}
        shootingStarsGroupRef={shootingStarsGroupRef}
      />

      {/* 核心标签球 - 使用最新的窗口尺寸 */}
      <CoreTags
        gRef={gRef}
        defsRef={defsRef}
        coreTags={coreTags}
        selectedTagId={selectedTagId}
        setSelectedTagId={setSelectedTagId}
        setHoveredTag={setHoveredTag}
        setShowSubTags={setShowSubTags}
        centerX={dimensions.width / 2}
        centerY={dimensions.height / 2}
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
        learningPath={laserLearningPath}
        tagPositions={allTagPositions}
        visible={!selectedTagId && laserLearningPath.length > 1}
      />

      {/* 学习路径起点：节点本体强高亮 */}
      <PathStartHighlight
        gRef={gRef}
        defsRef={defsRef}
        targetId={!selectedTagId && laserLearningPath.length > 0 ? laserLearningPath[0] : null}
        visible={!selectedTagId && laserLearningPath.length > 0}
      />

      {/* 路径规划子标签 - 在核心标签视图中显示 */}
      {!selectedTagId && fullLearningPath.length > 0 && (
        <PathSubTags
          gRef={gRef}
          defsRef={defsRef}
          svgRef={svgRef}
          learningPath={fullLearningPath}
          coreTagPositions={tagPositions}
          subTags={subTags}
          visible={!selectedTagId}
          onSubTagPositionsUpdate={setPathSubTagPositions}
          masteryMap={masteryMap}
          weakPoints={weakPoints}
          onPathSubTagHover={handlePathSubTagHover}
        />
      )}

      {/* 子标签球 - 传递选中的核心标签数据 */}
      <SubTags
        gRef={gRef}
        defsRef={defsRef}
        svgRef={svgRef}
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

      {/* 返回按钮 */}
      <BackButton
        staticGroupRef={staticGroupRef}
        selectedTagId={selectedTagId}
        showSubTags={showSubTags}
        returnToMainView={returnToMainView}
        width={svgRef.current?.clientWidth || 1000}
        transitionDuration={transitionDuration}
      />
    </div>
  );
};

export default KnowledgeGraph;
