import {
  app,
  BrowserWindow,
  ipcMain,
  session,
  shell,
  type Rectangle,
} from "electron";
import fs from "node:fs";
import path from "path";
import {
  isDesktopAppURL,
  registerDesktopAppProtocol,
  registerDesktopAppSchemePrivileges,
} from "./appProtocol";
import { DESKTOP_APP_ENTRY_URL } from "./appProtocolPath";
import { denyWindowOpenAndMaybeOpenPintia } from "./externalNavigation";
import { desktopUpdater } from "./updater";

registerDesktopAppSchemePrivileges();

process.env.DIST = path.join(__dirname, "../dist");
process.env.VITE_PUBLIC = app.isPackaged
  ? process.env.DIST
  : path.join(process.env.DIST, "../public");

let mainWindow: BrowserWindow | null = null;
const VITE_DEV_SERVER_URL = process.env.VITE_DEV_SERVER_URL;
const isMac = process.platform === "darwin";
const isLinux = process.platform === "linux";
const LINUX_DESKTOP_FILE = "ascendany.desktop";

function resolveWindowIconPath(): string | undefined {
  if (!isLinux) {
    return undefined;
  }

  const candidates = [
    path.join(__dirname, "../resources/icon.png"),
    path.join(process.resourcesPath, "icon.png"),
  ];

  return candidates.find((candidate) => fs.existsSync(candidate));
}

type LinuxGpuMode = "auto" | "off" | "x11" | "swiftshader";
type LinuxImeMode = "auto" | "on" | "off";

function resolveLinuxGpuMode(): LinuxGpuMode {
  const mode = (process.env.ASCENDANY_LINUX_GPU_MODE ?? "off").toLowerCase();
  if (mode === "auto" || mode === "off" || mode === "x11" || mode === "swiftshader") {
    return mode;
  }
  throw new Error(
    `ASCENDANY_LINUX_GPU_MODE must be one of auto, off, x11, or swiftshader; received "${process.env.ASCENDANY_LINUX_GPU_MODE}".`,
  );
}

function resolveLinuxImeMode(): LinuxImeMode {
  const mode = (process.env.ASCENDANY_LINUX_IME_MODE ?? "auto").toLowerCase();
  if (mode === "auto" || mode === "on" || mode === "off") {
    return mode;
  }
  throw new Error(
    `ASCENDANY_LINUX_IME_MODE must be one of auto, on, or off; received "${process.env.ASCENDANY_LINUX_IME_MODE}".`,
  );
}

function isWaylandSession() {
  const sessionType = (process.env.XDG_SESSION_TYPE ?? "").toLowerCase();
  return sessionType === "wayland" || Boolean(process.env.WAYLAND_DISPLAY);
}

function appendEnableFeatures(features: string[]) {
  const existing = app.commandLine
    .getSwitchValue("enable-features")
    .split(",")
    .map((feature) => feature.trim())
    .filter(Boolean);
  const merged = [...new Set([...existing, ...features])];
  if (merged.length === existing.length) {
    return;
  }
  app.commandLine.appendSwitch("enable-features", merged.join(","));
}

function configureLinuxGraphics(mode: LinuxGpuMode) {
  if (!isLinux) {
    return;
  }

  switch (mode) {
    case "off":
      // Most robust mode for mixed Wayland/GBM environments.
      app.disableHardwareAcceleration();
      app.commandLine.appendSwitch("disable-gpu");
      break;
    case "x11":
      // Force XWayland rendering path when native Wayland GBM is unstable.
      app.commandLine.appendSwitch("ozone-platform-hint", "x11");
      break;
    case "swiftshader":
      // Software GL fallback while keeping Chromium's compositor enabled.
      app.commandLine.appendSwitch("use-gl", "angle");
      app.commandLine.appendSwitch("use-angle", "swiftshader");
      break;
    case "auto":
      break;
  }
}

