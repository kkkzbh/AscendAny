// 知识图谱相关类型定义
import { Dispatch, SetStateAction } from 'react';

// 标签数据类型
export interface TagData {
  id: string;
  name: string;
  color: string;
  description?: string; // 添加 description 属性
  gradientId?: string; // 渐变ID
  x?: number; // 添加 x 坐标属性
  y?: number; // 添加 y 坐标属性
  children?: TagData[]; // 添加子标签数组，用于多级标签
  level?: number; // 添加层级属性，用于标记标签处于第几层
  parentId?: string; // 添加父标签ID，用于多级标签的向上引用
  totalDescendants?: number; // 记录所有后代节点的数量，用于布局计算
  animation?: {
    baseX: number;
    baseY: number;
    phaseX: number;
    phaseY: number;
    frequencyX: number;
    frequencyY: number;
    amplitude: number;
    active: boolean;
  }; // 动画相关属性，用于浮动动画的控制
  // Ascend API 集成相关属性
  mastery?: number;        // 掌握度 (0-1)
  isWeakPoint?: boolean;   // 是否薄弱点
  isOnPath?: boolean;      // 是否在学习路径上
  pathOrder?: number;      // 路径顺序（-1表示不在路径上）
}

// 多级标签项类型 - 可以是字符串或嵌套对象
export type TagConfigItem = string | { [key: string]: Array<TagConfigItem> };

// 标签配置文件类型
export type TagsConfig = {
  [key: string]: Array<TagConfigItem>;
};

// 星星数据类型
export interface Star {
  x: number;
  y: number;
  radius: number;
  opacity: number;
  twinkleSpeed: number; // 闪烁速度
  twinkleOffset: number; // 闪烁偏移量（让星星不同步闪烁）
}

// 流星数据类型
export interface ShootingStar {
  x: number;
  y: number;
  length: number;
  speed: number;
  angle: number;
  alpha: number;
  active: boolean;
}

// 信息框数据类型
export interface InfoBoxData {
  visible: boolean;
  x: number;
  y: number;
  content: string;
  opacity: number;
  // 新增字段，用于显示扩展信息
  tagId?: string;         // 标签ID
  tagName?: string;       // 标签名称
  tagScore?: number;      // 标签评分 (0-1)
  acCount?: number;       // 通过题目数
  recommendedProblems?: RecommendedProblem[]; // 推荐题目列表
  // 核心标签支持
  isCoreTag?: boolean;    // 是否为核心标签
  isWeakPoint?: boolean;  // 是否为薄弱点
  isOnLearningPath?: boolean; // 是否在学习路径上
}

// 推荐题目类型
export interface RecommendedProblem {
  id: string;             // 题目ID
  title: string;          // 题目标题
  tags: string[];         // 题目标签
  difficulty: number;     // 难度系数 (1-5)
  link: string;           // 题目链接
}

// 全局共享状态类型
export interface KnowledgeGraphState {
  selectedTagId: string | null;
  setSelectedTagId: Dispatch<SetStateAction<string | null>>;
  hoveredTag: TagData | null;
  setHoveredTag: Dispatch<SetStateAction<TagData | null>>;
  showSubTags: boolean;
  setShowSubTags: Dispatch<SetStateAction<boolean>>;
  infoBox: InfoBoxData;
  setInfoBox: Dispatch<SetStateAction<InfoBoxData>>;
}

// 核心标签映射类型
export type CoreTagsMap = Record<string, string>;

// 子标签映射类型
export type SubTagsMap = Record<string, { id: string; name: string; color: string }[]>;

// 多级标签结构类型，用于存储层级关系
export interface TagHierarchy {
  [key: string]: {
    children: TagHierarchy;
    level: number;
    parentId?: string;
  }
}