import * as sdk from "../src";
import type {
  Account,
  AccountProfileUpdateRequest,
  AccountSessionList,
  AdminAccount,
  AgentNote,
  AgentNoteMutationResult,
  AgentNotePage,
  AgentNoteStateMutationRequest,
  AgentRunEnqueueResult,
  AgentRunEvent,
  AutomaticAnalysisRequest,
  ChatMessage,
  ChatThreadPage,
  ArchiveAgentNoteData,
  AuthenticatedFeedbackRequest,
  AuthSession,
  AuthorizedCatalogPublicationRequest,
  AuthorizeKnowledgeCatalogPublicationData,
  AuthorizeKnowledgeCatalogPublicationResponses,
  CatalogPublicationAuthorizationRequest,
  CatalogPublicationAuthorizationResult,
  CatalogPublicationIntent,
  ConsumeEnrollmentClaimData,
  ConsumeEnrollmentClaimResponses,
  CreateConfigurationVersionData,
  CreateConfigurationVersionRequest,
  CreateConfigurationVersionResponses,
  CreateConfigurationVersionResult,
  CreateGenericConfigurationVersionRequest,
  CreateAgentNoteData,
  CreateAgentNoteRequest,
  CreateAgentNoteResponses,
  CreateChatThreadResponses,
  EnrollmentClaimRequest,
  EnrollmentIssueRequest,
  ExamAnalysisGenerationEvent,
  FeedbackSubmitResult,
  GetConfigurationData,
  GetConfigurationResponses,
  GetRecommendationReviewContextResponses,
  GetAgentNoteData,
  GetAgentNoteResponses,
  GetAgentRunData,
  GetAgentRunResponses,
  GetSelfStudentAnalyticsData,
  GetSelfStudentAnalyticsResponses,
  GetSelfRecommendationResponses,
  GetStudentLeaderboardData,
  GetStudentLeaderboardResponses,
  ImportEvent,
  IssuedEnrollment,
  IssueEnrollmentClaimData,
  IssueEnrollmentClaimResponses,
  LoginAccountResponses,
  ListAccountSessionsResponses,
  ListAgentNotesData,
  ListAgentNotesResponses,
  ListChatMessagesData,
  ListChatMessagesResponses,
  ListChatThreadsData,
  ListChatThreadsResponses,
  ListConfigurationsData,
  ListConfigurationsResponses,
  ListConfigurationVersionsData,
  ListConfigurationVersionsResponses,
  LogoutSessionData,
  RefreshSessionData,
  RefreshSessionResponses,
  ConfigurationKind,
  RecommendationFresh,
  RecommendationInferenceEvidenceV1,
  RecommendationInferenceInsufficientResultV1,
  RecommendationInferenceReadyResultV1,
  RecommendationInferenceResultV1,
  RecommendationKnowledgeMasteryV1,
  RecommendationLearningPathStepV2,
  RecommendationKnowledgeCatalogV1,
  RecommendationKnowledgePointV1,
  RecommendationModelProvenance,
  RecommendationReviewContext,
  RecommendationUnavailable,
  ReplaceAgentNoteData,
  ReplaceAgentNoteRequest,
  ReplaceAgentNoteResponses,
  RevokeEnrollmentClaimData,
  RevokeEnrollmentClaimResponses,
  RestoreAgentNoteData,
  RevokeAccountSessionData,
  RevokeAccountSessionResponses,
  SelfStudentAnalytics,
  SelfRecommendation,
  StreamImportEventsResponses,
  StreamExamAnalysisGenerationEventsData,
  StreamExamAnalysisGenerationEventsResponses,
  StreamAgentRunEventsData,
  StreamAgentRunEventsResponses,
  EnqueueAgentRunData,
  EnqueueAgentRunResponses,
  EnqueueSelfAutoAnalysisData,
  EnqueueSelfAutoAnalysisResponses,
  ReplyAgentRunEnqueueRequest,
  SubmitAuthenticatedFeedbackData,
  SubmitAuthenticatedFeedbackResponses,
  StudentAccount,
  StudentAnalyticsNoObservations,
  StudentAnalyticsNotGenerated,
  StudentAnalyticsReady,
  StudentMetricValues,
  StudentLeaderboard,
  StudentLeaderboardNoObservations,
  StudentLeaderboardNotGenerated,
  StudentLeaderboardReady,
  UpdateAccountProfileData,
  UpdateAccountProfileResponses,
} from "../src";

