import type {
  BrowserSession,
  ExamAnalysisGeneration,
  ExamAnalysisGenerationEvent,
} from "../src";
import { beforeEach, describe, expect, it, vi } from "vitest";

const generated = vi.hoisted(() => ({
  getExamAnalysisGeneration: vi.fn(),
  streamExamAnalysisGenerationEvents: vi.fn(),
}));

vi.mock("../src/generated", () => generated);

import {
  ExamAnalysisGenerationSequenceError,
  observeExamAnalysisGeneration,
  type ExamAnalysisGenerationConnectionState,
} from "../src/examAnalysisGenerationObserver";

const examId = "123e4567-e89b-42d3-a456-426614174000";
const firstGenerationId = "31";
const secondGenerationId = "32";

function generation(
  status: ExamAnalysisGeneration["status"],
  eventHead: number,
  generationId = firstGenerationId,
): ExamAnalysisGeneration {
  return {
    generationId,
    status,
    attemptCount: status === "queued" ? 0 : 1,
    createdAt: "2026-07-11T10:00:00Z",
    ...(status === "queued" ? {} : { startedAt: "2026-07-11T10:00:01Z" }),
    ...(status === "succeeded" || status === "superseded" || status === "failed"
      ? { finishedAt: "2026-07-11T10:00:02Z" }
      : {}),
    eventHead,
  };
}

function event(
  sequence: number,
  type: ExamAnalysisGenerationEvent["type"],
): ExamAnalysisGenerationEvent {
  return {
    sequence,
    type,
    payload: {},
    createdAt: `2026-07-11T10:00:0${sequence}Z`,
  };
}

function streamOf(...events: ExamAnalysisGenerationEvent[]) {
  return (async function* () {
    for (const item of events) yield item;
  })();
}

function fakeSession(): BrowserSession {
  return {
    client: { marker: "generated-client" },
    ensureAuthenticated: vi.fn().mockResolvedValue({}),
  } as unknown as BrowserSession;
}

function callbacks() {
  const generations: Array<{ value: ExamAnalysisGeneration; reset: boolean }> = [];
  const events: ExamAnalysisGenerationEvent[] = [];
  const states: ExamAnalysisGenerationConnectionState[] = [];
  return {
    generations,
    events,
    states,
    onGeneration: (value: ExamAnalysisGeneration, reset: boolean) => {
      generations.push({ value, reset });
    },
    onEvent: (value: ExamAnalysisGenerationEvent) => {
      events.push(value);
    },
    onConnectionState: (value: ExamAnalysisGenerationConnectionState) => {
      states.push(value);
    },
  };
}

