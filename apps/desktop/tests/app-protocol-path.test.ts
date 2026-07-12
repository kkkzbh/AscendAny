import path from "node:path";
import { describe, expect, it } from "vitest";
import {
  DESKTOP_APP_ENTRY_URL,
  DESKTOP_APP_ORIGIN,
  resolveDesktopAssetPath,
} from "../electron/appProtocolPath";

describe("desktop application protocol path", () => {
  const root = path.resolve("/opt/ascendany/dist");

  it("uses one stable secure application origin", () => {
    expect(DESKTOP_APP_ORIGIN).toBe("ascendany-app://bundle");
    expect(DESKTOP_APP_ENTRY_URL).toBe("ascendany-app://bundle/index.html");
  });

  it("maps only canonical bundle URLs below the distribution root", () => {
    expect(resolveDesktopAssetPath(root, DESKTOP_APP_ENTRY_URL)).toBe(
      path.join(root, "index.html"),
    );
    expect(resolveDesktopAssetPath(root, `${DESKTOP_APP_ORIGIN}/assets/app.js`)).toBe(
      path.join(root, "assets/app.js"),
    );
    expect(resolveDesktopAssetPath(root, `${DESKTOP_APP_ORIGIN}/`)).toBe(
      path.join(root, "index.html"),
    );
  });

  it("rejects foreign origins, malformed escapes, queries, and traversal forms", () => {
    for (const value of [
      "file:///opt/ascendany/dist/index.html",
      "https://bundle/index.html",
      "ascendany-app://foreign/index.html",
      `${DESKTOP_APP_ENTRY_URL}?cache=1`,
      `${DESKTOP_APP_ORIGIN}/assets/`,
      `${DESKTOP_APP_ORIGIN}/%2e%2e%2fsecret`,
      `${DESKTOP_APP_ORIGIN}/assets%5csecret`,
      `${DESKTOP_APP_ORIGIN}/%zz`,
    ]) {
      expect(resolveDesktopAssetPath(root, value), value).toBeNull();
    }
  });
});