type Equal<Left, Right> =
  (<Value>() => Value extends Left ? 1 : 2) extends
  (<Value>() => Value extends Right ? 1 : 2)
    ? (<Value>() => Value extends Right ? 1 : 2) extends
      (<Value>() => Value extends Left ? 1 : 2)
      ? true
      : false
    : false;

type Assert<Value extends true> = Value;

export type SSEPayloadIsImportEvent = Assert<
  Equal<StreamImportEventsResponses[200], ImportEvent>
>;
export type LoginReturnsAuthSession = Assert<
  Equal<LoginAccountResponses[200], AuthSession>
>;
export type RefreshReturnsAuthSession = Assert<
  Equal<RefreshSessionResponses[200], AuthSession>
>;
export type EnrollmentClaimReturnsAuthSession = Assert<
  Equal<ConsumeEnrollmentClaimResponses[200], AuthSession>
>;
export type EnrollmentClaimBodyIsRequired = Assert<
  Equal<ConsumeEnrollmentClaimData["body"], EnrollmentClaimRequest>
>;
export type EnrollmentIssueReturnsCredentialOnce = Assert<
  Equal<IssueEnrollmentClaimResponses[201], IssuedEnrollment>
>;
export type EnrollmentIssueBodyIsRequired = Assert<
  Equal<IssueEnrollmentClaimData["body"], EnrollmentIssueRequest>
>;
export type EnrollmentRevokeRequiresGrantPath = Assert<
  Equal<RevokeEnrollmentClaimData["path"], { grantId: string }>
>;
export type EnrollmentRevokeReturnsNoBody = Assert<
  Equal<RevokeEnrollmentClaimResponses[204], void>
>;
export type RefreshRequiresCSRF = Assert<
  Equal<RefreshSessionData["headers"]["X-AscendAny-CSRF"], string>
>;
export type LogoutRequiresCSRF = Assert<
  Equal<LogoutSessionData["headers"]["X-AscendAny-CSRF"], string>
>;
export type AccountIsRoleDiscriminated = Assert<
  Equal<Account, ({ role: "student" } & StudentAccount) | ({ role: "admin" } & AdminAccount)>
>;
export type StudentNumberIsRequired = Assert<Equal<StudentAccount["studentNumber"], string>>;
export type AdminStudentNumberIsNull = Assert<Equal<AdminAccount["studentNumber"], null>>;
export type ProfileUpdateBodyIsRequired = Assert<
  Equal<UpdateAccountProfileData["body"], AccountProfileUpdateRequest>
>;
export type ProfileUpdateReturnsAccount = Assert<
  Equal<UpdateAccountProfileResponses[200], Account>
>;
export type SessionListIsWrapped = Assert<
  Equal<ListAccountSessionsResponses[200], AccountSessionList>
>;
export type SessionRevokeRequiresPath = Assert<
  Equal<RevokeAccountSessionData["path"], { sessionId: string }>
>;
export type SessionRevokeReturnsNoBody = Assert<
  Equal<RevokeAccountSessionResponses[204], void>
>;
export type SelfAnalyticsResponseIsCanonicalUnion = Assert<
  Equal<GetSelfStudentAnalyticsResponses[200], SelfStudentAnalytics>
>;
export type SelfAnalyticsIsStateDiscriminated = Assert<
  Equal<
    SelfStudentAnalytics,
    | ({ state: "not_generated" } & StudentAnalyticsNotGenerated)
    | ({ state: "no_observations" } & StudentAnalyticsNoObservations)
    | ({ state: "ready" } & StudentAnalyticsReady)
  >
