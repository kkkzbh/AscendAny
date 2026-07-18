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
export type LocalChatBlockSnapshot = {
    kind: "text";
    text: string;
} | {
    kind: "tool";
    activity: LocalToolActivitySnapshot;
} | {
    kind: "problem";
    problem: LocalProblemRefSnapshot;
} | {
    kind: "choice";
    question: string;
    options: LocalChoiceOptionSnapshot[];
    answerIdx?: number;
    explanation?: string;
} | {
    kind: "math_steps";
    steps: LocalMathStepSnapshot[];
} | {
    kind: "code";
    lang: string;
    code: string;
} | {
    kind: "node_ref";
    point: string;
    label?: string;
} | {
    kind: "callout";
    tone: ChatCalloutTone;
    markdown: string;
};
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
export declare const NOTES_MAX_LENGTH = 32768;
export declare const NOTES_TITLE_MAX_LENGTH = 120;
export declare function deriveAutoNoteTitle(content: string): string;
export declare class LocalStateService {
    private db;
    constructor(dbPath: string);
    close(): void;
    hydrate(): LocalStateSnapshot;
    getOpaqueSidebarBackground(): boolean;
    saveSettings(value: unknown): boolean;
    saveLayout(value: unknown): boolean;
    saveChat(value: unknown): boolean;
    upsertNote(payload: unknown): LocalNoteSnapshot | null;
    createNote(): LocalNoteSnapshot;
    deleteNote(id: unknown): {
        activeNoteId: string;
    } | null;
    setActiveNote(id: unknown): boolean;
    clearNoteContent(id: unknown): LocalNoteSnapshot | null;
    bindActiveProfile(payload: unknown): LocalProfileSnapshot | null;
    private applyMigrations;
    private ensureActiveProfile;
    private getProfileRow;
    private profileFromRow;
    private touchProfile;
    private getSettings;
    private getLayout;
    private getChat;
    private getNotes;
    private fetchNote;
    private createNoteUnsafe;
    private activeNoteKey;
    private upsertProfileSetting;
    private getAppState;
    private setAppState;
    private activeSessionKey;
    private chatDraftsKey;
}
export {};
