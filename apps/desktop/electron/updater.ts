import { app, BrowserWindow, ipcMain } from "electron";
import electronUpdater from "electron-updater";

const { autoUpdater } = electronUpdater;

const STARTUP_CHECK_DELAY_MS = 12_000;

export type UpdateStatus =
  | "idle"
  | "checking"
  | "available"
  | "downloading"
  | "downloaded"
  | "up_to_date"
  | "error"
  | "disabled";

export interface UpdateStateSnapshot {
  status: UpdateStatus;
  currentVersion: string;
  latestVersion: string | null;
  progressPercent: number | null;
  lastCheckedAt: string | null;
  message: string | null;
}

export interface UpdateActionResult {
  success: boolean;
  message: string;
}

function nowIso(): string {
  return new Date().toISOString();
}

function normalizeUpdateError(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  if (typeof error === "string" && error.trim()) {
    return error.trim();
  }
  return "检查更新失败，请稍后重试。";
}

function roundProgress(value: number): number {
  if (!Number.isFinite(value)) {
    return 0;
  }
  const bounded = Math.min(100, Math.max(0, value));
  return Math.round(bounded * 100) / 100;
}

class DesktopUpdaterService {
  private state: UpdateStateSnapshot = {
    status: app.isPackaged ? "idle" : "disabled",
    currentVersion: app.getVersion(),
    latestVersion: null,
    progressPercent: null,
    lastCheckedAt: null,
    message: app.isPackaged ? null : "开发环境不检查自动更新。",
  };

  private eventBound = false;
  private ipcBound = false;
  private checking = false;

  getState(): UpdateStateSnapshot {
    return { ...this.state };
  }

  registerIpc(): void {
    if (this.ipcBound) {
      return;
    }
    this.ipcBound = true;

    ipcMain.handle("updater-get-state", () => this.getState());
    ipcMain.handle("updater-check-now", async () => this.checkForUpdates("manual"));
    ipcMain.handle("updater-start-download", async () => this.startDownload());
    ipcMain.handle("updater-quit-and-install", () => this.quitAndInstall());
  }

  start(): void {
    if (!app.isPackaged) {
      this.updateState({
        status: "disabled",
        message: "开发环境不检查自动更新。",
      });
      return;
    }

    if (this.eventBound) {
      return;
    }
    this.eventBound = true;

    autoUpdater.autoDownload = false;
    autoUpdater.autoInstallOnAppQuit = false;
    autoUpdater.allowPrerelease = false;
    autoUpdater.allowDowngrade = false;
    autoUpdater.logger = console;

    autoUpdater.on("checking-for-update", () => {
      this.updateState({
        status: "checking",
        progressPercent: null,
        lastCheckedAt: nowIso(),
        message: "正在检查更新...",
      });
    });

    autoUpdater.on("update-available", (info) => {
      this.updateState({
        status: "available",
        latestVersion: info.version ?? null,
        progressPercent: null,
        message: "发现新版本，请确认是否更新。",
      });
    });

    autoUpdater.on("download-progress", (progress) => {
      this.updateState({
        status: "downloading",
        progressPercent: roundProgress(progress.percent),
        message: "正在下载更新包...",
      });
    });

    autoUpdater.on("update-downloaded", (info) => {
      this.updateState({
        status: "downloaded",
        latestVersion: info.version ?? this.state.latestVersion,
        progressPercent: 100,
        message: "更新已下载完成，可重启安装。",
      });
    });

    autoUpdater.on("update-not-available", (info) => {
      this.updateState({
        status: "up_to_date",
        latestVersion: info.version ?? this.state.currentVersion,
        progressPercent: null,
        message: "当前已是最新版本。",
      });
    });

    autoUpdater.on("error", (error) => {
      this.checking = false;
      this.updateState({
        status: "error",
        progressPercent: null,
        message: normalizeUpdateError(error),
      });
    });

    setTimeout(() => {
      void this.checkForUpdates("startup");
    }, STARTUP_CHECK_DELAY_MS);
  }

  private updateState(next: Partial<UpdateStateSnapshot>): void {
    this.state = {
      ...this.state,
      ...next,
      currentVersion: app.getVersion(),
    };
    this.broadcastState();
  }

  private broadcastState(): void {
    const snapshot = this.getState();
    for (const window of BrowserWindow.getAllWindows()) {
      if (window.isDestroyed()) {
        continue;
      }
      window.webContents.send("updater-state-changed", snapshot);
    }
  }

  async checkForUpdates(trigger: "startup" | "manual"): Promise<UpdateActionResult> {
    if (!app.isPackaged) {
      return {
        success: false,
        message: "开发环境不检查自动更新。",
      };
    }

    if (this.checking) {
      return {
        success: false,
        message: "正在检查更新，请稍候。",
      };
    }

    this.checking = true;
    try {
      await autoUpdater.checkForUpdates();
      return {
        success: true,
        message: trigger === "manual" ? "已发起检查更新。" : "启动检查已触发。",
      };
    } catch (error) {
      const message = normalizeUpdateError(error);
      this.updateState({
        status: "error",
        lastCheckedAt: nowIso(),
        progressPercent: null,
        message,
      });
      return {
        success: false,
        message,
      };
    } finally {
      this.checking = false;
    }
  }

  async startDownload(): Promise<UpdateActionResult> {
    if (!app.isPackaged) {
      return {
        success: false,
        message: "开发环境不支持下载更新。",
      };
    }

    if (this.state.status === "downloading") {
      return {
        success: false,
        message: "更新包正在下载中。",
      };
    }

    if (this.state.status === "downloaded") {
      return {
        success: true,
        message: "更新包已下载完成。",
      };
    }

    if (this.state.status !== "available") {
      return {
        success: false,
        message: "当前没有可下载的更新包。",
      };
    }

    try {
      this.updateState({
        status: "downloading",
        progressPercent: 0,
        message: "正在下载更新包...",
      });
      await autoUpdater.downloadUpdate();
      return {
        success: true,
        message: "已开始下载更新包。",
      };
    } catch (error) {
      const message = normalizeUpdateError(error);
      this.updateState({
        status: "error",
        progressPercent: null,
        message,
      });
      return {
        success: false,
        message,
      };
    }
  }

  quitAndInstall(): UpdateActionResult {
    if (!app.isPackaged) {
      return {
        success: false,
        message: "开发环境不可执行安装。",
      };
    }

    if (this.state.status !== "downloaded") {
      return {
        success: false,
        message: "当前没有可安装的更新包。",
      };
    }

    setImmediate(() => {
      autoUpdater.quitAndInstall(false, true);
    });

    return {
      success: true,
      message: "正在重启并安装更新。",
    };
  }
}

export const desktopUpdater = new DesktopUpdaterService();
