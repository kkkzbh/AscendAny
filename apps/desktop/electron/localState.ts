import Database from "better-sqlite3";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

type ThemeMode = "light" | "dark";
type RightPanelTab = "ability" | "history" | "notes";
type ChatRole = "user" | "assistant" | "system";
type ToolActivityStatus = "running" | "done" | "error";
type ChatCalloutTone = "info" | "warn" | "tip";

export interface LocalProfileSnapshot {
  id: string;
  accountId: string | null;
  username: string | null;
  displayName: string | null;
  createdAt: number;
  updatedAt: number;
  lastUsedAt: number;
}

export interface LocalSettingsSnapshot {
  theme: ThemeMode;
  useOpaqueSidebarBackground: boolean;
  zoomPercent: number;
  activeRole: string;
}

export interface LocalLayoutSnapshot {
  isLeftSidebarCollapsed: boolean;
  leftSidebarRatio: number;
  isMetricsPanelVisible: boolean;
  activeRightPanelTab: RightPanelTab;
  rightPanelRatio: number;
  activeFullscreenView: "none" | "achievements";
}

export interface LocalChatMessageSnapshot {
  id: string;
  role: ChatRole;
  content: string;
  timestamp: number;
  blocks?: LocalChatBlockSnapshot[];
  roleId?: string;
  reasoningContent?: string;
  reasoningStartedAt?: number;
  reasoningEndedAt?: number;
  toolActivities?: LocalToolActivitySnapshot[];
  streaming?: boolean;
  reasoningStreaming?: boolean;
}

export interface LocalToolActivitySnapshot {
  id: string;
  label: string;
  status: ToolActivityStatus;
}

export interface LocalProblemRefSnapshot {
  problemId: string;
  title: string | null;
  difficulty: number | null;
  knowledgePoints: string[];
  reason: string | null;
}

export interface LocalChoiceOptionSnapshot {
  id: string;
  label: string;
}

export interface LocalMathStepSnapshot {
  title?: string;
  tex: string;
  note?: string;
}

export type LocalChatBlockSnapshot =
  | { kind: "text"; text: string }
  | { kind: "tool"; activity: LocalToolActivitySnapshot }
  | { kind: "problem"; problem: LocalProblemRefSnapshot }
  | {
      kind: "choice";
      question: string;
      options: LocalChoiceOptionSnapshot[];
      answerIdx?: number;
      explanation?: string;
    }
  | { kind: "math_steps"; steps: LocalMathStepSnapshot[] }
  | { kind: "code"; lang: string; code: string }
  | { kind: "node_ref"; point: string; label?: string }
  | { kind: "callout"; tone: ChatCalloutTone; markdown: string };

export interface LocalChatSessionSnapshot {
  id: string;
  title: string;
  messages: LocalChatMessageSnapshot[];
  summary: string;
  draft?: string;
  createdAt: number;
  updatedAt: number;
}

export interface LocalChatSnapshot {
  sessions: LocalChatSessionSnapshot[];
  activeSessionId: string | null;
  newSessionDraft: string;
}

export interface LocalNoteSnapshot {
  id: string;
  title: string;
  content: string;
  titleIsAuto: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface LocalNotesSnapshot {
  items: LocalNoteSnapshot[];
  activeNoteId: string;
}

export interface LocalStateSnapshot {
  profile: LocalProfileSnapshot;
  settings: LocalSettingsSnapshot;
  layout: LocalLayoutSnapshot;
  chat: LocalChatSnapshot;
  notes: LocalNotesSnapshot;
}

const SCHEMA_VERSION = 2;
const SETTINGS_KEY = "app_settings";
const ACTIVE_PROFILE_KEY = "active_profile_id";
const DEFAULT_ROLE_ID = "sakiko";
export const NOTES_MAX_LENGTH = 32_768;
export const NOTES_TITLE_MAX_LENGTH = 120;

const DEFAULT_SETTINGS: LocalSettingsSnapshot = {
  theme: "light",
  useOpaqueSidebarBackground: true,
  zoomPercent: 100,
  activeRole: DEFAULT_ROLE_ID,
};

const DEFAULT_LAYOUT: LocalLayoutSnapshot = {
  isLeftSidebarCollapsed: false,
  leftSidebarRatio: 0.22,
  isMetricsPanelVisible: true,
  activeRightPanelTab: "ability",
  rightPanelRatio: 0.36,
  activeFullscreenView: "none",
};

function nowMs(): number {
  return Date.now();
}

function newId(prefix: string): string {
  return `${prefix}_${crypto.randomUUID()}`;
}

function safeJsonParse(value: string | null): unknown {
  if (!value) {
    return null;
  }
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return null;
  }
}

