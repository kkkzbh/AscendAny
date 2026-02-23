import { app, BrowserWindow, ipcMain, safeStorage, type Rectangle } from "electron";
import fs from "node:fs";
import path from "path";

process.env.DIST = path.join(__dirname, "../dist");
process.env.VITE_PUBLIC = app.isPackaged
  ? process.env.DIST
  : path.join(process.env.DIST, "../public");

let mainWindow: BrowserWindow | null = null;
const VITE_DEV_SERVER_URL = process.env.VITE_DEV_SERVER_URL;
const isMac = process.platform === "darwin";
const isLinux = process.platform === "linux";
const CREDENTIAL_FILE_NAME = "secure-credentials.json";
const WINDOW_APPEARANCE_FILE_NAME = "window-appearance.json";
const LINUX_DESKTOP_FILE = "ascendany.desktop";

interface WindowAppearanceConfig {
  useOpaqueWindowBackground: boolean;
}

const DEFAULT_WINDOW_APPEARANCE: WindowAppearanceConfig = {
  useOpaqueWindowBackground: true,
};

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
  console.warn(
    `[AscendAny] Unknown ASCENDANY_LINUX_GPU_MODE="${process.env.ASCENDANY_LINUX_GPU_MODE}", fallback to "off".`,
  );
  return "off";
}

function resolveLinuxImeMode(): LinuxImeMode {
  const mode = (process.env.ASCENDANY_LINUX_IME_MODE ?? "auto").toLowerCase();
  if (mode === "auto" || mode === "on" || mode === "off") {
    return mode;
  }
  console.warn(
    `[AscendAny] Unknown ASCENDANY_LINUX_IME_MODE="${process.env.ASCENDANY_LINUX_IME_MODE}", fallback to "auto".`,
  );
  return "auto";
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

function windowAppearanceFilePath(): string {
  return path.join(app.getPath("userData"), WINDOW_APPEARANCE_FILE_NAME);
}

function loadWindowAppearance(): WindowAppearanceConfig {
  try {
    const filePath = windowAppearanceFilePath();
    if (!fs.existsSync(filePath)) {
      return { ...DEFAULT_WINDOW_APPEARANCE };
    }
    const raw = fs.readFileSync(filePath, "utf-8");
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== "object") {
      return { ...DEFAULT_WINDOW_APPEARANCE };
    }
    const useOpaqueWindowBackground = (parsed as Partial<WindowAppearanceConfig>).useOpaqueWindowBackground;
    if (typeof useOpaqueWindowBackground !== "boolean") {
      return { ...DEFAULT_WINDOW_APPEARANCE };
    }
    return {
      useOpaqueWindowBackground,
    };
  } catch {
    return { ...DEFAULT_WINDOW_APPEARANCE };
  }
}

function saveWindowAppearance(next: WindowAppearanceConfig): boolean {
  try {
    const filePath = windowAppearanceFilePath();
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, JSON.stringify(next), { encoding: "utf-8" });
    return true;
  } catch {
    return false;
  }
}

function normalizeOpaqueWindowBackground(value: unknown): boolean | null {
  if (typeof value !== "boolean") {
    return null;
  }
  return value;
}

let windowAppearance = loadWindowAppearance();

function loadMainWindow(window: BrowserWindow) {
  if (VITE_DEV_SERVER_URL) {
    void window.loadURL(VITE_DEV_SERVER_URL);
  } else {
    void window.loadFile(path.join(process.env.DIST!, "index.html"));
  }
}

