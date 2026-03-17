from __future__ import annotations

from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class ExamAnalysisExamListItemResponse(BaseModel):
    examId: str
    examName: str
    examType: str
    examDate: datetime | None
    participantCount: int
    generatedCount: int
    failedCount: int
    missingCount: int


class ExamAnalysisExamListResponse(BaseModel):
    items: list[ExamAnalysisExamListItemResponse]


class ExamAnalysisStudentRowResponse(BaseModel):
    studentEntityId: str
    studentId: str | None = None
    studentName: str | None = None
    rank: int | None = None
    totalScore: float | None = None
    solvedCount: int | None = None
    ratingDelta: int | None = None
    knowledge: float | None = None
    accuracy: float | None = None
    quality: float | None = None
    flexibility: float | None = None
    proficiency: float | None = None
    analysisStatus: str
    analysisReply: str
    generatedAt: datetime | None = None
    errorMessage: str | None = None


class ExamAnalysisExamDetailResponse(BaseModel):
    examId: str
    examName: str
    examType: str
    examDate: datetime | None
    participantCount: int
    generatedCount: int
    failedCount: int
    missingCount: int
    items: list[ExamAnalysisStudentRowResponse]


class ExamAnalysisGenerateRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    force: bool = Field(default=False)


class ExamAnalysisGenerateResponse(BaseModel):
    runId: str
    message: str
