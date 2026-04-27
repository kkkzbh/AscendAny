import { beforeEach, describe, expect, it } from "vitest";

import { useChatStore } from "@/stores/chatStore";

describe("chatStore sessions", () => {
  beforeEach(async () => {
    localStorage.clear();
    useChatStore.persist.setOptions({
      name: "ascendany_chat_guest",
    });
    useChatStore.getState().resetForAccount();
    await useChatStore.persist.rehydrate();
  });

  it("keeps messages isolated between local sessions", () => {
    const firstSessionId = useChatStore.getState().activeSessionId;
    useChatStore.getState().addMessage("user", "分析一下最近一次考试");

    const secondSessionId = useChatStore.getState().createSession();
    useChatStore.getState().addMessage("user", "我应该怎么训练质量");

    let state = useChatStore.getState();
    expect(state.sessions).toHaveLength(2);
    expect(state.getActiveSession().id).toBe(secondSessionId);
    expect(state.getActiveSession().messages[0]?.content).toBe("我应该怎么训练质量");

    state.selectSession(firstSessionId);
    state = useChatStore.getState();
    expect(state.getActiveSession().id).toBe(firstSessionId);
    expect(state.getActiveSession().messages[0]?.content).toBe("分析一下最近一次考试");
  });

  it("migrates legacy single-session persistence", async () => {
    localStorage.setItem(
      "ascendany_chat_guest",
      JSON.stringify({
        state: {
          session: {
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
        },
        version: 0,
      }),
    );

    await useChatStore.persist.rehydrate();

    const state = useChatStore.getState();
    expect(state.sessions).toHaveLength(1);
    expect(state.activeSessionId).toBe(state.sessions[0].id);
    expect(state.sessions[0].title).toBe("旧会话第一句");
    expect(state.sessions[0].summary).toBe("legacy summary");
  });
});
