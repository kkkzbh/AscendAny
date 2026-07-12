import { useCallback, useEffect, useState } from "react";
import type { ExamDetail } from "@ascendany/sdk";
import { Link, useParams } from "react-router-dom";
import { apiFailureMessage } from "../api/client";
import { loadExam } from "../api/operations";
import { ExamAnalysisGenerationPanel } from "../components/ExamAnalysisGenerationPanel";
import { useSession } from "../session/context";

function formatLimit(timeLimitMs: number | null, memoryLimitBytes: number | null): string {
  const time = timeLimitMs === null ? "—" : `${timeLimitMs} ms`;
  const memory = memoryLimitBytes === null ? "—" : `${(memoryLimitBytes / 1024 / 1024).toFixed(0)} MiB`;
  return `${time} / ${memory}`;
}

export function ExamDetailPage() {
  const { examId } = useParams();
  const { session } = useSession();
  const [exam, setExam] = useState<ExamDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (examId === undefined) return;
    setLoading(true);
    setError(null);
    try {
      setExam(await loadExam(session, examId));
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoading(false);
    }
  }, [examId, session]);

  useEffect(() => { void load(); }, [load]);

  if (loading) return <section className="state-panel" role="status"><span className="loading-dot" /><p>正在读取考试详情…</p></section>;
  if (error !== null) return <section className="state-panel error-state"><h2>考试详情读取失败</h2><p>{error}</p><button className="secondary-button" type="button" onClick={() => void load()}>重试</button></section>;
  if (exam === null) return null;

  return (
    <div className="page-stack">
      <section className="panel-card exam-detail-hero">
        <Link className="back-link" to="/exams">← 返回考试列表</Link>
        <span className="eyebrow">PINTIA · SNAPSHOT {exam.snapshotSequence}</span>
        <h2>{exam.title}</h2>
        <p>{exam.problemSetId}</p>
        <div className="exam-stat-row">
          <span><strong>{exam.problemCount}</strong>题目</span>
          <span><strong>{exam.participantCount}</strong>参与者</span>
          <span><strong>{exam.rankingCount}</strong>排名记录</span>
          <span><strong>{exam.submissionCount}</strong>提交</span>
        </div>
        <a className="secondary-button source-link" href={exam.sourceUrl} target="_blank" rel="noreferrer">打开 Pintia 来源</a>
      </section>
      <ExamAnalysisGenerationPanel examId={exam.id} session={session} />
      <section className="panel-card">
        <header className="section-heading"><div><span className="eyebrow">PROBLEMS</span><h2>题目统计</h2><p>通过人数来自活动快照的排名数据。</p></div></header>
        <div className="table-scroll">
          <table className="problem-table">
            <thead><tr><th>题目</th><th>满分</th><th>限制</th><th>提交</th><th>尝试人数</th><th>通过人数</th></tr></thead>
            <tbody>{exam.problems.map((problem) => (
              <tr key={problem.id}>
                <td className="problem-title"><strong>{problem.label ?? problem.problemId}</strong><span>{problem.title}</span></td>
                <td>{problem.maxScore ?? "—"}</td>
                <td>{formatLimit(problem.timeLimitMs, problem.memoryLimitBytes)}</td>
                <td>{problem.submissionCount}</td>
                <td>{problem.submittingParticipantCount}</td>
                <td>{problem.passedParticipantCount}</td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