function jsonString(value: unknown): string {
  return JSON.stringify(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function stringOrNull(value: unknown): string | null {
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

function normalizeTheme(value: unknown): ThemeMode {
  return value === "dark" ? "dark" : "light";
}

function normalizeZoomPercent(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_SETTINGS.zoomPercent;
  }
  const rounded = Math.round(value / 5) * 5;
  return Math.min(130, Math.max(80, rounded));
}

function normalizeSettings(value: unknown): LocalSettingsSnapshot {
  const input = isRecord(value) ? value : {};
  return {
    theme: normalizeTheme(input.theme),
    useOpaqueSidebarBackground:
      typeof input.useOpaqueSidebarBackground === "boolean"
        ? input.useOpaqueSidebarBackground
        : DEFAULT_SETTINGS.useOpaqueSidebarBackground,
    zoomPercent: normalizeZoomPercent(input.zoomPercent),
    activeRole: stringOrNull(input.activeRole) ?? DEFAULT_SETTINGS.activeRole,
  };
}

function normalizeLeftSidebarRatio(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_LAYOUT.leftSidebarRatio;
  }
  return Math.max(0.17, Math.min(0.32, value));
}

function normalizeRightPanelRatio(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_LAYOUT.rightPanelRatio;
  }
  return Math.max(0.32, Math.min(0.5, value));
}

function normalizeLayout(value: unknown): LocalLayoutSnapshot {
  const input = isRecord(value) ? value : {};
  return {
    isLeftSidebarCollapsed:
      typeof input.isLeftSidebarCollapsed === "boolean"
        ? input.isLeftSidebarCollapsed
        : DEFAULT_LAYOUT.isLeftSidebarCollapsed,
    leftSidebarRatio: normalizeLeftSidebarRatio(input.leftSidebarRatio),
    isMetricsPanelVisible:
      typeof input.isMetricsPanelVisible === "boolean"
        ? input.isMetricsPanelVisible
        : DEFAULT_LAYOUT.isMetricsPanelVisible,
    activeRightPanelTab:
      input.activeRightPanelTab === "history"
        ? "history"
        : input.activeRightPanelTab === "notes"
        ? "notes"
        : "ability",
    rightPanelRatio: normalizeRightPanelRatio(input.rightPanelRatio ?? input.splitRatio),
    activeFullscreenView: input.activeFullscreenView === "achievements" ? "achievements" : "none",
  };
}

function normalizeMessage(value: unknown): LocalChatMessageSnapshot {
  const input = isRecord(value) ? value : {};
  const role: ChatRole =
    input.role === "user" || input.role === "assistant" || input.role === "system"
      ? input.role
      : "system";
  const reasoningStartedAt =
    typeof input.reasoningStartedAt === "number" && Number.isFinite(input.reasoningStartedAt)
      ? input.reasoningStartedAt
      : undefined;
  const reasoningEndedAt =
    typeof input.reasoningEndedAt === "number" && Number.isFinite(input.reasoningEndedAt)
      ? input.reasoningEndedAt
      : undefined;
  return {
    id: stringOrNull(input.id) ?? newId("msg"),
    role,
    content: typeof input.content === "string" ? input.content : "",
    timestamp:
      typeof input.timestamp === "number" && Number.isFinite(input.timestamp)
        ? input.timestamp
        : nowMs(),
    blocks: normalizeChatBlocks(input.blocks),
    roleId: typeof input.roleId === "string" ? input.roleId : undefined,
    reasoningContent:
      typeof input.reasoningContent === "string" ? input.reasoningContent : undefined,
    reasoningStartedAt,
    reasoningEndedAt,
    toolActivities: normalizeToolActivities(input.toolActivities),
    streaming: typeof input.streaming === "boolean" ? input.streaming : false,
    reasoningStreaming:
      typeof input.reasoningStreaming === "boolean" ? input.reasoningStreaming : false,
  };
}

function normalizeChatBlocks(value: unknown): LocalChatBlockSnapshot[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const blocks = value
    .map(normalizeChatBlock)
    .filter((block): block is LocalChatBlockSnapshot => block !== null);
  return blocks.length > 0 ? blocks : undefined;
}