function configureLinuxInputMethod(gpuMode: LinuxGpuMode) {
  if (!isLinux) {
    return;
  }

  const imeMode = resolveLinuxImeMode();
  const shouldEnableWaylandIme = imeMode === "on" || (imeMode === "auto" && isWaylandSession());
  if (!shouldEnableWaylandIme) {
    return;
  }

  if (gpuMode === "x11") {
    console.info("[AscendAny] ASCENDANY_LINUX_GPU_MODE=x11; skip Wayland IME switches.");
    return;
  }

  app.commandLine.appendSwitch("enable-wayland-ime");
  if (!app.commandLine.hasSwitch("ozone-platform") && !app.commandLine.hasSwitch("ozone-platform-hint")) {
    app.commandLine.appendSwitch("ozone-platform-hint", "wayland");
  }
  appendEnableFeatures(["UseOzonePlatform"]);

  // Allow forcing IM module without overriding an existing desktop session setup.
  const imModule = process.env.ASCENDANY_LINUX_IM_MODULE?.trim();
  if (imModule) {
    process.env.GTK_IM_MODULE ??= imModule;
    process.env.QT_IM_MODULE ??= imModule;
    process.env.XMODIFIERS ??= `@im=${imModule}`;
  }
}

if (isLinux) {
  (app as Electron.App & { setDesktopName?: (desktopName: string) => void }).setDesktopName?.(
    LINUX_DESKTOP_FILE,
  );
  const linuxGpuMode = resolveLinuxGpuMode();
  configureLinuxGraphics(linuxGpuMode);
  configureLinuxInputMethod(linuxGpuMode);
}

function loadMainWindow(window: BrowserWindow) {
  if (VITE_DEV_SERVER_URL) {
    void window.loadURL(VITE_DEV_SERVER_URL);
  } else {
    void window.loadURL(DESKTOP_APP_ENTRY_URL);
  }
}

function isAllowedRendererURL(value: string): boolean {
  if (VITE_DEV_SERVER_URL) {
    try {
      return new URL(value).origin === new URL(VITE_DEV_SERVER_URL).origin;
    } catch {
      return false;
    }
  }
  return isDesktopAppURL(value);
}

function createWindow(options?: {
  show?: boolean;
  bounds?: Rectangle;
}) {
  const nextWindow = new BrowserWindow({
    show: options?.show ?? true,
    width: 1280,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    frame: false,
    titleBarStyle: isMac ? "hiddenInset" : "hidden",
    // Keep compositor transparency available; renderer CSS decides which pixels are transparent.
    transparent: true,
    vibrancy: isMac ? "under-window" : undefined,
    visualEffectState: isMac ? "active" : undefined,
    backgroundColor: "#00000000",
    icon: resolveWindowIconPath(),
    // On Linux, use rounded corners via CSS instead of OS-level transparency
    ...(isLinux && { backgroundMaterial: undefined }),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
    ...(options?.bounds ?? {}),
  });

  nextWindow.on("closed", () => {
    if (mainWindow === nextWindow) {
      mainWindow = null;
    }
  });
  nextWindow.webContents.setWindowOpenHandler(({ url }) => {
    return denyWindowOpenAndMaybeOpenPintia(
      url,
      (allowedURL) => shell.openExternal(allowedURL),
      (error) => {
        console.error("[AscendAny] Failed to open Pintia problem set URL.", error);
      },
    );
  });
  nextWindow.webContents.on("will-navigate", (event, targetURL) => {
    if (!isAllowedRendererURL(targetURL)) {
      event.preventDefault();
    }
  });

  mainWindow = nextWindow;
  loadMainWindow(nextWindow);
  return nextWindow;
}

// Window control IPC handlers
ipcMain.on("window-minimize", (event) => {
  const window = BrowserWindow.fromWebContents(event.sender) ?? mainWindow;
  window?.minimize();
});

ipcMain.on("window-maximize", (event) => {
  const window = BrowserWindow.fromWebContents(event.sender) ?? mainWindow;
  if (window?.isMaximized()) {
    window.unmaximize();
  } else {
    window?.maximize();
  }
});

ipcMain.on("window-close", (event) => {
  const window = BrowserWindow.fromWebContents(event.sender) ?? mainWindow;
  window?.close();
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
    mainWindow = null;
  }
});

app.on("activate", () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
  }
});

app.whenReady().then(() => {
  registerDesktopAppProtocol(process.env.DIST!);
  session.defaultSession.setPermissionRequestHandler((_webContents, _permission, callback) => {
    callback(false);
  });
  desktopUpdater.registerIpc();
  desktopUpdater.start();
  createWindow();
});
