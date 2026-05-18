/// <reference types="vite/client" />

declare const __ASCENDANY_WEB_BUILD__: boolean;

type UpdateStatus =
  | "idle"
  | "checking"
  | "available"
  | "downloading"
  | "downloaded"
  | "up_to_date"
  | "error"
  | "disabled";

interface UpdateStateSnapshot {
  status: UpdateStatus;
  currentVersion: string;
  latestVersion: string | null;
  progressPercent: number | null;
  lastCheckedAt: string | null;
  message: string | null;
}

interface UpdateActionResult {
  success: boolean;
  message: string;
}

interface LocalProfileSnapshot {
  id: string;
  accountId: string | null;
  username: string | null;
  displayName: string | null;
  createdAt: number;
  updatedAt: number;
  lastUsedAt: number;
}

interface LocalNoteSnapshot {
  id: string;
  title: string;
  content: string;
  titleIsAuto: boolean;
  createdAt: number;
  updatedAt: number;
}

interface LocalNotesSnapshot {
  items: LocalNoteSnapshot[];
  activeNoteId: string;
}

interface LocalStateSnapshot {
  profile: LocalProfileSnapshot;
  settings: unknown;
  layout: unknown;
  chat: unknown;
  notes?: LocalNotesSnapshot;
}

interface NotesExportPdfResult {
  success: boolean;
  canceled?: boolean;
  path?: string;
  message?: string;
}

interface ElectronAPI {
  minimize: () => void;
  maximize: () => void;
  close: () => void;
  setZoomFactor?: (factor: number) => Promise<boolean>;
  getOpaqueSidebarBackground?: () => Promise<boolean>;
  setOpaqueSidebarBackground?: (enabled: boolean) => Promise<boolean>;
  platform: string;
  credentialAvailable?: () => Promise<boolean>;
  credentialSave?: (username: string, password: string) => Promise<boolean>;
  credentialRead?: (username: string) => Promise<string | null>;
  credentialDelete?: (username: string) => Promise<boolean>;
  authSessionGet?: (key: string) => Promise<string | null>;
  authSessionSet?: (key: string, value: string) => Promise<boolean>;
  authSessionDelete?: (key: string) => Promise<boolean>;
  localStateHydrate?: () => Promise<LocalStateSnapshot | null>;
  localStateSaveSettings?: (value: unknown) => Promise<boolean>;
  localStateSaveLayout?: (value: unknown) => Promise<boolean>;
  localStateSaveChat?: (value: unknown) => Promise<boolean>;
  localStateBindProfile?: (value: unknown) => Promise<LocalProfileSnapshot | null>;
  localStateUpsertNote?: (
    value: { id: string; title: string; content: string; titleIsAuto: boolean },
  ) => Promise<LocalNoteSnapshot | null>;
  localStateCreateNote?: () => Promise<LocalNoteSnapshot | null>;
  localStateDeleteNote?: (id: string) => Promise<{ activeNoteId: string } | null>;
  localStateSetActiveNote?: (id: string) => Promise<boolean>;
  localStateClearNoteContent?: (id: string) => Promise<LocalNoteSnapshot | null>;
  notesExportPdf?: (payload: {
    html: string;
    defaultFilename?: string;
  }) => Promise<NotesExportPdfResult>;
  avatarSave?: (accountId: string, base64Data: string) => Promise<boolean>;
  avatarRead?: (accountId: string) => Promise<string | null>;
  avatarDelete?: (accountId: string) => Promise<boolean>;
  updaterGetState?: () => Promise<UpdateStateSnapshot>;
  updaterCheckNow?: () => Promise<UpdateActionResult>;
  updaterStartDownload?: () => Promise<UpdateActionResult>;
  updaterQuitAndInstall?: () => Promise<UpdateActionResult>;
  updaterOnStateChanged?: (listener: (state: UpdateStateSnapshot) => void) => () => void;
}

interface Window {
  electronAPI?: ElectronAPI;
}
