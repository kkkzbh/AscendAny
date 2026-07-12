export interface ProgressRecoveryCoordination {
  active: boolean;
  live: boolean;
  waitingUntil: string | null;
}

export interface ProgressRecoveryActions {
  requestState(): void;
  retry(): void;
}

interface ProgressRecoveryClock {
  now(): number;
  setTimer(callback: () => void, milliseconds: number): ReturnType<typeof setTimeout>;
  clearTimer(timer: ReturnType<typeof setTimeout>): void;
}

const systemProgressRecoveryClock: ProgressRecoveryClock = {
  now: () => Date.now(),
  setTimer: (callback, milliseconds) => setTimeout(callback, milliseconds),
  clearTimer: (timer) => clearTimeout(timer),
};

function waitingUntilMilliseconds(value: string | null): number | null {
  if (value === null) {
    return null;
  }
  const milliseconds = Date.parse(value);
  if (
    !Number.isSafeInteger(milliseconds) ||
    milliseconds < 0 ||
    new Date(milliseconds).toISOString() !== value
  ) {
    throw new Error("Export recovery waitingUntil must be an exact timestamp.");
  }
  return milliseconds;
}

export class ProgressRecoveryController {
  private retryPending = false;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    private readonly actions: ProgressRecoveryActions,
    private readonly clock: ProgressRecoveryClock = systemProgressRecoveryClock,
  ) {}

  observe(coordination: ProgressRecoveryCoordination): void {
    if (!coordination.active || coordination.live) {
      this.reset();
      return;
    }
    if (this.retryPending) {
      this.cancelTimer();
      return;
    }
    const deadline = waitingUntilMilliseconds(coordination.waitingUntil);
    const now = this.clock.now();
    if (deadline === null || deadline <= now) {
      this.cancelTimer();
      this.retryPending = true;
      this.actions.retry();
      return;
    }
    this.cancelTimer();
    this.timer = this.clock.setTimer(() => {
      this.timer = null;
      this.actions.requestState();
    }, deadline - now);
  }

  handleCommandError(activeOwnership: boolean): boolean {
    if (!activeOwnership && !this.retryPending) {
      return false;
    }
    this.retryPending = false;
    this.cancelTimer();
    this.actions.requestState();
    return true;
  }

  reset(): void {
    this.retryPending = false;
    this.cancelTimer();
  }

  private cancelTimer(): void {
    if (this.timer === null) {
      return;
    }
    this.clock.clearTimer(this.timer);
    this.timer = null;
  }
}
