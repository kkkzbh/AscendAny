import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type {
  AgentRun,
  AgentRunEnqueueResult,
  BrowserSession,
  ChatMessage,
  ChatThread,
  SelfStudentAnalytics,
} from "@ascendany/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChatView } from "../src/components/ChatView";

const session = {} as BrowserSession;
const operations = vi.hoisted(() => ({
  createStudentChatThread: vi.fn(),
  enqueueAutomaticAnalysis: vi.fn(),
  enqueueChatReply: vi.fn(),
  loadAgentRun: vi.fn(),
  loadChatMessages: vi.fn(),
  loadChatThreads: vi.fn(),
  loadSelfAnalytics: vi.fn(),
  openAgentRunEventStream: vi.fn(),
}));

vi.mock("../src/session/context", () => ({ useSession: () => ({ session }) }));
vi.mock("../src/api/operations", () => operations);

const thread: ChatThread = {
  id: "11111111-1111-4111-8111-111111111111",
  kind: "conversation",
  headRevision: 0,
  createdAt: "2026-07-11T01:00:00Z",
  updatedAt: "2026-07-11T01:00:00Z",
};
const input: ChatMessage = {
  id: "22222222-2222-4222-8222-222222222222",
  threadId: thread.id,
  sequence: 1,
  kind: "user",
  content: "分析这次考试",
  createdAt: "2026-07-11T01:01:00Z",
};
const run: AgentRun = {
  id: "33333333-3333-4333-8333-333333333333",
  threadId: thread.id,
  clientRequestId: "44444444-4444-4444-8444-444444444444",
  kind: "reply",
  inputMessageId: input.id,
  status: "queued",
  attemptCount: 0,
  createdAt: "2026-07-11T01:01:00Z",
  updatedAt: "2026-07-11T01:01:00Z",
};
const output: ChatMessage = {
  id: "55555555-5555-4555-8555-555555555555",
  threadId: thread.id,
  sequence: 2,
  kind: "assistant",
  content: "优先复习图论。",
  runId: run.id,
  createdAt: "2026-07-11T01:01:02Z",
};
const succeededRun: AgentRun = {
  ...run,
  status: "succeeded",
  outputMessageId: output.id,
  attemptCount: 1,
  finishedAt: "2026-07-11T01:01:02Z",
};

interface AutomaticFixture {
  thread: ChatThread;
  input: ChatMessage;
  run: AgentRun;
  output: ChatMessage;
  succeededRun: AgentRun;
  result: AgentRunEnqueueResult;
}

function automaticFixture(headRevision: number, inputSequence: number): AutomaticFixture {
  const automaticThread: ChatThread = {
    id: "61111111-1111-4111-8111-111111111111",
    kind: "auto_analysis",
    headRevision: inputSequence,
    createdAt: `2026-07-11T0${headRevision - 5}:00:00Z`,
    updatedAt: `2026-07-11T0${headRevision - 5}:00:00Z`,
  };
  const automaticInput: ChatMessage = {
    id: `${headRevision}2222222-2222-4222-8222-222222222222`,
    threadId: automaticThread.id,
    sequence: inputSequence,
    kind: "auto_analysis_request",
    content: `分析能力画像 head ${headRevision}`,
    createdAt: `2026-07-11T0${headRevision - 5}:00:00Z`,
  };
  const automaticRun: AgentRun = {
    id: `${headRevision}3333333-3333-4333-8333-333333333333`,
    threadId: automaticThread.id,
    clientRequestId: `${headRevision}4444444-4444-4444-8444-444444444444`,
    kind: "auto_analysis",
    inputMessageId: automaticInput.id,
    status: "queued",
    attemptCount: 0,
    createdAt: `2026-07-11T0${headRevision - 5}:00:00Z`,
    updatedAt: `2026-07-11T0${headRevision - 5}:00:00Z`,
  };
  const automaticOutput: ChatMessage = {
    id: `${headRevision}5555555-5555-4555-8555-555555555555`,
    threadId: automaticThread.id,
    sequence: inputSequence + 1,
    kind: "assistant",
    content: `桌面端自动分析：head ${headRevision} 已完成。`,
    runId: automaticRun.id,
    createdAt: `2026-07-11T0${headRevision - 5}:00:02Z`,
  };
  const automaticSucceededRun: AgentRun = {
    ...automaticRun,
    status: "succeeded",
    outputMessageId: automaticOutput.id,
    attemptCount: 1,
    finishedAt: `2026-07-11T0${headRevision - 5}:00:02Z`,
  };
  return {
    thread: automaticThread,
    input: automaticInput,
    run: automaticRun,
    output: automaticOutput,
    succeededRun: automaticSucceededRun,
    result: { run: automaticRun, message: automaticInput, created: true },
  };
}

const automatic7 = automaticFixture(7, 1);
const automatic8 = automaticFixture(8, 3);