function normalizeChatBlock(value: unknown): LocalChatBlockSnapshot | null {
  if (!isRecord(value)) {
    return null;
  }
  if (value.kind === "text") {
    return typeof value.text === "string" && value.text.length > 0
      ? { kind: "text", text: value.text }
      : null;
  }
  if (value.kind === "tool") {
    const activity = normalizeToolActivity(value.activity, { allowRunning: true });
    return activity ? { kind: "tool", activity } : null;
  }
  if (value.kind === "problem") {
    const raw = isRecord(value.problem) ? value.problem : null;
    if (!raw) return null;
    const problemId = stringOrNull(raw.problemId);
    if (!problemId) return null;
    const knowledgePoints = Array.isArray(raw.knowledgePoints)
      ? raw.knowledgePoints
          .filter((point): point is string => typeof point === "string")
          .map((point) => point.trim())
          .filter(Boolean)
      : [];
    return {
      kind: "problem",
      problem: {
        problemId,
        title: typeof raw.title === "string" ? raw.title : null,
        difficulty: typeof raw.difficulty === "number" ? raw.difficulty : null,
        knowledgePoints,
        reason: typeof raw.reason === "string" ? raw.reason : null,
      },
    };
  }
  if (value.kind === "choice") {
    const question = stringOrNull(value.question);
    const optionsRaw = Array.isArray(value.options) ? value.options : [];
    if (!question || optionsRaw.length === 0) return null;
    const options = optionsRaw
      .filter((option): option is Record<string, unknown> => isRecord(option))
      .map((option): LocalChoiceOptionSnapshot | null => {
        const id = stringOrNull(option.id);
        const label = stringOrNull(option.label);
        return id && label ? { id, label } : null;
      })
      .filter((option): option is LocalChoiceOptionSnapshot => option !== null);
    if (options.length === 0) return null;
    const answerIdx =
      typeof value.answerIdx === "number" &&
      value.answerIdx >= 0 &&
      value.answerIdx < options.length
        ? value.answerIdx
        : undefined;
    const explanation = stringOrNull(value.explanation) ?? undefined;
    return {
      kind: "choice",
      question,
      options,
      answerIdx,
      explanation,
    };
  }
  if (value.kind === "math_steps") {
    const stepsRaw = Array.isArray(value.steps) ? value.steps : [];
    const steps = stepsRaw
      .filter((step): step is Record<string, unknown> => isRecord(step))
      .map((step): LocalMathStepSnapshot | null => {
        const tex = stringOrNull(step.tex);
        if (!tex) return null;
        const out: LocalMathStepSnapshot = { tex };
        const title = stringOrNull(step.title);
        const note = stringOrNull(step.note);
        if (title) out.title = title;
        if (note) out.note = note;
        return out;
      })
      .filter((step): step is LocalMathStepSnapshot => step !== null);
    return steps.length > 0 ? { kind: "math_steps", steps } : null;
  }
  if (value.kind === "code") {
    if (typeof value.code !== "string" || !value.code) return null;
    return {
      kind: "code",
      lang: stringOrNull(value.lang) ?? "text",
      code: value.code,
    };
  }
  if (value.kind === "node_ref") {
    const point = stringOrNull(value.point);
    if (!point) return null;
    return {
      kind: "node_ref",
      point,
      label: stringOrNull(value.label) ?? undefined,
    };
  }
  if (value.kind === "callout") {
    const markdown = stringOrNull(value.markdown);
    if (!markdown) return null;
    const tone: ChatCalloutTone =
      value.tone === "warn" ? "warn" : value.tone === "tip" ? "tip" : "info";
    return { kind: "callout", tone, markdown };
  }
  return null;
}

function normalizeToolActivities(value: unknown): LocalToolActivitySnapshot[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const items = value
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .map((item): LocalToolActivitySnapshot | null => normalizeToolActivity(item))
    .filter((item): item is LocalToolActivitySnapshot => item !== null);
  return items.length > 0 ? items : undefined;
}

function normalizeToolActivity(
  value: unknown,
  options: { allowRunning?: boolean } = {},
): LocalToolActivitySnapshot | null {
  if (!isRecord(value)) {
    return null;
  }
  const id = stringOrNull(value.id);
  const label = stringOrNull(value.label);
  if (!id || !label) {
    return null;
  }
  const status: ToolActivityStatus =
    value.status === "error"
      ? "error"
      : options.allowRunning && value.status === "running"
        ? "running"
        : "done";
  return { id, label, status };
}

