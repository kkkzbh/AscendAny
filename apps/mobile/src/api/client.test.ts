import { describe, expect, it } from "vitest";
import { BrowserSessionError } from "@ascendany/sdk";
import { apiFailureMessage, resolveAPIOrigin } from "./client";

describe("mobile API origin", () => {
  it("uses the configured canonical origin", () => {
    expect(resolveAPIOrigin("https://api.example.com", "https://localhost", true)).toBe("https://api.example.com");
  });

  it("uses the current web origin for the Vite proxy", () => {
    expect(resolveAPIOrigin(undefined, "http://localhost:5173", false)).toBe("http://localhost:5173");
  });

  it("requires an explicit API origin for native builds", () => {
    expect(() => resolveAPIOrigin(undefined, "https://localhost", true)).toThrow("VITE_API_BASE_URL is required");
  });
});

describe("mobile API errors", () => {
  it("exposes the public server message from BrowserSession", () => {
    const error = new BrowserSessionError("login", 401, {
      code: "invalid_credentials",
      message: "用户名或密码错误",
    });
    expect(apiFailureMessage(error)).toBe("用户名或密码错误");
  });
});
