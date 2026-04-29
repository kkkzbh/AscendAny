import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const MAIN_SOURCE = readFileSync(resolve(process.cwd(), "electron/main.ts"), "utf8");
const PRELOAD_SOURCE = readFileSync(resolve(process.cwd(), "electron/preload.ts"), "utf8");

function getIpcHandlerSource(channel: string): string {
  const start = MAIN_SOURCE.indexOf(`ipcMain.handle("${channel}"`);
  expect(start).toBeGreaterThanOrEqual(0);
  const next = MAIN_SOURCE.indexOf("\n\nipcMain.", start + 1);
  return MAIN_SOURCE.slice(start, next === -1 ? undefined : next);
}

describe("sidebar background IPC", () => {
  it("exposes sidebar-specific appearance channels", () => {
    expect(PRELOAD_SOURCE).toContain("getOpaqueSidebarBackground");
    expect(PRELOAD_SOURCE).toContain("window-get-opaque-sidebar-background");
    expect(PRELOAD_SOURCE).toContain("setOpaqueSidebarBackground");
    expect(PRELOAD_SOURCE).toContain("window-set-opaque-sidebar-background");
  });

  it("keeps sidebar appearance changes hot without rebuilding the window", () => {
    const handler = getIpcHandlerSource("window-set-opaque-sidebar-background");

    expect(handler).toContain("getLocalStateService().saveSettings");
    expect(handler).toContain("return true");
    expect(handler).not.toContain("rebuildMainWindow");
    expect(handler).not.toContain("createWindow");
    expect(MAIN_SOURCE).not.toContain("function rebuildMainWindow");
    expect(MAIN_SOURCE).not.toContain("window-appearance.json");
  });

  it("creates transparent-capable windows while renderer CSS owns opaque pixels", () => {
    expect(MAIN_SOURCE).toContain("transparent: true");
    expect(MAIN_SOURCE).toContain('backgroundColor: "#00000000"');
  });
});
