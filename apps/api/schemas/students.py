from __future__ import annotations

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


class ResolvedIdentityResponse(BaseModel):
    studentId: str
    ptaNickname: str | None = None
    noSubmissionRecords: bool


class StudentDashboardResponse(BaseModel):
    metrics: StudentMetricsResponse
    rating: RatingInfoResponse
    identity: ResolvedIdentityResponse
