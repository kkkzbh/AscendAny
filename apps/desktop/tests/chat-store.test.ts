import { beforeEach, describe, expect, it, vi } from "vitest";

import { useChatStore } from "@/stores/chatStore";

describe("chatStore sessions", () => {
  const localStateSaveChat = vi.fn().mockResolvedValue(true);

  beforeEach(() => {
    localStateSaveChat.mockClear();
    window.electronAPI = {
      minimize: vi.fn(),
      maximize: vi.fn(),
      close: vi.fn(),
      platform: "linux",
      localStateSaveChat,
    };
    useChatStore.getState().resetForAccount();
  });

  it("starts on the blank composer without creating a persisted session", () => {
    const state = useChatStore.getState();

    expect(state.sessions).toHaveLength(0);
    expect(state.activeSessionId).toBeNull();
    expect(state.getActiveSession()).toBeNull();
    expect(localStateSaveChat.mock.calls.at(-1)?.[0]).toMatchObject({
      sessions: [],
      activeSessionId: null,
      newSessionDraft: "",
    });
  });

  it("keeps messages isolated between local sessions", () => {
    useChatStore.getState().addMessage("user", "分析一下最近一次考试");
    const firstSessionId = useChatStore.getState().activeSessionId;
    expect(firstSessionId).toEqual(expect.any(String));

    const secondSessionId = useChatStore.getState().createSession();
    useChatStore.getState().addMessage("user", "我应该怎么训练质量");

    let state = useChatStore.getState();
    expect(state.sessions).toHaveLength(2);
    expect(state.getActiveSession()?.id).toBe(secondSessionId);
    expect(state.getActiveSession()?.messages[0]?.content).toBe("我应该怎么训练质量");

    state.selectSession(firstSessionId!);
    state = useChatStore.getState();
    expect(state.getActiveSession()?.id).toBe(firstSessionId);
    expect(state.getActiveSession()?.messages[0]?.content).toBe("分析一下最近一次考试");
  });

  it("returns to the blank composer without inserting a new session", () => {
    useChatStore.getState().addMessage("user", "分析一下最近一次考试");
    const firstSessionId = useChatStore.getState().activeSessionId;
    useChatStore.getState().setCurrentDraft("继续问这个会话");

    useChatStore.getState().startNewSessionDraft();

    let state = useChatStore.getState();
    expect(state.sessions).toHaveLength(1);
    expect(state.activeSessionId).toBeNull();
    expect(state.newSessionDraft).toBe("继续问这个会话");

    state.setCurrentDraft("初始界面草稿");
    state.selectSession(firstSessionId!);
    state = useChatStore.getState();
    expect(state.getCurrentDraft()).toBe("继续问这个会话");

    state.startNewSessionDraft();
    state = useChatStore.getState();
    expect(state.newSessionDraft).toBe("初始界面草稿");
  });

  it("materializes the blank composer only when the first user message is sent", () => {
    useChatStore.getState().setCurrentDraft("分析下一次训练重点");
    useChatStore.getState().addMessage("user", "分析下一次训练重点");

    const state = useChatStore.getState();
    expect(state.sessions).toHaveLength(1);
    expect(state.activeSessionId).toBe(state.sessions[0]?.id);
    expect(state.newSessionDraft).toBe("");
    expect(state.sessions[0]?.draft).toBe("");
    expect(state.sessions[0]?.title).toBe("分析下一次训练重点");
    expect(state.sessions[0]?.messages[0]?.content).toBe("分析下一次训练重点");
  });

  it("returns to the blank composer after deleting the last session", () => {
    useChatStore.getState().addMessage("user", "分析一下最近一次考试");
    const sessionId = useChatStore.getState().activeSessionId;

    useChatStore.getState().deleteSession(sessionId!);

    const state = useChatStore.getState();
    expect(state.sessions).toHaveLength(0);
    expect(state.activeSessionId).toBeNull();
  });

  it("hydrates sessions from local state snapshot", () => {
    useChatStore.getState().hydrateFromLocalState({
      sessions: [
        {
          id: "session_old",
          title: "",
          messages: [
            {
              id: "msg_old",
              role: "user",
              content: "旧会话第一句",
              timestamp: 1710000000000,
            },
          ],
          summary: "legacy summary",
          createdAt: 1710000000000,
          updatedAt: 1710000001000,
        },
      ],
      activeSessionId: "session_old",
      newSessionDraft: "新的初始草稿",
    });

    const state = useChatStore.getState();
    expect(state.sessions).toHaveLength(1);
    expect(state.activeSessionId).toBe("session_old");
    expect(state.sessions[0].title).toBe("旧会话第一句");
    expect(state.sessions[0].summary).toBe("legacy summary");
    expect(state.newSessionDraft).toBe("新的初始草稿");
  });

  it("keeps legacy empty sessions when hydrating old local state", () => {
    useChatStore.getState().hydrateFromLocalState({
      sessions: [
        {
          id: "session_empty",
          title: "新对话",
          messages: [],
          summary: "",
          createdAt: 1710000000000,
          updatedAt: 1710000000000,
        },
      ],
      activeSessionId: "session_empty",
    });

    const state = useChatStore.getState();
    expect(state.sessions).toHaveLength(1);
    expect(state.activeSessionId).toBe("session_empty");
    expect(state.sessions[0]?.title).toBe("新对话");
    expect(state.sessions[0]?.messages).toEqual([]);
  });

  it("persists chat changes through desktop local state IPC", () => {
    useChatStore.getState().addMessage("user", "分析一下最近一次考试");
    expect(localStateSaveChat).toHaveBeenCalled();
    expect(localStateSaveChat.mock.calls.at(-1)?.[0].sessions[0].messages[0].content)
      .toBe("分析一下最近一次考试");
  });

  it("streams assistant drafts without leaking transient state into reload", async () => {
    useChatStore.getState().createSession();
    const draftId = useChatStore.getState().createAssistantDraft("sakiko");
    useChatStore.getState().appendMessageReasoning(draftId, "先查看数据。");
    useChatStore.getState().upsertMessageToolActivity(draftId, {
      id: "call_1",
      label: "查看考试数据",
      status: "running",
    });
    useChatStore.getState().upsertMessageToolActivity(draftId, {
      id: "call_1",
      label: "查看《数据结构第三次实验》数据",
      status: "done",
    });
    useChatStore.getState().appendMessageContent(draftId, "第一段");
    useChatStore.getState().appendMessageContent(draftId, "第二段");

    let message = useChatStore.getState().getActiveSession()?.messages[0];
    expect(message?.content).toBe("第一段第二段");
    expect(message?.reasoningContent).toBe("先查看数据。");
    expect(message?.reasoningStartedAt).toEqual(expect.any(Number));
    expect(message?.reasoningEndedAt).toBeUndefined();
    expect(message?.toolActivities).toEqual([
      { id: "call_1", label: "查看《数据结构第三次实验》数据", status: "done" },
    ]);
    expect(message?.streaming).toBe(true);
    expect(message?.reasoningStreaming).toBe(true);
    expect(message?.roleId).toBe("sakiko");

    useChatStore.getState().finalizeMessageReasoning(draftId);
    message = useChatStore.getState().getActiveSession()?.messages[0];
    expect(message?.streaming).toBe(true);
    expect(message?.reasoningStreaming).toBe(false);
    expect(message?.reasoningEndedAt).toEqual(expect.any(Number));
    const endedAt = message?.reasoningEndedAt;

    useChatStore.getState().finalizeMessage(draftId);
    message = useChatStore.getState().getActiveSession()?.messages[0];
    expect(message?.streaming).toBe(false);
    expect(message?.reasoningStreaming).toBe(false);
    expect(message?.reasoningEndedAt).toBe(endedAt);

    const emptyDraftId = useChatStore.getState().createAssistantDraft("xiaoD");
    useChatStore.getState().removeMessage(emptyDraftId);
    expect(
      useChatStore.getState().getActiveSession()?.messages.some((item) => item.id === emptyDraftId),
    ).toBe(false);
  });

  it("hydrates persisted reasoning content but clears transient reasoning stream state", () => {
    useChatStore.getState().hydrateFromLocalState({
      sessions: [
        {
          id: "session_reasoning",
          title: "reasoning",
          messages: [
            {
              id: "msg_reasoning",
              role: "assistant",
              content: "最终回答",
              reasoningContent: "思考内容",
              toolActivities: [
                { id: "call_running", label: "查看学习画像", status: "running" },
                { id: "call_error", label: "核对提交记录", status: "error" },
              ],
              reasoningStartedAt: 1710000000100,
              reasoningEndedAt: 1710000005100,
              timestamp: 1710000000000,
              streaming: true,
              reasoningStreaming: true,
            },
          ],
          summary: "",
          createdAt: 1710000000000,
          updatedAt: 1710000001000,
        },
      ],
      activeSessionId: "session_reasoning",
    });

    const message = useChatStore.getState().getActiveSession()?.messages[0];
    expect(message?.content).toBe("最终回答");
    expect(message?.reasoningContent).toBe("思考内容");
    expect(message?.reasoningStartedAt).toBe(1710000000100);
    expect(message?.reasoningEndedAt).toBe(1710000005100);
    expect(message?.toolActivities).toEqual([
      { id: "call_running", label: "查看学习画像", status: "done" },
      { id: "call_error", label: "核对提交记录", status: "error" },
    ]);
    expect(message?.streaming).toBe(false);
    expect(message?.reasoningStreaming).toBe(false);
  });
});
