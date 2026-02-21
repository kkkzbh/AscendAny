import type { MilestoneStreak } from "@/types/metrics";

interface MilestoneStreakCardProps {
  data: MilestoneStreak | null;
}

export function MilestoneStreakCard({ data }: MilestoneStreakCardProps) {
  if (!data || !data.available) {
    return (
      <section className="metric-section growth-card rounded-xl p-3.5">
        <h3 className="growth-card-title">里程碑与连胜</h3>
        <p className="growth-card-empty">暂无可比较数据</p>
      </section>
    );
  }

  const milestoneItems =
    data.newMilestones.length > 0 ? data.newMilestones : data.recentMilestones;

  return (
    <section className="metric-section growth-card rounded-xl p-3.5">
      <h3 className="growth-card-title">里程碑与连胜</h3>
      <div className="streak-grid">
        <div>
          <div className="growth-subtitle">当前连胜</div>
          <div className="streak-value">{data.currentPositiveStreak}</div>
        </div>
        <div>
          <div className="growth-subtitle">历史最佳</div>
          <div className="streak-value">{data.bestPositiveStreak}</div>
        </div>
      </div>
      {milestoneItems.length > 0 ? (
        <ul className="growth-list mt-2">
          {milestoneItems.slice(0, 3).map((item) => (
            <li key={`${item.code}-${item.examId ?? "na"}`}>{item.label}</li>
          ))}
        </ul>
      ) : (
        <p className="growth-card-empty mt-2">暂无可比较数据</p>
      )}
      {data.nextTargets.length > 0 && (
        <div className="growth-targets">
          {data.nextTargets.map((item) => (
            <span key={item} className="growth-target-chip">
              {item}
            </span>
          ))}
        </div>
      )}
    </section>
  );
}
