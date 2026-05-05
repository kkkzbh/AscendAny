import { TagData } from '../types';

// 定义关系结构
export interface TagRelation {
  source: string; // 源标签ID
  target: string; // 目标标签ID
}

// 定义完整的标签关系配置
export const tagRelations: TagRelation[] = [
  // 模拟相关连线
  { source: 'simulation', target: 'linear' },  // 模拟 -> 线性结构
  { source: 'simulation', target: 'string' },  // 模拟 -> 字符串
  { source: 'simulation', target: 'greedy' },  // 模拟 -> 贪心

  // 线性结构相关连线
  { source: 'linear', target: 'tree' },        // 线性结构 -> 树
  { source: 'linear', target: 'dp' },          // 线性结构 -> 动态规划
  { source: 'linear', target: 'divide' },      // 线性结构 -> 分治
  { source: 'linear', target: 'greedy' },      // 线性结构 -> 贪心

  // 树相关连线
  { source: 'tree', target: 'graph' },         // 树 -> 图
  { source: 'tree', target: 'search' },        // 树 -> 搜索
  { source: 'tree', target: 'dp' },            // 树 -> 动态规划
  { source: 'tree', target: 'divide' },        // 树 -> 分治
  { source: 'tree', target: 'greedy' },        // 树 -> 贪心

  // 图相关连线
  { source: 'graph', target: 'search' },       // 图 -> 搜索
  { source: 'graph', target: 'greedy' },       // 图 -> 贪心

  // 字符串相关连线
  { source: 'string', target: 'linear' }       // 字符串 -> 线性结构
];

/**
 * 计算标签的层次和位置
 * 左下到右上的分层布局
 * @param tags 所有标签数据
 * @param relations 标签间关系
 * @param width 视图宽度
 * @param height 视图高度
 * @returns 添加了位置信息的标签数组
 */
export function calculateTagPositions(
  tags: TagData[],
  relations: TagRelation[],
  width: number,
  height: number
): TagData[] {
  // 创建标签ID到标签的映射
  const tagMap = new Map<string, TagData>();
  tags.forEach(tag => tagMap.set(tag.id, { ...tag }));

  // 计算每个节点的入度和出度
  const inDegree: Record<string, number> = {};
  const outDegree: Record<string, number> = {};

  relations.forEach(rel => {
    if (tagMap.has(rel.source) && tagMap.has(rel.target)) {
      outDegree[rel.source] = (outDegree[rel.source] || 0) + 1;
      inDegree[rel.target] = (inDegree[rel.target] || 0) + 1;
    }
  });

  // 对每个节点计算层级分数 (0 = 源节点, 1 = 目标节点)
  const layerScores: Record<string, number> = {};

  tags.forEach(tag => {
    const id = tag.id;
    const ins = inDegree[id] || 0;
    const outs = outDegree[id] || 0;

    if (ins + outs === 0) {
      // 孤立节点放在中间
      layerScores[id] = 0.5;
    } else {
      // 计算层级得分: 高出度低入度的节点偏向左下方
      layerScores[id] = ins / (ins + outs);
    }
  });

  // 对层级分数进行分组 (创建5个主要层级)
  const layers: string[][] = [[], [], [], [], []];

  tags.forEach(tag => {
    const score = layerScores[tag.id];
    const layerIndex = Math.min(4, Math.floor(score * 5));
    layers[layerIndex].push(tag.id);
  });

  // 确定每个节点的位置
  const positions: TagData[] = [];
  const padding = 80;
  const usableWidth = width - padding * 2;
  const usableHeight = height - padding * 2;

  // 为每一层计算节点位置
  layers.forEach((layer, layerIndex) => {
    const layerSize = layer.length;
    if (layerSize === 0) return;

    // 层的基本角度和偏移量
    const baseAngle = Math.PI * 0.75 - layerIndex * (Math.PI / 3);
    const radius = Math.min(usableWidth, usableHeight) * 0.4;

    // 在这一层中布置节点
    layer.forEach((nodeId, i) => {
      const tag = tagMap.get(nodeId);
      if (!tag) return;

      // 计算节点位置
      const angle = baseAngle + (i - (layerSize - 1) / 2) * 0.3;
      const x = width / 2 + Math.cos(angle) * radius * (0.7 + layerIndex * 0.1);
      const y = height / 2 - Math.sin(angle) * radius * (0.7 + layerIndex * 0.1);

      positions.push({ ...tag, x, y });
    });
  });

  return positions;
}

/**
 * 计算连接线的端点
 * @param source 源节点
 * @param target 目标节点
 * @param nodeRadius 节点半径
 * @returns 线条起止坐标
 */
