from __future__ import annotations

from typing import Literal

from pydantic import BaseModel


class StudentMetricsResponse(BaseModel):
    knowledge: float
    accuracy: float
    quality: float
    flexibility: float
    proficiency: float


class RatingPointResponse(BaseModel):
    examId: str
    examName: str
    date: str
    oldRating: int
    delta: int
    newRating: int


class RatingInfoResponse(BaseModel):
    current: int
    lastDelta: int | None
    history: list[RatingPointResponse]


class MetricDeltaItemResponse(BaseModel):
    knowledge: int
    accuracy: int
    quality: int
    flexibility: int
    proficiency: int


class MetricDeltaInfoResponse(BaseModel):
    latestExamId: str | None
    latestExamName: str | None
    latestExamDate: str | None
    baseline: Literal["zero", "previous_exam"]
    values: MetricDeltaItemResponse


class ResolvedIdentityResponse(BaseModel):
    studentId: str
    ptaNickname: str | None = None
    noSubmissionRecords: bool


class StudentDashboardResponse(BaseModel):
    metrics: StudentMetricsResponse
    rating: RatingInfoResponse
    metricDelta: MetricDeltaInfoResponse
    identity: ResolvedIdentityResponse
