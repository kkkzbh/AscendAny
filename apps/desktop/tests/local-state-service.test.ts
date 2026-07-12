import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const SOURCE = readFileSync(resolve(process.cwd(), "electron/localState.ts"), "utf8");

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

});
