import { useEffect, useMemo, useState } from "react";
import type {
  ConfigurationItem,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
  RecommendationReviewProblem,
  RecommendationTrainingConfigurationV2,
} from "@ascendany/sdk";
import {
  loadRecommendationConfiguration,
  publishRecommendationConfiguration,
} from "../api/recommendation";
import {
  parseRecommendationKnowledgeCatalogV1,
  parseRecommendationTrainingConfigurationV2,
} from "../api/recommendationDocuments";
import {
  recommendationWorkflowIssue,
  type RecommendationWorkflowIssue,
  useRecommendationTrainingWorkflow,
} from "../hooks/useRecommendationTrainingWorkflow";
import { Field, PageHeader } from "../components/ui";

const KNOWLEDGE_CATALOG_SCHEMA = "ascendany.knowledge_catalog.recommendation.v1";
const TRAINING_CONFIGURATION_SCHEMA = "ascendany.training.recommendation.v2";
const DEFAULT_CATALOG_KEY = "recommendation.catalog.default";
const DEFAULT_TRAINING_KEY = "recommendation.training.default";

interface EditorState {
  key: string;
  expectedHeadRevision: number;
  document: string;
}

type CatalogCoverageStatus = "covered" | "fact changed" | "missing" | "draft invalid";

function compareText(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function catalogDocument(review: RecommendationReviewContext): RecommendationKnowledgeCatalogV1 {
  const assignments = review.problems.map((problem) => ({
    platform: problem.platform,
    problemId: problem.problemId,
    problemFactSha256: problem.problemFactSha256,
    knowledge: [{ knowledgePointId: "fundamentals", weight: 1 }],
  })).sort((left, right) => (
    compareText(left.platform, right.platform)
    || compareText(left.problemId, right.problemId)
    || compareText(left.problemFactSha256, right.problemFactSha256)
  ));
  return {
    taxonomyId: "recommendation.catalog.default",
    knowledgePoints: [{
      id: "fundamentals",
      label: "Fundamentals",
      description: "Reviewed Pintia problem fundamentals",
      prerequisiteIds: [],
    }],
    problemAssignments: assignments,
  };
}

function trainingDocument(knowledgeCatalogVersionId: string): RecommendationTrainingConfigurationV2 {
  return {
    algorithm: "knowledge_mirt_v1",
    knowledgeCatalogVersionId,
    accelerator: "cuda",
    seed: 2026,
    epochs: 100,
    patience: 10,
    batchSize: 32,
    learningRate: 0.01,
    weightDecay: 0.001,
    minTrainInteractions: 32,
    minActorInteractions: 2,
    minProblemInteractions: 1,
    validation: {
      minActors: 2,
      minInteractions: 2,
      minRelativeLogLossImprovement: 0,
    },
    pathPolicy: {
      targetMastery: 0.8,
      maxKnowledgeTargets: 3,
      minSteps: 2,
      maxSteps: 4,
      problemsPerStep: 2,
      targetSuccessProbability: 0.7,
    },
    rankingWeights: { knowledgeGap: 1, successDistance: 1 },
  };
}

function editorFromItem(item: ConfigurationItem): EditorState {
  return {
    key: item.key,
    expectedHeadRevision: item.headRevision,
    document: JSON.stringify(item.activeVersion?.document ?? {}, null, 2),
  };
}

function detailProblemKeys(issue: RecommendationWorkflowIssue | null): string[] {
  const details = issue?.details;
  if (details === null || details === undefined) return [];
  const value = details.problemKeys;
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) return [];
  return value;
}

function kindLabel(kind: "knowledge_catalog" | "training"): string {
  return kind === "knowledge_catalog" ? "knowledge catalog v1" : "recommendation training config v2";
}

