/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_DIRECT_LOGIN_ENABLED?: string;
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
  openFeedbackWindow?: () => Promise<boolean>;
  submitFeedback?: (payload: {
    title: string;
    content: string;
    images: Array<{ name: string; dataUrl: string }>;
  }) => Promise<{ success: boolean; message: string }>;
  setZoomFactor?: (factor: number) => Promise<boolean>;
  getOpaqueWindowBackground?: () => Promise<boolean>;
  setOpaqueWindowBackground?: (enabled: boolean) => Promise<boolean>;
  platform: string;
  credentialAvailable?: () => Promise<boolean>;
  credentialSave?: (username: string, password: string) => Promise<boolean>;
  credentialRead?: (username: string) => Promise<string | null>;
  credentialDelete?: (username: string) => Promise<boolean>;
  authSessionGet?: (key: string) => Promise<string | null>;
  authSessionSet?: (key: string, value: string) => Promise<boolean>;
  authSessionDelete?: (key: string) => Promise<boolean>;
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
