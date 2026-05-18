import { app, BrowserWindow, dialog, ipcMain, safeStorage, type Rectangle } from "electron";
import fs from "node:fs";
import path from "path";
import { desktopUpdater } from "./updater";
import { LocalStateService } from "./localState";

process.env.DIST = path.join(__dirname, "../dist");
process.env.VITE_PUBLIC = app.isPackaged
  ? process.env.DIST
  : path.join(process.env.DIST, "../public");

let mainWindow: BrowserWindow | null = null;
const VITE_DEV_SERVER_URL = process.env.VITE_DEV_SERVER_URL;
const isMac = process.platform === "darwin";
const isLinux = process.platform === "linux";
const CREDENTIAL_FILE_NAME = "secure-credentials.json";
const RENDERER_STATE_FILE_NAME = "renderer-state.json";
const LOCAL_STATE_FILE_NAME = "state_v2.sqlite";
const LINUX_DESKTOP_FILE = "ascendany.desktop";
const DEFAULT_OPAQUE_SIDEBAR_BACKGROUND = true;

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

function normalizeOpaqueSidebarBackground(value: unknown): boolean | null {
  if (typeof value !== "boolean") {
    return null;
  }
  return value;
}

let localStateService: LocalStateService | null = null;

function localStateFilePath(): string {
  return path.join(app.getPath("userData"), LOCAL_STATE_FILE_NAME);
}

function getLocalStateService(): LocalStateService {
  if (!localStateService) {
    localStateService = new LocalStateService(localStateFilePath());
  }
  return localStateService;
}

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

function rendererStateFilePath(): string {
  return path.join(app.getPath("userData"), RENDERER_STATE_FILE_NAME);
}

