/**
 * Ascend API 服务层
 * 用于与 AscendAny API通信（Bearer token）。
 */

import { apiFetch } from '../app/api'

// API 响应类型定义
export interface StudentMasteryData {
  student: string;
  student_id?: string;
  computed_at: string;
  knowledge_mastery: Record<string, { mastery: number; level: string; children?: Record<string, unknown> }>;
  weak_points: string[];
  recommendations?: Array<{
    problem_id: string;
    title: string;
    difficulty: number;
    knowledge_points: string[];
    score: number;
    reason: string;
  }>;
  summary: { overall_mastery: number; evaluated_points: number };
}

export interface LearningPathData {
  student: string;
  targets: string[];
  path: string[];
}

export interface KnowledgeTagDetailData {
  student: string;
  knowledge_point: string;
  solved: number;
  attempted: number;
  recommendations: Array<{
    problem_id: string;
    title: string;
    difficulty: number; // 1-5
    knowledge_points: string[];
    score?: number;
    reason?: string;
    url?: string;
  }>;
}

function ensureOk<T>(data: unknown): T {
  if (!data || typeof data !== 'object') {
    throw new Error('Invalid API response')
  }
  const d = data as any
  if (typeof d?.detail === 'string') {
    throw new Error(d.detail)
  }
  return data as T
}

// 知识点ID映射：API知识点名称 -> Web标签ID
export const knowledgePointMapping: Record<string, string> = {
  '基本概念': 'basic',
  '线性结构': 'linear',
  '树': 'tree',
  '模拟': 'simulation',
  '搜索': 'search',
  '图': 'graph',
  '数据结构': 'data-structure',
  '算法': 'algorithm',
  '数学': 'math',
  '动态规划': 'dp',
  '贪心': 'greedy',
  '字符串': 'string',
};

// 反向映射：Web标签ID -> API知识点名称
export const reverseMapping: Record<string, string> = Object.fromEntries(
  Object.entries(knowledgePointMapping).map(([k, v]) => [v, k])
);

/**
 * 获取学生掌握度数据
 * POST /mastery/knowledge-points
 */
export async function fetchStudentMastery(studentId: string): Promise<StudentMasteryData> {
  const data = await apiFetch<StudentMasteryData | Record<string, unknown>>('/api/mastery/knowledge-points', {
    method: 'POST',
    body: {
      student: studentId,
      student_id: studentId,
      include_recommendations: true,
      include_hierarchy: true,
      weak_point_threshold: 0.6,
    },
  })

  return ensureOk<StudentMasteryData>(data)
}

/**
 * 获取学习路径规划
 * POST /path/plan
 */
export async function fetchLearningPath(studentId: string): Promise<LearningPathData> {
  const data = await apiFetch<LearningPathData | Record<string, unknown>>('/api/path/plan', {
    method: 'POST',
    body: {
      student: studentId,
      top_n_targets: 5,
      min_evidence: 1,
      include_mastered: false,
      mastered_threshold: 0.8,
      attempt_penalty_alpha: 0.15,
    },
  })

  return ensureOk<LearningPathData>(data)
}

/**
 * 获取知识点悬停详情（已解题数 + 题目推荐）
 * POST /knowledge/tag-detail
 */
export async function fetchKnowledgeTagDetail(
  studentId: string,
  knowledgePoint: string,
  topK: number = 6,
): Promise<KnowledgeTagDetailData> {
  const data = await apiFetch<KnowledgeTagDetailData | Record<string, unknown>>('/api/knowledge/tag-detail', {
    method: 'POST',
    body: {
      student: studentId,
      student_id: studentId,
      knowledge_point: knowledgePoint,
      top_k: topK,
      exclude_done: true,
      include_links: true,
    },
  })

  return ensureOk<KnowledgeTagDetailData>(data)
}

/**
 * 将API返回的知识点名称转换为Web标签ID
 */
export function mapApiToWebId(apiName: string): string | undefined {
  return knowledgePointMapping[apiName];
}

/**
 * 将Web标签ID转换为API知识点名称
 */
export function mapWebIdToApi(webId: string): string | undefined {
  return reverseMapping[webId];
}

/**
 * 转换掌握度数据为Web格式
 * 返回 Record<标签ID, mastery值>
 */