function normalizeSession(value: unknown): LocalChatSessionSnapshot {
  const input = isRecord(value) ? value : {};
  const messages = Array.isArray(input.messages) ? input.messages.map(normalizeMessage) : [];
  const createdAt =
    typeof input.createdAt === "number" && Number.isFinite(input.createdAt)
      ? input.createdAt
      : nowMs();
  const updatedAt =
    typeof input.updatedAt === "number" && Number.isFinite(input.updatedAt)
      ? input.updatedAt
      : createdAt;
  return {
    id: stringOrNull(input.id) ?? newId("session"),
    title: stringOrNull(input.title) ?? "新对话",
    messages,
    summary: typeof input.summary === "string" ? input.summary : "",
    draft: typeof input.draft === "string" ? input.draft : "",
    createdAt,
    updatedAt,
  };
}

function normalizeChat(value: unknown): LocalChatSnapshot {
  const input = isRecord(value) ? value : {};
  const sessions = Array.isArray(input.sessions)
    ? input.sessions.map(normalizeSession)
    : [];
  const requestedActiveId = stringOrNull(input.activeSessionId);
  const activeSessionId =
    requestedActiveId && sessions.some((session) => session.id === requestedActiveId)
      ? requestedActiveId
      : sessions.length > 0 && input.activeSessionId !== null
        ? sessions[0]!.id
        : null;
  return {
    sessions,
    activeSessionId,
    newSessionDraft: typeof input.newSessionDraft === "string" ? input.newSessionDraft : "",
  };
}

interface LocalChatDraftState {
  newSessionDraft: string;
  sessionDrafts: Record<string, string>;
}

function normalizeChatDraftState(value: unknown): LocalChatDraftState {
  const input = isRecord(value) ? value : {};
  const rawSessionDrafts = isRecord(input.sessionDrafts) ? input.sessionDrafts : {};
  const sessionDrafts: Record<string, string> = {};
  for (const [key, draft] of Object.entries(rawSessionDrafts)) {
    if (typeof draft === "string") {
      sessionDrafts[key] = draft;
    }
  }
  return {
    newSessionDraft: typeof input.newSessionDraft === "string" ? input.newSessionDraft : "",
    sessionDrafts,
  };
}

interface ProfileRow {
  id: string;
  account_id: string | null;
  username: string | null;
  display_name: string | null;
  created_at: number;
  updated_at: number;
  last_used_at: number;
}

interface ValueRow {
  value_json: string;
}

interface SessionRow {
  id: string;
  title: string;
  summary: string;
  created_at: number;
  updated_at: number;
}

interface MessageRow {
  content_json: string;
}

interface NoteRow {
  id: string;
  title: string;
  content: string;
  title_is_auto: number;
  created_at: number;
  updated_at: number;
}

