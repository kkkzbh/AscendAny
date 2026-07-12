/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
  readonly VITE_CHAT_PROMPT_CONFIGURATION_KEY?: string;
  readonly VITE_CHAT_MODEL_CONFIGURATION_KEY?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

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

interface ElectronAPI {
  minimize: () => void;
  maximize: () => void;
  close: () => void;
  platform: string;
  updaterGetState?: () => Promise<UpdateStateSnapshot>;
  updaterCheckNow?: () => Promise<UpdateActionResult>;
  updaterStartDownload?: () => Promise<UpdateActionResult>;
  updaterQuitAndInstall?: () => Promise<UpdateActionResult>;
  updaterOnStateChanged?: (listener: (state: UpdateStateSnapshot) => void) => () => void;
}

interface Window {
  electronAPI?: ElectronAPI;
}
