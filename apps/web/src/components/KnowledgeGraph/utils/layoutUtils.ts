// layoutUtils.ts - 用于处理多级标签布局
import { TagData } from '../types';
import { Position } from './ConnectorUtils';

/**
 * 递归展平标签层级结构
 * @param tags 根标签数组
 * @returns 包含所有层级标签的扁平数组
 */
export const flattenTagHierarchy = (tags: TagData[]): TagData[] => {
  let flatList: TagData[] = [];
  tags.forEach(tag => {
    flatList.push(tag); // 添加当前标签
    if (tag.children && tag.children.length > 0) {
      // 递归添加子标签
      flatList = flatList.concat(flattenTagHierarchy(tag.children));
    }
  });
  return flatList;
};

/**
 * 计算多级标签布局
 * @param coreTag 核心标签数据（用于获取颜色等）
 * @param rootSubTags 核心标签的第一级子标签
 * @param coreX 核心标签X坐标
 * @param coreY 核心标签Y坐标
 * @param coreRadius 核心标签半径
 * @param baseRadius 基础子标签半径
 * @param scaleK 当前缩放比例
 * @returns 包含所有标签位置的Map
 */
export const calculateMultiLevelLayout = (
  _coreTag: TagData, // 添加下划线前缀标记为未使用
  rootSubTags: TagData[],
  coreX: number,
  coreY: number,
  coreRadius: number, // 原始核心标签半径
  baseRadius: number, // 原始子标签半径
  scaleK: number // 当前视图缩放比例
): Map<string, Position> => {
  const positions = new Map<string, Position>();

  // 计算缩放后的半径
  const scaledCoreRadius = coreRadius * scaleK; // 核心标签在子视图中的放大半径
  const scaledBaseRadius = baseRadius / scaleK; // 子标签在当前视图下的基础半径

  // 布局参数
  const minDistanceLevel1 = scaledCoreRadius * 1.8; // 第一级标签与核心的最小距离
  const levelDistanceFactor = scaledBaseRadius * 4; // 每层级增加的距离
  const angleSpreadFactor = Math.PI / 6; // 同级标签之间的角度扩散因子
  const descendantDistanceFactor = scaledBaseRadius * 0.5; // 根据后代数量增加的距离因子

  // 1. 定位第一级子标签
  const level1Tags = rootSubTags;
  const numLevel1 = level1Tags.length;
  level1Tags.forEach((tag, index) => {
    const angle = (index / numLevel1) * 2 * Math.PI; // 均匀分布角度
    // 根据后代数量调整距离（使用log平滑，避免过大差异）
    const distance = minDistanceLevel1 + Math.log1p(tag.totalDescendants || 0) * descendantDistanceFactor;

    const x = coreX + Math.cos(angle) * distance;
    const y = coreY + Math.sin(angle) * distance;
    positions.set(tag.id, { x, y });
    tag.x = x; // 存储位置信息以供子级使用
    tag.y = y;
  });

  // 2. 递归定位更深层级的标签
  const positionChildren = (parentTag: TagData) => {
    if (!parentTag.children || parentTag.children.length === 0) {
      return;
    }

    const children = parentTag.children;
    const numChildren = children.length;
    const parentPos = positions.get(parentTag.id)!;

    // 计算父节点相对于核心的角度，用于确定扩展方向
    const dxParent = parentPos.x - coreX;
    const dyParent = parentPos.y - coreY;
    const parentAngle = Math.atan2(dyParent, dxParent);

    // 计算当前层级的半径和角度范围
    const currentLevel = parentTag.level! + 1;
    const currentDistance = minDistanceLevel1 + (currentLevel - 1) * levelDistanceFactor + Math.log1p(parentTag.totalDescendants || 0) * descendantDistanceFactor;

    children.forEach((child, index) => {
      // 在父节点角度基础上进行小范围扩散
      const spreadAngle = (numChildren > 1) ? angleSpreadFactor / (currentLevel) : 0; // 层级越深，扩散越小
      const childAngle = parentAngle + (index - (numChildren - 1) / 2) * spreadAngle;

      // 根据后代数量微调距离
      const childDistance = currentDistance + Math.log1p(child.totalDescendants || 0) * descendantDistanceFactor * 0.5;

      const x = coreX + Math.cos(childAngle) * childDistance;
      const y = coreY + Math.sin(childAngle) * childDistance;
      positions.set(child.id, { x, y });
      child.x = x; // 存储位置
      child.y = y;

      // 递归处理孙子节点
      positionChildren(child);
    });
  };

  // 从第一级标签开始递归布局
  level1Tags.forEach(positionChildren);

  // TODO: 添加碰撞检测和位置微调逻辑

  return positions;
};