const STRIP_MD_PATTERNS: Array<[RegExp, string]> = [
  [/^#{1,6}\s+/m, ""],
  [/^>\s+/gm, ""],
  [/^[-*+]\s+/gm, ""],
  [/^\d+\.\s+/gm, ""],
  [/`{1,3}([^`]*)`{1,3}/g, "$1"],
  [/!\[[^\]]*\]\([^)]*\)/g, ""],
  [/\[([^\]]+)\]\([^)]*\)/g, "$1"],
  [/[*_~]+/g, ""],
];

function stripMarkdownInline(value: string): string {
  let text = value;
  for (const [pattern, replacement] of STRIP_MD_PATTERNS) {
    text = text.replace(pattern, replacement);
  }
  return text.trim();
}

export function deriveAutoNoteTitle(content: string): string {
  const text = (content || "").trim();
  if (!text) {
    return "";
  }
  const lines = text.split(/\r?\n/);
  for (const raw of lines) {
    const line = raw.trim();
    if (!line) {
      continue;
    }
    const heading = /^#{1,6}\s+(.+)$/.exec(line);
    if (heading) {
      return stripMarkdownInline(heading[1]).slice(0, NOTES_TITLE_MAX_LENGTH);
    }
    return stripMarkdownInline(line).slice(0, 30);
  }
  return "";
}

function clampNoteContent(value: unknown): string {
  if (typeof value !== "string") {
    return "";
  }
  if (value.length <= NOTES_MAX_LENGTH) {
    return value;
  }
  return value.slice(0, NOTES_MAX_LENGTH);
}

function clampNoteTitle(value: unknown): string {
  if (typeof value !== "string") {
    return "";
  }
  return value.slice(0, NOTES_TITLE_MAX_LENGTH);
}

function noteFromRow(row: NoteRow): LocalNoteSnapshot {
  return {
    id: row.id,
    title: row.title ?? "",
    content: row.content ?? "",
    titleIsAuto: row.title_is_auto !== 0,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
  };
}

export class LocalStateService {
  private db: Database.Database;

  constructor(dbPath: string) {
    fs.mkdirSync(path.dirname(dbPath), { recursive: true });
    this.db = new Database(dbPath);
    this.db.pragma("journal_mode = WAL");
    this.db.pragma("busy_timeout = 5000");
    this.applyMigrations();
    this.ensureActiveProfile();
  }

  close(): void {
    this.db.close();
  }

  hydrate(): LocalStateSnapshot {
    const profile = this.ensureActiveProfile();
    return {
      profile,
      settings: this.getSettings(profile.id),
      layout: this.getLayout(profile.id),
      chat: this.getChat(profile.id),
      notes: this.getNotes(profile.id),
    };
  }

  getOpaqueSidebarBackground(): boolean {
    return this.hydrate().settings.useOpaqueSidebarBackground;
  }

  saveSettings(value: unknown): boolean {
    const profile = this.ensureActiveProfile();
    const settings = normalizeSettings(value);
    this.upsertProfileSetting(profile.id, SETTINGS_KEY, settings);
    return true;
  }

  saveLayout(value: unknown): boolean {
    const profile = this.ensureActiveProfile();
    const layout = normalizeLayout(value);
    const timestamp = nowMs();
    this.db.prepare(`
      INSERT INTO layout_state (profile_id, value_json, updated_at)
      VALUES (?, ?, ?)
      ON CONFLICT(profile_id) DO UPDATE SET
        value_json = excluded.value_json,
        updated_at = excluded.updated_at
    `).run(profile.id, jsonString(layout), timestamp);
    return true;
  }

  saveChat(value: unknown): boolean {
    const profile = this.ensureActiveProfile();
    const chat = normalizeChat(value);
    const activeKey = this.activeSessionKey(profile.id);
    const draftsKey = this.chatDraftsKey(profile.id);
    const transaction = this.db.transaction(() => {
      this.db.prepare(`
        DELETE FROM chat_messages
        WHERE session_id IN (SELECT id FROM chat_sessions WHERE profile_id = ?)
      `).run(profile.id);
      this.db.prepare("DELETE FROM chat_sessions WHERE profile_id = ?").run(profile.id);

      const insertSession = this.db.prepare(`
        INSERT INTO chat_sessions (
          id, profile_id, title, active_role, summary, created_at, updated_at, archived_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, NULL)
      `);
      const insertMessage = this.db.prepare(`
        INSERT INTO chat_messages (
          id, session_id, seq, role, content_json, metadata_json, created_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?)
      `);

      for (const session of chat.sessions) {
        insertSession.run(
          session.id,
          profile.id,
          session.title,
          null,
          session.summary,
          session.createdAt,
          session.updatedAt,
        );
        session.messages.forEach((message, index) => {
          insertMessage.run(
            message.id,
            session.id,
            index,
            message.role,
            jsonString(message),
            jsonString({ roleId: message.roleId ?? null }),
            message.timestamp,
          );
        });
      }

      this.setAppState(activeKey, chat.activeSessionId);
      this.setAppState(draftsKey, {
        newSessionDraft: chat.newSessionDraft,
        sessionDrafts: Object.fromEntries(
          chat.sessions.map((session) => [session.id, session.draft ?? ""]),
        ),
      } satisfies LocalChatDraftState);
    });
    transaction();
    return true;
  }

  upsertNote(payload: unknown): LocalNoteSnapshot | null {
    if (!isRecord(payload)) {
      return null;
    }
    const id = stringOrNull(payload.id);
    if (!id) {
      return null;
    }
    const profile = this.ensureActiveProfile();
    const existing = this.db
      .prepare("SELECT * FROM notes WHERE id = ? AND profile_id = ?")
      .get(id, profile.id) as NoteRow | undefined;

    const title = clampNoteTitle(payload.title);
    const content = clampNoteContent(payload.content);
    const titleIsAuto =
      typeof payload.titleIsAuto === "boolean"
        ? (payload.titleIsAuto ? 1 : 0)
        : existing
        ? existing.title_is_auto
        : 1;
    const timestamp = nowMs();

    if (existing) {
      this.db
        .prepare(`
          UPDATE notes
          SET title = ?, content = ?, title_is_auto = ?, updated_at = ?
          WHERE id = ? AND profile_id = ?
        `)
        .run(title, content, titleIsAuto, timestamp, id, profile.id);
    } else {
      this.db
        .prepare(`
          INSERT INTO notes (id, profile_id, title, content, title_is_auto, created_at, updated_at)
          VALUES (?, ?, ?, ?, ?, ?, ?)
        `)
        .run(id, profile.id, title, content, titleIsAuto, timestamp, timestamp);
    }
    return this.fetchNote(id, profile.id);
  }

  createNote(): LocalNoteSnapshot {
    const profile = this.ensureActiveProfile();
    const id = newId("note");
    const timestamp = nowMs();
    this.db
      .prepare(`
        INSERT INTO notes (id, profile_id, title, content, title_is_auto, created_at, updated_at)
        VALUES (?, ?, '', '', 1, ?, ?)
      `)
      .run(id, profile.id, timestamp, timestamp);
    this.setAppState(this.activeNoteKey(profile.id), id);
    return this.fetchNote(id, profile.id)!;
  }

  deleteNote(id: unknown): { activeNoteId: string } | null {
    const noteId = stringOrNull(id);
    if (!noteId) {
      return null;
    }
    const profile = this.ensureActiveProfile();
    const transaction = this.db.transaction(() => {
      this.db
        .prepare("DELETE FROM notes WHERE id = ? AND profile_id = ?")
        .run(noteId, profile.id);
      const remaining = this.db
        .prepare(
          "SELECT id FROM notes WHERE profile_id = ? ORDER BY updated_at DESC LIMIT 1",
        )
        .get(profile.id) as { id: string } | undefined;
      let activeId: string;
      if (remaining) {
        activeId = remaining.id;
      } else {
        const replacement = this.createNoteUnsafe(profile.id);
        activeId = replacement.id;
      }
      this.setAppState(this.activeNoteKey(profile.id), activeId);
      return activeId;
    });
    const activeNoteId = transaction();
    return { activeNoteId };
  }

  setActiveNote(id: unknown): boolean {
    const noteId = stringOrNull(id);
    if (!noteId) {
      return false;
    }
    const profile = this.ensureActiveProfile();
    const exists = this.db
      .prepare("SELECT id FROM notes WHERE id = ? AND profile_id = ?")
      .get(noteId, profile.id);
    if (!exists) {
      return false;
    }
    this.setAppState(this.activeNoteKey(profile.id), noteId);
    return true;
  }

  clearNoteContent(id: unknown): LocalNoteSnapshot | null {
    const noteId = stringOrNull(id);
    if (!noteId) {
      return null;
    }
    const profile = this.ensureActiveProfile();
    const timestamp = nowMs();
    this.db
      .prepare(`
        UPDATE notes
        SET content = '', updated_at = ?
        WHERE id = ? AND profile_id = ?
      `)
      .run(timestamp, noteId, profile.id);
    return this.fetchNote(noteId, profile.id);
  }

  bindActiveProfile(payload: unknown): LocalProfileSnapshot | null {
    if (!isRecord(payload)) {
      return null;
    }
    const accountId = stringOrNull(payload.accountId);
    if (!accountId) {
      return null;
    }
    const profile = this.ensureActiveProfile();
    const timestamp = nowMs();
    this.db.prepare(`
      UPDATE profiles
      SET account_id = ?, username = ?, display_name = ?, updated_at = ?, last_used_at = ?
      WHERE id = ?
    `).run(
      accountId,
      stringOrNull(payload.username),
      stringOrNull(payload.displayName),
      timestamp,
      timestamp,
      profile.id,
    );
    return this.ensureActiveProfile();
  }

  private applyMigrations(): void {
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        applied_at INTEGER NOT NULL
      );
    `);

    const migrated = this.db
      .prepare("SELECT version FROM schema_migrations WHERE version = ?")
      .get(SCHEMA_VERSION);
    if (migrated) {
      return;
    }

    const transaction = this.db.transaction(() => {
      this.db.exec(`
        CREATE TABLE IF NOT EXISTS profiles (
          id TEXT PRIMARY KEY,
          account_id TEXT,
          username TEXT,
          display_name TEXT,
          created_at INTEGER NOT NULL,
          updated_at INTEGER NOT NULL,
          last_used_at INTEGER NOT NULL
        );

        CREATE TABLE IF NOT EXISTS app_state (
          key TEXT PRIMARY KEY,
          value_json TEXT NOT NULL,
          updated_at INTEGER NOT NULL
        );

        CREATE TABLE IF NOT EXISTS profile_settings (
          profile_id TEXT NOT NULL,
          key TEXT NOT NULL,
          value_json TEXT NOT NULL,
          updated_at INTEGER NOT NULL,
          PRIMARY KEY (profile_id, key),
          FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS layout_state (
          profile_id TEXT PRIMARY KEY,
          value_json TEXT NOT NULL,
          updated_at INTEGER NOT NULL,
          FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS chat_sessions (
          id TEXT PRIMARY KEY,
          profile_id TEXT NOT NULL,
          title TEXT NOT NULL,
          active_role TEXT,
          summary TEXT NOT NULL DEFAULT '',
          created_at INTEGER NOT NULL,
          updated_at INTEGER NOT NULL,
          archived_at INTEGER,
          FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS chat_messages (
          id TEXT PRIMARY KEY,
          session_id TEXT NOT NULL,
          seq INTEGER NOT NULL,
          role TEXT NOT NULL,
          content_json TEXT NOT NULL,
          metadata_json TEXT NOT NULL,
          created_at INTEGER NOT NULL,
          FOREIGN KEY (session_id) REFERENCES chat_sessions(id) ON DELETE CASCADE
        );

        CREATE INDEX IF NOT EXISTS idx_chat_sessions_profile_updated
          ON chat_sessions(profile_id, archived_at, updated_at DESC);
        CREATE INDEX IF NOT EXISTS idx_chat_messages_session_seq
          ON chat_messages(session_id, seq ASC);

        CREATE TABLE IF NOT EXISTS notes (
          id TEXT PRIMARY KEY,
          profile_id TEXT NOT NULL,
          title TEXT NOT NULL DEFAULT '',
          content TEXT NOT NULL DEFAULT '',
          title_is_auto INTEGER NOT NULL DEFAULT 1,
          created_at INTEGER NOT NULL,
          updated_at INTEGER NOT NULL,
          FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
        );

        CREATE INDEX IF NOT EXISTS idx_notes_profile_updated
          ON notes(profile_id, updated_at DESC);
      `);
      this.db.prepare("INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)")
        .run(SCHEMA_VERSION, nowMs());
    });
    transaction();
  }

  private ensureActiveProfile(): LocalProfileSnapshot {
    const activeProfileId = stringOrNull(this.getAppState(ACTIVE_PROFILE_KEY));
    if (activeProfileId) {
      const row = this.getProfileRow(activeProfileId);
      if (row) {
        this.touchProfile(row.id);
        return this.profileFromRow(this.getProfileRow(row.id)!);
      }
    }

    const existing = this.db.prepare("SELECT * FROM profiles ORDER BY last_used_at DESC LIMIT 1")
      .get() as ProfileRow | undefined;
    if (existing) {
      this.setAppState(ACTIVE_PROFILE_KEY, existing.id);
      this.touchProfile(existing.id);
      return this.profileFromRow(this.getProfileRow(existing.id)!);
    }

    const timestamp = nowMs();
    const id = newId("profile");
    this.db.prepare(`
      INSERT INTO profiles (id, account_id, username, display_name, created_at, updated_at, last_used_at)
      VALUES (?, NULL, NULL, ?, ?, ?, ?)
    `).run(id, "本地资料", timestamp, timestamp, timestamp);
    this.setAppState(ACTIVE_PROFILE_KEY, id);
    return this.profileFromRow(this.getProfileRow(id)!);
  }

  private getProfileRow(id: string): ProfileRow | undefined {
    return this.db.prepare("SELECT * FROM profiles WHERE id = ?").get(id) as ProfileRow | undefined;
  }

  private profileFromRow(row: ProfileRow): LocalProfileSnapshot {
    return {
      id: row.id,
      accountId: row.account_id,
      username: row.username,
      displayName: row.display_name,
      createdAt: row.created_at,
      updatedAt: row.updated_at,
      lastUsedAt: row.last_used_at,
    };
  }

  private touchProfile(profileId: string): void {
    const timestamp = nowMs();
    this.db.prepare("UPDATE profiles SET last_used_at = ? WHERE id = ?").run(timestamp, profileId);
  }

  private getSettings(profileId: string): LocalSettingsSnapshot {
    const row = this.db.prepare(`
      SELECT value_json FROM profile_settings WHERE profile_id = ? AND key = ?
    `).get(profileId, SETTINGS_KEY) as ValueRow | undefined;
    return normalizeSettings(safeJsonParse(row?.value_json ?? null));
  }

  private getLayout(profileId: string): LocalLayoutSnapshot {
    const row = this.db.prepare("SELECT value_json FROM layout_state WHERE profile_id = ?")
      .get(profileId) as ValueRow | undefined;
    return normalizeLayout(safeJsonParse(row?.value_json ?? null));
  }

  private getChat(profileId: string): LocalChatSnapshot {
    const sessionRows = this.db.prepare(`
      SELECT id, title, summary, created_at, updated_at
      FROM chat_sessions
      WHERE profile_id = ? AND archived_at IS NULL
      ORDER BY updated_at DESC, created_at DESC
    `).all(profileId) as SessionRow[];
    const draftState = normalizeChatDraftState(this.getAppState(this.chatDraftsKey(profileId)));

    const messageQuery = this.db.prepare(`
      SELECT content_json
      FROM chat_messages
      WHERE session_id = ?
      ORDER BY seq ASC
    `);
    const sessions = sessionRows.map((session): LocalChatSessionSnapshot => {
      const messageRows = messageQuery.all(session.id) as MessageRow[];
      return {
        id: session.id,
        title: session.title,
        summary: session.summary,
        draft: draftState.sessionDrafts[session.id] ?? "",
        createdAt: session.created_at,
        updatedAt: session.updated_at,
        messages: messageRows.map((row) => normalizeMessage(safeJsonParse(row.content_json))),
      };
    });
    return normalizeChat({
      sessions,
      activeSessionId: this.getAppState(this.activeSessionKey(profileId)),
      newSessionDraft: draftState.newSessionDraft,
    });
  }

  private getNotes(profileId: string): LocalNotesSnapshot {
    const rows = this.db
      .prepare(
        "SELECT * FROM notes WHERE profile_id = ? ORDER BY updated_at DESC, created_at DESC",
      )
      .all(profileId) as NoteRow[];
    let items = rows.map(noteFromRow);
    if (items.length === 0) {
      const created = this.createNoteUnsafe(profileId);
      items = [created];
    }
    const requested = stringOrNull(this.getAppState(this.activeNoteKey(profileId)));
    const activeNoteId =
      requested && items.some((note) => note.id === requested)
        ? requested
        : items[0]!.id;
    if (activeNoteId !== requested) {
      this.setAppState(this.activeNoteKey(profileId), activeNoteId);
    }
    return { items, activeNoteId };
  }

  private fetchNote(id: string, profileId: string): LocalNoteSnapshot | null {
    const row = this.db
      .prepare("SELECT * FROM notes WHERE id = ? AND profile_id = ?")
      .get(id, profileId) as NoteRow | undefined;
    return row ? noteFromRow(row) : null;
  }

  private createNoteUnsafe(profileId: string): LocalNoteSnapshot {
    const id = newId("note");
    const timestamp = nowMs();
    this.db
      .prepare(`
        INSERT INTO notes (id, profile_id, title, content, title_is_auto, created_at, updated_at)
        VALUES (?, ?, '', '', 1, ?, ?)
      `)
      .run(id, profileId, timestamp, timestamp);
    return this.fetchNote(id, profileId)!;
  }

  private activeNoteKey(profileId: string): string {
    return `active_note_id:${profileId}`;
  }

  private upsertProfileSetting(profileId: string, key: string, value: unknown): void {
    const timestamp = nowMs();
    this.db.prepare(`
      INSERT INTO profile_settings (profile_id, key, value_json, updated_at)
      VALUES (?, ?, ?, ?)
      ON CONFLICT(profile_id, key) DO UPDATE SET
        value_json = excluded.value_json,
        updated_at = excluded.updated_at
    `).run(profileId, key, jsonString(value), timestamp);
  }

  private getAppState(key: string): unknown {
    const row = this.db.prepare("SELECT value_json FROM app_state WHERE key = ?")
      .get(key) as ValueRow | undefined;
    return safeJsonParse(row?.value_json ?? null);
  }

  private setAppState(key: string, value: unknown): void {
    const timestamp = nowMs();
    this.db.prepare(`
      INSERT INTO app_state (key, value_json, updated_at)
      VALUES (?, ?, ?)
      ON CONFLICT(key) DO UPDATE SET
        value_json = excluded.value_json,
        updated_at = excluded.updated_at
    `).run(key, jsonString(value), timestamp);
  }

  private activeSessionKey(profileId: string): string {
    return `active_session_id:${profileId}`;
  }

  private chatDraftsKey(profileId: string): string {
    return `chat_drafts:${profileId}`;
  }
}