function readyAnalytics(headRevision: number): SelfStudentAnalytics {
  return {
    state: "ready",
    headRevision,
    referenceTime: "2026-07-11T02:00:00Z",
    rating: 1200,
    current: {
      knowledge: 0.7,
      accuracy: 0.8,
      quality: 0.6,
      flexibility: 0.5,
      proficiency: 0.65,
    },
    examHistory: [],
    ratingHistory: [],
  };
}

async function* completedEvents(sequence = 1) {
  yield {
    sequence,
    type: "run_succeeded",
    payload: {},
    createdAt: "2026-07-11T02:00:02Z",
  };
}

async function* oneEvent(sequence: number) {
  yield {
    sequence,
    type: "message_delta",
    payload: {},
    createdAt: "2026-07-11T02:00:01Z",
  };
}

function configureAutomaticAnalysis(created = true) {
  operations.loadSelfAnalytics.mockResolvedValue(readyAnalytics(7));
  operations.enqueueAutomaticAnalysis.mockResolvedValue({
    ...automatic7.result,
    created,
  });
  operations.loadChatThreads.mockResolvedValue({
    items: [automatic7.thread, thread],
    nextCursor: null,
  });
  operations.loadChatMessages.mockImplementation(
    async (_session: BrowserSession, threadId: string, afterSequence: number) => {
      if (threadId === automatic7.thread.id && afterSequence === automatic7.input.sequence) {
        return { items: [automatic7.output], lastSequence: automatic7.output.sequence };
      }
      return { items: [], lastSequence: 0 };
    },
  );
  operations.openAgentRunEventStream.mockImplementation(async () => ({
    stream: completedEvents(),
    failure: () => undefined,
  }));
  operations.loadAgentRun.mockResolvedValue(automatic7.succeededRun);
}

