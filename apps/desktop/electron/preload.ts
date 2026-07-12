import { contextBridge, ipcRenderer } from "electron";
import type { UpdateActionResult, UpdateStateSnapshot } from "./updater";

contextBridge.exposeInMainWorld("electronAPI", {
  minimize: () => ipcRenderer.send("window-minimize"),
  maximize: () => ipcRenderer.send("window-maximize"),
  close: () => ipcRenderer.send("window-close"),
  platform: process.platform,
  updaterGetState: () =>
    ipcRenderer.invoke("updater-get-state") as Promise<UpdateStateSnapshot>,
  updaterCheckNow: () =>
    ipcRenderer.invoke("updater-check-now") as Promise<UpdateActionResult>,
  updaterStartDownload: () =>
    ipcRenderer.invoke("updater-start-download") as Promise<UpdateActionResult>,
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