/**
 * 计算路径子标签在核心标签周围的布局位置
 * @param coreTagId 核心标签ID
 * @param coreTagPos 核心标签位置
 * @param subTagIds 需要布局的子标签ID列表
 * @param coreTagRadius 核心标签半径（默认35）
 * @returns 子标签位置映射 Map<subTagId, {x, y}>
 */
export const calculatePathSubTagLayout = (
  coreTagId: string,
  coreTagPos: { x: number; y: number },
  subTagIds: string[],
  coreTagRadius: number = 35,
  subTagRadius: number = coreTagRadius * 0.6
): Map<string, { x: number; y: number }> => {
  void coreTagId; // 标记未使用参数
  const positions = new Map<string, { x: number; y: number }>();

  if (subTagIds.length === 0) {
    return positions;
  }

  // 布局参数
  // 让子标签离核心更远一些：避免近距离短连线（观感差），并给浮动留空间
  const n = subTagIds.length;
  const padding = Math.max(14, subTagRadius * 0.75);
  const extra = Math.min(60, Math.max(0, n - 4) * 8);
  // 用户体验：路径子标签需要明显更远的轨道距离
  // 基础距离 * 2.5，并保留按数量扩展的 extra
  const baseDistance = coreTagRadius + subTagRadius + padding + extra;
  const distanceFromCore = baseDistance * 2.5;
  const startAngle = -Math.PI / 2; // 从顶部开始布局

  // 计算每个子标签的位置
  const numSubTags = subTagIds.length;
  subTagIds.forEach((subTagId, index) => {
    // 在核心标签周围均匀分布
    const angle = startAngle + (index / numSubTags) * 2 * Math.PI;

    const x = coreTagPos.x + Math.cos(angle) * distanceFromCore;
    const y = coreTagPos.y + Math.sin(angle) * distanceFromCore;

    positions.set(subTagId, { x, y });
  });

  return positions;
};

/**
 * 从学习路径中提取子标签并按核心标签分组
 * @param learningPath 学习路径标签名称数组（中文名称）
 * @param subTagsData 所有子标签数据 Record<coreTagId, TagData[]>
 * @returns 按核心标签分组的子标签ID Map<coreTagId, subTagId[]>
 */
export const extractPathSubTags = (
  learningPath: string[],
  subTagsData: Record<string, TagData[]>
): Map<string, string[]> => {
  const result = new Map<string, string[]>();

  // 创建子标签名称到 {coreTagId, subTagId} 的映射，用于快速查找
  const subTagNameToInfo = new Map<string, { coreTagId: string; subTagId: string }>();

  // 递归遍历所有子标签，建立名称到ID的映射
  const processSubTags = (coreTagId: string, tags: TagData[]) => {
    tags.forEach(tag => {
      // 使用子标签名称作为键
      subTagNameToInfo.set(tag.name, { coreTagId, subTagId: tag.id });
      // 同时存储 trim 后的版本
      subTagNameToInfo.set(tag.name.trim(), { coreTagId, subTagId: tag.id });

      if (tag.children && tag.children.length > 0) {
        processSubTags(coreTagId, tag.children);
      }
    });
  };

  // 构建子标签名称到信息的映射
  Object.entries(subTagsData).forEach(([coreTagId, tags]) => {
    processSubTags(coreTagId, tags);
  });

  // 从学习路径中提取子标签并分组
  learningPath.forEach(tagName => {
    const trimmedName = tagName.trim();
    const info = subTagNameToInfo.get(trimmedName) || subTagNameToInfo.get(tagName);

    if (info) {
      // 找到了匹配的子标签
      const existing = result.get(info.coreTagId) || [];
      if (!existing.includes(info.subTagId)) {
        existing.push(info.subTagId);
        result.set(info.coreTagId, existing);
      }
    }
  });

  return result;
};
