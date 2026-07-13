import { useCallback, useEffect, useMemo, useState } from "react";
import type {
  ConfigurationItem,
  RecommendationKnowledgeCatalogV1,
  RecommendationReviewContext,
} from "@ascendany/sdk";
import {
  loadRecommendationKnowledgeCatalog,
  loadRecommendationReviewContext,
  RecommendationCatalogApiError,
} from "../api/recommendation";
import { parseRecommendationKnowledgeCatalogV1 } from "../api/recommendationDocuments";
import { Field, PageHeader } from "../components/ui";

const KNOWLEDGE_CATALOG_SCHEMA = "ascendany.knowledge_catalog.recommendation.v1";
const KNOWLEDGE_CATALOG_KEY = "recommendation.catalog.active";

type CatalogCoverageStatus = "covered" | "fact changed" | "missing";

interface CatalogIssue {
  message: string;
  details: Record<string, unknown> | null;
}

function catalogIssue(error: unknown): CatalogIssue {
  if (error instanceof RecommendationCatalogApiError) {
    return {
      message: error.apiError.message,
      details: error.apiError.details ?? null,
    };
  }
  return {
    message: error instanceof Error ? error.message : "推荐知识目录请求失败。",
    details: null,
  };
}

function CatalogIssueNotice({ issue }: { issue: CatalogIssue | null }) {
  if (issue === null) return null;
  return (
    <section className="notice notice-error" role="alert">
      <strong>读取失败</strong>
      <div>{issue.message}</div>
      {issue.details ? <pre className="recommendation-issue-details">{JSON.stringify(issue.details, null, 2)}</pre> : null}
    </section>
  );
}

function parseActiveCatalog(item: ConfigurationItem): RecommendationKnowledgeCatalogV1 {
  if (item.key !== KNOWLEDGE_CATALOG_KEY) {
    throw new Error(`Knowledge catalog key 必须是 ${KNOWLEDGE_CATALOG_KEY}。`);
  }
  if (item.kind !== "knowledge_catalog" || item.activeVersion === null) {
    throw new Error(`${KNOWLEDGE_CATALOG_KEY} 没有 active knowledge catalog version。`);
  }
  if (item.activeVersion.schemaId !== KNOWLEDGE_CATALOG_SCHEMA) {
    throw new Error(`${KNOWLEDGE_CATALOG_KEY} active version schema 必须是 ${KNOWLEDGE_CATALOG_SCHEMA}。`);
  }
  return parseRecommendationKnowledgeCatalogV1(JSON.stringify(item.activeVersion.document));
}

