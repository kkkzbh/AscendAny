import type { ProgressExplanation } from "@/types/metrics";

interface ProgressExplanationCardProps {
  data: ProgressExplanation | null;
}

export function ProgressExplanationCard({ data }: ProgressExplanationCardProps) {
  if (!data || !data.available) {
    return (
      <section className="metric-section growth-card rounded-xl p-3.5">
        <h3 className="growth-card-title">进步解释</h3>
        <p className="growth-card-empty">暂无可比较数据</p>
      </section>
    );
  }

  return (
    <section className="metric-section growth-card rounded-xl p-3.5">
      <div className="growth-card-head">
        <h3 className="growth-card-title">进步解释</h3>
        <span className="growth-card-meta">
          {data.latestExamName ?? "最近考试"}
          {data.latestExamDate ? ` · ${data.latestExamDate}` : ""}
        </span>
      </div>
      <p className="growth-card-summary">{data.summary || "暂无可比较数据"}</p>
      <div className="growth-columns">
        <div>
          <div className="growth-subtitle">提升信号</div>
          {data.keyImprovements.length > 0 ? (
            <ul className="growth-list">
              {data.keyImprovements.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          ) : (
            <p className="growth-card-empty">暂无可比较数据</p>
          )}
        </div>
        <div>
          <div className="growth-subtitle">待稳住项</div>
          {data.keySetbacks.length > 0 ? (
            <ul className="growth-list">
              {data.keySetbacks.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          ) : (
            <p className="growth-card-empty">暂无可比较数据</p>
          )}
        </div>
      </div>
    </section>
  );
}
