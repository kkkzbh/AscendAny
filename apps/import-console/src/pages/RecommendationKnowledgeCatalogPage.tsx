import { useCallback, useEffect, useMemo, useState } from "react";
import type {
  ConfigurationItem,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
  RecommendationReviewProblem,
} from "@ascendany/sdk";
import {
  loadRecommendationKnowledgeCatalog,
  loadRecommendationReviewContext,
  publishRecommendationKnowledgeCatalog,
  RecommendationCatalogApiError,
} from "../api/recommendation";
import { parseRecommendationKnowledgeCatalogV1 } from "../api/recommendationDocuments";
import { Field, PageHeader } from "../components/ui";

const KNOWLEDGE_CATALOG_SCHEMA = "ascendany.knowledge_catalog.recommendation.v1";
const KNOWLEDGE_CATALOG_KEY = "recommendation.catalog.active";

interface EditorState {
  expectedHeadRevision: number;
  document: string;
}

type CatalogCoverageStatus = "covered" | "fact changed" | "missing" | "draft invalid";

interface CatalogIssue {
  kind: "conflict" | "validation" | "request";
  message: string;
  details: Record<string, unknown> | null;
}

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
    knowledge: [],
  })).sort((left, right) => (
    compareText(left.platform, right.platform)
    || compareText(left.problemId, right.problemId)
    || compareText(left.problemFactSha256, right.problemFactSha256)
  ));
  return {
    taxonomyId: KNOWLEDGE_CATALOG_KEY,
    knowledgePoints: [],
    problemAssignments: assignments,
  };
}

function editorFromItem(item: ConfigurationItem): EditorState {
  if (item.key !== KNOWLEDGE_CATALOG_KEY) {
    throw new Error(`Knowledge catalog key 必须是 ${KNOWLEDGE_CATALOG_KEY}。`);
  }
  return {
    expectedHeadRevision: item.headRevision,
    document: JSON.stringify(item.activeVersion?.document ?? {}, null, 2),
  };
}

function catalogIssue(error: unknown): CatalogIssue {
  if (error instanceof RecommendationCatalogApiError) {
    return {
      kind: error.status === 409 ? "conflict" : error.status === 422 ? "validation" : "request",
      message: error.apiError.message,
      details: error.apiError.details ?? null,
    };
  }
  return {
    kind: "request",
    message: error instanceof Error ? error.message : "推荐知识目录请求失败。",
    details: null,
  };
}

function detailProblemKeys(issue: CatalogIssue | null): string[] {
  const value = issue?.details?.problemKeys;
  if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) return [];
  return value;
}

function CatalogIssueNotice({ issue }: { issue: CatalogIssue | null }) {
  if (issue === null) return null;
  const heading = issue.kind === "conflict"
    ? "409 configuration conflict"
    : issue.kind === "validation"
      ? "422 semantic / coverage validation"
      : "请求失败";
  return (
    <section className={`notice ${issue.kind === "conflict" ? "notice-warning" : "notice-error"}`} role="alert">
      <strong>{heading}</strong>
      <div>{issue.message}</div>
      {issue.details ? <pre className="recommendation-issue-details">{JSON.stringify(issue.details, null, 2)}</pre> : null}
    </section>
  );
}