function createWindow(options?: {
  show?: boolean;
  bounds?: Rectangle;
}) {
  const useOpaqueWindowBackground = windowAppearance.useOpaqueWindowBackground;
  const enableTranslucentWindow = !useOpaqueWindowBackground;
  const nextWindow = new BrowserWindow({
    show: options?.show ?? true,
    width: 1280,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    frame: false,
    titleBarStyle: isMac ? "hiddenInset" : "hidden",
    // Linux translucency is opt-in via setting because some environments can be unstable.
    transparent: isMac || enableTranslucentWindow,
    vibrancy: isMac ? "under-window" : undefined,
    visualEffectState: isMac ? "active" : undefined,
    backgroundColor: enableTranslucentWindow || isMac ? "#00000000" : "#f0f2f8",
    icon: resolveWindowIconPath(),
    // On Linux, use rounded corners via CSS instead of OS-level transparency
    ...(isLinux && { backgroundMaterial: undefined }),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
    },
    ...(options?.bounds ?? {}),
  });

  nextWindow.on("closed", () => {
    if (mainWindow === nextWindow) {
      mainWindow = null;
    }
  });

  mainWindow = nextWindow;
  loadMainWindow(nextWindow);
  return nextWindow;
}

let isRebuildingMainWindow = false;

async function rebuildMainWindow(): Promise<boolean> {
  const currentWindow = mainWindow;
  if (!currentWindow || currentWindow.isDestroyed()) {
    createWindow();
    return true;
  }

  if (isRebuildingMainWindow) {
    return false;
  }
  isRebuildingMainWindow = true;

  const wasMaximized = currentWindow.isMaximized();
  const wasFullScreen = currentWindow.isFullScreen();
  const bounds = currentWindow.getBounds();
  const nextWindow = createWindow({
    show: false,
    bounds: wasMaximized || wasFullScreen ? undefined : bounds,
  });

  try {
    await new Promise<void>((resolve) => {
      nextWindow.once("ready-to-show", () => resolve());
      nextWindow.webContents.once("did-finish-load", () => resolve());
      nextWindow.webContents.once("did-fail-load", () => resolve());
    });

    if (wasFullScreen) {
      nextWindow.setFullScreen(true);
    } else if (wasMaximized) {
      nextWindow.maximize();
    }

    nextWindow.show();
    currentWindow.close();
    return true;
  } catch {
    nextWindow.close();
    mainWindow = currentWindow;
    return false;
  } finally {
    isRebuildingMainWindow = false;
  }
}

function credentialFilePath(): string {
  return path.join(app.getPath("userData"), CREDENTIAL_FILE_NAME);
}

function loadCredentialStore(): Record<string, string> {
  try {
    const filePath = credentialFilePath();
    if (!fs.existsSync(filePath)) {
      return {};
    }
    const raw = fs.readFileSync(filePath, "utf-8");
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== "object") {
      return {};
    }
    const result: Record<string, string> = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof value === "string") {
        result[key] = value;
      }
    }
    return result;
  } catch {
    return {};
  }
}

function saveCredentialStore(next: Record<string, string>): boolean {
  try {
    const filePath = credentialFilePath();
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, JSON.stringify(next), { encoding: "utf-8" });
    return true;
  } catch {
    return false;
  }
}

function normalizeCredentialKey(username: unknown): string {
  return typeof username === "string" ? username.trim() : "";
}

ipcMain.handle("credential-available", () => {
  return safeStorage.isEncryptionAvailable();
});

ipcMain.handle("credential-save", (_event, username: unknown, password: unknown) => {
  const key = normalizeCredentialKey(username);
  const secret = typeof password === "string" ? password : "";
  if (!key || !secret || !safeStorage.isEncryptionAvailable()) {
    return false;
  }
  const encrypted = safeStorage.encryptString(secret).toString("base64");
  const next = loadCredentialStore();
  next[key] = encrypted;
  return saveCredentialStore(next);
});

ipcMain.handle("credential-read", (_event, username: unknown) => {
  const key = normalizeCredentialKey(username);
  if (!key || !safeStorage.isEncryptionAvailable()) {
    return null;
  }
  const store = loadCredentialStore();
  const encoded = store[key];
  if (!encoded) {
    return null;
  }
  try {
    return safeStorage.decryptString(Buffer.from(encoded, "base64"));
  } catch {
    return null;
  }
});

