import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  AgentRun,
  AgentRunEnqueueResult,
  ChatMessage,
  ChatThread,
} from "@ascendany/sdk";
import { apiFailureMessage } from "../api/client";
import {
  createStudentChatThread,
  enqueueAutomaticAnalysis,
  enqueueChatReply,
  loadAgentRun,
  loadChatMessages,
  loadChatThreads,
  loadSelfAnalytics,
  openAgentRunEventStream,
} from "../api/operations";
import { useSession } from "../session/SessionContext";

const pageSize = 200;
const configurationKey = /^[a-z][a-z0-9_.-]{0,127}$/;
const terminalStatuses = new Set<AgentRun["status"]>([
  "succeeded",
  "failed",
  "superseded",
]);

type AutomaticAnalysisPhase =
  | "checking"
  | "not_needed"
  | "queued"
  | "running"
  | "complete"
  | "error";

interface AutomaticAnalysisState {
  phase: AutomaticAnalysisPhase;
  message: string;
}

function mergeMessages(current: ChatMessage[], incoming: ChatMessage[]): ChatMessage[] {
  const merged = new Map(current.map((message) => [message.id, message]));
  for (const message of incoming) merged.set(message.id, message);
  return [...merged.values()].sort((left, right) => left.sequence - right.sequence);
}

