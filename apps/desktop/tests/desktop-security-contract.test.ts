import { readFileSync, readdirSync } from "node:fs";
import { extname, join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const desktopRoot = resolve(process.cwd());
const mainSource = readFileSync(join(desktopRoot, "electron/main.ts"), "utf8");
const preloadSource = readFileSync(join(desktopRoot, "electron/preload.ts"), "utf8");

function readRendererSources(directory: string): string {
  return readdirSync(directory, { withFileTypes: true })
    .map((entry) => {
      const target = join(directory, entry.name);
      if (entry.isDirectory()) return readRendererSources(target);
      return [".ts", ".tsx"].includes(extname(entry.name))
        ? readFileSync(target, "utf8")
        : "";
    })
    .join("\n");
}

const rendererSource = readRendererSources(join(desktopRoot, "src"));

describe("desktop security contract", () => {
  it("keeps the isolated sandbox and rejects renderer escape routes", () => {
    expect(mainSource).toContain("contextIsolation: true");
    expect(mainSource).toContain("nodeIntegration: false");
    expect(mainSource).toContain("sandbox: true");
    expect(mainSource).toContain("setWindowOpenHandler(({ url }) =>");
    expect(mainSource).toContain("denyWindowOpenAndMaybeOpenPintia(");
    expect(mainSource).toContain("shell.openExternal(allowedURL)");
    expect(mainSource).toContain('webContents.on("will-navigate"');
    expect(mainSource).toContain("setPermissionRequestHandler");
    expect(mainSource).toContain("callback(false)");
    expect(mainSource).toContain("ASCENDANY_LINUX_GPU_MODE must be one of");
    expect(mainSource).toContain("ASCENDANY_LINUX_IME_MODE must be one of");
    expect(mainSource).not.toContain("fallback to");
  });

  it("exposes only window controls and updater operations through preload", () => {
    expect(`${mainSource}\n${preloadSource}`).not.toMatch(
      /credential-|auth-session-|local-state-|notes-export-|avatar-/,
    );
    expect(preloadSource).toContain('ipcRenderer.send("window-minimize")');
    expect(preloadSource).toContain('ipcRenderer.invoke("updater-get-state")');
  });

  it("contains no handwritten endpoint, token handoff, or unsupported online client", () => {
    expect(rendererSource).not.toContain("/api/v1");
    expect(rendererSource).not.toContain("/api/v2");
    expect(rendererSource).not.toMatch(/\bfetch\s*\(/);
    expect(rendererSource).not.toContain("URLSearchParams");
    expect(rendererSource).not.toMatch(/location\.(search|hash)/);
    expect(rendererSource).not.toMatch(/accessToken|refreshToken/);
    expect(rendererSource).not.toMatch(/feedback/i);
    expect(rendererSource).toContain("getSelfAchievements");
    expect(rendererSource).toContain("getSelfRecommendation");
    expect(rendererSource).toContain("streamAgentRunEvents");
    expect(rendererSource).toContain("streamOjJudgeEvents");
    expect(rendererSource).toContain("uploadOjSubmission");
    expect(rendererSource).toContain("uploadOjProblemVersion");
    expect(rendererSource).not.toMatch(/sso|\/register/i);
  });
});
