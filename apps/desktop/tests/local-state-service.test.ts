import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { LocalStateService } from "../electron/localState";

const SOURCE = readFileSync(resolve(process.cwd(), "electron/localState.ts"), "utf8");

function isNativeAbiMismatch(error: unknown): boolean {
  return (
    error instanceof Error &&
    /NODE_MODULE_VERSION|different Node\.js version|better_sqlite3\.node/.test(error.message)
  );
}

describe("LocalStateService source contract", () => {
  it("uses the planned SQLite schema for local profile state", () => {
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS schema_migrations");
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS profiles");
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS app_state");
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS profile_settings");
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS layout_state");
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS chat_sessions");
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS chat_messages");
  });

  it("keeps login binding separate from chat and settings writes", () => {
    expect(SOURCE).toContain("bindActiveProfile");
    expect(SOURCE).toContain("UPDATE profiles");
    expect(SOURCE).not.toMatch(/bindActiveProfile[\\s\\S]*DELETE FROM chat_sessions/);
    expect(SOURCE).not.toMatch(/bindActiveProfile[\\s\\S]*DELETE FROM profile_settings/);
  });

  it("enables WAL and busy timeout for the desktop SQLite store", () => {
    expect(SOURCE).toContain('this.db.pragma("journal_mode = WAL")');
    expect(SOURCE).toContain('this.db.pragma("busy_timeout = 5000")');
  });

  it("includes the notes table in the v2 migration", () => {
    expect(SOURCE).toContain("const SCHEMA_VERSION = 2;");
    expect(SOURCE).toContain("CREATE TABLE IF NOT EXISTS notes");
    expect(SOURCE).toContain("title_is_auto INTEGER NOT NULL DEFAULT 1");
    expect(SOURCE).toContain("idx_notes_profile_updated");
  });

  it("exposes notes CRUD methods scoped to the active profile", () => {
    expect(SOURCE).toContain("upsertNote(payload: unknown)");
    expect(SOURCE).toContain("createNote(): LocalNoteSnapshot");
    expect(SOURCE).toContain("deleteNote(id: unknown)");
    expect(SOURCE).toContain("setActiveNote(id: unknown)");
    expect(SOURCE).toContain("clearNoteContent(id: unknown)");
  });

  it("allows an empty chat composer without auto-creating a blank session", () => {
    expect(SOURCE).toContain("activeSessionId: string | null");
    expect(SOURCE).toContain("newSessionDraft: string");
    expect(SOURCE).toContain("chatDraftsKey(profileId: string)");
    expect(SOURCE).not.toMatch(/if \(sessionRows\.length === 0\)[\s\S]*this\.saveChat/);
  });

  it("round-trips assistant reasoning and rich chat blocks through SQLite", () => {
    const dir = mkdtempSync(join(tmpdir(), "ascendany-local-state-"));
    let service: LocalStateService | null = null;
    try {
      try {
        service = new LocalStateService(join(dir, "state.db"));
      } catch (error) {
        if (isNativeAbiMismatch(error)) {
          console.warn(
            "[local-state-service] skipping SQLite round-trip because better-sqlite3 is built for a different ABI",
          );
          return;
        }
        throw error;
      }

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
                  {
                    kind: "problem",
                    problem: {
                      problemId: "p1001",
                      title: "A+B",
                      difficulty: 2,
                      knowledgePoints: ["基础语法"],
                      reason: "补基础",
                    },
                  },
                  {
                    kind: "choice",
                    question: "下一步练什么？",
                    options: [
                      { id: "a", label: "数组" },
                      { id: "b", label: "链表" },
                    ],
                    answerIdx: 1,
                    explanation: "薄弱点更集中",
                  },
                  {
                    kind: "math_steps",
                    steps: [{ title: "半衰期", tex: "2^{-d / h}", note: "用于衰减" }],
                  },
                  { kind: "code", lang: "python", code: "print('ok')" },
                  { kind: "node_ref", point: "array.basic", label: "数组基础" },
                  { kind: "callout", tone: "tip", markdown: "**先补数组**" },
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
        {
          kind: "problem",
          problem: {
            problemId: "p1001",
            title: "A+B",
            difficulty: 2,
            knowledgePoints: ["基础语法"],
            reason: "补基础",
          },
        },
        {
          kind: "choice",
          question: "下一步练什么？",
          options: [
            { id: "a", label: "数组" },
            { id: "b", label: "链表" },
          ],
          answerIdx: 1,
          explanation: "薄弱点更集中",
        },
        {
          kind: "math_steps",
          steps: [{ title: "半衰期", tex: "2^{-d / h}", note: "用于衰减" }],
        },
        { kind: "code", lang: "python", code: "print('ok')" },
        { kind: "node_ref", point: "array.basic", label: "数组基础" },
        { kind: "callout", tone: "tip", markdown: "**先补数组**" },
      ]);
    } finally {
      service?.close();
      rmSync(dir, { recursive: true, force: true });
    }
  });
});