ipcMain.handle("credential-delete", (_event, username: unknown) => {
  const key = normalizeCredentialKey(username);
  if (!key) {
    return false;
  }
  const next = loadCredentialStore();
  if (!(key in next)) {
    return true;
  }
  delete next[key];
  return saveCredentialStore(next);
});

// ---------------------------------------------------------------------------
// Avatar local storage
// ---------------------------------------------------------------------------

function avatarDir(): string {
  return path.join(app.getPath("userData"), "avatars");
}

function avatarFilePath(accountId: string): string {
  // Sanitise accountId to prevent path traversal
  const safe = accountId.replace(/[^a-zA-Z0-9_-]/g, "_");
  return path.join(avatarDir(), `${safe}.png`);
}

function normalizeZoomFactor(value: unknown): number | null {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return null;
  }
  return Math.min(1.3, Math.max(0.8, value));
}

ipcMain.handle("avatar-save", (_event, accountId: unknown, base64Data: unknown) => {
  const id = typeof accountId === "string" ? accountId.trim() : "";
  const data = typeof base64Data === "string" ? base64Data : "";
  if (!id || !data) return false;

  try {
    // Strip optional data-URL prefix (e.g. "data:image/png;base64,")
    const raw = data.replace(/^data:image\/\w+;base64,/, "");
    const dir = avatarDir();
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(avatarFilePath(id), Buffer.from(raw, "base64"));
    return true;
  } catch {
    return false;
  }
});

ipcMain.handle("avatar-read", (_event, accountId: unknown) => {
  const id = typeof accountId === "string" ? accountId.trim() : "";
  if (!id) return null;

  try {
    const filePath = avatarFilePath(id);
    if (!fs.existsSync(filePath)) return null;
    const buf = fs.readFileSync(filePath);
    return `data:image/png;base64,${buf.toString("base64")}`;
  } catch {
    return null;
  }
});

ipcMain.handle("avatar-delete", (_event, accountId: unknown) => {
  const id = typeof accountId === "string" ? accountId.trim() : "";
  if (!id) return false;

  try {
    const filePath = avatarFilePath(id);
    if (fs.existsSync(filePath)) {
      fs.unlinkSync(filePath);
    }
    return true;
  } catch {
    return false;
  }
});

ipcMain.handle("window-set-zoom-factor", (event, value: unknown) => {
  const zoomFactor = normalizeZoomFactor(value);
  if (zoomFactor === null) {
    return false;
  }
  const window = BrowserWindow.fromWebContents(event.sender);
  if (!window) {
    return false;
  }
  window.webContents.setZoomFactor(zoomFactor);
  return true;
});

ipcMain.handle("window-get-opaque-background", () => {
  return windowAppearance.useOpaqueWindowBackground;
});

ipcMain.handle("window-set-opaque-background", async (_event, value: unknown) => {
  const useOpaqueWindowBackground = normalizeOpaqueWindowBackground(value);
  if (useOpaqueWindowBackground === null) {
    return false;
  }

  if (windowAppearance.useOpaqueWindowBackground === useOpaqueWindowBackground) {
    return true;
  }

  const next: WindowAppearanceConfig = {
    useOpaqueWindowBackground,
  };
  const saved = saveWindowAppearance(next);
  if (!saved) {
    return false;
  }

  windowAppearance = next;
  // `transparent` is immutable after window creation; recreate window to apply seamlessly.
  return rebuildMainWindow();
});

// Window control IPC handlers
ipcMain.on("window-minimize", () => {
  mainWindow?.minimize();
});

ipcMain.on("window-maximize", () => {
  if (mainWindow?.isMaximized()) {
    mainWindow.unmaximize();
  } else {
    mainWindow?.maximize();
  }
});

ipcMain.on("window-close", () => {
  mainWindow?.close();
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
  createWindow();
});
