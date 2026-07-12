import { useCallback, useEffect, useRef, useState } from "react";
import type {
  OjJudgeEvent,
  OjProblem,
  OjProblemVersionMetadata,
  OjSubmissionDetail,
  OjSubmissionMode,
} from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import {
  loadOjProblem,
  loadOjProblems,
  loadOjSubmission,
  openOjJudgeEventStream,
  publishOjProblemVersion,
  submitOjSource,
} from "../api/operations";
import { useSession } from "../session/SessionContext";
import { OjLspStatus } from "./OjLspStatus";

const starterSource = `#include <iostream>

int main() {
    std::ios::sync_with_stdio(false);
    std::cin.tie(nullptr);

    return 0;
}
`;

const terminalEventTypes = new Set(["completed", "system_error"]);

export function OjView() {
  const { account, session } = useSession();
  const [problems, setProblems] = useState<OjProblem[]>([]);
  const [selected, setSelected] = useState<OjProblem | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [source, setSource] = useState(starterSource);
  const [stdin, setStdin] = useState("\n");
  const [mode, setMode] = useState<OjSubmissionMode>("run");
  const [submitting, setSubmitting] = useState(false);
  const [submission, setSubmission] = useState<OjSubmissionDetail | null>(null);
  const [events, setEvents] = useState<OjJudgeEvent[]>([]);
  const [streamActive, setStreamActive] = useState(false);
  const streamAbort = useRef<AbortController | null>(null);

  const refreshProblems = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const page = await loadOjProblems(session, 100, undefined, account?.role === "admin");
      setProblems(page.items);
      setSelected((current) => page.items.find((problem) => problem.id === current?.id)
        ?? page.items[0]
        ?? null);
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoading(false);
    }
  }, [account?.role, session]);

  useEffect(() => {
    void refreshProblems();
    return () => streamAbort.current?.abort();
  }, [refreshProblems]);

  const selectProblem = async (problem: OjProblem) => {
    streamAbort.current?.abort();
    setError(null);
    setSubmission(null);
    setEvents([]);
    try {
      setSelected(await loadOjProblem(session, problem.id));
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    }
  };

  const refreshSubmission = useCallback(async (submissionId: string) => {
    const detail = await loadOjSubmission(session, submissionId);
    setSubmission(detail);
    return detail;
  }, [session]);

  const watchSubmission = useCallback(async (submissionId: string, afterSequence: number) => {
    streamAbort.current?.abort();
    const abort = new AbortController();
    streamAbort.current = abort;
    setStreamActive(true);
    setError(null);
    try {
      const eventStream = await openOjJudgeEventStream(
        session,
        submissionId,
        afterSequence,
        abort.signal,
      );
      let terminal = false;
      for await (const event of eventStream.stream) {
        if (abort.signal.aborted) return;
        setEvents((current) => current.some((item) => item.sequence === event.sequence)
          ? current
          : [...current, event]);
        if (terminalEventTypes.has(event.type)) terminal = true;
      }
      if (abort.signal.aborted) return;
      await refreshSubmission(submissionId);
      const streamFailure = eventStream.failure();
      if (!terminal && streamFailure !== undefined) {
        setError(apiFailureMessage(streamFailure));
      }
    } catch (streamError) {
      if (!abort.signal.aborted) setError(apiFailureMessage(streamError));
    } finally {
      if (streamAbort.current === abort) {
        streamAbort.current = null;
        setStreamActive(false);
      }
    }
  }, [refreshSubmission, session]);

  const submit = async () => {
    if (selected === null || submitting) return;
    if (source.trim().length === 0) {
      setError("请输入 C++20 源码。");
      return;
    }
    setSubmitting(true);
    setError(null);
    setEvents([]);
    setSubmission(null);
    try {
      const result = await submitOjSource(session, selected, mode, source, stdin);
      const detail = await refreshSubmission(result.submission.id);
      if (!isTerminal(detail)) await watchSubmission(detail.id, 0);
    } catch (submitError) {
      setError(apiFailureMessage(submitError));
    } finally {
      setSubmitting(false);
    }
  };

  const manualRefresh = async () => {
    if (submission === null) return;
    setError(null);
    try {
      await refreshSubmission(submission.id);
    } catch (refreshError) {
      setError(apiFailureMessage(refreshError));
    }
  };

  if (loading && problems.length === 0) {
    return <section className="state-panel" role="status"><span className="loading-dot" /><p>正在读取 OJ 题库…</p></section>;
  }

  return (
    <div className="oj-page">
      <section className="panel-card oj-problem-panel">
        <header className="section-heading">
          <div><span className="eyebrow">ONLINE JUDGE</span><h2>题库</h2><p>选择不可变题目版本并提交 C++20 程序。</p></div>
          <button className="text-button" type="button" onClick={() => void refreshProblems()}>刷新</button>
        </header>
        {problems.length === 0 ? <p className="empty-copy">暂无可用题目。</p> : (
          <div className="oj-problem-list" role="listbox" aria-label="OJ 题目">
            {problems.map((problem) => (
              <button
                aria-selected={selected?.id === problem.id}
                className={selected?.id === problem.id ? "active" : ""}
                key={problem.id}
                onClick={() => void selectProblem(problem)}
                role="option"
                type="button"
              >
                <span>{problem.slug}</span>
                <strong>{problem.currentVersion.title}</strong>
                <small>v{problem.currentVersion.number} · {problem.currentVersion.lifecycle === "active" ? "开放" : "归档"}</small>
              </button>
            ))}
          </div>
        )}
      </section>

      {selected === null ? null : (
        <>
          <ProblemStatement problem={selected} />
          <section className="panel-card oj-editor-panel">
            <header className="section-heading compact-heading">
              <div><span className="eyebrow">C++20</span><h2>代码编辑器</h2><p>运行使用自定义标准输入；提交使用题目测试集。</p></div>
              <div className="oj-mode-toggle" aria-label="执行模式">
                <button className={mode === "run" ? "active" : ""} onClick={() => setMode("run")} type="button">运行</button>
                <button className={mode === "submit" ? "active" : ""} onClick={() => setMode("submit")} type="button">提交</button>
              </div>
            </header>
            <label className="oj-code-field">
              <span>main.cpp</span>
              <textarea aria-label="C++20 源码" onChange={(event) => setSource(event.target.value)} spellCheck={false} value={source} />
            </label>
            <OjLspStatus session={session} source={source} />
            {mode === "run" ? (
              <label className="oj-stdin-field"><span>标准输入</span><textarea aria-label="标准输入" onChange={(event) => setStdin(event.target.value)} spellCheck={false} value={stdin} /></label>
            ) : null}
            <div className="oj-submit-row">
              <span>题目 head revision {selected.headRevision}</span>
              <button className="primary-button" disabled={submitting || streamActive} onClick={() => void submit()} type="button">
                {submitting ? "正在入队…" : mode === "run" ? "运行代码" : "提交评测"}
              </button>
            </div>
          </section>
          {submission === null ? null : (
            <SubmissionResult
              events={events}
              onRefresh={() => void manualRefresh()}
              onResume={() => void watchSubmission(
                submission.id,
                events.length === 0 ? 0 : events[events.length - 1]?.sequence ?? 0,
              )}
              streamActive={streamActive}
              submission={submission}
            />
          )}
        </>
      )}

      {account?.role === "admin" ? <AdminProblemEditor current={selected} onPublished={refreshProblems} /> : null}
      {error === null ? null : <p className="global-error oj-global-error" role="alert">{error}</p>}
    </div>
  );
}

