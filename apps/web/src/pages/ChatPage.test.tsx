import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type {
  AgentRun,
  AgentRunEnqueueResult,
  BrowserSession,
  ChatInputMessage,
  ChatMessage,
  ChatThread,
  SelfStudentAnalytics,
} from "@ascendany/sdk";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChatPage } from "./ChatPage";

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

vi.mock("../session/context", () => ({
  useSession: () => ({ session }),
}));

vi.mock("../api/operations", () => operations);

const thread: ChatThread = {
  id: "11111111-1111-4111-8111-111111111111",
  kind: "conversation",
  headRevision: 0,
  createdAt: "2026-07-11T01:00:00Z",
  updatedAt: "2026-07-11T01:00:00Z",
};

const inputMessage: ChatMessage = {
  id: "22222222-2222-4222-8222-222222222222",
  threadId: thread.id,
  sequence: 1,
  kind: "user",
  content: "分析这次考试",
  createdAt: "2026-07-11T01:01:00Z",
};

const queuedRun: AgentRun = {
  id: "33333333-3333-4333-8333-333333333333",
  threadId: thread.id,
  clientRequestId: "44444444-4444-4444-8444-444444444444",
  kind: "reply",
  inputMessageId: inputMessage.id,
  status: "queued",
  attemptCount: 0,
  createdAt: "2026-07-11T01:01:00Z",
  updatedAt: "2026-07-11T01:01:00Z",
};

const succeededRun: AgentRun = {
  ...queuedRun,
  status: "succeeded",
  outputMessageId: "55555555-5555-4555-8555-555555555555",
  attemptCount: 1,
  startedAt: "2026-07-11T01:01:01Z",
  finishedAt: "2026-07-11T01:01:02Z",
  updatedAt: "2026-07-11T01:01:02Z",
};

const outputMessage: ChatMessage = {
  id: succeededRun.outputMessageId!,
  threadId: thread.id,
  sequence: 2,
  kind: "assistant",
  content: "本次考试的主要短板是图论。",
  reasoningContent: "基于当前发布的能力画像。",
  runId: queuedRun.id,
  createdAt: "2026-07-11T01:01:02Z",
};

const automaticThread: ChatThread = {
  id: "61111111-1111-4111-8111-111111111111",
  kind: "auto_analysis",
  headRevision: 1,
  createdAt: "2026-07-11T02:00:00Z",
  updatedAt: "2026-07-11T02:00:00Z",
};

const automaticInput: ChatInputMessage = {
  id: "62222222-2222-4222-8222-222222222222",
  threadId: automaticThread.id,
  sequence: 1,
  kind: "auto_analysis_request",
  content: "分析能力画像 head 7",
  createdAt: "2026-07-11T02:00:00Z",
};

const automaticRun: AgentRun = {
  id: "63333333-3333-4333-8333-333333333333",
  threadId: automaticThread.id,
  clientRequestId: "64444444-4444-4444-8444-444444444444",
  kind: "auto_analysis",
  inputMessageId: automaticInput.id,
  status: "queued",
  attemptCount: 0,
  createdAt: "2026-07-11T02:00:00Z",
  updatedAt: "2026-07-11T02:00:00Z",
};

const automaticOutput: ChatMessage = {
  id: "65555555-5555-4555-8555-555555555555",
  threadId: automaticThread.id,
  sequence: 2,
  kind: "assistant",
  content: "自动分析：当前应优先巩固图论。",
  runId: automaticRun.id,
  createdAt: "2026-07-11T02:00:02Z",
};

const automaticSucceededRun: AgentRun = {
  ...automaticRun,
  status: "succeeded",
  outputMessageId: automaticOutput.id,
  attemptCount: 1,
  startedAt: "2026-07-11T02:00:01Z",
  finishedAt: "2026-07-11T02:00:02Z",
  updatedAt: "2026-07-11T02:00:02Z",
};

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

async function* completedEventStream(sequence = 1) {
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
  const result: AgentRunEnqueueResult = {
    run: automaticRun,
    message: automaticInput,
    created,
  };
  operations.loadSelfAnalytics.mockResolvedValue(readyAnalytics(7));
  operations.enqueueAutomaticAnalysis.mockResolvedValue(result);
  operations.loadChatThreads.mockResolvedValue({
    items: [automaticThread, thread],
    nextCursor: null,
  });
  operations.loadChatMessages.mockImplementation(
    async (_session: BrowserSession, threadId: string, afterSequence: number) => {
      if (threadId === automaticThread.id && afterSequence === 0) {
        return { items: [], lastSequence: 0 };
      }
      if (threadId === automaticThread.id && afterSequence === 1) {
        return { items: [automaticOutput], lastSequence: 2 };
      }
      return { items: [], lastSequence: 0 };
    },
  );
  operations.openAgentRunEventStream.mockImplementation(async () => ({
    stream: completedEventStream(),
    failure: () => undefined,
  }));
  operations.loadAgentRun.mockResolvedValue(automaticSucceededRun);
  return result;
}

