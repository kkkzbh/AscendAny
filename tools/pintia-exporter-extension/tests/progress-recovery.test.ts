import { afterEach, describe, expect, it, vi } from "vitest";
import { ProgressRecoveryController } from "../src/platform/progress-recovery";

afterEach(() => {
  vi.useRealTimers();
});

describe("progress-page interrupted export recovery", () => {
  it("waits for the safety window, refreshes state, then retries only from the refreshed state", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-12T00:00:00.000Z"));
    const requestState = vi.fn();
    const retry = vi.fn();
    const controller = new ProgressRecoveryController({ requestState, retry });
    const waitingUntil = "2026-07-12T00:02:00.000Z";

    controller.observe({ active: true, live: false, waitingUntil });
    controller.observe({ active: true, live: false, waitingUntil });
    expect(vi.getTimerCount()).toBe(1);
    await vi.advanceTimersByTimeAsync(119_999);
    expect(requestState).not.toHaveBeenCalled();
    expect(retry).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    expect(requestState).toHaveBeenCalledOnce();
    expect(retry).not.toHaveBeenCalled();

    controller.observe({ active: true, live: false, waitingUntil });
    expect(retry).toHaveBeenCalledOnce();
    controller.observe({ active: true, live: false, waitingUntil });
    expect(retry).toHaveBeenCalledOnce();
  });

  it("cancels the only scheduled refresh when a live owner appears or the page disconnects", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-12T00:00:00.000Z"));
    const requestState = vi.fn();
    const retry = vi.fn();
    const controller = new ProgressRecoveryController({ requestState, retry });
    const waitingUntil = "2026-07-12T00:02:00.000Z";

    controller.observe({ active: true, live: false, waitingUntil });
    controller.observe({ active: true, live: true, waitingUntil });
    await vi.advanceTimersByTimeAsync(120_000);
    expect(requestState).not.toHaveBeenCalled();
    expect(retry).not.toHaveBeenCalled();

    controller.observe({ active: true, live: false, waitingUntil: "2026-07-12T00:04:00.000Z" });
    controller.reset();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(requestState).not.toHaveBeenCalled();
  });

  it("refreshes ownership after a blocked recovery error before allowing one new retry", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-12T00:00:00.000Z"));
    const requestState = vi.fn();
    const retry = vi.fn();
    const controller = new ProgressRecoveryController({ requestState, retry });

    controller.observe({ active: true, live: false, waitingUntil: null });
    expect(retry).toHaveBeenCalledOnce();
    expect(controller.handleCommandError(true)).toBe(true);
    expect(requestState).toHaveBeenCalledOnce();

    controller.observe({ active: true, live: false, waitingUntil: null });
    expect(retry).toHaveBeenCalledTimes(2);
    expect(controller.handleCommandError(false)).toBe(true);
    expect(requestState).toHaveBeenCalledTimes(2);
    expect(controller.handleCommandError(false)).toBe(false);
  });
});
