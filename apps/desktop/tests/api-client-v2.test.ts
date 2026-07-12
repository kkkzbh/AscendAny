import { BrowserSessionError } from "@ascendany/sdk";
import { describe, expect, it } from "vitest";
import {
  apiFailureMessage,
  resolveDesktopAPIOrigin,
} from "../src/api/client";

describe("desktop v2 API origin", () => {
  it("uses an explicitly configured canonical origin", () => {
    expect(
      resolveDesktopAPIOrigin(
        "https://api.example.com",
        "ascendany-app://bundle",
        "ascendany-app:",
      ),
    ).toBe("https://api.example.com");
  });

  it("uses the current HTTP origin for development and web preview", () => {
    expect(
      resolveDesktopAPIOrigin(undefined, "http://127.0.0.1:5173", "http:"),
    ).toBe("http://127.0.0.1:5173");
  });

  it("requires an explicit API origin for the packaged application scheme", () => {
    expect(() =>
      resolveDesktopAPIOrigin(
        undefined,
        "ascendany-app://bundle",
        "ascendany-app:",
      ),
    ).toThrow("VITE_API_BASE_URL is required");
  });
});

describe("desktop v2 API errors", () => {
  it("exposes the public API error message", () => {
    const error = new BrowserSessionError("login", 401, {
      code: "invalid_credentials",
      message: "用户名或密码错误",
      requestId: "123e4567-e89b-42d3-a456-426614174000",
    });

    expect(apiFailureMessage(error)).toBe("用户名或密码错误");
  });

  it("keeps unknown server payloads opaque", () => {
    const error = new BrowserSessionError("bootstrap", 500, {
      detail: "internal",
    });

    expect(apiFailureMessage(error)).toContain("bootstrap failed with HTTP 500");
  });
});
