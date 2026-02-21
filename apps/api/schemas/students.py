from __future__ import annotations

from typing import Literal

from pydantic import BaseModel


class StudentMetricsResponse(BaseModel):
    knowledge: float
    accuracy: float
    quality: float
    flexibility: float
    proficiency: float


class MetricMissingItemResponse(BaseModel):
    knowledge: bool
    accuracy: bool
    quality: bool
    flexibility: bool
    proficiency: bool


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


class ProgressExplanationResponse(BaseModel):
    available: bool
    latestExamId: str | None
    latestExamName: str | None
    latestExamDate: str | None
    ratingDelta: int | None
    keyImprovements: list[str]
    keySetbacks: list[str]
    summary: str


class MilestoneItemResponse(BaseModel):
    code: str
    label: str
    detail: str
    examId: str | None = None
    examDate: str | None = None


class MilestoneStreakResponse(BaseModel):
    available: bool
    currentPositiveStreak: int
    bestPositiveStreak: int
    newMilestones: list[MilestoneItemResponse]
    recentMilestones: list[MilestoneItemResponse]
    nextTargets: list[str]


class PeerMetricGapResponse(BaseModel):
    score: float | None
    solved: int | None
    knowledge: int | None
    accuracy: int | None
    quality: int | None
    flexibility: int | None
    proficiency: int | None


class PercentileBandComparisonResponse(BaseModel):
    totalParticipants: int
    myRank: int | None
    myPercentile: float | None
    bandCode: str | None
    bandLabel: str
    gapVsBandMedian: PeerMetricGapResponse


class PreviousRankerComparisonResponse(BaseModel):
    available: bool
    rankGap: int | None
    scoreGap: float | None
    solvedGap: int | None
    metricGapVsPrevious: PeerMetricGapResponse


class PeerComparisonResponse(BaseModel):
    available: bool
    defaultMode: Literal["percentile_band", "previous_ranker"]
    percentileBand: PercentileBandComparisonResponse
    previousRanker: PreviousRankerComparisonResponse


class PostExamSupportResponse(BaseModel):
    available: bool
    mode: Literal["recovery", "steady", "reinforce"]
    headline: str
    message: str
    actionPlan: list[str]
    checkInQuestion: str


class StudentDashboardResponse(BaseModel):
    metrics: StudentMetricsResponse
    metricMissing: MetricMissingItemResponse
    rating: RatingInfoResponse
    metricDelta: MetricDeltaInfoResponse
    identity: ResolvedIdentityResponse
    progressExplanation: ProgressExplanationResponse
    milestoneStreak: MilestoneStreakResponse
    peerComparison: PeerComparisonResponse
    postExamSupport: PostExamSupportResponse