export function calculateConnectionEndpoints(
  source: TagData,
  target: TagData,
  nodeRadius: number
) {
  if (!source.x || !source.y || !target.x || !target.y) {
    return null;
  }

  const dx = target.x - source.x;
  const dy = target.y - source.y;
  const distance = Math.sqrt(dx * dx + dy * dy);

  if (distance === 0) return null;

  // 单位向量
  const unitX = dx / distance;
  const unitY = dy / distance;

  // 计算连接点（从节点边缘开始）
  const sourceX = source.x + unitX * (nodeRadius + 2);
  const sourceY = source.y + unitY * (nodeRadius + 2); // 修复：这里应该是unitY而不是unitX
  const targetX = target.x - unitX * (nodeRadius + 2);
  const targetY = target.y - unitY * (nodeRadius + 2); // 修复：这里应该是unitY而不是unitX

  return {
    x1: sourceX,
    y1: sourceY,
    x2: targetX,
    y2: targetY
  };
}

// 定义边的接口
export interface Edge {
  source: string;  // 源节点ID
  target: string;  // 目标节点ID
}

// 定义固定位置布局类型
export interface FixedLayout {
  [key: string]: { x: number, y: number };
}

/**
 * 核心标签的固定位置布局
 * 这些位置基于手绘布局示意图，根据红色箭头调整了位置
 */
export const coreTagsFixedLayout: FixedLayout = {
  'simulation': { x: 0.15, y: 0.8 },  // 模拟 - 左下角
  'dp': { x: 0.3, y: 0.3 },           // 动态规划 - 靠左上方
  'tree': { x: 0.60, y: 0.45 },        // 树 - 中间偏上
  'graph': { x: 0.85, y: 0.25 },      // 图 - 右上角
  'greedy': { x: 0.6, y: 0.85 },      // 贪心 - 右下角位置
  'search': { x: 0.85, y: 0.55 },     // 搜索 - 向右下方移动，方向指向图
  'linear': { x: 0.4, y: 0.6 },       // 线性结构 - 中间偏下
  'string': { x: 0.15, y: 0.5 },      // 字符串 - 左侧中间
  'divide': { x: 0.55, y: 0.15 }      // 分治 - 向上移动
};

/**
 * 核心标签间的连接关系定义
 */
export const coreTagRelations: Edge[] = [
  // 模拟相关连线
  { source: 'simulation', target: 'linear' },  // 模拟 -> 线性结构
  { source: 'simulation', target: 'string' },  // 模拟 -> 字符串
  { source: 'simulation', target: 'greedy' },  // 模拟 -> 贪心

  // 线性结构相关连线
  { source: 'linear', target: 'tree' },        // 线性结构 -> 树
  { source: 'linear', target: 'dp' },          // 线性结构 -> 动态规划
  { source: 'linear', target: 'divide' },      // 线性结构 -> 分治
  { source: 'linear', target: 'greedy' },      // 线性结构 -> 贪心

  // 树相关连线
  { source: 'tree', target: 'graph' },         // 树 -> 图
  { source: 'tree', target: 'search' },        // 树 -> 搜索
  { source: 'tree', target: 'dp' },            // 树 -> 动态规划
  { source: 'tree', target: 'divide' },        // 树 -> 分治
  { source: 'tree', target: 'greedy' },        // 树 -> 贪心

  // 图相关连线
  { source: 'graph', target: 'search' },       // 图 -> 搜索
  { source: 'graph', target: 'greedy' },       // 图 -> 贪心

  // 字符串相关连线
  { source: 'string', target: 'linear' }       // 字符串 -> 线性结构
];

/**
 * 计算各个标签的位置
 * @param tags 标签数据
 * @param layout 固定位置布局
 * @param width 画布宽度
 * @param height 画布高度
 * @returns 添加了位置信息的标签数组
 */
export function calculateFixedPositions(
  tags: TagData[],
  layout: FixedLayout,
  width: number,
  height: number
): TagData[] {
  // 创建位置映射
  const positionedTags = tags.map(tag => {
    const fixedPos = layout[tag.id];

    if (fixedPos) {
      // 使用固定位置布局中的比例换算成实际坐标
      const x = fixedPos.x * width;
      const y = fixedPos.y * height;
      return { ...tag, x, y };
    } else {
      // 如果没有固定位置，放在中心
      return { ...tag, x: width / 2, y: height / 2 };
    }
  });

  return positionedTags;
}

/**
 * 计算连接线的端点
 * @param source 源节点位置
 * @param target 目标节点位置
 * @param nodeRadius 节点半径
 * @returns 连接线的起止点坐标
 */
export function calculateConnectorEndpoints(
  source: {x: number, y: number},
  target: {x: number, y: number},
  nodeRadius: number
) {
  const dx = target.x - source.x;
  const dy = target.y - source.y;
  const distance = Math.sqrt(dx * dx + dy * dy);

  // 避免除以零
  if (distance === 0) {
    return { x1: source.x, y1: source.y, x2: target.x, y2: target.y };
  }

  const unitDx = dx / distance;
  const unitDy = dy / distance;

  // 从球体边缘开始
  const startX = source.x + unitDx * nodeRadius;
  const startY = source.y + unitDy * nodeRadius;
  const endX = target.x - unitDx * nodeRadius;
  const endY = target.y - unitDy * nodeRadius;

  return { x1: startX, y1: startY, x2: endX, y2: endY };
}
