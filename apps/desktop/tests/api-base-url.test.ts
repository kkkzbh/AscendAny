import { describe, expect, it, vi } from "vitest";
import { resolveApiBaseUrl } from "@/lib/api";

describe("resolveApiBaseUrl", () => {
  it("uses the browser origin for the web build when no env override is provided", () => {
    expect(
      resolveApiBaseUrl({
        location: {
          origin: "https://ascendai.kkkzbh.cn",
          protocol: "https:",
        },
      }),
    ).toBe("https://ascendai.kkkzbh.cn");
  });

  it("keeps the local API default for Electron renderers", () => {
    expect(
      resolveApiBaseUrl({
        location: {
          origin: "http://localhost:5173",
          protocol: "http:",
        },
        electronAPI: {
          minimize: vi.fn(),
          maximize: vi.fn(),
          close: vi.fn(),
          platform: "linux",
        },
      }),
    ).toBe("http://127.0.0.1:8000");
  });

  it("falls back to the local API default for non-http origins", () => {
    expect(
      resolveApiBaseUrl({
        location: {
          origin: "null",
          protocol: "file:",
        },
      }),
    ).toBe("http://127.0.0.1:8000");
  });
});
