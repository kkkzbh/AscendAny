import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ImportEvent, ImportJob } from "@ascendany/sdk";
import { useImportJobStream } from "./useImportJobStream";

const mocks = vi.hoisted(() => ({
  streamImportEvents: vi.fn(),
  readImportJob: vi.fn(),
  ensureAuthenticated: vi.fn(),
}));

vi.mock("@ascendany/sdk", () => ({
  streamImportEvents: mocks.streamImportEvents,
}));

vi.mock("../api/import", () => ({
  readImportJob: mocks.readImportJob,
}));

vi.mock("../api/v2Client", () => ({
  v2Client: { kind: "test-client" },
  browserSession: { ensureAuthenticated: mocks.ensureAuthenticated },
  apiFailureMessage: (error: unknown) => error instanceof Error ? error.message : "request failed",
}));

const runningJob: ImportJob = {
  id: "123e4567-e89b-42d3-a456-426614174000",
  artifactSha256: "a".repeat(64),
  status: "running",
  stage: "validating",
  createdAt: "2026-07-11T04:00:00Z",
  updatedAt: "2026-07-11T04:00:01Z",
  examId: null,
  snapshotId: null,
  error: null,
};

const completedJob: ImportJob = {
  ...runningJob,
  status: "succeeded",
  stage: "completed",
  updatedAt: "2026-07-11T04:00:02Z",
  examId: "223e4567-e89b-42d3-a456-426614174001",
  snapshotId: "323e4567-e89b-42d3-a456-426614174002",
};

function event(sequence: number, type: string): ImportEvent {
  return {
    sequence,
    type,
    occurredAt: `2026-07-11T04:00:0${sequence}Z`,
    payload: {},
  };
}

function stream(events: ImportEvent[]) {
  return {
    stream: (async function* () {
      for (const item of events) yield item;
    })(),
  };
}

describe("useImportJobStream", () => {
  beforeEach(() => {
    mocks.streamImportEvents.mockReset();
    mocks.readImportJob.mockReset();
    mocks.ensureAuthenticated.mockReset();
    mocks.ensureAuthenticated.mockResolvedValue(undefined);
  });

  it("resumes a normally closed stream from the last durable event sequence", async () => {
    mocks.streamImportEvents
      .mockResolvedValueOnce(stream([event(1, "received")]))
      .mockResolvedValueOnce(stream([event(2, "completed")]));
    mocks.readImportJob
      .mockResolvedValueOnce(runningJob)
      .mockResolvedValueOnce(completedJob);
    const { result } = renderHook(() => useImportJobStream());

    act(() => result.current.connect(runningJob.id));

    await waitFor(() => expect(result.current.status).toBe("done"));
    expect(result.current.result).toEqual(completedJob);
    expect(result.current.logs.map((item) => item.message)).toEqual([
      "快照已持久化并进入导入队列",
      "事件流连接已到期，正在从序号 1 恢复",
      "导入与分析已完成",
    ]);
    expect(mocks.streamImportEvents).toHaveBeenCalledTimes(2);
    expect(mocks.ensureAuthenticated).toHaveBeenCalledTimes(2);
    expect(mocks.streamImportEvents.mock.calls[0]?.[0]).toMatchObject({
      path: { jobId: runningJob.id },
      sseMaxRetryAttempts: 1,
    });
    expect(mocks.streamImportEvents.mock.calls[1]?.[0]).toMatchObject({
      path: { jobId: runningJob.id },
      headers: { "Last-Event-ID": "1" },
    });
  });
});