function ProblemStatement({ problem }: { problem: OjProblem }) {
  const version = problem.currentVersion;
  return (
    <section className="panel-card oj-statement-panel">
      <header className="section-heading compact-heading">
        <div><span className="eyebrow">{problem.slug}</span><h2>{version.title}</h2></div>
        <dl className="oj-limits">
          <div><dt>时间</dt><dd>{version.timeLimitMs} ms</dd></div>
          <div><dt>内存</dt><dd>{formatBytes(version.memoryLimitBytes)}</dd></div>
          <div><dt>输出</dt><dd>{formatBytes(version.outputLimitBytes)}</dd></div>
        </dl>
      </header>
      <pre className="oj-statement">{version.statementMarkdown}</pre>
      <div className="oj-tags">{version.knowledgeTags.map((tag) => <span key={tag}>{tag}</span>)}</div>
    </section>
  );
}

function SubmissionResult({ events, onRefresh, onResume, streamActive, submission }: {
  events: OjJudgeEvent[];
  onRefresh: () => void;
  onResume: () => void;
  streamActive: boolean;
  submission: OjSubmissionDetail;
}) {
  const terminal = isTerminal(submission);
  return (
    <section className="panel-card oj-result-panel" aria-live="polite">
      <header className="section-heading compact-heading">
        <div><span className="eyebrow">JUDGE</span><h2>{verdictLabel(submission)}</h2><p>尝试次数 {submission.attemptCount}</p></div>
        <div className="oj-result-actions">
          {!terminal && !streamActive ? <button className="text-button" onClick={onResume} type="button">续接事件流</button> : null}
          <button className="text-button" onClick={onRefresh} type="button">刷新结果</button>
        </div>
      </header>
      {submission.result === undefined ? null : (
        <dl className="oj-result-metrics">
          <div><dt>得分</dt><dd>{Math.round(submission.result.scoreFraction * 100)}%</dd></div>
          <div><dt>测试点</dt><dd>{submission.result.passedCaseCount}/{submission.result.totalCaseCount}</dd></div>
          <div><dt>最大时间</dt><dd>{submission.result.maxTimeMs} ms</dd></div>
          <div><dt>最大内存</dt><dd>{formatBytes(submission.result.maxMemoryBytes)}</dd></div>
        </dl>
      )}
      {submission.failureCode === undefined ? null : <p className="form-error">系统错误：{submission.failureCode}</p>}
      <ol className="oj-event-list">
        {events.map((event) => <li key={event.sequence}><span>{event.sequence}</span><strong>{event.type}</strong><time>{new Date(event.createdAt).toLocaleTimeString("zh-CN")}</time></li>)}
      </ol>
    </section>
  );
}

