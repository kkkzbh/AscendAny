import { contextBridge, ipcRenderer } from "electron";
import type { UpdateActionResult, UpdateStateSnapshot } from "./updater";

contextBridge.exposeInMainWorld("electronAPI", {
  minimize: () => ipcRenderer.send("window-minimize"),
  maximize: () => ipcRenderer.send("window-maximize"),
  close: () => ipcRenderer.send("window-close"),
  openFeedbackWindow: () => ipcRenderer.invoke("window-open-feedback") as Promise<boolean>,
  submitFeedback: (payload: { title: string; content: string; images: Array<{ name: string; dataUrl: string }> }) =>
    ipcRenderer.invoke("feedback-submit", payload) as Promise<{ success: boolean; message: string }>,
  setZoomFactor: (factor: number) =>
    ipcRenderer.invoke("window-set-zoom-factor", factor) as Promise<boolean>,
  getOpaqueWindowBackground: () =>
    ipcRenderer.invoke("window-get-opaque-background") as Promise<boolean>,
  setOpaqueWindowBackground: (enabled: boolean) =>
    ipcRenderer.invoke("window-set-opaque-background", enabled) as Promise<boolean>,
  platform: process.platform,
  credentialAvailable: () => ipcRenderer.invoke("credential-available") as Promise<boolean>,
  credentialSave: (username: string, password: string) =>
    ipcRenderer.invoke("credential-save", username, password) as Promise<boolean>,
  credentialRead: (username: string) =>
    ipcRenderer.invoke("credential-read", username) as Promise<string | null>,
  credentialDelete: (username: string) =>
    ipcRenderer.invoke("credential-delete", username) as Promise<boolean>,
  authSessionGet: (key: string) =>
    ipcRenderer.invoke("auth-session-get", key) as Promise<string | null>,
  authSessionSet: (key: string, value: string) =>
    ipcRenderer.invoke("auth-session-set", key, value) as Promise<boolean>,
  authSessionDelete: (key: string) =>
    ipcRenderer.invoke("auth-session-delete", key) as Promise<boolean>,
  avatarSave: (accountId: string, base64Data: string) =>
    ipcRenderer.invoke("avatar-save", accountId, base64Data) as Promise<boolean>,
  avatarRead: (accountId: string) =>
    ipcRenderer.invoke("avatar-read", accountId) as Promise<string | null>,
  avatarDelete: (accountId: string) =>
    ipcRenderer.invoke("avatar-delete", accountId) as Promise<boolean>,
  updaterGetState: () =>
    ipcRenderer.invoke("updater-get-state") as Promise<UpdateStateSnapshot>,
  updaterCheckNow: () =>
    ipcRenderer.invoke("updater-check-now") as Promise<UpdateActionResult>,
  updaterQuitAndInstall: () =>
    ipcRenderer.invoke("updater-quit-and-install") as Promise<UpdateActionResult>,
  updaterOnStateChanged: (listener: (state: UpdateStateSnapshot) => void) => {
    const channel = "updater-state-changed";
    const wrapped = (_event: Electron.IpcRendererEvent, state: UpdateStateSnapshot) => {
      listener(state);
    };
    ipcRenderer.on(channel, wrapped);
    return () => {
      ipcRenderer.removeListener(channel, wrapped);
    };
  },
});
