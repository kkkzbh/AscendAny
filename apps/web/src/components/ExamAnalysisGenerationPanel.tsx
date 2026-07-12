import type { BrowserSession, ExamAnalysisGeneration } from "@ascendany/sdk";
import { useExamAnalysisGeneration } from "./useExamAnalysisGeneration";

const statusLabel: Record<ExamAnalysisGeneration["status"], string> = {
  queued: "等待处理",
  running: "正在生成",
  succeeded: "生成成功",
  superseded: "已被新版本取代",
  failed: "生成失败",
};

export function ExamAnalysisGenerationPanel({
  examId,
  session,
}: {
  examId: string;
  session: BrowserSession;
}) {
  const state = useExamAnalysisGeneration(session, examId);

  if (state.loading && state.generation === null) {
    return <section className="panel-card exam-generation-panel" role="status"><span className="loading-dot" /><p>正在读取当前分析生成…</p></section>;
  }
  if (state.generation === null) {
    return (
      <section className="panel-card exam-generation-panel error-state">
        <h2>分析生成状态读取失败</h2>
        <p role="alert">{state.error}</p>
        <button className="secondary-button" type="button" onClick={state.retry}>重试</button>
      </section>
    );
  }

  const { generation } = state;
  const lastSequence = state.events.at(-1)?.sequence ?? 0;
  return (
    <section className="panel-card exam-generation-panel">
      <header className="section-heading">
        <div>
          <span className="eyebrow">ANALYSIS GENERATION</span>
          <h2>考试分析生成</h2>
          <p>当前 generation #{generation.generationId}，事件 head 为 {generation.eventHead}。</p>
        </div>
        <button className="text-button" type="button" onClick={state.retry}>刷新</button>
      </header>
      <div className="exam-generation-summary">
        <span className={`exam-generation-status status-${generation.status}`}>{statusLabel[generation.status]}</span>
        <dl>
          <div><dt>尝试次数</dt><dd>{generation.attemptCount}</dd></div>
          <div><dt>创建时间</dt><dd>{formatTimestamp(generation.createdAt)}</dd></div>
          <div><dt>开始时间</dt><dd>{generation.startedAt === undefined ? "—" : formatTimestamp(generation.startedAt)}</dd></div>
          <div><dt>完成时间</dt><dd>{generation.finishedAt === undefined ? "—" : formatTimestamp(generation.finishedAt)}</dd></div>
        </dl>
      </div>
      <p className="exam-generation-connection" data-state={state.connectionState ?? "idle"}>
        {connectionLabel(state.connectionState, lastSequence, state.error !== null)}
      </p>
      {generation.errorCode === undefined ? null : <p className="inline-error">错误代码：{generation.errorCode}</p>}
      {state.error === null ? null : (
        <div className="exam-generation-stream-error" role="alert">
          <p>{state.error}</p>
          <small>已保留至事件 {lastSequence}，重试会从该 Last-Event-ID 续传。</small>
          <button className="secondary-button" type="button" onClick={state.retry}>从事件 {lastSequence} 重试</button>
        </div>
      )}
      {state.events.length === 0 ? <p className="empty-copy">本次打开后尚未收到新事件。</p> : (
        <ol className="exam-generation-events" aria-label="分析生成事件">
          {state.events.map((event) => (
            <li key={event.sequence}>
              <span>#{event.sequence}</span>
              <strong>{statusLabel[event.type]}</strong>
              <time dateTime={event.createdAt}>{formatTimestamp(event.createdAt)}</time>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(new Date(value));
}

function connectionLabel(
  state: ReturnType<typeof useExamAnalysisGeneration>["connectionState"],
  lastSequence: number,
  failed: boolean,
): string {
  if (failed) return `事件流已中断于 ${lastSequence}`;
  switch (state) {
    case "connecting": return `正在从事件 ${lastSequence} 连接…`;
    case "live": return `事件流已连接，当前游标 ${lastSequence}`;
    case "reconnecting": return `连接周期结束，正在从事件 ${lastSequence} 续传…`;
    case "closed": return `生成已结束，事件流关闭于 ${lastSequence}`;
    case null: return "正在准备事件流…";
  }
}
