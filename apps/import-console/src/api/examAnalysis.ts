import { apiFetch } from "./client";

export interface ExamAnalysisExamItem {
  examId: string;
  examName: string;
  examType: string;
  examDate: string | null;
  participantCount: number;
  generatedCount: number;
  failedCount: number;
  missingCount: number;
}

export interface ExamAnalysisStudentItem {
  studentEntityId: string;
  studentId: string | null;
  studentName: string | null;
  rank: number | null;
  totalScore: number | null;
  solvedCount: number | null;
  ratingDelta: number | null;
  knowledge: number | null;
  accuracy: number | null;
  quality: number | null;
  flexibility: number | null;
  proficiency: number | null;
  analysisStatus: string;
  analysisReply: string;
  generatedAt: string | null;
  errorMessage: string | null;
}

export interface ExamAnalysisDetail {
  examId: string;
  examName: string;
  examType: string;
  examDate: string | null;
  participantCount: number;
  generatedCount: number;
  failedCount: number;
  missingCount: number;
  items: ExamAnalysisStudentItem[];
}

export interface ExamAnalysisExamListResponse {
  items: ExamAnalysisExamItem[];
}

export interface ExamAnalysisGenerateResponse {
  runId: string;
  message: string;
}

export type ExamAnalysisExamDetail = ExamAnalysisDetail;

export function listExamAnalysisExams(): Promise<ExamAnalysisExamListResponse> {
  return apiFetch("/api/v1/exam-analysis/exams");
}

export function getExamAnalysisDetail(examId: string): Promise<ExamAnalysisDetail> {
  return apiFetch(`/api/v1/exam-analysis/exams/${encodeURIComponent(examId)}`);
}

export function generateExamAnalysis(
  examId: string,
  payload: { force?: boolean } = {},
): Promise<ExamAnalysisGenerateResponse> {
  return apiFetch(`/api/v1/exam-analysis/exams/${encodeURIComponent(examId)}/generate`, {
    method: "POST",
    body: JSON.stringify({
      force: Boolean(payload.force),
    }),
  });
}

export const startExamAnalysisRun = generateExamAnalysis;
