/// <reference types="vite/client" />

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
  avatarSave?: (accountId: string, base64Data: string) => Promise<boolean>;
  avatarRead?: (accountId: string) => Promise<string | null>;
  avatarDelete?: (accountId: string) => Promise<boolean>;
}

interface Window {
  electronAPI?: ElectronAPI;
}
