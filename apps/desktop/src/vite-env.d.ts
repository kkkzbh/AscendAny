/// <reference types="vite/client" />

interface ElectronAPI {
  minimize: () => void;
  maximize: () => void;
  close: () => void;
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
