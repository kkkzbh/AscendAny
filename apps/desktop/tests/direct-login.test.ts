import { describe, expect, it } from "vitest";
import {
  extractDirectLoginParamsFromUrl,
  isDirectLoginEnabled,
  scrubDirectLoginParams,
} from "@/lib/directLogin";

describe("direct login helpers", () => {
  it("extracts default direct login params", () => {
    const url = new URL("https://example.com/?username=alice&password=secret");
    expect(extractDirectLoginParamsFromUrl(url)).toEqual({
      username: "alice",
      password: "secret",
      passwordMode: "plain",
      autoLogin: true,
      rememberPassword: false,
      deviceId: undefined,
    });
  });

  it("supports alias keys and boolean overrides", () => {
    const url = new URL(
      "https://example.com/?aa_username=bob&pass=pwd&autoLogin=false&rememberPassword=yes&aa_device_id=embed",
    );
    expect(extractDirectLoginParamsFromUrl(url)).toEqual({
      username: "bob",
      password: "pwd",
      passwordMode: "plain",
      autoLogin: false,
      rememberPassword: true,
      deviceId: "embed",
    });
  });

  it("prefers stored password params when provided", () => {
    const url = new URL(
      "https://example.com/?username=alice&password=plain&storedPassword=hashed",
    );
    expect(extractDirectLoginParamsFromUrl(url)).toEqual({
      username: "alice",
      password: "hashed",
      passwordMode: "stored_value",
      autoLogin: true,
      rememberPassword: false,
      deviceId: undefined,
    });
  });

  it("returns null when credentials are incomplete", () => {
    expect(
      extractDirectLoginParamsFromUrl(new URL("https://example.com/?username=alice")),
    ).toBeNull();
    expect(
      extractDirectLoginParamsFromUrl(new URL("https://example.com/?password=secret")),
    ).toBeNull();
  });

  it("scrubs sensitive params from URL", () => {
    const url = new URL(
      "https://example.com/path?foo=1&username=alice&password=secret&storedPassword=hashed&rememberPassword=1#view",
    );
    expect(scrubDirectLoginParams(url)).toBe("/path?foo=1#view");
  });

  it("parses enable switch values", () => {
    expect(isDirectLoginEnabled("true")).toBe(true);
    expect(isDirectLoginEnabled("YES")).toBe(true);
    expect(isDirectLoginEnabled("0")).toBe(false);
    expect(isDirectLoginEnabled(undefined)).toBe(false);
  });
});