function AdminProblemEditor({ current, onPublished }: { current: OjProblem | null; onPublished: () => Promise<void> }) {
  const { session } = useSession();
  const [form, setForm] = useState(() => problemForm(current));
  const [bundle, setBundle] = useState<File | null>(null);
  const [publishing, setPublishing] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setForm(problemForm(current));
    setBundle(null);
    setStatus(null);
    setError(null);
  }, [current]);

  const publish = async () => {
    if (bundle === null || publishing) {
      if (bundle === null) setError("请选择测试包 TAR 文件。");
      return;
    }
    setPublishing(true);
    setError(null);
    setStatus(null);
    try {
      const metadata = problemMetadata(form);
      const result = await publishOjProblemVersion(session, metadata, bundle);
      setStatus(result.idempotent ? "内容与当前版本相同，已完成幂等重放。" : `已发布 ${result.problem.slug} v${result.problem.currentVersion.number}。`);
      await onPublished();
    } catch (publishError) {
      setError(apiFailureMessage(publishError));
    } finally {
      setPublishing(false);
    }
  };

  return (
    <section className="panel-card oj-admin-panel">
      <header className="section-heading"><div><span className="eyebrow">ADMIN</span><h2>发布不可变题目版本</h2><p>归档通过发布 lifecycle=archived 的新版本完成。</p></div></header>
      <div className="oj-admin-grid">
        <TextField label="Slug" value={form.slug} onChange={(slug) => setForm({ ...form, slug })} />
        <TextField label="Expected head revision" type="number" value={form.expectedHeadRevision} onChange={(expectedHeadRevision) => setForm({ ...form, expectedHeadRevision })} />
        <label><span>Lifecycle</span><select value={form.lifecycle} onChange={(event) => setForm({ ...form, lifecycle: event.target.value as "active" | "archived" })}><option value="active">active</option><option value="archived">archived</option></select></label>
        <TextField label="标题" value={form.title} onChange={(title) => setForm({ ...form, title })} />
        <TextField label="知识标签（逗号分隔）" value={form.tags} onChange={(tags) => setForm({ ...form, tags })} />
        <TextField label="时间限制 ms" type="number" value={form.timeLimitMs} onChange={(timeLimitMs) => setForm({ ...form, timeLimitMs })} />
        <TextField label="内存限制 bytes" type="number" value={form.memoryLimitBytes} onChange={(memoryLimitBytes) => setForm({ ...form, memoryLimitBytes })} />
        <TextField label="输出限制 bytes" type="number" value={form.outputLimitBytes} onChange={(outputLimitBytes) => setForm({ ...form, outputLimitBytes })} />
        <label className="oj-admin-wide"><span>题面 Markdown</span><textarea value={form.statement} onChange={(event) => setForm({ ...form, statement: event.target.value })} /></label>
        <label className="oj-admin-wide"><span>题解 Markdown</span><textarea value={form.solution} onChange={(event) => setForm({ ...form, solution: event.target.value })} /></label>
        <label className="oj-admin-wide"><span>题目规格 JSON</span><textarea className="oj-code-input" value={form.problemSpec} onChange={(event) => setForm({ ...form, problemSpec: event.target.value })} /></label>
        <label className="oj-admin-wide"><span>测试包 TAR</span><input aria-label="测试包 TAR" accept=".tar,application/x-tar" onChange={(event) => setBundle(event.target.files?.[0] ?? null)} type="file" /></label>
      </div>
      {error === null ? null : <p className="form-error" role="alert">{error}</p>}
      {status === null ? null : <p className="form-success" role="status">{status}</p>}
      <button className="primary-button" disabled={publishing} onClick={() => void publish()} type="button">{publishing ? "正在发布…" : "发布版本"}</button>
    </section>
  );
}