>;
export type SelfAnalyticsLimitIsOptionalNumber = Assert<
  Equal<NonNullable<GetSelfStudentAnalyticsData["query"]>["limit"], number | undefined>
>;
export type SelfAnalyticsMetricIsNullable = Assert<
  Equal<StudentMetricValues["knowledge"], number | null>
>;
export type SelfRecommendationResponseIsCanonicalUnion = Assert<
  Equal<GetSelfRecommendationResponses[200], SelfRecommendation>
>;
export type SelfRecommendationIsStateDiscriminated = Assert<
  Equal<
    SelfRecommendation,
    | ({ state: "fresh" } & RecommendationFresh)
    | ({ state: "unavailable" } & RecommendationUnavailable)
  >
>;
export type RecommendationInferenceResultV1IsStatusDiscriminated = Assert<
  Equal<
    RecommendationInferenceResultV1,
    | ({ status: "ready" } & RecommendationInferenceReadyResultV1)
    | ({ status: "insufficient" } & RecommendationInferenceInsufficientResultV1)
  >
>;
export type RecommendationInferenceReadyResultUsesTypedLearningPath = Assert<
  Equal<RecommendationInferenceReadyResultV1["learningPath"], Array<RecommendationLearningPathStepV2>>
>;
export type RecommendationInferenceResultSourceRatingIsNumber = Assert<
  Equal<RecommendationInferenceResultV1["sourceRating"], number>
>;
export type RecommendationInferenceResultUsesExactSchema = Assert<
  Equal<RecommendationInferenceResultV1["schema"], "ascendany.recommendation.inference-result.v1">
>;
export type RecommendationInferenceEvidenceIsObservationOwned = Assert<
  Equal<
    RecommendationInferenceEvidenceV1,
    {
      observationCount: number;
      distinctProblemCount: number;
      passedProblemCount: number;
    }
  >
>;
export type RecommendationKnowledgeMasteryUsesObservationCount = Assert<
  Equal<RecommendationKnowledgeMasteryV1["observationCount"], number>
>;
export type RecommendationKnowledgeMasteryHasNoTrainingCount = Assert<
  Equal<Extract<keyof RecommendationKnowledgeMasteryV1, "trainInteractionCount">, never>
>;
export type RecommendationFreshUsesAnalyticsAndModelHeads = Assert<
  Equal<
    Pick<
      RecommendationFresh,
      "currentAnalyticsGenerationId" | "currentAnalyticsHeadRevision" | "modelHeadRevision"
    >,
    {
      currentAnalyticsGenerationId: string;
      currentAnalyticsHeadRevision: number;
      modelHeadRevision: number;
    }
  >
>;
export type UnavailableRecommendationHasNoResult = Assert<
  Equal<Extract<keyof RecommendationUnavailable, "result">, never>
>;
export type RecommendationHasNoLegacyHeadField = Assert<
  Equal<
    Extract<
      keyof RecommendationFresh | keyof RecommendationUnavailable,
      "recommendationHeadRevision"
    >,
    never
  >
>;
export type UnavailableRecommendationUsesClosedInferenceReasons = Assert<
  Equal<
    RecommendationUnavailable["unavailableReason"],
    | "analytics_unavailable"
    | "actor_analytics_unavailable"
    | "knowledge_catalog_unavailable"
    | "knowledge_catalog_mismatch"
    | "eligible_problems_unavailable"
  >
>;
export type UnavailableRecommendationKeepsOptionalGenerationAndRequiredHeads = Assert<
  Equal<
    Pick<
      RecommendationUnavailable,
      "currentAnalyticsGenerationId" | "currentAnalyticsHeadRevision" | "modelHeadRevision"
    >,
    {
      currentAnalyticsGenerationId?: string;
      currentAnalyticsHeadRevision: number;
      modelHeadRevision: number;
    }
  >
