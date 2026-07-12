import { describe, expect, it } from "vitest";
import { PUBLIC_BASE_PATH, ROUTER_BASENAME } from "../publicDelivery.ts";

describe("student web public delivery contract", () => {
  it("owns the fixed /app route", () => {
    expect(PUBLIC_BASE_PATH).toBe("/app/");
    expect(ROUTER_BASENAME).toBe("/app");
  });
});