interface ProblemForm {
  slug: string;
  expectedHeadRevision: string;
  lifecycle: "active" | "archived";
  title: string;
  statement: string;
  solution: string;
  tags: string;
  timeLimitMs: string;
  memoryLimitBytes: string;
  outputLimitBytes: string;
  problemSpec: string;
}

function problemForm(problem: OjProblem | null): ProblemForm {
  const version = problem?.currentVersion;
  return {
    slug: problem?.slug ?? "",
    expectedHeadRevision: String(problem?.headRevision ?? 0),
    lifecycle: version?.lifecycle ?? "active",
    title: version?.title ?? "",
    statement: version?.statementMarkdown ?? "",
    solution: version?.solutionMarkdown ?? "",
    tags: version?.knowledgeTags.join(", ") ?? "",
    timeLimitMs: String(version?.timeLimitMs ?? 1000),
    memoryLimitBytes: String(version?.memoryLimitBytes ?? 268435456),
    outputLimitBytes: String(version?.outputLimitBytes ?? 1048576),
    problemSpec: JSON.stringify(version?.problemSpec ?? {
      checker: "tokens",
      schema: "ascendany.oj.problem-spec.v1",
    }, null, 2),
  };
}

function problemMetadata(form: ProblemForm): OjProblemVersionMetadata {
  const specification = JSON.parse(form.problemSpec) as unknown;
  if (typeof specification !== "object" || specification === null || Array.isArray(specification)) {
    throw new Error("题目规格必须是 JSON object。");
  }
  const expectedHeadRevision = requiredInteger(form.expectedHeadRevision, 0, "Expected head revision");
  const timeLimitMs = requiredInteger(form.timeLimitMs, 1, "时间限制");
  const memoryLimitBytes = requiredInteger(form.memoryLimitBytes, 1, "内存限制");
  const outputLimitBytes = requiredInteger(form.outputLimitBytes, 1, "输出限制");
  const slug = form.slug.trim();
  const title = form.title.trim();
  if (!/^[a-z0-9][a-z0-9_-]{0,127}$/.test(slug)) throw new Error("Slug 格式无效。");
  if (title.length === 0) throw new Error("标题不能为空。");
  return {
    slug,
    expectedHeadRevision,
    lifecycle: form.lifecycle,
    title,
    statementMarkdown: form.statement,
    solutionMarkdown: form.solution.trim().length === 0 ? null : form.solution,
    knowledgeTags: [...new Set(form.tags.split(",").map((item) => item.trim()).filter(Boolean))].sort(),
    timeLimitMs,
    memoryLimitBytes,
    outputLimitBytes,
    problemSpec: specification as Record<string, unknown>,
  };
}

function requiredInteger(value: string, minimum: number, label: string): number {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < minimum) throw new Error(`${label} 必须是有效整数。`);
  return parsed;
}

function TextField({ label, onChange, type = "text", value }: {
  label: string;
  onChange: (value: string) => void;
  type?: "text" | "number";
  value: string;
}) {
  return <label><span>{label}</span><input onChange={(event) => onChange(event.target.value)} type={type} value={value} /></label>;
}

function isTerminal(submission: OjSubmissionDetail): boolean {
  return submission.status === "completed" || submission.status === "system_error";
}

function verdictLabel(submission: OjSubmissionDetail): string {
  if (submission.result !== undefined) {
    return {
      accepted: "Accepted",
      wrong_answer: "Wrong Answer",
      compile_error: "Compile Error",
      runtime_error: "Runtime Error",
      time_limit_exceeded: "Time Limit Exceeded",
      memory_limit_exceeded: "Memory Limit Exceeded",
      output_limit_exceeded: "Output Limit Exceeded",
    }[submission.result.verdict];
  }
  return {
    queued: "排队中",
    running: "评测中",
    completed: "已完成",
    system_error: "系统错误",
  }[submission.status];
}

function formatBytes(value: number): string {
  if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(0)} MiB`;
  if (value >= 1024) return `${(value / 1024).toFixed(0)} KiB`;
  return `${value} B`;
}