function loadRendererStateStore(): Record<string, string> {
  try {
    const filePath = rendererStateFilePath();
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

function saveRendererStateStore(next: Record<string, string>): boolean {
  try {
    const filePath = rendererStateFilePath();
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, JSON.stringify(next), { encoding: "utf-8" });
    return true;
  } catch {
    return false;
  }
}

function normalizeRendererStateKey(key: unknown): string {
  return typeof key === "string" ? key.trim() : "";
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

ipcMain.handle("auth-session-get", (_event, key: unknown) => {
  const normalized = normalizeRendererStateKey(key);
  if (!normalized) {
    return null;
  }
  const store = loadRendererStateStore();
  return store[normalized] ?? null;
});

ipcMain.handle("auth-session-set", (_event, key: unknown, value: unknown) => {
  const normalized = normalizeRendererStateKey(key);
  if (!normalized || typeof value !== "string") {
    return false;
  }
  const next = loadRendererStateStore();
  next[normalized] = value;
  return saveRendererStateStore(next);
});

ipcMain.handle("auth-session-delete", (_event, key: unknown) => {
  const normalized = normalizeRendererStateKey(key);
  if (!normalized) {
    return false;
  }
  const next = loadRendererStateStore();
  if (!(normalized in next)) {
    return true;
  }
  delete next[normalized];
  return saveRendererStateStore(next);
});

ipcMain.handle("local-state-hydrate", () => {
  try {
    return getLocalStateService().hydrate();
  } catch (error) {
    console.error("[AscendAny] Failed to hydrate local state:", error);
    return null;
  }
});

ipcMain.handle("local-state-save-settings", (_event, value: unknown) => {
  try {
    return getLocalStateService().saveSettings(value);
  } catch (error) {
    console.error("[AscendAny] Failed to save local settings:", error);
    return false;
  }
});

ipcMain.handle("local-state-save-layout", (_event, value: unknown) => {
  try {
    return getLocalStateService().saveLayout(value);
  } catch (error) {
    console.error("[AscendAny] Failed to save local layout:", error);
    return false;
  }
});

ipcMain.handle("local-state-save-chat", (_event, value: unknown) => {
  try {
    return getLocalStateService().saveChat(value);
  } catch (error) {
    console.error("[AscendAny] Failed to save local chat:", error);
    return false;
  }
});

ipcMain.handle("local-state-bind-profile", (_event, value: unknown) => {
  try {
    return getLocalStateService().bindActiveProfile(value);
  } catch (error) {
    console.error("[AscendAny] Failed to bind local profile:", error);
    return null;
  }
});

ipcMain.handle("local-state-upsert-note", (_event, value: unknown) => {
  try {
    return getLocalStateService().upsertNote(value);
  } catch (error) {
    console.error("[AscendAny] Failed to upsert note:", error);
    return null;
  }
});

ipcMain.handle("local-state-create-note", () => {
  try {
    return getLocalStateService().createNote();
  } catch (error) {
    console.error("[AscendAny] Failed to create note:", error);
    return null;
  }
});

ipcMain.handle("local-state-delete-note", (_event, value: unknown) => {
  try {
    return getLocalStateService().deleteNote(value);
  } catch (error) {
    console.error("[AscendAny] Failed to delete note:", error);
    return null;
  }
});

ipcMain.handle("local-state-set-active-note", (_event, value: unknown) => {
  try {
    return getLocalStateService().setActiveNote(value);
  } catch (error) {
    console.error("[AscendAny] Failed to set active note:", error);
    return false;
  }
});

ipcMain.handle("local-state-clear-note-content", (_event, value: unknown) => {
  try {
    return getLocalStateService().clearNoteContent(value);
  } catch (error) {
    console.error("[AscendAny] Failed to clear note content:", error);
    return null;
  }
});

interface NotesExportPdfPayload {
  html: string;
  defaultFilename?: string;
}

function parseNotesExportPdfPayload(value: unknown): NotesExportPdfPayload | null {
  if (!value || typeof value !== "object") {
    return null;
  }
  const candidate = value as Partial<NotesExportPdfPayload>;
  if (typeof candidate.html !== "string" || !candidate.html.trim()) {
    return null;
  }
  return {
    html: candidate.html,
    defaultFilename:
      typeof candidate.defaultFilename === "string" && candidate.defaultFilename.trim()
        ? candidate.defaultFilename.trim()
        : "notes.pdf",
  };
}

async function exportNotesAsPdf(
  payload: NotesExportPdfPayload,
  parentWindow: BrowserWindow | null,
): Promise<{ success: boolean; canceled?: boolean; path?: string; message?: string }> {
  const offscreen = new BrowserWindow({
    show: false,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  try {
    const dataUrl = "data:text/html;charset=utf-8," + encodeURIComponent(payload.html);
    await offscreen.loadURL(dataUrl);
    const pdf = await offscreen.webContents.printToPDF({
      printBackground: true,
      pageSize: "A4",
      margins: { marginType: "default" },
    });
    const filenameSuggestion = payload.defaultFilename ?? "notes.pdf";
    const target = parentWindow ?? mainWindow;
    const result = target
      ? await dialog.showSaveDialog(target, {
          title: "导出笔记为 PDF",
          defaultPath: filenameSuggestion,
          filters: [{ name: "PDF", extensions: ["pdf"] }],
        })
      : await dialog.showSaveDialog({
          title: "导出笔记为 PDF",
          defaultPath: filenameSuggestion,
          filters: [{ name: "PDF", extensions: ["pdf"] }],
        });
    if (result.canceled || !result.filePath) {
      return { success: false, canceled: true };
    }
    const finalPath = result.filePath.toLowerCase().endsWith(".pdf")
      ? result.filePath
      : `${result.filePath}.pdf`;
    fs.writeFileSync(finalPath, pdf);
    return { success: true, path: finalPath };
  } finally {
    offscreen.destroy();
  }
}

ipcMain.handle("notes-export-pdf", async (event, value: unknown) => {
  const payload = parseNotesExportPdfPayload(value);
  if (!payload) {
    return { success: false, message: "Invalid PDF export payload." };
  }
  try {
    const sender = BrowserWindow.fromWebContents(event.sender);
    return await exportNotesAsPdf(payload, sender ?? mainWindow);
  } catch (error) {
    console.error("[AscendAny] Notes PDF export failed:", error);
    return {
      success: false,
      message: error instanceof Error ? error.message : "Notes PDF export failed.",
    };
  }
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
  const window = BrowserWindow.fromWebContents(event.sender) ?? mainWindow;
  if (!window) {
    return false;
  }
  window.webContents.setZoomFactor(zoomFactor);
  return true;
});

ipcMain.handle("window-get-opaque-sidebar-background", () => {
  try {
    return getLocalStateService().getOpaqueSidebarBackground();
  } catch {
    return DEFAULT_OPAQUE_SIDEBAR_BACKGROUND;
  }
});

ipcMain.handle("window-set-opaque-sidebar-background", (_event, value: unknown) => {
  const useOpaqueSidebarBackground = normalizeOpaqueSidebarBackground(value);
  if (useOpaqueSidebarBackground === null) {
    return false;
  }

  try {
    const current = getLocalStateService().hydrate().settings;
    getLocalStateService().saveSettings({
      ...current,
      useOpaqueSidebarBackground,
    });
  } catch {
    return false;
  }
  return true;
});

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

app.on("before-quit", () => {
  localStateService?.close();
  localStateService = null;
});

app.whenReady().then(() => {
  getLocalStateService();
  desktopUpdater.registerIpc();
  desktopUpdater.start();
  createWindow();
});
