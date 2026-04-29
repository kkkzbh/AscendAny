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
export declare class LocalStateService {
    private db;
    constructor(dbPath: string);
    close(): void;
    hydrate(): LocalStateSnapshot;
    getOpaqueSidebarBackground(): boolean;
    saveSettings(value: unknown): boolean;
    saveLayout(value: unknown): boolean;
    saveChat(value: unknown): boolean;
    bindActiveProfile(payload: unknown): LocalProfileSnapshot | null;
    private applyMigrations;
    private ensureActiveProfile;
    private getProfileRow;
    private profileFromRow;
    private touchProfile;
    private getSettings;
    private getLayout;
    private getChat;
    private upsertProfileSetting;
    private getAppState;
    private setAppState;
    private activeSessionKey;
}
export {};