function WorkflowIssueNotice({ issue }: { issue: RecommendationWorkflowIssue | null }) {
  if (issue === null) return null;
  return (
    <section className={`notice ${issue.kind === "drift" ? "notice-warning" : "notice-error"}`} role="alert">
      <strong>{issue.kind === "drift" ? "409 analytics drift" : issue.kind === "validation" ? "422 semantic / coverage validation" : "请求失败"}</strong>
      <div>{issue.message}</div>
      {issue.details ? <pre className="recommendation-issue-details">{JSON.stringify(issue.details, null, 2)}</pre> : null}
    </section>
  );
}

export function RecommendationTrainingPage() {
  const workflow = useRecommendationTrainingWorkflow();
  const [catalogEditor, setCatalogEditor] = useState<EditorState>({
    key: DEFAULT_CATALOG_KEY,
    expectedHeadRevision: 0,
    document: "",
  });
  const [trainingEditor, setTrainingEditor] = useState<EditorState>({
    key: DEFAULT_TRAINING_KEY,
    expectedHeadRevision: 0,
    document: "",
  });
  const [catalogVersion, setCatalogVersion] = useState<ConfigurationItem | null>(null);
  const [trainingVersion, setTrainingVersion] = useState<ConfigurationItem | null>(null);
  const [localIssue, setLocalIssue] = useState<RecommendationWorkflowIssue | null>(null);
  const [catalogBusy, setCatalogBusy] = useState(false);
  const [trainingBusy, setTrainingBusy] = useState(false);

  useEffect(() => {
    void workflow.reloadReview();
  }, [workflow.reloadReview]);

  useEffect(() => {
    const review = workflow.review;
    if (review === null) return;
    setCatalogEditor((current) => current.document.trim() === ""
      ? { ...current, document: JSON.stringify(catalogDocument(review), null, 2) }
      : current);
  }, [workflow.review]);

  const problemByKey = useMemo(() => new Map(
    (workflow.review?.problems ?? []).map((problem) => [problem.problemKey, problem]),
  ), [workflow.review]);
  const affectedProblems = detailProblemKeys(workflow.queueIssue)
    .map((key) => problemByKey.get(key))
    .filter((problem): problem is RecommendationReviewProblem => problem !== undefined);
  const catalogCoverage = useMemo(() => {
    const result = new Map<string, CatalogCoverageStatus>();
    if (workflow.review === null) return result;
    try {
      const catalog = parseRecommendationKnowledgeCatalogV1(catalogEditor.document);
      const assignmentFacts = new Map<string, Set<string>>();
      for (const assignment of catalog.problemAssignments) {
        const identity = `${assignment.platform}\0${assignment.problemId}`;
        const facts = assignmentFacts.get(identity) ?? new Set<string>();
        facts.add(assignment.problemFactSha256);
        assignmentFacts.set(identity, facts);
      }
      for (const problem of workflow.review.problems) {
        const facts = assignmentFacts.get(`${problem.platform}\0${problem.problemId}`);
        result.set(problem.problemKey, facts?.has(problem.problemFactSha256)
          ? "covered"
          : facts === undefined ? "missing" : "fact changed");
      }
    } catch {
      for (const problem of workflow.review.problems) result.set(problem.problemKey, "draft invalid");
    }
    return result;
  }, [catalogEditor.document, workflow.review]);

  const reloadReview = async () => {
    setLocalIssue(null);
    await workflow.reloadReview();
  };

  const loadConfiguration = async (kind: "knowledge_catalog" | "training") => {
    const editor = kind === "knowledge_catalog" ? catalogEditor : trainingEditor;
    const setBusy = kind === "knowledge_catalog" ? setCatalogBusy : setTrainingBusy;
    setBusy(true);
    setLocalIssue(null);
    try {
      const item = await loadRecommendationConfiguration(editor.key);
      if (item.kind !== kind || item.activeVersion === null) {
        throw new Error(`${editor.key} 没有 active ${kindLabel(kind)} version。`);
      }
      const expectedSchema = kind === "knowledge_catalog" ? KNOWLEDGE_CATALOG_SCHEMA : TRAINING_CONFIGURATION_SCHEMA;
      if (item.activeVersion.schemaId !== expectedSchema) {
        throw new Error(`${editor.key} active version schema 必须是 ${expectedSchema}。`);
      }
      if (kind === "knowledge_catalog") {
        setCatalogEditor(editorFromItem(item));
        setCatalogVersion(item);
        setTrainingVersion(null);
      } else {
        setTrainingEditor(editorFromItem(item));
        setTrainingVersion(item);
      }
    } catch (error) {
      setLocalIssue(recommendationWorkflowIssue(error));
    } finally {
      setBusy(false);
    }
  };

  const publishConfiguration = async (kind: "knowledge_catalog" | "training") => {
    const editor = kind === "knowledge_catalog" ? catalogEditor : trainingEditor;
    const setBusy = kind === "knowledge_catalog" ? setCatalogBusy : setTrainingBusy;
    setBusy(true);
    setLocalIssue(null);
    try {
      const result = kind === "knowledge_catalog"
        ? await publishRecommendationConfiguration({
          key: editor.key,
          kind: "knowledge_catalog",
          expectedHeadRevision: editor.expectedHeadRevision,
          schemaId: KNOWLEDGE_CATALOG_SCHEMA,
          document: parseRecommendationKnowledgeCatalogV1(editor.document),
          credentialRef: null,
        })
        : await publishRecommendationConfiguration({
          key: editor.key,
          kind: "training",
          expectedHeadRevision: editor.expectedHeadRevision,
          schemaId: TRAINING_CONFIGURATION_SCHEMA,
          document: parseRecommendationTrainingConfigurationV2(editor.document),
          credentialRef: null,
        });
      if (result.item.activeVersion === null) {
        throw new Error("服务端未返回 active immutable configuration version。");
      }
      if (kind === "knowledge_catalog") {
        setCatalogEditor(editorFromItem(result.item));
        setCatalogVersion(result.item);
        setTrainingEditor((current) => ({
          ...current,
          document: JSON.stringify(trainingDocument(result.item.activeVersion!.id), null, 2),
        }));
        setTrainingVersion(null);
      } else {
        setTrainingEditor(editorFromItem(result.item));
        setTrainingVersion(result.item);
      }
    } catch (error) {
      setLocalIssue(recommendationWorkflowIssue(error));
    } finally {
      setBusy(false);
    }
  };

  const queueTraining = async () => {
    if (workflow.review === null || trainingVersion === null) return;
    setLocalIssue(null);
    await workflow.queue({
      trainingConfigurationKey: trainingVersion.key,
      expectedAnalyticsGenerationId: workflow.review.analyticsGenerationId,
      expectedAnalyticsHeadRevision: workflow.review.analyticsHeadRevision,
    });
  };

  const catalogReady = catalogVersion !== null && catalogVersion.activeVersion !== null;
  const trainingReady = trainingVersion !== null && trainingVersion.activeVersion !== null;

  const rebuildCatalogTemplate = () => {
    const review = workflow.review;
    if (review === null) return;
    setLocalIssue(null);
    setCatalogEditor((current) => ({
      ...current,
      document: JSON.stringify(catalogDocument(review), null, 2),
    }));
    setCatalogVersion(null);
    setTrainingVersion(null);
  };

  return (
    <div className="page recommendation-training-page">
      <PageHeader
        title="推荐训练"
        description="Review analytics provenance，发布 catalog/config immutable versions，再以同一 generation/head fence 排队并跟踪 durable run；页面重载后按相同 provenance 再次排队会恢复既有 run。"
        actions={(
          <button className="button" type="button" disabled={workflow.loadingReview} onClick={() => void reloadReview()}>
            {workflow.loadingReview ? "加载中" : "重新加载 review"}
          </button>
        )}
      />

      <WorkflowIssueNotice issue={workflow.reviewIssue} />

      {workflow.review ? (
        <section className="panel recommendation-review" aria-label="Recommendation review context">
          <div className="panel-title"><span>1. Analytics review context</span><span>{workflow.review.problems.length} candidates</span></div>
          <dl className="recommendation-provenance">
            <div><dt>Generation</dt><dd>{workflow.review.analyticsGenerationId}</dd></div>
            <div><dt>Head revision</dt><dd>{workflow.review.analyticsHeadRevision}</dd></div>
            <div><dt>Input manifest</dt><dd><code>{workflow.review.inputManifestSha256}</code></dd></div>
          </dl>
          <div className="table-wrap recommendation-candidates">
            <table>
              <thead><tr><th>problemKey</th><th>Problem</th><th>sourceProblemSets</th><th>Catalog draft</th></tr></thead>
              <tbody>
                {workflow.review.problems.map((problem) => (
                  <tr key={problem.problemKey}>
                    <td><code>{problem.problemKey}</code></td>
                    <td><strong>{problem.title}</strong><span className="muted-block">{problem.platform}:{problem.problemId}</span></td>
                    <td>{problem.sourceProblemSets.map((source) => (
                      <a key={`${source.problemSetId}:${source.sourceUrl}`} href={source.sourceUrl} target="_blank" rel="noopener noreferrer">
                        {source.problemSetId}
                      </a>
                    ))}</td>
                    <td><span className={`recommendation-coverage recommendation-coverage-${catalogCoverage.get(problem.problemKey)?.replace(" ", "-") ?? "draft-invalid"}`}>{catalogCoverage.get(problem.problemKey) ?? "draft invalid"}</span></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : (
        <section className="panel empty-state">尚未加载有效 review context；排队入口保持禁用。</section>
      )}

      <WorkflowIssueNotice issue={localIssue} />

      {affectedProblems.length > 0 ? (
        <section className="panel recommendation-affected" aria-label="Coverage affected problems">
          <div className="panel-title">Coverage affected problems</div>
          <ul>{affectedProblems.map((problem) => <li key={problem.problemKey}><code>{problem.problemKey}</code> · {problem.title}</li>)}</ul>
        </section>
      ) : null}

      <div className="recommendation-configuration-grid">
        <section className="panel recommendation-configuration" aria-label="Knowledge catalog editor">
          <div className="panel-title"><span>2. Knowledge catalog v1</span><span>CAS r{catalogEditor.expectedHeadRevision}</span></div>
          <div className="recommendation-editor-body">
            <Field label="Configuration key"><input value={catalogEditor.key} onChange={(event) => {
              setCatalogEditor((current) => ({ ...current, key: event.target.value, expectedHeadRevision: 0 }));
              setCatalogVersion(null);
              setTrainingVersion(null);
            }} /></Field>
            <Field label="Schema"><code>{KNOWLEDGE_CATALOG_SCHEMA}</code></Field>
            <Field label="Document"><textarea aria-label="Knowledge catalog document" value={catalogEditor.document} onChange={(event) => {
              setCatalogEditor((current) => ({ ...current, document: event.target.value }));
              setCatalogVersion(null);
              setTrainingVersion(null);
            }} spellCheck={false} /></Field>
          </div>
          <div className="recommendation-editor-actions">
            <button className="button" type="button" disabled={catalogBusy} onClick={() => void loadConfiguration("knowledge_catalog")}>读取 active version</button>
            <button className="button" type="button" disabled={catalogBusy || workflow.review === null} onClick={rebuildCatalogTemplate}>从 review 重建模板</button>
            <button className="button button-primary" type="button" disabled={catalogBusy || workflow.review === null} onClick={() => void publishConfiguration("knowledge_catalog")}>{catalogBusy ? "处理中" : "发布 catalog v1"}</button>
          </div>
          {catalogReady ? <div className="notice notice-success">Catalog version ID: {catalogVersion.activeVersion!.id}</div> : null}
        </section>

        <section className="panel recommendation-configuration" aria-label="Recommendation training configuration editor">
          <div className="panel-title"><span>3. Recommendation training config v2</span><span>CAS r{trainingEditor.expectedHeadRevision}</span></div>
          <div className="recommendation-editor-body">
            <Field label="Configuration key"><input value={trainingEditor.key} onChange={(event) => {
              setTrainingEditor((current) => ({ ...current, key: event.target.value, expectedHeadRevision: 0 }));
              setTrainingVersion(null);
            }} /></Field>
            <Field label="Schema"><code>{TRAINING_CONFIGURATION_SCHEMA}</code></Field>
            <Field label="Document"><textarea aria-label="Recommendation training document" value={trainingEditor.document} onChange={(event) => {
              setTrainingEditor((current) => ({ ...current, document: event.target.value }));
              setTrainingVersion(null);
            }} spellCheck={false} /></Field>
          </div>
          <div className="recommendation-editor-actions">
            <button className="button" type="button" disabled={trainingBusy} onClick={() => void loadConfiguration("training")}>读取 active version</button>
            <button className="button button-primary" type="button" disabled={trainingBusy || !catalogReady} onClick={() => void publishConfiguration("training")}>{trainingBusy ? "处理中" : "发布 training v2"}</button>
          </div>
          {trainingReady ? <div className="notice notice-success">Training version ID: {trainingVersion.activeVersion!.id}</div> : null}
        </section>
      </div>

      <section className="panel recommendation-run" aria-label="Recommendation training run">
        <div className="panel-title"><span>4. Durable training run</span><span>{workflow.polling ? "polling" : workflow.run?.status ?? "idle"}</span></div>
        <div className="recommendation-run-actions">
          <button className="button button-primary" type="button" disabled={workflow.queueing || workflow.review === null || !trainingReady} onClick={() => void queueTraining()}>
            {workflow.queueing ? "排队中" : "按已 review provenance 排队"}
          </button>
          {workflow.review ? <code>generation={workflow.review.analyticsGenerationId} / head={workflow.review.analyticsHeadRevision}</code> : <span>409 drift 后必须重新 review。</span>}
        </div>
        <WorkflowIssueNotice issue={workflow.queueIssue} />
        <WorkflowIssueNotice issue={workflow.trackingIssue} />
        {workflow.queueCreated !== null ? (
          <div className="notice notice-success" role="status">
            {workflow.queueCreated ? "已创建新的 durable 训练任务。" : "created=false：已恢复现有训练任务并继续追踪。"}
          </div>
        ) : null}
        {workflow.trackingStopped ? (
          <div className="recommendation-run-actions">
            <button className="button" type="button" disabled={workflow.polling} onClick={workflow.retryTracking}>
              继续追踪 durable run
            </button>
            <span>轮询请求失败后保持 run ID 与 event cursor，由操作员显式继续。</span>
          </div>
        ) : null}
        {workflow.run ? (
          <>
            <dl className="recommendation-run-detail">
              <div><dt>Run ID</dt><dd>{workflow.run.id}</dd></div>
              <div><dt>Status</dt><dd>{workflow.run.status}</dd></div>
              <div><dt>Attempts</dt><dd>{workflow.run.attemptCount}</dd></div>
              <div><dt>Generation / head</dt><dd>{workflow.run.sourceAnalyticsGenerationId} / {workflow.run.sourceAnalyticsHeadRevision}</dd></div>
            </dl>
            {workflow.run.failure ? (
              <div className="notice notice-error" role="status">
                <strong>Safe terminal failure: {workflow.run.failure.code}</strong>
                <div>{workflow.run.failure.message}</div>
              </div>
            ) : null}
          </>
        ) : null}
        <ol className="recommendation-event-list" aria-label="Ordered durable training events">
          {workflow.events.map((event) => (
            <li key={event.sequence}>
              <span>#{event.sequence}</span><strong>{event.type}</strong><time dateTime={event.createdAt}>{event.createdAt}</time>
              <code>{JSON.stringify(event.payload)}</code>
            </li>
          ))}
        </ol>
      </section>
    </div>
  );
}
