import { describe, expect, it, vi } from "vitest";
import {
  denyWindowOpenAndMaybeOpenPintia,
  validatedPintiaProblemSetURL,
} from "../electron/externalNavigation";

describe("desktop external navigation allowlist", () => {
  it.each([
    [
      "canonical root",
      "https://pintia.cn/problem-sets/2039341868571590656",
      "https://pintia.cn/problem-sets/2039341868571590656",
    ],
    [
      "canonical typed problem list",
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/7",
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/7",
    ],
    [
      "explicit default port",
      "https://pintia.cn:443/problem-sets/2039341868571590656",
      "https://pintia.cn/problem-sets/2039341868571590656",
    ],
    [
      "uppercase hostname",
      "https://PINTIA.CN/problem-sets/2039341868571590656",
      "https://pintia.cn/problem-sets/2039341868571590656",
    ],
    [
      "surrounding ASCII control whitespace",
      "\t\nhttps://PINTIA.CN:443/problem-sets/2039341868571590656\r\n",
      "https://pintia.cn/problem-sets/2039341868571590656",
    ],
    [
      "embedded ASCII tab",
      "https://pin\ttia.cn/problem-sets/2039341868571590656",
      "https://pintia.cn/problem-sets/2039341868571590656",
    ],
  ])("returns a canonical URL for %s", (_label, value, canonical) => {
    expect(validatedPintiaProblemSetURL(value)).toBe(canonical);
  });

  it.each([
    ["non-HTTPS scheme", "http://pintia.cn/problem-sets/2039341868571590656"],
    ["subdomain", "https://student.pintia.cn/problem-sets/2039341868571590656"],
    ["userinfo", "https://user@pintia.cn/problem-sets/2039341868571590656"],
    ["password", "https://user:secret@pintia.cn/problem-sets/2039341868571590656"],
    ["non-default port", "https://pintia.cn:8443/problem-sets/2039341868571590656"],
    ["query", "https://pintia.cn/problem-sets/2039341868571590656?next=https://evil.example"],
    ["hash", "https://pintia.cn/problem-sets/2039341868571590656#section"],
    ["problem deep link", "https://pintia.cn/problem-sets/2039341868571590656/problems/501"],
    ["untyped problem list", "https://pintia.cn/problem-sets/2039341868571590656/problems"],
    ["extra typed path", "https://pintia.cn/problem-sets/2039341868571590656/problems/type/7/extra"],
    ["encoded problem-set ID", "https://pintia.cn/problem-sets/%32%30%33%39"],
    ["encoded path separator", "https://pintia.cn/problem-sets/2039341868571590656%2fproblems%2ftype%2f7"],
    ["encoded dot traversal", "https://pintia.cn/problem-sets/2039341868571590656/%2e%2e/admin"],
    ["javascript URL", "javascript:alert(1)"],
    ["malformed URL", "not a URL"],
  ])("rejects %s", (_label, value) => {
    expect(validatedPintiaProblemSetURL(value)).toBeNull();
  });

  it("opens only an allowlisted URL externally and always denies a BrowserWindow", () => {
    const openExternal = vi.fn(async () => undefined);
    const reportFailure = vi.fn();
    const allowed =
      "\tHTTPS://PINTIA.CN:443/problem-sets/2039341868571590656/problems/type/7\r";
    const canonical =
      "https://pintia.cn/problem-sets/2039341868571590656/problems/type/7";

    expect(
      denyWindowOpenAndMaybeOpenPintia(allowed, openExternal, reportFailure),
    ).toEqual({ action: "deny" });
    expect(openExternal).toHaveBeenCalledOnce();
    expect(openExternal).toHaveBeenCalledWith(canonical);

    expect(
      denyWindowOpenAndMaybeOpenPintia(
        "https://pintia.cn.evil.example/problem-sets/2039341868571590656",
        openExternal,
        reportFailure,
      ),
    ).toEqual({ action: "deny" });
    expect(openExternal).toHaveBeenCalledOnce();
    expect(reportFailure).not.toHaveBeenCalled();
  });
});