describe("ChatPage", () => {
  beforeEach(() => {
    vi.stubEnv("VITE_CHAT_PROMPT_CONFIGURATION_KEY", "agent.prompt.default");
    vi.stubEnv("VITE_CHAT_MODEL_CONFIGURATION_KEY", "agent.model.default");
    for (const mock of Object.values(operations)) mock.mockReset();
    operations.loadSelfAnalytics.mockResolvedValue({
      state: "no_observations",
      headRevision: 1,
    });
    operations.loadChatThreads.mockResolvedValue({ items: [thread], nextCursor: null });
    operations.loadChatMessages.mockImplementation(
      async (_session: BrowserSession, threadId: string, afterSequence: number) => {
        if (threadId === thread.id && afterSequence === 1) {
          return { items: [outputMessage], lastSequence: 2 };
        }
        return { items: [], lastSequence: 0 };
      },
    );
    operations.enqueueChatReply.mockResolvedValue({
      run: queuedRun,
      message: inputMessage,
      created: true,
    });
    operations.openAgentRunEventStream.mockImplementation(async () => ({
      stream: completedEventStream(),
      failure: () => undefined,
    }));
    operations.loadAgentRun.mockResolvedValue(succeededRun);
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllEnvs();
  });

  it("persists a reply request and renders the durable assistant result", async () => {
    render(<ChatPage />);

    await screen.findByText("发送第一条消息开始对话。");
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
    expect(await screen.findByText("本次考试的主要短板是图论。")).toBeInTheDocument();
    expect(operations.loadAgentRun).toHaveBeenCalledWith(session, queuedRun.id);
    expect(operations.loadChatMessages).toHaveBeenLastCalledWith(session, thread.id, 1, 200);
  });

  it("queues ready analytics, finds the dedicated thread, and follows its pinned run", async () => {
    configureAutomaticAnalysis(true);
    operations.loadChatThreads
      .mockResolvedValueOnce({ items: [thread], nextCursor: "next-page" })
      .mockResolvedValueOnce({ items: [automaticThread], nextCursor: null });

    render(<ChatPage />);

    expect(await screen.findByText(automaticOutput.content)).toBeInTheDocument();
    expect(operations.enqueueAutomaticAnalysis).toHaveBeenCalledWith(session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 7,
    });
    expect(operations.loadChatThreads).toHaveBeenNthCalledWith(2, session, 50, "next-page");
    expect(operations.openAgentRunEventStream).toHaveBeenCalledWith(
      session,
      automaticRun.id,
      0,
      expect.any(AbortSignal),
    );
    expect(screen.queryByPlaceholderText("输入你想分析的问题…")).not.toBeInTheDocument();
    expect(operations.loadSelfAnalytics.mock.invocationCallOrder[0]).toBeLessThan(
      operations.enqueueAutomaticAnalysis.mock.invocationCallOrder[0],
    );
  });

  it("restores and displays an existing automatic-analysis run", async () => {
    configureAutomaticAnalysis(false);

    render(<ChatPage />);

    expect(await screen.findByText(automaticOutput.content)).toBeInTheDocument();
    expect(screen.getByText(automaticInput.content)).toBeInTheDocument();
    expect(operations.openAgentRunEventStream).toHaveBeenCalledWith(
      session,
      automaticRun.id,
      0,
      expect.any(AbortSignal),
    );
  });

  it("posts again after a full unmount and lets the service converge duplicates", async () => {
    configureAutomaticAnalysis(false);

    const first = render(<ChatPage />);
    await waitFor(() => expect(operations.enqueueAutomaticAnalysis).toHaveBeenCalledTimes(1));
    first.unmount();
    render(<ChatPage />);

    await waitFor(() => expect(operations.enqueueAutomaticAnalysis).toHaveBeenCalledTimes(2));
    expect(operations.enqueueAutomaticAnalysis).toHaveBeenNthCalledWith(2, session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 7,
    });
  });

  it("uses the advanced analytics head to start and select the next automatic run", async () => {
    const threadAtHead8: ChatThread = {
      ...automaticThread,
      headRevision: 3,
      updatedAt: "2026-07-11T03:00:00Z",
    };
    const inputAtHead8: ChatInputMessage = {
      ...automaticInput,
      id: "72222222-2222-4222-8222-222222222222",
      sequence: 3,
      content: "分析能力画像 head 8",
      createdAt: "2026-07-11T03:00:00Z",
    };
    const runAtHead8: AgentRun = {
      ...automaticRun,
      id: "73333333-3333-4333-8333-333333333333",
      clientRequestId: "74444444-4444-4444-8444-444444444444",
      inputMessageId: inputAtHead8.id,
      createdAt: "2026-07-11T03:00:00Z",
      updatedAt: "2026-07-11T03:00:00Z",
    };
    const outputAtHead8: ChatMessage = {
      ...automaticOutput,
      id: "75555555-5555-4555-8555-555555555555",
      sequence: 4,
      content: "自动分析：head 8 已纳入最新考试。",
      runId: runAtHead8.id,
      createdAt: "2026-07-11T03:00:02Z",
    };
    const succeededAtHead8: AgentRun = {
      ...runAtHead8,
      status: "succeeded",
      outputMessageId: outputAtHead8.id,
      attemptCount: 1,
      finishedAt: "2026-07-11T03:00:02Z",
    };
    operations.loadSelfAnalytics
      .mockResolvedValueOnce(readyAnalytics(7))
      .mockResolvedValueOnce(readyAnalytics(8));
    operations.enqueueAutomaticAnalysis
      .mockResolvedValueOnce({ run: automaticRun, message: automaticInput, created: true })
      .mockResolvedValueOnce({ run: runAtHead8, message: inputAtHead8, created: true });
    operations.loadChatThreads
      .mockResolvedValueOnce({ items: [automaticThread], nextCursor: null })
      .mockResolvedValueOnce({ items: [threadAtHead8], nextCursor: null });
    let initialLoadCount = 0;
    operations.loadChatMessages.mockImplementation(
      async (_session: BrowserSession, threadId: string, afterSequence: number) => {
        if (threadId !== automaticThread.id) return { items: [], lastSequence: 0 };
        if (afterSequence === 0) {
          initialLoadCount += 1;
          return initialLoadCount === 1
            ? { items: [], lastSequence: 0 }
            : { items: [automaticInput, automaticOutput], lastSequence: 2 };
        }
        if (afterSequence === 1) return { items: [automaticOutput], lastSequence: 2 };
        return { items: [outputAtHead8], lastSequence: 4 };
      },
    );
    operations.openAgentRunEventStream.mockImplementation(async () => ({
      stream: completedEventStream(),
      failure: () => undefined,
    }));
    operations.loadAgentRun.mockImplementation(async (_session: BrowserSession, runId: string) => (
      runId === automaticRun.id ? automaticSucceededRun : succeededAtHead8
    ));

    render(<ChatPage />);
    await screen.findByText(automaticOutput.content);
    fireEvent.click(screen.getByRole("button", { name: "检查最新画像" }));

    expect(await screen.findByText(outputAtHead8.content)).toBeInTheDocument();
    expect(operations.enqueueAutomaticAnalysis).toHaveBeenNthCalledWith(2, session, {
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 8,
    });
    expect(operations.openAgentRunEventStream).toHaveBeenCalledWith(
      session,
      runAtHead8.id,
      0,
      expect.any(AbortSignal),
    );
  });

  it.each([
    [{ state: "not_generated", headRevision: 0 } as SelfStudentAnalytics, "尚未生成能力画像，无需运行自动分析。"],
    [{ state: "no_observations", headRevision: 3 } as SelfStudentAnalytics, "当前能力画像没有有效观测，无需运行自动分析。"],
  ])("does not enqueue when analytics cannot produce an analysis", async (analytics, message) => {
    operations.loadSelfAnalytics.mockResolvedValue(analytics);

    render(<ChatPage />);

    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(operations.enqueueAutomaticAnalysis).not.toHaveBeenCalled();
  });

  it("shows a retry state for an analytics-head conflict", async () => {
    operations.loadSelfAnalytics.mockResolvedValue(readyAnalytics(7));
    operations.enqueueAutomaticAnalysis.mockRejectedValue(
      new Error("Published analytics head revision changed."),
    );

    render(<ChatPage />);

    expect(await screen.findByText(/Published analytics head revision changed/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试自动分析" })).toBeEnabled();
  });

  it("resumes a disconnected automatic stream from its last durable event", async () => {
    configureAutomaticAnalysis(false);
    let streamCount = 0;
    operations.openAgentRunEventStream.mockImplementation(async () => {
      streamCount += 1;
      return streamCount === 1
        ? {
            stream: oneEvent(2),
            failure: () => new Error("network disconnected"),
          }
        : {
            stream: completedEventStream(3),
            failure: () => undefined,
          };
    });

    render(<ChatPage />);

    const resume = await screen.findByRole("button", { name: "继续接收自动分析事件" });
    expect(screen.getByText(/自动分析事件连接中断/)).toBeInTheDocument();
    fireEvent.click(resume);

    expect(await screen.findByText(automaticOutput.content)).toBeInTheDocument();
    expect(operations.openAgentRunEventStream).toHaveBeenNthCalledWith(
      2,
      session,
      automaticRun.id,
      2,
      expect.any(AbortSignal),
    );
  });

  it("fails closed when the build-time model configuration is missing", async () => {
    vi.stubEnv("VITE_CHAT_MODEL_CONFIGURATION_KEY", "");
    render(<ChatPage />);

    await screen.findByText("发送第一条消息开始对话。");
    fireEvent.change(screen.getByPlaceholderText("输入你想分析的问题…"), {
      target: { value: "分析" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发送" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Chat prompt/model configuration key 未配置",
    );
    expect(operations.enqueueChatReply).not.toHaveBeenCalled();
  });
});