function time(value: string): string {
  return new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function validateAutomaticAnalysisResult(result: AgentRunEnqueueResult): void {
  if (
    result.run.kind !== "auto_analysis"
    || result.message.kind !== "auto_analysis_request"
    || result.run.threadId !== result.message.threadId
    || result.run.inputMessageId !== result.message.id
  ) {
    throw new Error("自动分析接口返回了不一致的专用运行契约。");
  }
}

export function ChatView() {
  const { session } = useSession();
  const [threads, setThreads] = useState<ChatThread[]>([]);
  const [selectedThreadId, setSelectedThreadId] = useState<string | null>(null);
  const [nextThreadCursor, setNextThreadCursor] = useState<string | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [lastMessageSequence, setLastMessageSequence] = useState(0);
  const [moreMessages, setMoreMessages] = useState(false);
  const [draft, setDraft] = useState("");
  const [loadingThreads, setLoadingThreads] = useState(true);
  const [loadingMessages, setLoadingMessages] = useState(false);
  const [creating, setCreating] = useState(false);
  const [activeRun, setActiveRun] = useState<AgentRun | null>(null);
  const [eventSequence, setEventSequence] = useState(0);
  const [inputSequence, setInputSequence] = useState(0);
  const [eventType, setEventType] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [automaticAnalysisAttempt, setAutomaticAnalysisAttempt] = useState(0);
  const [automaticAnalysisResult, setAutomaticAnalysisResult] =
    useState<AgentRunEnqueueResult | null>(null);
  const [automaticAnalysisState, setAutomaticAnalysisState] =
    useState<AutomaticAnalysisState>({
      phase: "checking",
      message: "正在检查最新能力画像…",
    });
  const [loadedMessageKey, setLoadedMessageKey] = useState<string | null>(null);
  const streamAbort = useRef<AbortController | null>(null);
  const watchedAutomaticRun = useRef<string | null>(null);

  const promptKey = import.meta.env.VITE_CHAT_PROMPT_CONFIGURATION_KEY;
  const modelKey = import.meta.env.VITE_CHAT_MODEL_CONFIGURATION_KEY;
  const configurationReady = configurationKey.test(promptKey ?? "")
    && configurationKey.test(modelKey ?? "");
  const selectedThread = useMemo(
    () => threads.find((thread) => thread.id === selectedThreadId) ?? null,
    [selectedThreadId, threads],
  );
  const selectedAutomaticResult = useMemo(
    () => automaticAnalysisResult?.run.threadId === selectedThreadId
      ? automaticAnalysisResult
      : null,
    [automaticAnalysisResult, selectedThreadId],
  );
  const selectedMessageKey = selectedThreadId === null
    ? null
    : `${selectedThreadId}:${selectedAutomaticResult?.run.id ?? "conversation"}`;

  useEffect(() => () => streamAbort.current?.abort(), []);

  useEffect(() => {
    streamAbort.current?.abort();
    setActiveRun(null);
    setEventSequence(0);
    setInputSequence(0);
    setEventType(null);
    if (selectedThreadId === null || selectedMessageKey === null) {
      setMessages([]);
      setLastMessageSequence(0);
      setMoreMessages(false);
      setLoadedMessageKey(null);
      return;
    }
    let active = true;
    setLoadingMessages(true);
    setError(null);
    void (async () => {
      try {
        let page = await loadChatMessages(session, selectedThreadId, 0, pageSize);
        let initialMessages = [...page.items];
        if (selectedAutomaticResult !== null) {
          const previousSequence = selectedAutomaticResult.message.sequence - 1;
          while (page.lastSequence < previousSequence && page.items.length === pageSize) {
            page = await loadChatMessages(session, selectedThreadId, page.lastSequence, pageSize);
            initialMessages = mergeMessages(initialMessages, page.items);
          }
          if (page.lastSequence < previousSequence) {
            throw new Error("自动分析专用对话的消息序列不完整。");
          }
          initialMessages = mergeMessages(initialMessages, [selectedAutomaticResult.message]);
        }
        if (!active) return;
        setMessages(initialMessages);
        setLastMessageSequence(Math.max(
          page.lastSequence,
          selectedAutomaticResult?.message.sequence ?? 0,
        ));
        setMoreMessages(page.items.length === pageSize);
        setLoadedMessageKey(selectedMessageKey);
      } catch (loadError) {
        if (active) setError(apiFailureMessage(loadError));
      } finally {
        if (active) setLoadingMessages(false);
      }
    })();
    return () => {
      active = false;
    };
  }, [selectedAutomaticResult, selectedMessageKey, selectedThreadId, session]);

  const finishRun = useCallback(async (run: AgentRun, afterInputSequence: number) => {
    const current = await loadAgentRun(session, run.id);
    setActiveRun(current);
    if (!terminalStatuses.has(current.status)) {
      throw new Error("Agent 运行仍在继续，请继续接收事件。");
    }
    if (current.threadId === selectedThreadId) {
      const page = await loadChatMessages(session, current.threadId, afterInputSequence, pageSize);
      setMessages((existing) => mergeMessages(existing, page.items));
      setLastMessageSequence((existing) => Math.max(existing, page.lastSequence));
      setMoreMessages(page.items.length === pageSize);
    }
    if (current.status === "failed") {
      throw new Error(`Agent 运行失败：${current.errorCode ?? "unknown_failure"}`);
    }
    if (current.status === "superseded") {
      throw new Error("Agent 运行已被更新请求取代。");
    }
    if (current.kind === "auto_analysis") {
      setAutomaticAnalysisState({
        phase: "complete",
        message: "最新能力画像的自动分析已完成。",
      });
    }
  }, [selectedThreadId, session]);

  const watchRun = useCallback(async (
    run: AgentRun,
    afterEventSequence: number,
    afterInputSequence: number,
  ) => {
    streamAbort.current?.abort();
    const abort = new AbortController();
    streamAbort.current = abort;
    setActiveRun(run);
    setEventSequence(afterEventSequence);
    setInputSequence(afterInputSequence);
    setEventType("queued");
    setError(null);
    if (run.kind === "auto_analysis") {
      setAutomaticAnalysisState({
        phase: "running",
        message: "正在接收自动分析运行事件…",
      });
    }
    try {
      const events = await openAgentRunEventStream(
        session,
        run.id,
        afterEventSequence,
        abort.signal,
      );
      for await (const event of events.stream) {
        if (abort.signal.aborted) return;
        setEventSequence(event.sequence);
        setEventType(event.type);
      }
      if (abort.signal.aborted) return;
      if (events.failure() !== undefined) throw events.failure();
    } catch (streamError) {
      if (abort.signal.aborted) return;
      const message = apiFailureMessage(streamError);
      setError(message);
      if (run.kind === "auto_analysis") {
        setAutomaticAnalysisState({
          phase: "error",
          message: `自动分析事件连接中断：${message}。可以从已接收位置继续。`,
        });
      }
      return;
    }
    try {
      await finishRun(run, afterInputSequence);
    } catch (runError) {
      if (abort.signal.aborted) return;
      const message = apiFailureMessage(runError);
      setError(message);
      if (run.kind === "auto_analysis") {
        setAutomaticAnalysisState({
          phase: "error",
          message: `自动分析运行未完成：${message}。可以重试。`,
        });
      }
    }
  }, [finishRun, session]);

  useEffect(() => {
    let active = true;
    setLoadingThreads(true);
    setAutomaticAnalysisResult(null);
    setLoadedMessageKey(null);
    watchedAutomaticRun.current = null;
    setAutomaticAnalysisState({
      phase: "checking",
      message: "正在检查最新能力画像…",
    });

    void (async () => {
      let result: AgentRunEnqueueResult | null = null;
      let nextState: AutomaticAnalysisState;
      try {
        const analytics = await loadSelfAnalytics(session);
        if (!active) return;
        if (analytics.state === "not_generated") {
          nextState = {
            phase: "not_needed",
            message: "尚未生成能力画像，无需运行自动分析。",
          };
        } else if (analytics.state === "no_observations") {
          nextState = {
            phase: "not_needed",
            message: "当前能力画像没有有效观测，无需运行自动分析。",
          };
        } else {
          if (!configurationReady || promptKey === undefined || modelKey === undefined) {
            throw new Error("Chat prompt/model configuration key 未配置，自动分析已停止。");
          }
          result = await enqueueAutomaticAnalysis(session, {
            promptConfigurationKey: promptKey,
            modelConfigurationKey: modelKey,
            expectedAnalyticsHeadRevision: analytics.headRevision,
          });
          if (!active) return;
          validateAutomaticAnalysisResult(result);
          nextState = result.created
            ? {
                phase: "queued",
                message: "已创建最新能力画像的自动分析，正在载入专用对话。",
              }
            : {
                phase: "queued",
                message: "已找到最新能力画像的既有自动分析，正在恢复专用对话。",
              };
        }
      } catch (automaticError) {
        const message = apiFailureMessage(automaticError);
        nextState = {
          phase: "error",
          message: `自动分析启动失败：${message}。可以重试。`,
        };
        result = null;
      }

      if (!active) return;
      try {
        let page = await loadChatThreads(session);
        let items = [...page.items];
        let cursor = page.nextCursor;
        while (
          result !== null
          && !items.some((thread) => thread.id === result?.run.threadId)
          && cursor !== null
        ) {
          page = await loadChatThreads(session, 50, cursor);
          const known = new Set(items.map((thread) => thread.id));
          items = [...items, ...page.items.filter((thread) => !known.has(thread.id))];
          cursor = page.nextCursor;
        }
        if (!active) return;
        if (result !== null) {
          const automaticThread = items.find((thread) => thread.id === result?.run.threadId);
          if (automaticThread === undefined) {
            throw new Error("自动分析专用对话未出现在当前账号的对话列表中。");
          }
          if (
            automaticThread.kind !== "auto_analysis"
            || automaticThread.headRevision < result.message.sequence
          ) {
            throw new Error("自动分析专用对话未覆盖返回的输入消息。");
          }
        }
        setThreads(items);
        setNextThreadCursor(cursor);
        setAutomaticAnalysisResult(result);
        setAutomaticAnalysisState(nextState);
        setSelectedThreadId((current) => {
          if (result !== null) return result.run.threadId;
          return current !== null && items.some((thread) => thread.id === current)
            ? current
            : items[0]?.id ?? null;
        });
      } catch (loadError) {
        const message = apiFailureMessage(loadError);
        setAutomaticAnalysisResult(null);
        setError(message);
        if (result !== null) {
          setAutomaticAnalysisState({
            phase: "error",
            message: `自动分析专用对话载入失败：${message}。可以重试。`,
          });
        } else {
          setAutomaticAnalysisState(nextState);
        }
      } finally {
        if (active) setLoadingThreads(false);
      }
    })();

    return () => {
      active = false;
    };
  }, [automaticAnalysisAttempt, configurationReady, modelKey, promptKey, session]);

  useEffect(() => {
    if (
      automaticAnalysisResult === null
      || selectedThreadId !== automaticAnalysisResult.run.threadId
      || loadedMessageKey
        !== `${automaticAnalysisResult.run.threadId}:${automaticAnalysisResult.run.id}`
      || watchedAutomaticRun.current === automaticAnalysisResult.run.id
    ) return;
    watchedAutomaticRun.current = automaticAnalysisResult.run.id;
    void watchRun(
      automaticAnalysisResult.run,
      0,
      automaticAnalysisResult.message.sequence,
    );
  }, [automaticAnalysisResult, loadedMessageKey, selectedThreadId, watchRun]);

  const createThread = async () => {
    setCreating(true);
    setError(null);
    try {
      const thread = await createStudentChatThread(session);
      setThreads((current) => [thread, ...current.filter((item) => item.id !== thread.id)]);
      setSelectedThreadId(thread.id);
    } catch (createError) {
      setError(apiFailureMessage(createError));
    } finally {
      setCreating(false);
    }
  };

  const loadMoreThreads = async () => {
    if (nextThreadCursor === null) return;
    setLoadingThreads(true);
    setError(null);
    try {
      const page = await loadChatThreads(session, 50, nextThreadCursor);
      setThreads((current) => {
        const known = new Set(current.map((thread) => thread.id));
        return [...current, ...page.items.filter((thread) => !known.has(thread.id))];
      });
      setNextThreadCursor(page.nextCursor);
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoadingThreads(false);
    }
  };

  const loadMoreMessages = async () => {
    if (selectedThreadId === null || !moreMessages) return;
    setLoadingMessages(true);
    setError(null);
    try {
      const page = await loadChatMessages(session, selectedThreadId, lastMessageSequence, pageSize);
      setMessages((current) => mergeMessages(current, page.items));
      setLastMessageSequence(page.lastSequence);
      setMoreMessages(page.items.length === pageSize);
    } catch (loadError) {
      setError(apiFailureMessage(loadError));
    } finally {
      setLoadingMessages(false);
    }
  };

  const send = async () => {
    const running = activeRun !== null && !terminalStatuses.has(activeRun.status);
    if (selectedThread?.kind !== "conversation" || running) return;
    const content = draft.trim();
    if (content.length === 0) return;
    if (!configurationReady || promptKey === undefined || modelKey === undefined) {
      setError("Chat prompt/model configuration key 未配置，应用已停止发送请求。");
      return;
    }
    setError(null);
    try {
      const result = await enqueueChatReply(session, selectedThread.id, {
        clientRequestId: crypto.randomUUID(),
        content,
        promptConfigurationKey: promptKey,
        modelConfigurationKey: modelKey,
      });
      setDraft("");
      setMessages((current) => mergeMessages(current, [result.message]));
      setLastMessageSequence((current) => Math.max(current, result.message.sequence));
      void watchRun(result.run, 0, result.message.sequence);
    } catch (sendError) {
      setError(apiFailureMessage(sendError));
    }
  };

  const running = activeRun !== null && !terminalStatuses.has(activeRun.status);
  const automaticAnalysisBusy = automaticAnalysisState.phase === "checking"
    || automaticAnalysisState.phase === "queued"
    || automaticAnalysisState.phase === "running";

  return (
    <section className="mobile-chat view-stack">
      <div className="panel-card chat-thread-controls">
        <div className="section-heading">
          <div><span className="eyebrow">CHAT</span><h2>学习助手</h2><p>对话、运行与工具结果均持久化</p></div>
          <button className="primary-button compact" type="button" disabled={creating} onClick={() => void createThread()}>
            {creating ? "创建中…" : "新建"}
          </button>
        </div>
        {threads.length > 0 ? (
          <label className="chat-thread-select">
            <span>当前对话</span>
            <select value={selectedThreadId ?? ""} onChange={(event) => setSelectedThreadId(event.target.value)}>
              {threads.map((thread, index) => (
                <option key={thread.id} value={thread.id}>
                  {thread.kind === "auto_analysis" ? "自动分析" : `对话 ${threads.length - index}`} · {time(thread.updatedAt)}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        {nextThreadCursor !== null ? <button className="text-button" type="button" onClick={() => void loadMoreThreads()}>加载更多对话</button> : null}
      </div>

      <div className={`panel-card auto-analysis-status ${automaticAnalysisState.phase}`} role="status">
        <div><strong>自动学习分析</strong><p>{automaticAnalysisState.message}</p></div>
        <button
          className="text-button"
          type="button"
          disabled={automaticAnalysisBusy || (activeRun?.kind === "auto_analysis" && running)}
          onClick={() => setAutomaticAnalysisAttempt((attempt) => attempt + 1)}
        >
          {automaticAnalysisState.phase === "error" ? "重试自动分析" : "检查最新画像"}
        </button>
      </div>

      {error !== null ? <div className="global-error" role="alert">{error}</div> : null}

      <div className="panel-card chat-card">
        {selectedThread === null ? (
          <div className="chat-empty"><span>◇</span><strong>{loadingThreads ? "正在读取对话…" : "创建第一段对话"}</strong></div>
        ) : (
          <>
            <div className="chat-message-list" aria-live="polite">
              {moreMessages ? <button className="text-button" type="button" disabled={loadingMessages} onClick={() => void loadMoreMessages()}>加载后续消息</button> : null}
              {loadingMessages && messages.length === 0 ? <p className="empty-copy">正在读取消息…</p> : null}
              {!loadingMessages && messages.length === 0 ? (
                <p className="empty-copy">{selectedThread.kind === "auto_analysis" ? "正在等待自动分析结果。" : "发送第一条消息开始分析。"}</p>
              ) : null}
              {messages.map((message) => <Message key={message.id} message={message} />)}
              {running ? <article className="mobile-chat-message assistant pending"><span>Agent 正在处理</span><strong>{eventType ?? activeRun.status}</strong></article> : null}
            </div>
            {running && error !== null ? (
              <button className="text-button" type="button" onClick={() => void watchRun(activeRun, eventSequence, inputSequence)}>
                {activeRun.kind === "auto_analysis" ? "继续接收自动分析事件" : "继续接收运行事件"}
              </button>
            ) : null}
            {selectedThread.kind === "conversation" ? (
              <div className="mobile-chat-composer">
                <textarea
                  value={draft}
                  rows={3}
                  maxLength={131072}
                  disabled={running}
                  placeholder="输入你想分析的问题…"
                  onChange={(event) => setDraft(event.target.value)}
                />
                <button className="primary-button compact" type="button" disabled={running || draft.trim().length === 0} onClick={() => void send()}>
                  {running ? "处理中" : "发送"}
                </button>
              </div>
            ) : <p className="empty-copy">该专用对话由最新能力画像自动生成。</p>}
          </>
        )}
      </div>
    </section>
  );
}

function Message({ message }: { message: ChatMessage }) {
  const assistant = message.kind === "assistant";
  const author = assistant
    ? "学习助手"
    : message.kind === "auto_analysis_request" ? "自动分析请求" : "你";
  return (
    <article className={`mobile-chat-message ${assistant ? "assistant" : "user"}`}>
      <header><strong>{author}</strong><span>#{message.sequence} · {time(message.createdAt)}</span></header>
      <p>{message.content}</p>
      {assistant && message.reasoningContent !== undefined ? <details><summary>查看推理过程</summary><pre>{message.reasoningContent}</pre></details> : null}
    </article>
  );
}
