import { beforeEach, describe, expect, it } from "vitest";
import {
  clearTokens,
  consumeTokenHandoff,
  getStoredRefreshToken,
  getStoredToken,
} from "./client";

describe("token handoff", () => {
  beforeEach(() => {
    clearTokens();
    window.history.replaceState(null, "", "/");
  });

  it("stores URL handoff tokens and clears them from the address bar", () => {
    window.history.replaceState(
      null,
      "",
      "/?aa_access_token=access-123&aa_refresh_token=refresh-456&tab=students",
    );

    expect(consumeTokenHandoff()).toBe("access-123");
    expect(getStoredToken()).toBe("access-123");
    expect(getStoredRefreshToken()).toBe("refresh-456");
    expect(window.location.search).toBe("?tab=students");
  });

  it("ignores incomplete handoff URLs", () => {
    window.history.replaceState(null, "", "/?aa_access_token=access-123");

    expect(consumeTokenHandoff()).toBeNull();
    expect(getStoredToken()).toBeNull();
    expect(window.location.search).toBe("?aa_access_token=access-123");
  });
});
