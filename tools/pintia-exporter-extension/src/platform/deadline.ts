export class OperationDeadlineError extends Error {
  constructor(readonly operation: string, readonly milliseconds: number) {
    super(`${operation} exceeded its ${milliseconds}ms deadline.`);
    this.name = "OperationDeadlineError";
  }
}

function abortReason(signal: AbortSignal): Error {
  return signal.reason instanceof Error
    ? signal.reason
    : new DOMException("Operation aborted.", "AbortError");
}

export function withDeadline<T>(
  promise: Promise<T>,
  milliseconds: number,
  operation: string,
  signal?: AbortSignal,
): Promise<T> {
  if (!Number.isSafeInteger(milliseconds) || milliseconds <= 0) {
    throw new Error(`${operation} deadline must be a positive safe integer.`);
  }
  if (signal?.aborted === true) {
    return Promise.reject(abortReason(signal));
  }
  return new Promise<T>((resolve, reject) => {
    let settled = false;
    const finish = (callback: () => void): void => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timeout);
      signal?.removeEventListener("abort", onAbort);
      callback();
    };
    const onAbort = (): void => finish(() => reject(abortReason(signal as AbortSignal)));
    const timeout = setTimeout(
      () => finish(() => reject(new OperationDeadlineError(operation, milliseconds))),
      milliseconds,
    );
    signal?.addEventListener("abort", onAbort, { once: true });
    promise.then(
      (value) => finish(() => resolve(value)),
      (error: unknown) => finish(() => reject(error)),
    );
  });
}

export function abortableDelay(milliseconds: number, signal: AbortSignal): Promise<void> {
  if (!Number.isSafeInteger(milliseconds) || milliseconds < 0) {
    throw new Error("Delay must be a non-negative safe integer.");
  }
  if (milliseconds === 0) {
    signal.throwIfAborted();
    return Promise.resolve();
  }
  return withDeadline(
    new Promise<void>((resolve) => setTimeout(resolve, milliseconds)),
    milliseconds + 1,
    "delay",
    signal,
  );
}