export function RecommendationKnowledgeCatalogPage() {
  const [review, setReview] = useState<RecommendationReviewContext | null>(null);
  const [activeCatalog, setActiveCatalog] = useState<ConfigurationItem | null>(null);
  const [catalogDocument, setCatalogDocument] = useState<RecommendationKnowledgeCatalogV1 | null>(null);
  const [reviewIssue, setReviewIssue] = useState<CatalogIssue | null>(null);
  const [catalogIssueState, setCatalogIssueState] = useState<CatalogIssue | null>(null);
  const [loading, setLoading] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    setReviewIssue(null);
    setCatalogIssueState(null);
    const [reviewResult, catalogResult] = await Promise.allSettled([
      loadRecommendationReviewContext(),
      loadRecommendationKnowledgeCatalog(KNOWLEDGE_CATALOG_KEY),
    ]);
    if (reviewResult.status === "fulfilled") {
      setReview(reviewResult.value);
    } else {
      setReview(null);
      setReviewIssue(catalogIssue(reviewResult.reason));
    }
    if (catalogResult.status === "fulfilled") {
      try {
        setCatalogDocument(parseActiveCatalog(catalogResult.value));
        setActiveCatalog(catalogResult.value);
      } catch (error) {
        setCatalogDocument(null);
        setActiveCatalog(null);
        setCatalogIssueState(catalogIssue(error));
      }
    } else {
      setCatalogDocument(null);
      setActiveCatalog(null);
      setCatalogIssueState(catalogIssue(catalogResult.reason));
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const catalogCoverage = useMemo(() => {
    const result = new Map<string, CatalogCoverageStatus>();
    if (review === null || catalogDocument === null) return result;
    const assignmentFacts = new Map<string, Set<string>>();
    for (const assignment of catalogDocument.problemAssignments) {
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
    return result;
  }, [catalogDocument, review]);

  const catalogCoverageExact = useMemo(() => {
    if (review === null || catalogDocument === null) return false;
    const reviewed = new Set(review.problems.map((problem) => (
      `${problem.platform}\0${problem.problemId}\0${problem.problemFactSha256}`
    )));
    const assigned = new Set(catalogDocument.problemAssignments.map((assignment) => (
      `${assignment.platform}\0${assignment.problemId}\0${assignment.problemFactSha256}`
    )));
    return reviewed.size === assigned.size && [...reviewed].every((identity) => assigned.has(identity));
  }, [catalogDocument, review]);

  return (
    <div className="page recommendation-catalog-page">
      <PageHeader
        title="推荐知识目录"
        description="查看 release 绑定的 immutable knowledge catalog、analytics provenance 与当前题目覆盖。Catalog 由停服期 operator CLI 发布，在线控制台仅提供审查视图。"
        actions={(
          <button className="button" type="button" disabled={loading} onClick={() => void reload()}>
            {loading ? "加载中" : "重新加载"}
          </button>
        )}
      />

      <CatalogIssueNotice issue={reviewIssue} />
      <CatalogIssueNotice issue={catalogIssueState} />

      {activeCatalog?.activeVersion && catalogDocument ? (
        <section className="panel recommendation-configuration" aria-label="Active knowledge catalog">
          <div className="panel-title">
            <span>Release-owned knowledge catalog</span>
            <span>head r{activeCatalog.headRevision}</span>
          </div>
          <dl className="recommendation-provenance">
            <div><dt>Version ID</dt><dd>{activeCatalog.activeVersion.id}</dd></div>
            <div><dt>Document SHA-256</dt><dd><code>{activeCatalog.activeVersion.documentSha256}</code></dd></div>
            <div><dt>Created at</dt><dd>{activeCatalog.activeVersion.createdAt}</dd></div>
          </dl>
          <div className="recommendation-editor-body">
            <Field label="Configuration key"><code>{activeCatalog.key}</code></Field>
            <Field label="Schema"><code>{activeCatalog.activeVersion.schemaId}</code></Field>
            <Field label="Taxonomy"><code>{catalogDocument.taxonomyId}</code></Field>
            <Field label="Knowledge points">{catalogDocument.knowledgePoints.length}</Field>
            <Field label="Problem assignments">{catalogDocument.problemAssignments.length}</Field>
          </div>
        </section>
      ) : (
        <section className="panel empty-state">尚未读取到经过严格校验的 active release catalog。</section>
      )}

      {review ? (
        <section className="panel recommendation-review" aria-label="Recommendation review context">
          <div className="panel-title">
            <span>Analytics review context</span>
            <span>{review.problems.length} problems</span>
          </div>
          <dl className="recommendation-provenance">
            <div><dt>Generation</dt><dd>{review.analyticsGenerationId}</dd></div>
            <div><dt>Head revision</dt><dd>{review.analyticsHeadRevision}</dd></div>
            <div><dt>Input manifest</dt><dd><code>{review.inputManifestSha256}</code></dd></div>
          </dl>
          {catalogDocument ? (
            <div className={`notice ${catalogCoverageExact ? "notice-success" : "notice-warning"}`} role="status">
              {catalogCoverageExact
                ? "Active catalog 精确覆盖当前 analytics problem identity/fact 集合。"
                : "Active catalog 与当前 analytics problem identity/fact 集合不一致；推理发布验收应直接失败。"}
            </div>
          ) : null}
          <div className="table-wrap recommendation-candidates">
            <table>
              <thead><tr><th>problemKey</th><th>Problem</th><th>sourceProblemSets</th><th>Active catalog</th></tr></thead>
              <tbody>
                {review.problems.map((problem) => {
                  const coverage = catalogCoverage.get(problem.problemKey);
                  return (
                    <tr key={problem.problemKey}>
                      <td><code>{problem.problemKey}</code></td>
                      <td><strong>{problem.title}</strong><span className="muted-block">{problem.platform}:{problem.problemId}</span></td>
                      <td>{problem.sourceProblemSets.map((source) => (
                        <a key={`${source.problemSetId}:${source.sourceUrl}`} href={source.sourceUrl} target="_blank" rel="noopener noreferrer">
                          {source.problemSetId}
                        </a>
                      ))}</td>
                      <td>{coverage ? (
                        <span className={`recommendation-coverage recommendation-coverage-${coverage.replace(" ", "-")}`}>{coverage}</span>
                      ) : <span className="recommendation-coverage">unavailable</span>}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      ) : (
        <section className="panel empty-state">尚未加载有效 analytics review context。</section>
      )}
    </div>
  );
}