>;
export type RecommendationModelProvenanceIsExactInferenceRelease = Assert<
  Equal<
    RecommendationModelProvenance,
    {
      modelId: string;
      purpose: "production" | "acceptance_test";
      artifactSha256: string;
      artifactSizeBytes: number;
      artifactMode: 420;
      modelSchema: "ascendany.recommendation.inference-model.v1";
      algorithm: "knowledge_mirt_feature_v1";
      inferenceContract: "ascendany.recommendation.inference.v1";
      trainedAt: string;
      trainingProvenanceSha256: string;
      featureSchemaSha256: string;
      knowledgeCatalogSha256: string;
      parameterSha256: string;
      goldenVectorsSha256: string;
      modelHeadRevision: number;
      applicationVersion: string;
      applicationCommit: string;
      applicationBuildTime: string;
    }
  >
>;
export type OnlineRecommendationTrainingOperationsAreAbsent = Assert<
  Equal<
    Extract<
      keyof typeof sdk,
      | "queueRecommendationTrainingRun"
      | "getRecommendationTrainingRun"
      | "listRecommendationTrainingRunEvents"
    >,
    never
  >
>;
export type RecommendationReviewContextIsGeneratedResponse = Assert<
  Equal<GetRecommendationReviewContextResponses[200], RecommendationReviewContext>
>;
export type CatalogPublicationAuthorizationBodyIsGenerated = Assert<
  Equal<AuthorizeKnowledgeCatalogPublicationData["body"], CatalogPublicationAuthorizationRequest>
>;
export type CatalogPublicationAuthorizationReturnsImmutableRequest = Assert<
  Equal<AuthorizeKnowledgeCatalogPublicationResponses[201], CatalogPublicationAuthorizationResult>
>;
export type AuthorizedCatalogPublicationRequestAddsOnlyIdentity = Assert<
  Equal<AuthorizedCatalogPublicationRequest, CatalogPublicationIntent & { authorizationId: string }>
>;
export type RecommendationCatalogV1UsesTypedClosedEntries = Assert<
  Equal<RecommendationKnowledgeCatalogV1["knowledgePoints"], Array<RecommendationKnowledgePointV1>>
>;
export type OnlineConfigurationPublishCannotCarryReleaseProvenance = Assert<
  Equal<
		Extract<
			keyof CreateConfigurationVersionRequest,
			| "expectedAnalyticsGenerationId"
			| "expectedAnalyticsHeadRevision"
			| "expectedInputManifestSha256"
			| "expectedCurrentModelHeadRevision"
			| "expectedCurrentModelArtifactSha256"
		>,
		never
  >
>;
export type ConfigurationContractHasNoTrainingKind = Assert<
  Equal<
    ConfigurationKind,
    | "prompt"
    | "model_connection"
    | "knowledge_catalog"
    | "feedback_policy"
    | "feedback_delivery"
  >
>;
export type ConfigurationPublishHasNoTrainingVariant = Assert<
  Equal<Extract<CreateConfigurationVersionRequest, { kind: "training" }>, never>
>;
export type GenericConfigurationPublishCannotOwnKnowledgeCatalog = Assert<
  Equal<Extract<CreateGenericConfigurationVersionRequest["kind"], "knowledge_catalog">, never>
>;
export type OnlineConfigurationPublishCannotOwnKnowledgeCatalog = Assert<
  Equal<Extract<CreateConfigurationVersionRequest["kind"], "knowledge_catalog">, never>
>;
export type SelfAnalyticsMissingHeadIsLiteralZero = Assert<
  Equal<StudentAnalyticsNotGenerated["headRevision"], 0>
>;
export type StudentLeaderboardResponseIsCanonicalUnion = Assert<
  Equal<GetStudentLeaderboardResponses[200], StudentLeaderboard>
