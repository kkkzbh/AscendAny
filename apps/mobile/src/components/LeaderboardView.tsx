import { useCallback, useEffect, useState } from "react";
import type { StudentLeaderboard } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadStudentLeaderboard } from "../api/operations";
import { useSession } from "../session/SessionContext";

export function LeaderboardView() {
  const { session, account } = useSession();
  const [leaderboard, setLeaderboard] = useState<StudentLeaderboard | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setLeaderboard(await loadStudentLeaderboard(session));
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoading(false);
    }
  }, [session]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) return <section className="state-panel" role="status"><span className="loading-dot" /><p>正在读取排行榜…</p></section>;
  if (error !== null) return <section className="state-panel error-state"><h2>排行榜读取失败</h2><p>{error}</p><button className="secondary-button" type="button" onClick={() => void load()}>重试</button></section>;
  if (leaderboard === null) return null;
  if (leaderboard.state === "not_generated") return <section className="state-panel"><div className="state-symbol">◇</div><h2>排行尚未发布</h2><p>首轮分析发布后将生成排行榜。</p></section>;
  if (leaderboard.state === "no_observations") return <section className="state-panel"><div className="state-symbol">◇</div><h2>暂无排行观测</h2><p>当前发布版本中没有可参与排名的学生。</p></section>;

  const currentStudentNumber = account?.role === "student" ? account.studentNumber : null;

  return (
    <section className="panel-card leaderboard-panel">
      <div className="section-heading">
        <div><span className="eyebrow">LEADERBOARD</span><h2>能力排行</h2><p>{leaderboard.population} 名学生 · 发布版本 {leaderboard.headRevision}</p></div>
        <button className="text-button" type="button" onClick={() => void load()}>刷新</button>
      </div>
      <div className="table-scroll">
        <table className="leaderboard-table">
          <thead><tr><th>排名</th><th>学生</th><th>Rating</th><th>知识</th><th>准确</th></tr></thead>
          <tbody>
            {leaderboard.items.map((item) => (
              <tr className={item.studentNumber === currentStudentNumber ? "current-row" : ""} key={item.studentNumber}>
                <td><span className="rank-chip">{item.rank}</span></td>
                <td><strong>{item.displayName ?? "未设置姓名"}</strong><span>{item.studentNumber}</span></td>
                <td className="rating-cell">{item.rating}</td>
                <td>{item.metrics.knowledge?.toFixed(1) ?? "—"}</td>
                <td>{item.metrics.accuracy?.toFixed(1) ?? "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
