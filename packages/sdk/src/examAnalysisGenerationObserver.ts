import {
  getExamAnalysisGeneration,
  streamExamAnalysisGenerationEvents,
  type ExamAnalysisGeneration,
  type ExamAnalysisGenerationEvent,
} from "./generated";
import type { BrowserSession } from "./browserSession";

const DEFAULT_RECONNECT_DELAY_MS = 1_000;

export type ExamAnalysisGenerationConnectionState =
  | "connecting"
  | "live"
  | "reconnecting"
  | "closed";

export interface ExamAnalysisGenerationResumeCursor {
  generationId?: string;
  afterSequence: number;
}

export interface ObserveExamAnalysisGenerationOptions {
  session: BrowserSession;
  examId: string;
  signal: AbortSignal;
  resume: ExamAnalysisGenerationResumeCursor;
  reconnectDelayMs?: number;
  onGeneration: (generation: ExamAnalysisGeneration, resetEvents: boolean) => void;
  onEvent: (event: ExamAnalysisGenerationEvent) => void;
  onConnectionState: (state: ExamAnalysisGenerationConnectionState) => void;
}

export class ExamAnalysisGenerationSequenceError extends Error {
  readonly expectedSequence: number;
  readonly receivedSequence: number;

  constructor(expectedSequence: number, receivedSequence: number) {
    super(
      `Exam analysis generation event sequence is not contiguous: expected ${expectedSequence}, received ${receivedSequence}.`,
    );
    this.name = "ExamAnalysisGenerationSequenceError";
    this.expectedSequence = expectedSequence;
    this.receivedSequence = receivedSequence;
  }
}

export async function observeExamAnalysisGeneration({
  session,
  examId,
  signal,
  resume,
  reconnectDelayMs = DEFAULT_RECONNECT_DELAY_MS,
  onGeneration,
  onEvent,
  onConnectionState,
}: ObserveExamAnalysisGenerationOptions): Promise<void> {
  validateResume(resume);
  if (!Number.isSafeInteger(reconnectDelayMs) || reconnectDelayMs < 0) {
    throw new TypeError("reconnectDelayMs must be a non-negative safe integer.");
  }

  let current = await readCurrentGeneration(session, examId, signal);
  if (signal.aborted) return;

  let generationId = resume.generationId;
  let afterSequence = resume.afterSequence;
  let resetEvents = generationId !== current.generationId;
  if (resetEvents) {
    generationId = current.generationId;
    afterSequence = 0;
  } else if (afterSequence > current.eventHead) {
    throw new ExamAnalysisGenerationSequenceError(current.eventHead, afterSequence);
  }
  onGeneration(current, resetEvents);

  while (!signal.aborted) {
    if (isTerminalStatus(current.status) && afterSequence === current.eventHead) {
      onConnectionState("closed");
      return;
    }

    onConnectionState("connecting");
    await session.ensureAuthenticated();
    if (signal.aborted) return;

    let streamFailure: unknown;
    const streamController = new AbortController();
    const abortStream = () => streamController.abort();
    signal.addEventListener("abort", abortStream, { once: true });

    const result = await streamExamAnalysisGenerationEvents({
      client: session.client,
      path: { examId, generationId: current.generationId },
      headers: { "Last-Event-ID": String(afterSequence) },
      signal: streamController.signal,
      sseMaxRetryAttempts: 1,
      onSseError: (error) => {
        streamFailure = error;
      },
    });

    let generationChanged = false;
    let terminalCaughtUp = false;
    onConnectionState("live");

    try {
      for await (const event of result.stream) {
        if (signal.aborted) return;
        if (event.sequence <= afterSequence) continue;
        if (event.sequence !== afterSequence + 1) {
          streamController.abort();
          throw new ExamAnalysisGenerationSequenceError(afterSequence + 1, event.sequence);
        }

        afterSequence = event.sequence;
        onEvent(event);

        const refreshed = await readCurrentGeneration(session, examId, signal);
        if (signal.aborted) return;
        if (refreshed.generationId !== generationId) {
          streamController.abort();
          current = refreshed;
          generationId = refreshed.generationId;
          afterSequence = 0;
          generationChanged = true;
          onGeneration(refreshed, true);
          break;
        }
        if (refreshed.eventHead < afterSequence) {
          streamController.abort();
          throw new ExamAnalysisGenerationSequenceError(afterSequence, refreshed.eventHead);
        }

        current = refreshed;
        onGeneration(refreshed, false);
        if (isTerminalEvent(event) && !isTerminalStatus(refreshed.status)) {
          streamController.abort();
          throw new Error("Pinned generation terminal event disagrees with current generation state.");
        }
        if (isTerminalStatus(refreshed.status) && afterSequence === refreshed.eventHead) {
          terminalCaughtUp = true;
          streamController.abort();
          break;
        }
      }
    } finally {
      streamController.abort();
      signal.removeEventListener("abort", abortStream);
    }

    if (signal.aborted) return;
    if (generationChanged) continue;
    if (terminalCaughtUp) {
      onConnectionState("closed");
      return;
    }
    if (streamFailure !== undefined) throw streamFailure;

    const refreshed = await readCurrentGeneration(session, examId, signal);
    if (signal.aborted) return;
    resetEvents = refreshed.generationId !== generationId;
    current = refreshed;
    if (resetEvents) {
      generationId = refreshed.generationId;
      afterSequence = 0;
    }
    onGeneration(refreshed, resetEvents);
    if (isTerminalStatus(refreshed.status) && afterSequence === refreshed.eventHead) {
      onConnectionState("closed");
      return;
    }

    onConnectionState("reconnecting");
    await abortableDelay(reconnectDelayMs, signal);
  }
}

async function readCurrentGeneration(
  session: BrowserSession,
  examId: string,
  signal: AbortSignal,
): Promise<ExamAnalysisGeneration> {
  await session.ensureAuthenticated();
  const result = await getExamAnalysisGeneration({
    client: session.client,
    path: { examId },
    signal,
    throwOnError: true,
  });
  return result.data;
}

function validateResume(resume: ExamAnalysisGenerationResumeCursor): void {
  if (!Number.isSafeInteger(resume.afterSequence) || resume.afterSequence < 0) {
    throw new TypeError("afterSequence must be a non-negative safe integer.");
  }
  if (resume.afterSequence > 0 && resume.generationId === undefined) {
    throw new TypeError("generationId is required when afterSequence is greater than zero.");
  }
}

function isTerminalEvent(event: ExamAnalysisGenerationEvent): boolean {
  return isTerminalStatus(event.type);
}

function isTerminalStatus(status: ExamAnalysisGeneration["status"]): boolean {
  return status === "succeeded" || status === "superseded" || status === "failed";
}

function abortableDelay(delayMs: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted || delayMs === 0) return Promise.resolve();
  return new Promise((resolve) => {
    const timeout = setTimeout(() => {
      signal.removeEventListener("abort", handleAbort);
      resolve();
    }, delayMs);
    const handleAbort = () => {
      clearTimeout(timeout);
      resolve();
    };
    signal.addEventListener("abort", handleAbort, { once: true });
  });
}
