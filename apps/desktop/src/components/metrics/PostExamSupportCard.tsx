import type { PostExamSupport } from "@/types/metrics";

interface PostExamSupportCardProps {
  data: PostExamSupport | null;
}

const MODE_LABEL: Record<PostExamSupport["mode"], string> = {
  recovery: "恢复模式",
  steady: "稳态模式",
  reinforce: "强化模式",
};

export function PostExamSupportCard({ data }: PostExamSupportCardProps) {
  if (!data || !data.available) {
    return (
      <section className="metric-section growth-card rounded-xl p-3.5">
        <h3 className="growth-card-title">心理支持</h3>
        <p className="growth-card-empty">暂无可比较数据</p>
      </section>
    );
  }

  return (
    <section className="metric-section growth-card rounded-xl p-3.5">
      <div className="growth-card-head">
        <h3 className="growth-card-title">心理支持</h3>
        <span className="growth-mode-chip">{MODE_LABEL[data.mode]}</span>
      </div>
      <p className="growth-card-summary">{data.headline}</p>
      <p className="growth-card-copy">{data.message}</p>
      {data.actionPlan.length > 0 && (
        <ol className="growth-list growth-list-numbered">
          {data.actionPlan.slice(0, 3).map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ol>
      )}
      {data.checkInQuestion && (
        <p className="growth-checkin">追问：{data.checkInQuestion}</p>
      )}
    </section>
  );
}
