import { useCallback, useEffect, useState } from "react";
import type {
  RecommendationInsufficiencyV2,
  RecommendationProblemV2,
  SelfRecommendation,
} from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadSelfRecommendation } from "../api/operations";
import { useSession } from "../session/context";

function percentage(value: number) { return `${Math.round(value * 100)}%`; }
function insufficiencyMessage(reason: RecommendationInsufficiencyV2["reasonCode"]) {
  switch (reason) {
    case "mastery_target_satisfied": return "当前知识点掌握度已达到训练目标。";
    case "path_below_minimum": return "可形成的学习路径短于配置要求。";
    case "path_exceeds_maximum": return "完整前置依赖路径超过配置上限。";
    case "problem_candidates_below_minimum": return "部分知识点缺少足量且适合的练习题。";
  }
}

function RecommendedProblems({ problems }: { problems: RecommendationProblemV2[] }) {
  return (
    <ul className="recommendation-problem-list">
      {problems.map((problem) => (
        <li key={problem.problemKey}>
          <div className="recommendation-problem-heading">
            <strong>{problem.title}</strong>
            <span>题目 ID {problem.problemId}</span>
          </div>
          <p>预计通过 {percentage(problem.predictedSuccessProbability)}</p>
          <div className="recommendation-problem-sources">
            {problem.sourceProblemSets.map((source) => (
              <a
                key={`${source.problemSetId}:${source.sourceUrl}`}
                href={source.sourceUrl}
                target="_blank"
                rel="noopener noreferrer"
              >
                打开题目集 {source.problemSetId}
              </a>
            ))}
          </div>
        </li>
      ))}
    </ul>
  );
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
    const reason = recommendation.unavailableReason === "no_active_model" ? "管理员尚未发布推荐模型。" : "当前推荐模型尚未覆盖你的分析记录。";
    return <section className="panel-card recommendation-panel"><header className="section-heading"><div><span className="eyebrow">LEARNING PATH</span><h2>学习建议待生成</h2><p>{reason}</p></div></header><p className="recommendation-provenance">分析 head r{recommendation.currentAnalyticsHeadRevision} · 推荐 head r{recommendation.recommendationHeadRevision}</p></section>;
  }
  return (
    <section className="panel-card recommendation-panel">
      <header className="section-heading"><div><span className="eyebrow">LEARNING PATH</span><h2>个性化学习建议</h2><p>基于 Rating {recommendation.result.sourceRating} 与已发布模型生成。</p></div><span className={`recommendation-state ${recommendation.state}`}>{recommendation.state === "fresh" ? "最新" : "待更新"}</span></header>
      {recommendation.state === "stale" ? <p className="recommendation-warning">分析数据已更新；当前结果保留完整模型来源，等待下一轮训练发布。</p> : null}
      <p className="recommendation-provenance">训练证据 {recommendation.result.evidence.trainInteractionCount} 条 · 验证证据 {recommendation.result.evidence.validationInteractionCount} 条 · 覆盖 {recommendation.result.evidence.distinctProblemCount} 题 · 已通过 {recommendation.result.evidence.passedProblemCount} 题</p>
      <div className="recommendation-grid">{recommendation.result.knowledgeMastery.map((knowledge) => <article key={knowledge.knowledgePointId}><span>知识点掌握度</span><strong>{knowledge.label} · {percentage(knowledge.mastery)}</strong><p>{knowledge.description}</p><p>训练证据 {knowledge.trainInteractionCount} 条</p></article>)}</div>
      {recommendation.result.status === "ready" ? (
        <div className="recommendation-grid">
          {recommendation.result.learningPath.map((step) => (
            <article key={step.order}>
              <span>步骤 {step.order} · {step.reasonCode === "prerequisite" ? "前置知识" : "知识缺口"}</span>
              <strong>{step.label} · {percentage(step.mastery)} → {percentage(step.targetMastery)}</strong>
              <p>{step.description}</p>
              <RecommendedProblems problems={step.recommendedProblems} />
            </article>
          ))}
        </div>
      ) : (
        <p className="recommendation-warning">{insufficiencyMessage(recommendation.result.insufficiency.reasonCode)} 候选路径 {recommendation.result.insufficiency.candidatePathSteps} 步，可用题目 {recommendation.result.insufficiency.eligibleProblemCount} 题。</p>
      )}
      <dl className="recommendation-provenance"><div><dt>模型</dt><dd>{recommendation.model.modelSchema}</dd></div><div><dt>训练配置</dt><dd>{recommendation.model.trainingConfigurationKey} v{recommendation.model.trainingConfigurationVersion}</dd></div><div><dt>知识目录</dt><dd>{recommendation.model.knowledgeCatalogKey} v{recommendation.model.knowledgeCatalogVersion}</dd></div><div><dt>分析来源</dt><dd>generation {recommendation.model.analyticsGenerationId} · head r{recommendation.model.analyticsHeadRevision}</dd></div></dl>
    </section>
  );
}
