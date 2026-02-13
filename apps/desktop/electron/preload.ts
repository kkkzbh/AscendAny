import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("electronAPI", {
  minimize: () => ipcRenderer.send("window-minimize"),
  maximize: () => ipcRenderer.send("window-maximize"),
  close: () => ipcRenderer.send("window-close"),
  platform: process.platform,
  credentialAvailable: () => ipcRenderer.invoke("credential-available") as Promise<boolean>,
  credentialSave: (username: string, password: string) =>
    ipcRenderer.invoke("credential-save", username, password) as Promise<boolean>,
  credentialRead: (username: string) =>
    ipcRenderer.invoke("credential-read", username) as Promise<string | null>,
  credentialDelete: (username: string) =>
    ipcRenderer.invoke("credential-delete", username) as Promise<boolean>,
});