describe("Desktop ChatView", () => {
  beforeEach(() => {
    vi.stubEnv("VITE_CHAT_PROMPT_CONFIGURATION_KEY", "agent.prompt.default");
    vi.stubEnv("VITE_CHAT_MODEL_CONFIGURATION_KEY", "agent.model.default");
    for (const mock of Object.values(operations)) mock.mockReset();
    operations.loadSelfAnalytics.mockResolvedValue({ state: "no_observations", headRevision: 1 });
    operations.loadChatThreads.mockResolvedValue({ items: [thread], nextCursor: null });
    operations.loadChatMessages.mockImplementation(
      async (_session: BrowserSession, threadId: string, afterSequence: number) => {
        if (threadId === thread.id && afterSequence === 1) {
          return { items: [output], lastSequence: 2 };
        }
        return { items: [], lastSequence: 0 };
      },
    );
    operations.enqueueChatReply.mockResolvedValue({ run, message: input, created: true });
    operations.openAgentRunEventStream.mockImplementation(async () => ({
      stream: completedEvents(),
      failure: () => undefined,
    }));
    operations.loadAgentRun.mockResolvedValue(succeededRun);
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllEnvs();
  });

  it("resumes a durable reply run and renders its assistant message", async () => {
    render(<ChatView />);
    await screen.findByText("发送第一条消息开始分析。");
    fireEvent.change(screen.getByPlaceholderText("输入你想分析的问题…"), {
      target: { value: "  分析这次考试  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => {
      expect(operations.enqueueChatReply).toHaveBeenCalledWith(session, thread.id, {
        clientRequestId: expect.any(String),
        content: "分析这次考试",
        promptConfigurationKey: "agent.prompt.default",
        modelConfigurationKey: "agent.model.default",
      });
    });
    expect(await screen.findByText(output.content)).not.toBeNull();
  });

  it("queues ready analytics, resolves pagination, and observes the pinned run", async () => {
    configureAutomaticAnalysis(true);
    operations.loadChatThreads
      .mockResolvedValueOnce({ items: [thread], nextCursor: "next-page" })
      .mockResolvedValueOnce({ items: [automatic7.thread], nextCursor: null });

    render(<ChatView />);

    expect(await screen.findByText(automatic7.output.content)).not.toBeNull();
    expect(operations.enqueueAutomaticAnalysis).toHaveBeenCalledWith(session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 7,
    });
    expect(operations.loadChatThreads).toHaveBeenNthCalledWith(2, session, 50, "next-page");
    expect(operations.openAgentRunEventStream).toHaveBeenCalledWith(
      session,
      automatic7.run.id,
      0,
      expect.any(AbortSignal),
    );
    expect(screen.queryByPlaceholderText("输入你想分析的问题…")).toBeNull();
  });

  it("restores and displays an existing automatic-analysis run", async () => {
    configureAutomaticAnalysis(false);

    render(<ChatView />);

    expect(await screen.findByText(automatic7.output.content)).not.toBeNull();
    expect(screen.getByText(automatic7.input.content)).not.toBeNull();
    expect(operations.openAgentRunEventStream).toHaveBeenCalledWith(
      session,
      automatic7.run.id,
      0,
      expect.any(AbortSignal),
    );
  });

  it("posts again after a full reload boundary", async () => {
    configureAutomaticAnalysis(false);

    const first = render(<ChatView />);
    await waitFor(() => expect(operations.enqueueAutomaticAnalysis).toHaveBeenCalledTimes(1));
    first.unmount();
    render(<ChatView />);

    await waitFor(() => expect(operations.enqueueAutomaticAnalysis).toHaveBeenCalledTimes(2));
    expect(operations.enqueueAutomaticAnalysis).toHaveBeenNthCalledWith(2, session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 7,
    });
  });

  it("starts the next automatic run after analytics head advancement", async () => {
    operations.loadSelfAnalytics
      .mockResolvedValueOnce(readyAnalytics(7))
      .mockResolvedValueOnce(readyAnalytics(8));
    operations.enqueueAutomaticAnalysis
      .mockResolvedValueOnce(automatic7.result)
      .mockResolvedValueOnce(automatic8.result);
    operations.loadChatThreads
      .mockResolvedValueOnce({ items: [automatic7.thread], nextCursor: null })
      .mockResolvedValueOnce({ items: [automatic8.thread], nextCursor: null });
    let initialLoadCount = 0;
    operations.loadChatMessages.mockImplementation(
      async (_session: BrowserSession, threadId: string, afterSequence: number) => {
        if (threadId !== automatic7.thread.id) return { items: [], lastSequence: 0 };
        if (afterSequence === 0) {
          initialLoadCount += 1;
          return initialLoadCount === 1
            ? { items: [], lastSequence: 0 }
            : { items: [automatic7.input, automatic7.output], lastSequence: 2 };
        }
        if (afterSequence === automatic7.input.sequence) {
          return { items: [automatic7.output], lastSequence: automatic7.output.sequence };
        }
        return { items: [automatic8.output], lastSequence: automatic8.output.sequence };
      },
    );
    operations.openAgentRunEventStream.mockImplementation(async () => ({
      stream: completedEvents(),
      failure: () => undefined,
    }));
    operations.loadAgentRun.mockImplementation(async (_session: BrowserSession, runId: string) => (
      runId === automatic7.run.id ? automatic7.succeededRun : automatic8.succeededRun
    ));

    render(<ChatView />);
    await screen.findByText(automatic7.output.content);
    fireEvent.click(screen.getByRole("button", { name: "检查最新画像" }));

    expect(await screen.findByText(automatic8.output.content)).not.toBeNull();
    expect(operations.enqueueAutomaticAnalysis).toHaveBeenNthCalledWith(2, session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 8,
    });
  });

  it.each([
    [{ state: "not_generated", headRevision: 0 } as SelfStudentAnalytics, "尚未生成能力画像，无需运行自动分析。"],
    [{ state: "no_observations", headRevision: 3 } as SelfStudentAnalytics, "当前能力画像没有有效观测，无需运行自动分析。"],
  ])("does not enqueue a non-ready analytics state", async (analytics, message) => {
    operations.loadSelfAnalytics.mockResolvedValue(analytics);

    render(<ChatView />);

    expect(await screen.findByText(message)).not.toBeNull();
    expect(operations.enqueueAutomaticAnalysis).not.toHaveBeenCalled();
  });

  it("shows retry for head conflicts and missing configuration", async () => {
    operations.loadSelfAnalytics.mockResolvedValue(readyAnalytics(7));
    operations.enqueueAutomaticAnalysis.mockRejectedValueOnce(
      new Error("Published analytics head revision changed."),
    );

    const first = render(<ChatView />);
    expect(await screen.findByText(/Published analytics head revision changed/)).not.toBeNull();
    expect((screen.getByRole("button", { name: "重试自动分析" }) as HTMLButtonElement).disabled)
      .toBe(false);
    first.unmount();

    vi.stubEnv("VITE_CHAT_MODEL_CONFIGURATION_KEY", "");
    operations.enqueueAutomaticAnalysis.mockReset();
    render(<ChatView />);
    expect(await screen.findByText(/configuration key 未配置/)).not.toBeNull();
    expect(operations.enqueueAutomaticAnalysis).not.toHaveBeenCalled();
  });

  it("resumes a disconnected automatic stream at its durable cursor", async () => {
    configureAutomaticAnalysis(false);
    let streamCount = 0;
    operations.openAgentRunEventStream.mockImplementation(async () => {
      streamCount += 1;
      return streamCount === 1
        ? { stream: oneEvent(2), failure: () => new Error("network disconnected") }
        : { stream: completedEvents(3), failure: () => undefined };
    });

    render(<ChatView />);

    const resume = await screen.findByRole("button", { name: "继续接收自动分析事件" });
    expect(screen.getByText(/自动分析事件连接中断/)).not.toBeNull();
    fireEvent.click(resume);

    expect(await screen.findByText(automatic7.output.content)).not.toBeNull();
    expect(operations.openAgentRunEventStream).toHaveBeenNthCalledWith(
      2,
      session,
      automatic7.run.id,
      2,
      expect.any(AbortSignal),
    );
  });
});
