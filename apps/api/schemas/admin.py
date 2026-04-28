from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field

AdminModelProviderId = Literal["siliconflow", "openai", "copilot", "deepseek"]
AdminModelRequestMode = Literal["chat_completions", "responses"]
AdminModelListSource = Literal["dynamic", "static"]


class AdminModelOption(BaseModel):
    modelId: str
    label: str
    requestMode: AdminModelRequestMode = "chat_completions"
    deprecated: bool = False
    disabled: bool = False
    disabledReason: str | None = None


class AdminModelProviderConfig(BaseModel):
    id: AdminModelProviderId
    title: str
    provider: str
    strategyId: str
    adapter: str
    baseUrl: str
    model: str
    transportModel: str
    apiKeyEnv: str
    apiKeyConfigured: bool
    active: bool
    requestMode: AdminModelRequestMode
    modelOptions: list[AdminModelOption]
    description: str
    modelHint: str


class AdminModelServerDefault(BaseModel):
    mode: str
    baseUrl: str
    model: str
    apiKeyEnv: str


class AdminModelConfigResponse(BaseModel):
    configPath: str
    envFilePath: str
    activeProvider: AdminModelProviderId
    providers: list[AdminModelProviderConfig]
    activeRuntime: AdminModelServerDefault


class AdminModelProviderPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    id: AdminModelProviderId
    adapter: str | None = None
    baseUrl: str | None = None
    model: str | None = None
    apiKeyEnv: str | None = None
    requestMode: AdminModelRequestMode | None = None
    apiKey: str | None = Field(default=None, max_length=10000)


class AdminModelConfigPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    activeProvider: AdminModelProviderId | None = None
    provider: AdminModelProviderPatch | None = None


class AdminModelConnectionTestRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    providerId: AdminModelProviderId
    adapter: str | None = None
    baseUrl: str
    model: str
    apiKeyEnv: str
    requestMode: AdminModelRequestMode | None = None
    apiKey: str | None = Field(default=None, max_length=10000)


class AdminModelConnectionTestResponse(BaseModel):
    ok: bool
    status: str
    message: str
    provider: str
    model: str
    elapsedMs: int


class AdminDeepSeekModelsRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    providerId: AdminModelProviderId = "deepseek"
    adapter: str | None = None
    baseUrl: str
    model: str | None = None
    apiKeyEnv: str
    requestMode: AdminModelRequestMode | None = None
    apiKey: str | None = Field(default=None, max_length=10000)


class AdminDeepSeekModelsResponse(BaseModel):
    models: list[AdminModelOption]
    source: AdminModelListSource
    error: str | None = None


class AdminMetricsConfig(BaseModel):
    winsorLow: float
    winsorHigh: float
    flexibilityModeDefault: str
    includedProblemKinds: list[str]
    randomExamMissingDrawnSetPolicy: str
    randomExamSlotSourcePriority: list[str]


class AdminMappingConfig(BaseModel):
    primaryKeys: list[str]
    actorSources: list[str]
    strictMode: bool
    autoBindOnIngest: bool
    claimIdentitySource: str


class AdminFusionHalfLifeConfig(BaseModel):
    knowledge: float
    accuracy: float
    quality: float
    flexibility: float
    proficiency: float


class AdminRatingConfig(BaseModel):
    initialRating: int
    maxBinarySearchRating: int
    minBinarySearchRating: int
    binarySearchSteps: int


class AdminWarmupConfig(BaseModel):
    enabled: bool
    apiBaseUrl: str | None
    tokenEnv: str
    timeoutSeconds: float
    roleId: str


class AdminPreprocessConfig(BaseModel):
    practiceRoot: str
    encodings: list[str]
    fingerprintRoles: list[str]
    timezone: str
    metrics: AdminMetricsConfig
    mapping: AdminMappingConfig
    fusionHalfLifeDays: AdminFusionHalfLifeConfig
    rating: AdminRatingConfig
    warmup: AdminWarmupConfig


class AdminMetricsConfigPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    winsorLow: float | None = Field(default=None, ge=0, le=1)
    winsorHigh: float | None = Field(default=None, ge=0, le=1)
    flexibilityModeDefault: str | None = None
    includedProblemKinds: list[str] | None = None
    randomExamMissingDrawnSetPolicy: str | None = None
    randomExamSlotSourcePriority: list[str] | None = None


class AdminMappingConfigPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    primaryKeys: list[str] | None = None
    actorSources: list[str] | None = None
    strictMode: bool | None = None
    autoBindOnIngest: bool | None = None
    claimIdentitySource: str | None = None


class AdminFusionHalfLifePatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    knowledge: float | None = Field(default=None, gt=0)
    accuracy: float | None = Field(default=None, gt=0)
    quality: float | None = Field(default=None, gt=0)
    flexibility: float | None = Field(default=None, gt=0)
    proficiency: float | None = Field(default=None, gt=0)


class AdminRatingConfigPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    initialRating: int | None = None
    maxBinarySearchRating: int | None = None
    minBinarySearchRating: int | None = None
    binarySearchSteps: int | None = Field(default=None, ge=1, le=100)


class AdminWarmupConfigPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    enabled: bool | None = None
    apiBaseUrl: str | None = None
    tokenEnv: str | None = None
    timeoutSeconds: float | None = Field(default=None, gt=0, le=600)
    roleId: str | None = None


class AdminPreprocessConfigPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    practiceRoot: str | None = None
    encodings: list[str] | None = None
    fingerprintRoles: list[str] | None = None
    timezone: str | None = None
    metrics: AdminMetricsConfigPatch | None = None
    mapping: AdminMappingConfigPatch | None = None
    fusionHalfLifeDays: AdminFusionHalfLifePatch | None = None
    rating: AdminRatingConfigPatch | None = None
    warmup: AdminWarmupConfigPatch | None = None


class AdminConfigResponse(BaseModel):
    preprocessConfigPath: str
    preprocess: AdminPreprocessConfig
    restartRequiredKeys: list[str] = Field(default_factory=list)


class AdminConfigPatch(BaseModel):
    model_config = ConfigDict(extra="forbid")

    preprocess: AdminPreprocessConfigPatch | None = None


class AdminStudentSummary(BaseModel):
    studentEntityId: str
    studentId: str | None
    studentName: str | None
    grade: str | None
    username: str | None
    rating: int
    knowledge: float | None
    accuracy: float | None
    quality: float | None
    flexibility: float | None
    proficiency: float | None
    latestExamAt: datetime | None
    examCount: int
    generatedReports: int
    failedReports: int
    missingReports: int
    reportCompletionRate: float


class AdminStudentListResponse(BaseModel):
    items: list[AdminStudentSummary]
    total: int


class AdminStudentExamReport(BaseModel):
    examId: str
    examName: str
    examType: str
    examDate: datetime | None
    rank: int | None
    totalScore: float | None
    solvedCount: int | None
    ratingDelta: int | None
    oldRating: int | None
    newRating: int | None
    knowledge: float | None
    accuracy: float | None
    quality: float | None
    flexibility: float | None
    proficiency: float | None
    analysisStatus: str
    analysisReply: str
    generatedAt: datetime | None
    errorMessage: str | None


class AdminStudentExamReportsResponse(BaseModel):
    student: AdminStudentSummary | None
    items: list[AdminStudentExamReport]


class AdminAccountSummary(BaseModel):
    accountId: str
    username: str
    displayName: str
    isActive: bool
    isAdmin: bool
    provisionSource: str
    studentId: str | None
    ptaNickname: str | None
    createdAt: datetime | None
    updatedAt: datetime | None
    lastLoginAt: datetime | None


class AdminAccountsResponse(BaseModel):
    items: list[AdminAccountSummary]
    total: int


class AdminAuditLogItem(BaseModel):
    id: str
    kind: str
    status: str
    title: str
    detail: str
    actor: str | None = None
    createdAt: datetime
    payload: dict[str, object] = Field(default_factory=dict)


class AdminAuditLogResponse(BaseModel):
    items: list[AdminAuditLogItem]
    total: int