export function transformMasteryData(
  data: StudentMasteryData
): { masteryMap: Record<string, number>; weakPoints: string[] } {
  const masteryMap: Record<string, number> = {};

  // 递归处理所有层级（包括子标签）
  function processNode(name: string, info: { mastery: number; level: string; children?: Record<string, unknown> }) {
    // 优先使用映射后的webId，如果没有映射则直接使用原始名称
    const webId = mapApiToWebId(name);
    masteryMap[webId || name] = info.mastery;

    if (info.children) {
      for (const [childName, childInfo] of Object.entries(info.children)) {
        processNode(childName, childInfo as { mastery: number; level: string; children?: Record<string, unknown> });
      }
    }
  }

  for (const [name, info] of Object.entries(data.knowledge_mastery)) {
    processNode(name, info);
  }

  // 转换薄弱点列表
  // NOTE: 叶子知识点（例如“递归/多维数组”）通常没有 webId 映射。
  // 这里同时保留映射后的 id 和原始名称，避免前端效果/高亮丢失。
  const weakPoints = Array.from(
    new Set(
      data.weak_points
        .flatMap((name) => {
          const trimmed = name.trim();
          const mapped = mapApiToWebId(trimmed);
          return mapped ? [mapped, trimmed] : [trimmed];
        })
        .filter((v): v is string => Boolean(v))
    )
  );

  return { masteryMap, weakPoints };
}

/**
 * 转换学习路径为Web格式
 * 只保留能映射到核心标签的知识点（用于激光连线显示）
 * 子标签会被过滤掉，因为连线只在核心标签之间显示
 */
export function transformLearningPath(data: LearningPathData): string[] {
  const result: string[] = [];

  for (const name of data.path) {
    const trimmed = name.trim();
    const mapped = mapApiToWebId(trimmed);

    // 只保留能映射到核心标签的项目
    if (mapped && !result.includes(mapped)) {
      result.push(mapped);
    }
  }

  // 如果只有1个或0个核心标签，无法形成连线
  // 尝试从 targets 中补充
  if (result.length < 2 && data.targets) {
    for (const name of data.targets) {
      const trimmed = name.trim();
      const mapped = mapApiToWebId(trimmed);
      if (mapped && !result.includes(mapped)) {
        result.push(mapped);
      }
    }
  }

  return result;
}

/**
 * 转换完整学习路径，保留所有知识点（包括子标签名称）
 * 用于在核心标签视图中显示路径涉及的子标签
 * @returns 包含所有知识点名称的数组（中文名称，用于匹配子标签）
 */
export function transformFullLearningPath(data: LearningPathData): string[] {
  const result: string[] = [];

  for (const name of data.path) {
    const trimmed = name.trim();
    if (trimmed && !result.includes(trimmed)) {
      result.push(trimmed);
    }
  }

  return result;
}

export type QaSource = {
  problem_id: string
  title?: string
  tags?: string[]
  url?: string
  snippet?: string
}

export type QaSearchResponse = {
  query: string
  answer_markdown: string
  answer_html: string
  sources?: QaSource[]
  model?: string
}

/**
 * 知识问答（单轮）
 * POST /qa/search
 */
export async function fetchQaAnswer(query: string): Promise<QaSearchResponse> {
  const q = (query || '').toString().trim()
  if (!q) {
    throw new Error('请输入问题')
  }

  const data = await apiFetch<QaSearchResponse | Record<string, unknown>>('/api/qa/search', {
    method: 'POST',
    body: {
      query: q,
      top_k: 4,
      include_sources: true,
    },
  })

  // DRF validation errors come back as { field: ["..."] }.
  try {
    const d: any = data
    const qerr = Array.isArray(d?.query) ? d.query : null
    if (qerr && typeof qerr?.[0] === 'string') {
      throw new Error(String(qerr[0] || '参数错误'))
    }
  } catch (e) {
    if (e instanceof Error) throw e
  }

  const res = ensureOk<QaSearchResponse>(data)
  if (!res || typeof res !== 'object') {
    throw new Error('Invalid API response')
  }
  if (typeof (res as any).answer_html !== 'string') {
    throw new Error('Invalid API response')
  }
  if (typeof (res as any).answer_markdown !== 'string') {
    throw new Error('Invalid API response')
  }
  return res
}
