import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react';
import {
  fetchStudentMastery,
  fetchLearningPath,
  transformMasteryData,
  transformLearningPath,
  transformFullLearningPath,
  StudentMasteryData,
} from '../services/api';

// Context 状态类型
export interface StudentState {
  studentId: string | null;
  masteryData: StudentMasteryData | null;
  masteryMap: Record<string, number>;  // 标签ID -> 掌握度
  weakPoints: string[];                 // 薄弱知识点标签ID列表
  learningPath: string[];               // 学习路径（核心标签ID顺序，用于激光连线）
  fullLearningPath: string[];           // 完整学习路径（包含所有知识点名称，包括子标签）
  loading: boolean;
  error: string | null;
  overallMastery: number;
}

// Context 接口
interface StudentContextType extends StudentState {
  setStudentId: (id: string | null) => void;
  refreshData: () => Promise<void>;
}

// 创建 Context
const StudentContext = createContext<StudentContextType | null>(null);

// Provider Props
interface StudentProviderProps {
  children: ReactNode;
  initialStudentId?: string | null;
}

/**
 * StudentProvider 组件
 * 管理学生相关状态和数据获取
 */
export const StudentProvider: React.FC<StudentProviderProps> = ({
  children,
  initialStudentId
}) => {
  const [studentId, setStudentId] = useState<string | null>(initialStudentId ?? null);
  const [masteryData, setMasteryData] = useState<StudentMasteryData | null>(null);
  const [masteryMap, setMasteryMap] = useState<Record<string, number>>({});
  const [weakPoints, setWeakPoints] = useState<string[]>([]);
  const [learningPath, setLearningPath] = useState<string[]>([]);
  const [fullLearningPath, setFullLearningPath] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [overallMastery, setOverallMastery] = useState(0);

  // 获取数据的函数
  const refreshData = async () => {
    if (!studentId) {
      // 无学生ID时使用默认值
      setMasteryMap({});
      setWeakPoints([]);
      setLearningPath([]);
      setFullLearningPath([]);
      setMasteryData(null);
      setOverallMastery(0);
      return;
    }

    setLoading(true);
    setError(null);

    try {
      // 并行获取掌握度和学习路径数据
      const [masteryResponse, pathResponse] = await Promise.all([
        fetchStudentMastery(studentId),
        fetchLearningPath(studentId),
      ]);

      // 存储原始数据
      setMasteryData(masteryResponse);
      setOverallMastery(masteryResponse.summary?.overall_mastery ?? 0);

      // 转换数据
      const { masteryMap: newMasteryMap, weakPoints: newWeakPoints } =
        transformMasteryData(masteryResponse);
      setMasteryMap(newMasteryMap);
      setWeakPoints(newWeakPoints);

      const newLearningPath = transformLearningPath(pathResponse);
      setLearningPath(newLearningPath);

      const newFullLearningPath = transformFullLearningPath(pathResponse);
      setFullLearningPath(newFullLearningPath);

      console.log('[StudentContext] Raw API response:', {
        masteryResponse,
        pathResponse,
      });
      console.log('[StudentContext] Transformed data:', {
        masteryMap: newMasteryMap,
        weakPoints: newWeakPoints,
        learningPath: newLearningPath,
        fullLearningPath: newFullLearningPath,
      });
    } catch (err) {
      const errorMessage = err instanceof Error ? err.message : 'Unknown error';
      console.warn('[StudentContext] API fetch failed, using defaults:', errorMessage);
      setError(errorMessage);

      // API 不可用时使用默认值（降级策略）
      setMasteryMap({});
      setWeakPoints([]);
      setLearningPath([]);
      setFullLearningPath([]);
    } finally {
      setLoading(false);
    }
  };

  // 当 studentId 变化时重新获取数据
  useEffect(() => {
    refreshData();
  }, [studentId]);

  // SPA: studentId is controlled by the app; no URL param syncing.

  const value: StudentContextType = {
    studentId,
    masteryData,
    masteryMap,
    weakPoints,
    learningPath,
    fullLearningPath,
    loading,
    error,
    overallMastery,
    setStudentId,
    refreshData,
  };

  return (
    <StudentContext.Provider value={value}>
      {children}
    </StudentContext.Provider>
  );
};

/**
 * 使用 StudentContext 的 Hook
 */
export function useStudent(): StudentContextType {
  const context = useContext(StudentContext);
  if (!context) {
    throw new Error('useStudent must be used within a StudentProvider');
  }
  return context;
}

/**
 * 获取指定标签的掌握度
 * @param tagId 标签ID
 * @returns 掌握度值 (0-1)，默认返回 0
 */
export function useMastery(tagId: string): number {
  const { masteryMap } = useStudent();
  return masteryMap[tagId] ?? 0;
}

/**
 * 检查标签是否为薄弱知识点
 */
export function useIsWeakPoint(tagId: string): boolean {
  const { weakPoints } = useStudent();
  return weakPoints.includes(tagId);
}

/**
 * 检查标签是否在学习路径上
 */
export function useIsOnPath(tagId: string): boolean {
  const { learningPath } = useStudent();
  return learningPath.includes(tagId);
}

/**
 * 获取标签在学习路径中的顺序
 * @returns 顺序号（从0开始），不在路径上返回 -1
 */
export function usePathOrder(tagId: string): number {
  const { learningPath } = useStudent();
  return learningPath.indexOf(tagId);
}

export default StudentContext;
