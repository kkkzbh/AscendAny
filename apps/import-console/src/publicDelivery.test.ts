import { describe, expect, it } from "vitest";
import { PUBLIC_BASE_PATH, ROUTER_BASENAME } from "../publicDelivery.ts";

describe("import console public delivery contract", () => {
  it("owns the fixed /admin route", () => {
    expect(PUBLIC_BASE_PATH).toBe("/admin/");
    expect(ROUTER_BASENAME).toBe("/admin");
  });
});
