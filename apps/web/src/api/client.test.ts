import { BrowserSessionError } from "@ascendany/sdk";
import { describe, expect, it } from "vitest";
import { apiFailureMessage, resolveAPIOrigin } from "./client";

describe("web API origin", () => {
  it("uses an explicitly configured canonical origin", () => {
    expect(
      resolveAPIOrigin("https://api.example.com", "https://app.example.com"),
    ).toBe("https://api.example.com");
  });

  it("uses the page origin for same-origin deployment", () => {
    expect(resolveAPIOrigin(undefined, "http://localhost:5175")).toBe(
      "http://localhost:5175",
    );
    expect(resolveAPIOrigin("", "https://ascendany.example.com")).toBe(
      "https://ascendany.example.com",
    );
  });
});

describe("web API errors", () => {
  it("exposes the public v2 API error message", () => {
    const error = new BrowserSessionError("login", 401, {
      code: "invalid_credentials",
      message: "用户名或密码错误",
      requestId: "123e4567-e89b-42d3-a456-426614174000",
    });
    expect(apiFailureMessage(error)).toBe("用户名或密码错误");
  });

  it("does not reinterpret an unknown payload", () => {
    const error = new BrowserSessionError("bootstrap", 500, {
      detail: "internal",
    });
    expect(apiFailureMessage(error)).toContain("bootstrap failed with HTTP 500");
  });
});