>;
export type StudentLeaderboardIsStateDiscriminated = Assert<
  Equal<
    StudentLeaderboard,
    | ({ state: "not_generated" } & StudentLeaderboardNotGenerated)
    | ({ state: "no_observations" } & StudentLeaderboardNoObservations)
    | ({ state: "ready" } & StudentLeaderboardReady)
  >
>;
export type StudentLeaderboardLimitIsOptionalNumber = Assert<
  Equal<NonNullable<GetStudentLeaderboardData["query"]>["limit"], number | undefined>
>;
export type ConfigurationCreateBodyIsRequired = Assert<
  Equal<CreateConfigurationVersionData["body"], CreateConfigurationVersionRequest>
>;
export type ConfigurationCredentialReferenceIsExplicitlyNullable = Assert<
  Equal<CreateConfigurationVersionRequest["credentialRef"], string | null>
>;
export type ConfigurationCreateAndReplayShareOneEnvelope = Assert<
  Equal<
    CreateConfigurationVersionResponses,
    { 200: CreateConfigurationVersionResult; 201: CreateConfigurationVersionResult }
  >
>;
export type ConfigurationListReturnsCanonicalPage = Assert<
  Equal<ListConfigurationsResponses[200]["nextCursor"], string | null>
>;
export type ConfigurationListKindIsOptional = Assert<
  Equal<
    NonNullable<ListConfigurationsData["query"]>["kind"],
    | "prompt"
    | "model_connection"
    | "knowledge_catalog"
    | "feedback_policy"
    | "feedback_delivery"
    | undefined
  >
>;
export type ConfigurationGetRequiresCanonicalKeyPath = Assert<
  Equal<GetConfigurationData["path"], { key: string }>
>;
export type ConfigurationGetReturnsItem = Assert<
  Equal<GetConfigurationResponses[200], ListConfigurationsResponses[200]["items"][number]>
>;
export type ConfigurationVersionListRequiresKeyPath = Assert<
  Equal<ListConfigurationVersionsData["path"], { key: string }>
>;
export type ConfigurationVersionCursorIsOptionalNumber = Assert<
  Equal<
    NonNullable<ListConfigurationVersionsData["query"]>["beforeNumber"],
    number | undefined
  >
>;
export type ConfigurationVersionListHasNullableCursor = Assert<
  Equal<ListConfigurationVersionsResponses[200]["nextBeforeNumber"], number | null>
>;
export type FeedbackBodyIsRequired = Assert<
  Equal<SubmitAuthenticatedFeedbackData["body"], AuthenticatedFeedbackRequest>
>;
export type FeedbackMetadataIsOptionalAndNullable = Assert<
  Equal<AuthenticatedFeedbackRequest["platform"], string | null | undefined>
>;
export type FeedbackAlwaysReturnsAcceptedEnvelope = Assert<
  Equal<SubmitAuthenticatedFeedbackResponses, { 202: FeedbackSubmitResult }>
>;
export type AgentNoteListReturnsCanonicalPage = Assert<
  Equal<ListAgentNotesResponses[200], AgentNotePage>
>;
export type AgentNoteCursorIsOptional = Assert<
  Equal<NonNullable<ListAgentNotesData["query"]>["cursor"], string | undefined>
>;
export type AgentNoteCreateBodyIsRequired = Assert<
  Equal<CreateAgentNoteData["body"], CreateAgentNoteRequest>
>;
export type AgentNoteCreateHeadIsLiteralZero = Assert<
  Equal<CreateAgentNoteRequest["expectedHeadRevision"], 0>
>;
export type AgentNoteCreateAndReplayShareEnvelope = Assert<
  Equal<CreateAgentNoteResponses, { 200: AgentNoteMutationResult; 201: AgentNoteMutationResult }>
>;
export type AgentNoteGetRequiresOwnedPath = Assert<
  Equal<GetAgentNoteData["path"], { noteId: string }>
>;
export type AgentNoteGetReturnsDocument = Assert<
  Equal<GetAgentNoteResponses[200], AgentNote>