export function RecommendationKnowledgeCatalogPage() {
  const [review, setReview] = useState<RecommendationReviewContext | null>(null);
  const [reviewIssue, setReviewIssue] = useState<CatalogIssue | null>(null);
  const [loadingReview, setLoadingReview] = useState(false);
  const [catalogEditor, setCatalogEditor] = useState<EditorState>({
    expectedHeadRevision: 0,
    document: "",
  });
  const [activeCatalog, setActiveCatalog] = useState<ConfigurationItem | null>(null);
  const [catalogIssueState, setCatalogIssueState] = useState<CatalogIssue | null>(null);
  const [catalogBusy, setCatalogBusy] = useState(false);

  const reloadReview = useCallback(async () => {
    setLoadingReview(true);
    setReviewIssue(null);
    try {
      setReview(await loadRecommendationReviewContext());
    } catch (error) {
      setReview(null);
      setReviewIssue(catalogIssue(error));
    } finally {
      setLoadingReview(false);
    }
  }, []);

  useEffect(() => {
    void reloadReview();
  }, [reloadReview]);

  useEffect(() => {
    if (review === null) return;
    setCatalogEditor((current) => current.document.trim() === ""
      ? { ...current, document: JSON.stringify(catalogDocument(review), null, 2) }
      : current);
  }, [review]);

  const problemByKey = useMemo(() => new Map(
    (review?.problems ?? []).map((problem) => [problem.problemKey, problem]),
  ), [review]);

  const affectedProblems = detailProblemKeys(catalogIssueState)
    .map((key) => problemByKey.get(key))
    .filter((problem): problem is RecommendationReviewProblem => problem !== undefined);

  const catalogDraft = useMemo(() => {
    try {
      return {
        document: parseRecommendationKnowledgeCatalogV1(catalogEditor.document),
        error: null,
      };
    } catch (error) {
      return {
        document: null,
        error: error instanceof Error ? error.message : "Knowledge catalog document 无效。",
      };
    }
  }, [catalogEditor.document]);

  const catalogCoverage = useMemo(() => {
    const result = new Map<string, CatalogCoverageStatus>();
    if (review === null) return result;
    if (catalogDraft.document !== null) {
      const assignmentFacts = new Map<string, Set<string>>();
      for (const assignment of catalogDraft.document.problemAssignments) {
        const identity = `${assignment.platform}\0${assignment.problemId}`;
        const facts = assignmentFacts.get(identity) ?? new Set<string>();
        facts.add(assignment.problemFactSha256);
        assignmentFacts.set(identity, facts);
      }
      for (const problem of review.problems) {
        const facts = assignmentFacts.get(`${problem.platform}\0${problem.problemId}`);
        result.set(problem.problemKey, facts?.has(problem.problemFactSha256)
          ? "covered"
          : facts === undefined ? "missing" : "fact changed");
      }
    } else {
      for (const problem of review.problems) result.set(problem.problemKey, "draft invalid");
    }
    return result;
  }, [catalogDraft.document, review]);

  const catalogCoverageExact = useMemo(() => {
    if (review === null || catalogDraft.document === null) return false;
    const reviewed = new Set(review.problems.map((problem) => (
      `${problem.platform}\0${problem.problemId}\0${problem.problemFactSha256}`
    )));
    const assigned = new Set(catalogDraft.document.problemAssignments.map((assignment) => (
      `${assignment.platform}\0${assignment.problemId}\0${assignment.problemFactSha256}`
    )));
    return reviewed.size === assigned.size && [...reviewed].every((identity) => assigned.has(identity));
  }, [catalogDraft.document, review]);

  const loadCatalog = async () => {
    setCatalogBusy(true);
    setCatalogIssueState(null);
    try {
      const item = await loadRecommendationKnowledgeCatalog(KNOWLEDGE_CATALOG_KEY);
      if (item.kind !== "knowledge_catalog" || item.activeVersion === null) {
        throw new Error(`${KNOWLEDGE_CATALOG_KEY} 没有 active knowledge catalog version。`);
      }
      if (item.activeVersion.schemaId !== KNOWLEDGE_CATALOG_SCHEMA) {
        throw new Error(`${KNOWLEDGE_CATALOG_KEY} active version schema 必须是 ${KNOWLEDGE_CATALOG_SCHEMA}。`);
      }
      setCatalogEditor(editorFromItem(item));
      setActiveCatalog(item);
    } catch (error) {
      setCatalogIssueState(catalogIssue(error));
    } finally {
      setCatalogBusy(false);
    }
  };

  const publishCatalog = async () => {
    if (catalogDraft.document === null) {
      setCatalogIssueState({
        kind: "validation",
        message: catalogDraft.error ?? "Knowledge catalog document 无效。",
        details: null,
      });
      return;
    }
	if (review === null || !catalogCoverageExact) {
	  setCatalogIssueState({
	    kind: "validation",
	    message: "Catalog assignments 必须精确覆盖当前 review problem identity/fact 集合。",
	    details: null,
	  });
	  return;
	}
    setCatalogBusy(true);
    setCatalogIssueState(null);
    try {
      const result = await publishRecommendationKnowledgeCatalog({
        key: KNOWLEDGE_CATALOG_KEY,
        kind: "knowledge_catalog",
        expectedHeadRevision: catalogEditor.expectedHeadRevision,
		expectedAnalyticsGenerationId: review.analyticsGenerationId,
		expectedAnalyticsHeadRevision: review.analyticsHeadRevision,
		expectedInputManifestSha256: review.inputManifestSha256,
        schemaId: KNOWLEDGE_CATALOG_SCHEMA,
        document: catalogDraft.document,
        credentialRef: null,
      });
      if (result.item.activeVersion === null) {
        throw new Error("服务端未返回 active immutable knowledge catalog version。");
      }
      setCatalogEditor(editorFromItem(result.item));
      setActiveCatalog(result.item);
    } catch (error) {
      setCatalogIssueState(catalogIssue(error));
    } finally {
      setCatalogBusy(false);
    }
  };

  const rebuildCatalogTemplate = () => {
    if (review === null) return;
    setCatalogIssueState(null);
    setCatalogEditor((current) => ({
      ...current,
      document: JSON.stringify(catalogDocument(review), null, 2),
    }));
    setActiveCatalog(null);
  };

  return (
    <div className="page recommendation-catalog-page">
      <PageHeader
        title="推荐知识目录"
        description="基于当前 analytics review context 维护题目与知识点的严格映射，并以 CAS 发布 immutable catalog version。模型 artifact 由独立 release 流程发布。"
        actions={(
          <button className="button" type="button" disabled={loadingReview} onClick={() => void reloadReview()}>
            {loadingReview ? "加载中" : "重新加载 review"}
          </button>
        )}
      />

      <CatalogIssueNotice issue={reviewIssue} />

      {review ? (
        <section className="panel recommendation-review" aria-label="Recommendation review context">
          <div className="panel-title"><span>1. Analytics review context</span><span>{review.problems.length} candidates</span></div>
          <dl className="recommendation-provenance">
            <div><dt>Generation</dt><dd>{review.analyticsGenerationId}</dd></div>
            <div><dt>Head revision</dt><dd>{review.analyticsHeadRevision}</dd></div>
            <div><dt>Input manifest</dt><dd><code>{review.inputManifestSha256}</code></dd></div>
          </dl>
          <div className="table-wrap recommendation-candidates">
            <table>
              <thead><tr><th>problemKey</th><th>Problem</th><th>sourceProblemSets</th><th>Catalog draft</th></tr></thead>
              <tbody>
                {review.problems.map((problem) => (
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
        <section className="panel empty-state">尚未加载有效 review context；catalog 发布入口保持禁用。</section>
      )}

      <CatalogIssueNotice issue={catalogIssueState} />

      {affectedProblems.length > 0 ? (
        <section className="panel recommendation-affected" aria-label="Coverage affected problems">
          <div className="panel-title">Coverage affected problems</div>
          <ul>{affectedProblems.map((problem) => <li key={problem.problemKey}><code>{problem.problemKey}</code> · {problem.title}</li>)}</ul>
        </section>
      ) : null}

      <section className="panel recommendation-configuration" aria-label="Knowledge catalog editor">
        <div className="panel-title"><span>2. Knowledge catalog v1</span><span>CAS r{catalogEditor.expectedHeadRevision}</span></div>
        <div className="recommendation-editor-body">
          <Field label="Configuration key"><code>{KNOWLEDGE_CATALOG_KEY}</code></Field>
          <Field label="Schema"><code>{KNOWLEDGE_CATALOG_SCHEMA}</code></Field>
          <Field label="Document"><textarea aria-label="Knowledge catalog document" value={catalogEditor.document} onChange={(event) => {
            setCatalogEditor((current) => ({ ...current, document: event.target.value }));
            setActiveCatalog(null);
          }} spellCheck={false} /></Field>
        </div>
        {catalogDraft.document === null && review !== null ? (
          <div className="notice notice-warning" role="status">
            Identity/fact 骨架不包含推测的知识语义。请依据题目内容填写真实 taxonomy、knowledgePoints、prerequisiteIds 与每题 knowledge weights；document 通过严格校验后才能发布。
            <span className="muted-block">{catalogDraft.error}</span>
          </div>
        ) : null}
		{catalogDraft.document !== null && review !== null && !catalogCoverageExact ? (
		  <div className="notice notice-warning" role="status">
		    Catalog assignments 必须逐项匹配当前 review 的 platform、problemId 与 problemFactSha256，且不能缺失或包含悬空 identity。
		  </div>
		) : null}
        <div className="recommendation-editor-actions">
          <button className="button" type="button" disabled={catalogBusy} onClick={() => void loadCatalog()}>读取 active version</button>
          <button className="button" type="button" disabled={catalogBusy || review === null} onClick={rebuildCatalogTemplate}>从 review 重建 identity/fact 骨架</button>
          <button className="button button-primary" type="button" disabled={catalogBusy || review === null || catalogDraft.document === null || !catalogCoverageExact} onClick={() => void publishCatalog()}>{catalogBusy ? "处理中" : "发布 catalog v1"}</button>
        </div>
        {activeCatalog?.activeVersion ? (
          <div className="notice notice-success">
            Active catalog version ID: {activeCatalog.activeVersion.id} · SHA-256: <code>{activeCatalog.activeVersion.documentSha256}</code>
          </div>
        ) : null}
      </section>
    </div>
  );
}
