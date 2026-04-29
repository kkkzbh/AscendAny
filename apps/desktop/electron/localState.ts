import Database from "better-sqlite3";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

type ThemeMode = "light" | "dark";
type RightPanelTab = "ability" | "history";
type ChatRole = "user" | "assistant" | "system";

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
  splitRatio: number;
  activeFullscreenView: "none" | "achievements";
}

export interface LocalChatMessageSnapshot {
  id: string;
  role: ChatRole;
  content: string;
  timestamp: number;
  roleId?: string;
  streaming?: boolean;
}

export interface LocalChatSessionSnapshot {
  id: string;
  title: string;
  messages: LocalChatMessageSnapshot[];
  summary: string;
  createdAt: number;
  updatedAt: number;
}

export interface LocalChatSnapshot {
  sessions: LocalChatSessionSnapshot[];
  activeSessionId: string;
}

export interface LocalStateSnapshot {
  profile: LocalProfileSnapshot;
  settings: LocalSettingsSnapshot;
  layout: LocalLayoutSnapshot;
  chat: LocalChatSnapshot;
}

const SCHEMA_VERSION = 1;
const SETTINGS_KEY = "app_settings";
const ACTIVE_PROFILE_KEY = "active_profile_id";
const DEFAULT_ROLE_ID = "sakiko";

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
  splitRatio: 0.55,
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

function normalizeSplitRatio(value: unknown): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return DEFAULT_LAYOUT.splitRatio;
  }
  return Math.max(0.3, Math.min(0.7, value));
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
    activeRightPanelTab: input.activeRightPanelTab === "history" ? "history" : "ability",
    splitRatio: normalizeSplitRatio(input.splitRatio),
    activeFullscreenView: input.activeFullscreenView === "achievements" ? "achievements" : "none",
  };
}

function normalizeMessage(value: unknown): LocalChatMessageSnapshot {
  const input = isRecord(value) ? value : {};
  const role: ChatRole =
    input.role === "user" || input.role === "assistant" || input.role === "system"
      ? input.role
      : "system";
  return {
    id: stringOrNull(input.id) ?? newId("msg"),
    role,
    content: typeof input.content === "string" ? input.content : "",
    timestamp:
      typeof input.timestamp === "number" && Number.isFinite(input.timestamp)
        ? input.timestamp
        : nowMs(),
    roleId: typeof input.roleId === "string" ? input.roleId : undefined,
    streaming: typeof input.streaming === "boolean" ? input.streaming : false,
  };
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
    createdAt,
    updatedAt,
  };
}

function createEmptySession(): LocalChatSessionSnapshot {
  const timestamp = nowMs();
  return {
    id: newId("session"),
    title: "新对话",
    messages: [],
    summary: "",
    createdAt: timestamp,
    updatedAt: timestamp,
  };
}

function normalizeChat(value: unknown): LocalChatSnapshot {
  const input = isRecord(value) ? value : {};
  const sessions = Array.isArray(input.sessions)
    ? input.sessions.map(normalizeSession)
    : [];
  const safeSessions = sessions.length > 0 ? sessions : [createEmptySession()];
  const requestedActiveId = stringOrNull(input.activeSessionId);
  const activeSessionId =
    requestedActiveId && safeSessions.some((session) => session.id === requestedActiveId)
      ? requestedActiveId
      : safeSessions[0]!.id;
  return {
    sessions: safeSessions,
    activeSessionId,
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
    });
    transaction();
    return true;
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

    if (sessionRows.length === 0) {
      const session = createEmptySession();
      const chat = { sessions: [session], activeSessionId: session.id };
      this.saveChat(chat);
      return chat;
    }

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
        createdAt: session.created_at,
        updatedAt: session.updated_at,
        messages: messageRows.map((row) => normalizeMessage(safeJsonParse(row.content_json))),
      };
    });
    return normalizeChat({
      sessions,
      activeSessionId: this.getAppState(this.activeSessionKey(profileId)),
    });
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
}
