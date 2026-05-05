import { TagData, TagConfigItem, TagsConfig } from '../types';
import tagsConfig from '../../../config/knowledge_tree.json';

/**
 * ID映射表 - 将中文名称映射到ID
 * 更新为匹配 knowledge_tree.json 的结构
 */
const nameToId: Record<string, string> = {
  '基本概念': 'basic',
  '线性结构': 'linear',
  '树': 'tree',
  '模拟': 'simulation',
  '搜索': 'search',
  '图': 'graph',
  '数据结构': 'data-structure',
  '算法': 'algorithm'
};

/**
 * 颜色映射表 - 每个核心标签的颜色
 */
const colorMap: Record<string, string> = {
  'basic': '#1E90FF',        // Dodger Blue
  'linear': '#4169E1',       // Royal Blue
  'tree': '#32CD32',         // Lime Green
  'simulation': '#FF69B4',   // Hot Pink
  'search': '#9370DB',       // Medium Purple
  'graph': '#FF8C00',        // Dark Orange
  'data-structure': '#FF4500', // Orange Red
  'algorithm': '#20B2AA'     // Light Sea Green
};

/**
 * 核心标签描述
 */
const descriptionMap: Record<string, string> = {
  'basic': '包含时间复杂度、空间复杂度、递归等基础概念',
  'linear': '包含数组、链表、栈、队列、字符串等线性数据结构',
  'tree': '包括二叉树、树的遍历、森林、哈夫曼树、堆等树形结构',
  'simulation': '通过编程模拟问题场景的算法思想',
  'search': '包括深度优先搜索(DFS)、广度优先搜索(BFS)等搜索算法',
  'graph': '涉及图的表示、最短路径、拓扑排序、最小生成树等图论算法',
  'data-structure': '包含线段树、对顶堆、STL、平衡树、并查集等高级数据结构',
  'algorithm': '包含贪心、动态规划、二分、双指针、前缀和、差分等算法'
};

/**
 * 加载核心标签数据 - 从 knowledge_tree.json 动态读取
 * @returns 核心标签数据
 */
export const loadCoreTags = (): TagData[] => {
  const typedTagsConfig = tagsConfig as unknown as TagsConfig;
  const coreTags: TagData[] = [];

  Object.keys(typedTagsConfig).forEach((coreName) => {
    const coreId = nameToId[coreName];
    if (!coreId) return;

    coreTags.push({
      id: coreId,
      name: coreName,
      color: colorMap[coreId] || '#607D8B',
      description: descriptionMap[coreId] || `${coreName}相关算法和技术`
    });
  });

  return coreTags;
};

/**
 * 生成唯一ID
 * @param parentId 父标签ID
 * @param name 标签名称
 * @param index 索引
 * @returns 唯一ID
 */
const generateTagId = (parentId: string, name: string, index: number): string => {
  const safeName = name.replace(/\s+/g, '-').toLowerCase();
  return `${parentId}-${safeName}-${index}`;
};

/**
 * 递归处理多级标签结构，并计算后代总数
 * @param items 标签项数组
 * @param parentId 父标签ID
 * @param parentColor 父标签颜色
 * @param level 当前层级
 * @returns 处理后的标签数据数组
 */
const processTagItems = (
  items: Array<TagConfigItem>,
  parentId: string,
  parentColor: string,
  level: number
): TagData[] => {
  const result: TagData[] = [];

  items.forEach((item, index) => {
    let currentTag: TagData | null = null;

    if (typeof item === 'string') {
      // 处理简单字符串标签（叶子节点）
      currentTag = {
        id: generateTagId(parentId, item, index),
        name: item,
        color: parentColor,
        description: `${item}相关算法和技术`,
        level: level,
        parentId: parentId,
        children: [],
        totalDescendants: 0
      };
    } else {
      // 处理嵌套对象标签（有子节点）
      Object.entries(item).forEach(([subName, subItems]) => {
        const subTagId = generateTagId(parentId, subName, index);

        // 递归处理下一级标签
        const children = processTagItems(subItems, subTagId, parentColor, level + 1);

        // 计算当前标签的后代总数
        const currentDescendants = children.length + children.reduce((sum, child) => sum + (child.totalDescendants || 0), 0);

        currentTag = {
          id: subTagId,
          name: subName,
          color: parentColor,
          description: `${subName}相关算法和技术`,
          level: level,
          parentId: parentId,
          children: children,
          totalDescendants: currentDescendants
        };
      });
    }

    if (currentTag) {
      result.push(currentTag);
    }
  });

  return result;
};

/**
 * 根据config加载多级子标签数据
 * @returns 子标签数据，包含层级和后代信息
 */
export const loadSubTags = (): Record<string, TagData[]> => {
  const result: Record<string, TagData[]> = {};
  const typedTagsConfig = tagsConfig as unknown as TagsConfig;

  Object.entries(typedTagsConfig).forEach(([coreName, subItems]) => {
    const coreId = nameToId[coreName];
    if (!coreId) return;
    const color = colorMap[coreId] || '#607D8B';

    const processedTags = processTagItems(subItems, coreId, color, 1);
    result[coreId] = processedTags;
  });

  return result;
};
