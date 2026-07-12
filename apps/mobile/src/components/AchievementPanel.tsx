import { useCallback, useEffect, useState } from "react";
import type { AchievementItem, SelfAchievements } from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import { loadSelfAchievements } from "../api/operations";
import { useSession } from "../session/SessionContext";

const stateCopy: Record<SelfAchievements["state"], { label: string; detail: string }> = {
  not_generated: { label: "等待首轮分析", detail: "分析尚未发布；以下是当前生效的完整规则与服务端计算进度。" },
  no_observations: { label: "暂无考试观测", detail: "当前分析没有你的考试观测；以下仍展示完整规则与服务端计算进度。" },
  ready: { label: "进度已更新", detail: "进度来自当前分析版本、当前规则集与成功对话记录。" },
};

function tierLabel(tier: number): string {
  switch (tier) {
    case 0: return "未解锁";
    case 1: return "青铜";
    case 2: return "白银";
    case 3: return "黄金";
    default: throw new TypeError(`Unsupported achievement tier: ${tier}`);
  }
}

function nextTarget(item: AchievementItem): number {
  switch (item.tier) {
    case 0: return item.bronzeTarget;
    case 1: return item.silverTarget;
    case 2:
    case 3: return item.goldTarget;
    default: throw new TypeError(`Unsupported achievement tier: ${item.tier}`);
  }
}

function progressWidth(item: AchievementItem): string {
  return `${Math.min(100, (item.progress / nextTarget(item)) * 100)}%`;
}

export function AchievementPanel() {
  const { session } = useSession();
  const [achievements, setAchievements] = useState<SelfAchievements | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const load = useCallback(async () => {
    setLoading(true); setError(null);
    try { setAchievements(await loadSelfAchievements(session)); }
    catch (loadError) { setError(apiFailureMessage(loadError)); }
    finally { setLoading(false); }
  }, [session]);
  useEffect(() => { void load(); }, [load]);

  if (loading) return <section className="panel-card achievement-panel" role="status"><p>正在读取成就进度…</p></section>;
  if (error !== null) return <section className="panel-card achievement-panel error-state"><h2>成就进度读取失败</h2><p>{error}</p><button className="secondary-button" type="button" onClick={() => void load()}>重试</button></section>;
  if (achievements === null) throw new Error("Achievement request completed without data.");

  const state = stateCopy[achievements.state];
  return (
    <section className="panel-card achievement-panel" aria-labelledby="achievement-panel-title">
      <header className="section-heading"><div><span className="eyebrow">ACHIEVEMENTS</span><h2 id="achievement-panel-title">成就进度</h2><p>{state.detail}</p></div><span className={`achievement-state ${achievements.state}`}>{state.label}</span></header>
      <div className="achievement-summary" aria-label="成就汇总"><article><span>规则</span><strong>{achievements.summary.total}</strong></article><article><span>未解锁</span><strong>{achievements.summary.locked}</strong></article><article><span>青铜</span><strong>{achievements.summary.bronze}</strong></article><article><span>白银</span><strong>{achievements.summary.silver}</strong></article><article><span>黄金</span><strong>{achievements.summary.gold}</strong></article></div>
      <div className="achievement-list">
        {achievements.items.map((item) => <article className={`achievement-card tier-${item.tier}`} key={item.code}><header><div><span>{item.code}</span><h3>{item.title}</h3></div><strong>{tierLabel(item.tier)}</strong></header><p>{item.description}</p><div className="achievement-progress-copy"><span>当前进度</span><strong>{item.progress}</strong></div><div className="achievement-progress-track" aria-hidden="true"><span style={{ width: progressWidth(item) }} /></div><dl className="achievement-targets"><div><dt>青铜</dt><dd>{item.bronzeTarget}</dd></div><div><dt>白银</dt><dd>{item.silverTarget}</dd></div><div><dt>黄金</dt><dd>{item.goldTarget}</dd></div></dl></article>)}
      </div>
      <dl className="achievement-provenance" aria-label="成就计算来源"><div><dt>分析 head</dt><dd>r{achievements.analyticsHeadRevision}</dd></div><div><dt>规则集</dt><dd>v{achievements.ruleSetVersion}</dd></div><div><dt>规则 head</dt><dd>r{achievements.ruleHeadRevision}</dd></div></dl>
    </section>
  );
}
