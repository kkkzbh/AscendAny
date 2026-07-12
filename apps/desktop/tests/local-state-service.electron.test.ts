import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { LocalStateService } from "../electron/localState";

describe("LocalStateService Electron SQLite integration", () => {
  it("round-trips assistant reasoning and rich chat blocks through SQLite", () => {
    expect(process.versions.electron).toBeDefined();

    const dir = mkdtempSync(join(tmpdir(), "ascendany-local-state-"));

    try {
      const service = new LocalStateService(join(dir, "state.db"));

      try {
        service.saveChat({
          sessions: [
            {
              id: "session_rich",
              title: "富文本会话",
              summary: "summary",
              draft: "draft",
              createdAt: 1710000000000,
              updatedAt: 1710000009000,
              messages: [
                {
                  id: "msg_rich",
                  role: "assistant",
                  content: "最终回答",
                  timestamp: 1710000001000,
                  roleId: "sakiko",
                  reasoningContent: "先看画像，再看提交记录。",
                  reasoningStartedAt: 1710000001100,
                  reasoningEndedAt: 1710000005100,
                  streaming: true,
                  reasoningStreaming: true,
                  toolActivities: [
                    { id: "call_legacy", label: "查看学习画像", status: "running" },
                  ],
                  blocks: [
                    { kind: "text", text: "最终回答" },
                    {
                      kind: "tool",
                      activity: { id: "call_1", label: "查看学习画像", status: "running" },
                    },
                  ],
                },
              ],
            },
          ],
          activeSessionId: "session_rich",
          newSessionDraft: "new draft",
        });

        const hydrated = service.hydrate();
        const session = hydrated.chat.sessions[0];
        const message = session?.messages[0];

        expect(hydrated.chat.activeSessionId).toBe("session_rich");
        expect(hydrated.chat.newSessionDraft).toBe("new draft");
        expect(session?.draft).toBe("draft");
        expect(message).toMatchObject({
          id: "msg_rich",
          role: "assistant",
          content: "最终回答",
          roleId: "sakiko",
          reasoningContent: "先看画像，再看提交记录。",
          reasoningStartedAt: 1710000001100,
          reasoningEndedAt: 1710000005100,
          streaming: true,
          reasoningStreaming: true,
        });
        expect(message?.toolActivities).toEqual([
          { id: "call_legacy", label: "查看学习画像", status: "done" },
        ]);
        expect(message?.blocks).toEqual([
          { kind: "text", text: "最终回答" },
          {
            kind: "tool",
            activity: { id: "call_1", label: "查看学习画像", status: "running" },
          },
        ]);
      } finally {
        service.close();
      }
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
