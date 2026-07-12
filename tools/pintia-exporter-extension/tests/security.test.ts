import { access, readFile } from "node:fs/promises";
import { constants } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  exactPintiaNavigationTarget,
  navigationReached,
  programmingProblemsNavigationTarget,
} from "../src/platform/navigation";
import manifest from "../src/static/manifest.json";

const extensionRoot = new URL("../", import.meta.url);

describe("extension runtime boundary", () => {
  it("keeps the manifest permission surface explicit and minimal", () => {
    expect(manifest.permissions).toEqual(["downloads", "offscreen", "scripting", "unlimitedStorage"]);
    expect(manifest.host_permissions).toEqual(["https://pintia.cn/*"]);
    expect(manifest.background.service_worker).toBe("background.js");
    expect(manifest).not.toHaveProperty("content_scripts");
    expect(manifest).not.toHaveProperty("web_accessible_resources");
    expect(manifest.content_security_policy.extension_pages).toBe("script-src 'self'; object-src 'none'");
  });

  it("uses Chrome's MAIN-world result channel and has no page-visible message bridge", async () => {
    const background = await readFile(new URL("../src/background.ts", import.meta.url), "utf8");
    const collector = await readFile(new URL("../src/main-world-collector.ts", import.meta.url), "utf8");
    const runtime = await readFile(
      new URL("../src/platform/chrome-export-runtime.ts", import.meta.url),
      "utf8",
    );
    const allRuntimeSource = `${background}\n${collector}\n${runtime}`;

    expect(runtime).toContain("chrome.scripting.executeScript");
    expect(runtime).toContain('world: "MAIN"');
    expect(background).not.toContain("chrome.scripting.executeScript");
    expect(background).not.toContain("chrome.downloads");
    expect(background).not.toContain("chrome.tabs.sendMessage");
    expect(allRuntimeSource).not.toContain("window.postMessage");
    expect(allRuntimeSource).not.toContain("addEventListener(\"message\"");
    for (const removed of [
      "../src/content-script.ts",
      "../src/page-bridge.ts",
      "../src/platform/bridge-security.ts",
    ]) {
      await expect(access(new URL(removed, import.meta.url), constants.F_OK)).rejects.toThrow();
    }
  });

  it("passes only an OPFS handle through the offscreen runtime message", async () => {
    const blobStore = await readFile(new URL("../src/platform/snapshot-blob-store.ts", import.meta.url), "utf8");
    const createMessage = blobStore.match(
      /sendMessage\(\{[\s\S]*?ASCENDANY_CREATE_SNAPSHOT_BLOB_V2[\s\S]*?\}\)/,
    )?.[0];
    expect(createMessage).toBeDefined();
    expect(createMessage).toContain("fileName: file.fileName");
    expect(createMessage).toContain("expectedBytes");
    expect(createMessage).not.toMatch(/\bjson\b/);
  });

  it("accepts only the structured canonical routes needed for a multi-paper problem set", () => {
    const target = programmingProblemsNavigationTarget("2039341868571590656");
    expect(target.requestedUrl).toBe(
      "https://pintia.cn/problem-sets/2039341868571590656/problems",
    );
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems",
    )).toBe(true);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/6",
    )).toBe(true);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/6?paperIndex=1",
    )).toBe(true);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/6?paperIndex=2",
    )).toBe(false);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/6?paperIndex=1&filter=all",
    )).toBe(false);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems?paperIndex=1",
    )).toBe(false);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/6?",
    )).toBe(false);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/6#paper-1",
    )).toBe(false);
    expect(navigationReached(
      target,
      "https://pintia.cn/problem-sets/999/problems/type/6?paperIndex=1",
    )).toBe(false);
    expect(navigationReached(
      target,
      "https://evil.example/problem-sets/2039341868571590656/problems/type/6?paperIndex=1",
    )).toBe(false);

    const rankings = exactPintiaNavigationTarget(
      "/problem-sets/2039341868571590656/rankings",
    );
    expect(navigationReached(
      rankings,
      "https://pintia.cn/problem-sets/2039341868571590656/rankings?filter=all",
    )).toBe(false);
  });

  it("removes every handwritten JavaScript runtime entry", async () => {
    for (const name of [
      "background.js",
      "offscreen.js",
      "popup.js",
      "progress.js",
      "manifest.json",
    ]) {
      await expect(access(new URL(name, extensionRoot), constants.F_OK)).rejects.toThrow();
    }
  });

  it("wires the persistent coordinator and bounded detail batches into background", async () => {
    const background = await readFile(new URL("../src/background.ts", import.meta.url), "utf8");
    const collector = await readFile(
      new URL("../src/main-world-collector.ts", import.meta.url),
      "utf8",
    );

    expect(background).toContain("const exportCoordinator = new ExportCoordinator({");
    expect(background).toContain("execute: (context) => runExportWithinCoordinator(port, command, context)");
    expect(background).toContain("start += DETAIL_BATCH_SIZE");
    expect(background).toContain("await paceDetailBatch(submissionIds.length, context.signal)");
    expect(background).toContain("chromeExportCoordinatorRuntime.keepServiceWorkerAlive(");
    expect(background).toContain('"submission-details",\n            submissionIds');
    expect(background).toContain("beginCaptureAttempt(task)");
    expect(collector).not.toContain("detailRequestSpacingMs");
    expect(collector).not.toContain(" pacing`");
  });

  it("keeps generated validator artifacts out of source control", async () => {
    const ignore = await readFile(new URL("../.gitignore", import.meta.url), "utf8");
    expect(ignore.split(/\r?\n/)).toContain(".generated/");
  });
});