>;
export type AgentNoteReplaceBodyIsRequired = Assert<
  Equal<ReplaceAgentNoteData["body"], ReplaceAgentNoteRequest>
>;
export type AgentNoteReplaceReturnsMutation = Assert<
  Equal<ReplaceAgentNoteResponses[200], AgentNoteMutationResult>
>;
export type AgentNoteArchiveUsesStateCAS = Assert<
  Equal<ArchiveAgentNoteData["body"], AgentNoteStateMutationRequest>
>;
export type AgentNoteRestoreRequiresOwnedPath = Assert<
  Equal<RestoreAgentNoteData["path"], { noteId: string }>
>;
export type ChatThreadListReturnsCanonicalPage = Assert<
  Equal<ListChatThreadsResponses[200], ChatThreadPage>
>;
export type ChatThreadCursorIsOptional = Assert<
  Equal<NonNullable<ListChatThreadsData["query"]>["cursor"], string | undefined>
>;
export type ChatThreadCreateReturnsThread = Assert<
  Equal<CreateChatThreadResponses[201]["headRevision"], number>
>;
export type ChatThreadCreateReturnsExplicitKind = Assert<
  Equal<CreateChatThreadResponses[201]["kind"], "conversation" | "auto_analysis">
>;
export type ChatMessagesRequireOwnedThreadPath = Assert<
  Equal<ListChatMessagesData["path"], { threadId: string }>
>;
export type ChatMessagePageUsesDiscriminatedMessages = Assert<
  Equal<ListChatMessagesResponses[200]["items"][number], ChatMessage>
>;
export type AgentRunEnqueueBodyIsRequired = Assert<
  Equal<EnqueueAgentRunData["body"], ReplyAgentRunEnqueueRequest>
>;
export type ReplyAgentRunRequiresExplicitNullAnalytics = Assert<
  Equal<ReplyAgentRunEnqueueRequest["expectedAnalyticsHeadRevision"], null>
>;
export type AgentRunEnqueueSharesReplayEnvelope = Assert<
  Equal<EnqueueAgentRunResponses, { 200: AgentRunEnqueueResult; 202: AgentRunEnqueueResult }>
>;
export type AutomaticAnalysisOwnsMinimalServerIdentityRequest = Assert<
  Equal<EnqueueSelfAutoAnalysisData["body"], AutomaticAnalysisRequest>
>;
export type AutomaticAnalysisSharesReplayEnvelope = Assert<
  Equal<EnqueueSelfAutoAnalysisResponses, { 200: AgentRunEnqueueResult; 202: AgentRunEnqueueResult }>
>;
export type AgentRunGetRequiresOwnedRunPath = Assert<
  Equal<GetAgentRunData["path"], { runId: string }>
>;
export type AgentRunGetReturnsDurableState = Assert<
  Equal<GetAgentRunResponses[200]["status"], "queued" | "running" | "succeeded" | "failed" | "superseded">
>;
export type AgentRunSSECursorIsOptionalString = Assert<
  Equal<NonNullable<StreamAgentRunEventsData["headers"]>["Last-Event-ID"], string | undefined>
>;
export type AgentRunSSEPayloadIsDurableEvent = Assert<
  Equal<StreamAgentRunEventsResponses[200], AgentRunEvent>
>;
export type ExamGenerationSSERequiresExplicitGenerationPin = Assert<
  Equal<StreamExamAnalysisGenerationEventsData["path"], { examId: string; generationId: string }>
>;
export type ExamGenerationSSEUsesPinnedResourcePath = Assert<
  Equal<
    StreamExamAnalysisGenerationEventsData["url"],
    "/api/v2/exams/{examId}/analysis-generations/{generationId}/events"
  >
>;
export type ExamGenerationSSEPayloadIsDurableEvent = Assert<
  Equal<StreamExamAnalysisGenerationEventsResponses[200], ExamAnalysisGenerationEvent>
>;
