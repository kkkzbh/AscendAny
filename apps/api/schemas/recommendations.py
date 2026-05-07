from __future__ import annotations

from datetime import datetime
from typing import Any

from pydantic import BaseModel, ConfigDict, Field


class ProblemRecommendationItem(BaseModel):
    problemId: str
    title: str | None = None
    url: str | None = None
    knowledgePoints: list[str] = Field(default_factory=list)
    difficulty: float | None = None
    score: float | None = None
    reason: str | None = None
    rank: int
    meta: dict[str, Any] = Field(default_factory=dict)


class ProblemRecommendationsResponse(BaseModel):
    studentEntityId: int
    studentEntityIds: list[int]
    modelRunId: int | None = None
    generatedAt: datetime | None = None
    items: list[ProblemRecommendationItem] = Field(default_factory=list)


class LearningPathResponse(BaseModel):
    studentEntityId: int
    studentEntityIds: list[int]
    modelRunId: int | None = None
    generatedAt: datetime | None = None
    targets: list[str] = Field(default_factory=list)
    path: list[str] = Field(default_factory=list)
    explanations: dict[str, Any] = Field(default_factory=dict)


class RecommendationTrainRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    modelType: str = Field(default="rgcn", min_length=1)
    config: dict[str, Any] = Field(default_factory=dict)


class RecommendationTrainResponse(BaseModel):
    modelRunId: int
    status: str
    command: list[str]


class RecommendationRunItem(BaseModel):
    modelRunId: int
    status: str
    modelType: str
    metrics: dict[str, Any] = Field(default_factory=dict)
    artifactPath: str | None = None
    errorMessage: str | None = None
    createdAt: datetime
    startedAt: datetime | None = None
    finishedAt: datetime | None = None


class RecommendationRunsResponse(BaseModel):
    items: list[RecommendationRunItem]


class LearningPathStatusItem(BaseModel):
    point: str
    mastery: float = 0.0
    attempted: int = 0
    correct: int = 0
    lastTriedAt: datetime | None = None


class LearningPathStatusResponse(BaseModel):
    studentEntityId: int
    studentEntityIds: list[int]
    items: list[LearningPathStatusItem] = Field(default_factory=list)


class KnowledgeNodeRecentDay(BaseModel):
    date: str
    attempted: int = 0
    correct: int = 0


class KnowledgeNodeStats(BaseModel):
    attempted: int = 0
    correct: int = 0
    accuracy: float = 0.0
    lastTriedAt: datetime | None = None
    recentSeries: list[KnowledgeNodeRecentDay] = Field(default_factory=list)


class KnowledgeNodeProblem(BaseModel):
    problemId: str
    title: str | None = None
    difficulty: float | None = None
    knowledgePoints: list[str] = Field(default_factory=list)
    score: float | None = None
    reason: str | None = None


class KnowledgeNodeDetailResponse(BaseModel):
    point: str
    level: str | None = None
    parents: list[str] = Field(default_factory=list)
    children: list[str] = Field(default_factory=list)
    prerequisites: list[str] = Field(default_factory=list)
    successors: list[str] = Field(default_factory=list)
    description: str | None = None
    mastery: float = 0.0
    stats: KnowledgeNodeStats = Field(default_factory=KnowledgeNodeStats)
    problems: list[KnowledgeNodeProblem] = Field(default_factory=list)
