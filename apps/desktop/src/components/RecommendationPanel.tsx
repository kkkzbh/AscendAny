import { useCallback, useEffect, useState } from "react";
import type {
  RecommendationInsufficiencyV2,
  RecommendationModelProvenance,
  RecommendationProblemV2,
  RecommendationUnavailable,
  SelfRecommendation,
} from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadSelfRecommendation } from "../api/operations";
import { useSession } from "../session/context";

function percentage(value: number) { return `${Math.round(value * 100)}%`; }
function insufficiencyMessage(reason: RecommendationInsufficiencyV2["reasonCode"]) {
  switch (reason) {
    case "mastery_target_satisfied": return "当前知识点掌握度已达到学习目标。";
    case "path_below_minimum": return "可形成的学习路径短于配置要求。";
    case "path_exceeds_maximum": return "完整前置依赖路径超过配置上限。";
    case "problem_candidates_below_minimum": return "部分知识点缺少足量且适合的练习题。";
  }
}
function unavailableMessage(reason: RecommendationUnavailable["unavailableReason"]) {
  switch (reason) {
    case "analytics_unavailable": return "系统尚未发布分析结果。";
    case "actor_analytics_unavailable": return "当前账号尚无可用于推理的个人分析。";
    case "knowledge_catalog_unavailable": return "推荐知识目录尚未发布。";
    case "knowledge_catalog_mismatch": return "推荐知识目录与当前模型的 immutable provenance 不一致。";
    case "eligible_problems_unavailable": return "当前目录中没有满足约束的候选练习题。";
  }
}
function ModelProvenance({ model }: { model: RecommendationModelProvenance }) {
  return <dl className="recommendation-provenance"><div><dt>模型 ID</dt><dd><code>{model.modelId}</code></dd></div><div><dt>Deployment purpose</dt><dd>{model.purpose}</dd></div><div><dt>Artifact</dt><dd><code>{model.artifactSha256}</code> · {model.artifactSizeBytes} bytes · mode {model.artifactMode}</dd></div><div><dt>Runtime contract</dt><dd>{model.modelSchema} · {model.algorithm} · {model.inferenceContract}</dd></div><div><dt>模型 head</dt><dd>r{model.modelHeadRevision} · trained {model.trainedAt}</dd></div><div><dt>Training provenance</dt><dd><code>{model.trainingProvenanceSha256}</code></dd></div><div><dt>Feature schema</dt><dd><code>{model.featureSchemaSha256}</code></dd></div><div><dt>知识目录</dt><dd><code>{model.knowledgeCatalogSha256}</code></dd></div><div><dt>Parameters</dt><dd><code>{model.parameterSha256}</code></dd></div><div><dt>Golden vectors</dt><dd><code>{model.goldenVectorsSha256}</code></dd></div><div><dt>应用 release</dt><dd>{model.applicationVersion} · <code>{model.applicationCommit}</code> · {model.applicationBuildTime}</dd></div></dl>;
}
function RecommendedProblems({ problems }: { problems: RecommendationProblemV2[] }) {
  return <ul className="recommendation-problem-list">{problems.map((problem) => <li key={problem.problemKey}><div className="recommendation-problem-heading"><strong>{problem.title}</strong><span>题目 ID {problem.problemId}</span></div><p>预计通过 {percentage(problem.predictedSuccessProbability)}</p><div className="recommendation-problem-sources">{problem.sourceProblemSets.map((source) => <a key={`${source.problemSetId}:${source.sourceUrl}`} href={source.sourceUrl} target="_blank" rel="noopener noreferrer">打开题目集 {source.problemSetId}</a>)}</div></li>)}</ul>;
}

export function RecommendationPanel() {
  const { session } = useSession();
  const [recommendation, setRecommendation] = useState<SelfRecommendation | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(async () => {
    setLoading(true); setError(null);
    try { setRecommendation(await loadSelfRecommendation(session)); }
    catch (loadError) { setError(apiFailureMessage(loadError)); }
    finally { setLoading(false); }
  }, [session]);
  useEffect(() => { void load(); }, [load]);

  if (loading) return <section className="panel-card recommendation-panel" role="status"><p>正在读取学习建议…</p></section>;
  if (error !== null) return <section className="panel-card recommendation-panel error-state"><h2>学习建议读取失败</h2><p>{error}</p><button className="secondary-button" type="button" onClick={() => void load()}>重试</button></section>;
  if (recommendation === null) return null;
  if (recommendation.state === "unavailable") {
    return <section className="panel-card recommendation-panel"><header className="section-heading"><div><span className="eyebrow">INFERENCE</span><h2>学习建议暂不可用</h2><p>{unavailableMessage(recommendation.unavailableReason)}</p></div></header><p className="recommendation-provenance">分析 head r{recommendation.currentAnalyticsHeadRevision} · 模型 head r{recommendation.modelHeadRevision}{recommendation.currentAnalyticsGenerationId === undefined ? "" : ` · generation ${recommendation.currentAnalyticsGenerationId}`}</p><ModelProvenance model={recommendation.model} /></section>;
  }
  return (
    <section className="panel-card recommendation-panel">
      <header className="section-heading"><div><span className="eyebrow">INFERENCE</span><h2>个性化学习建议</h2><p>基于 Rating {recommendation.result.sourceRating} 与 immutable model 实时推理。</p></div><span className="recommendation-state fresh">最新</span></header>
      <p className="recommendation-provenance">观测 {recommendation.result.evidence.observationCount} 条 · 覆盖 {recommendation.result.evidence.distinctProblemCount} 题 · 已通过 {recommendation.result.evidence.passedProblemCount} 题 · 分析 generation {recommendation.currentAnalyticsGenerationId} / head r{recommendation.currentAnalyticsHeadRevision} · 模型 head r{recommendation.modelHeadRevision}</p>
      <div className="recommendation-grid">{recommendation.result.knowledgeMastery.map((knowledge) => <article key={knowledge.knowledgePointId}><span>知识点掌握度</span><strong>{knowledge.label} · {percentage(knowledge.mastery)}</strong><p>{knowledge.description}</p><p>观测 {knowledge.observationCount} 条</p></article>)}</div>
      {recommendation.result.status === "ready" ? <div className="recommendation-grid">{recommendation.result.learningPath.map((step) => <article key={step.order}><span>步骤 {step.order} · {step.reasonCode === "prerequisite" ? "前置知识" : "知识缺口"}</span><strong>{step.label} · {percentage(step.mastery)} → {percentage(step.targetMastery)}</strong><p>{step.description}</p><RecommendedProblems problems={step.recommendedProblems} /></article>)}</div> : <p className="recommendation-warning">{insufficiencyMessage(recommendation.result.insufficiency.reasonCode)} 候选路径 {recommendation.result.insufficiency.candidatePathSteps} 步，可用题目 {recommendation.result.insufficiency.eligibleProblemCount} 题。</p>}
      <p className="recommendation-provenance">推理结果 <code>{recommendation.result.schema}</code> · SHA-256 <code>{recommendation.result.sha256}</code></p>
      <ModelProvenance model={recommendation.model} />
    </section>
  );
}