describe("exam analysis generation observer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("replays an exact terminal generation through its pinned stream before closing", async () => {
    const session = fakeSession();
    const observed = callbacks();
    const current = generation("succeeded", 3);
    generated.getExamAnalysisGeneration.mockResolvedValue({ data: current });
    generated.streamExamAnalysisGenerationEvents.mockResolvedValue({
      stream: streamOf(event(1, "queued"), event(2, "running"), event(3, "succeeded")),
    });

    await observeExamAnalysisGeneration({
      session,
      examId,
      signal: new AbortController().signal,
      resume: { afterSequence: 0 },
      ...observed,
    });

    expect(observed.generations[0]).toEqual({ value: current, reset: true });
    expect(observed.events.map((item) => item.sequence)).toEqual([1, 2, 3]);
    expect(observed.states).toEqual(["connecting", "live", "closed"]);
    expect(generated.streamExamAnalysisGenerationEvents).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { examId, generationId: firstGenerationId },
        headers: { "Last-Event-ID": "0" },
      }),
    );
  });

  it("resumes a clean non-terminal close with Last-Event-ID and deduplicates replay", async () => {
    const session = fakeSession();
    const observed = callbacks();
    const queued = event(1, "queued");
    const succeeded = event(2, "succeeded");
    generated.getExamAnalysisGeneration
      .mockResolvedValueOnce({ data: generation("running", 2) })
      .mockResolvedValueOnce({ data: generation("running", 2) })
      .mockResolvedValueOnce({ data: generation("running", 2) })
      .mockResolvedValueOnce({ data: generation("succeeded", 2) });
    generated.streamExamAnalysisGenerationEvents
      .mockResolvedValueOnce({ stream: streamOf(queued) })
      .mockResolvedValueOnce({ stream: streamOf(queued, succeeded) });

    await observeExamAnalysisGeneration({
      session,
      examId,
      signal: new AbortController().signal,
      resume: { afterSequence: 0 },
      reconnectDelayMs: 0,
      ...observed,
    });

    expect(observed.events.map((item) => item.sequence)).toEqual([1, 2]);
    expect(generated.streamExamAnalysisGenerationEvents).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        path: { examId, generationId: firstGenerationId },
        headers: { "Last-Event-ID": "0" },
      }),
    );
    expect(generated.streamExamAnalysisGenerationEvents).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        path: { examId, generationId: firstGenerationId },
        headers: { "Last-Event-ID": "1" },
      }),
    );
    expect(observed.states).toEqual([
      "connecting",
      "live",
      "reconnecting",
      "connecting",
      "live",
      "closed",
    ]);
    expect(observed.generations.at(-1)?.value.status).toBe("succeeded");
    const terminalStreamOptions = generated.streamExamAnalysisGenerationEvents.mock.calls[1]?.[0] as
      | { signal?: AbortSignal }
      | undefined;
    expect(terminalStreamOptions?.signal?.aborted).toBe(true);
  });

  it("preserves a matching generation resume cursor on manual retry", async () => {
    const session = fakeSession();
    const observed = callbacks();
    generated.getExamAnalysisGeneration
      .mockResolvedValueOnce({ data: generation("running", 4) })
      .mockResolvedValueOnce({ data: generation("failed", 4) });
    generated.streamExamAnalysisGenerationEvents.mockResolvedValue({
      stream: streamOf(event(4, "failed")),
    });

    await observeExamAnalysisGeneration({
      session,
      examId,
      signal: new AbortController().signal,
      resume: { generationId: firstGenerationId, afterSequence: 3 },
      ...observed,
    });

    expect(observed.generations[0]?.reset).toBe(false);
    expect(observed.events.map((item) => item.sequence)).toEqual([4]);
    expect(generated.streamExamAnalysisGenerationEvents).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { examId, generationId: firstGenerationId },
        headers: { "Last-Event-ID": "3" },
      }),
    );
  });

  it("resets the cursor when GET selects a newer current generation", async () => {
    const session = fakeSession();
    const observed = callbacks();
    generated.getExamAnalysisGeneration
      .mockResolvedValueOnce({ data: generation("running", 1, secondGenerationId) })
      .mockResolvedValueOnce({ data: generation("succeeded", 1, secondGenerationId) });
    generated.streamExamAnalysisGenerationEvents.mockResolvedValue({
      stream: streamOf(event(1, "succeeded")),
    });

    await observeExamAnalysisGeneration({
      session,
      examId,
      signal: new AbortController().signal,
      resume: { generationId: firstGenerationId, afterSequence: 3 },
      ...observed,
    });

    expect(observed.generations[0]?.reset).toBe(true);
    expect(generated.streamExamAnalysisGenerationEvents).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { examId, generationId: secondGenerationId },
        headers: { "Last-Event-ID": "0" },
      }),
    );
  });

  it("pins the GET generation across the SSE race and restarts a changed current generation at zero", async () => {
    const session = fakeSession();
    const observed = callbacks();
    const oldRunning = generation("running", 2, firstGenerationId);
    const newSucceeded = generation("succeeded", 2, secondGenerationId);
    generated.getExamAnalysisGeneration
      .mockResolvedValueOnce({ data: oldRunning })
      .mockResolvedValueOnce({ data: newSucceeded })
      .mockResolvedValueOnce({ data: newSucceeded })
      .mockResolvedValueOnce({ data: newSucceeded });
    generated.streamExamAnalysisGenerationEvents
      .mockResolvedValueOnce({ stream: streamOf(event(1, "running")) })
      .mockResolvedValueOnce({ stream: streamOf(event(1, "queued"), event(2, "succeeded")) });

    await observeExamAnalysisGeneration({
      session,
      examId,
      signal: new AbortController().signal,
      resume: { afterSequence: 0 },
      ...observed,
    });

    expect(generated.streamExamAnalysisGenerationEvents).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        path: { examId, generationId: firstGenerationId },
        headers: { "Last-Event-ID": "0" },
      }),
    );
    expect(generated.streamExamAnalysisGenerationEvents).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        path: { examId, generationId: secondGenerationId },
        headers: { "Last-Event-ID": "0" },
      }),
    );
    expect(observed.generations.filter((item) => item.reset).map((item) => item.value.generationId)).toEqual([
      firstGenerationId,
      secondGenerationId,
    ]);
    expect(observed.states.at(-1)).toBe("closed");
  });

  it("rejects an event gap and aborts that stream", async () => {
    const session = fakeSession();
    const observed = callbacks();
    generated.getExamAnalysisGeneration.mockResolvedValue({
      data: generation("running", 2),
    });
    generated.streamExamAnalysisGenerationEvents.mockResolvedValue({
      stream: streamOf(event(2, "running")),
    });

    await expect(observeExamAnalysisGeneration({
      session,
      examId,
      signal: new AbortController().signal,
      resume: { afterSequence: 0 },
      ...observed,
    })).rejects.toEqual(expect.objectContaining({
      name: "ExamAnalysisGenerationSequenceError",
      expectedSequence: 1,
      receivedSequence: 2,
    }));
    expect(observed.events).toEqual([]);
    expect(ExamAnalysisGenerationSequenceError).toBeDefined();
  });
});
