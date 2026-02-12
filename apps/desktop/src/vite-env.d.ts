/// <reference types="vite/client" />

interface ElectronAPI {
  minimize: () => void;
  maximize: () => void;
  close: () => void;
  platform: string;
}

interface Window {
  electronAPI?: ElectronAPI;
}
