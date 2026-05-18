import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  streamChatReply,
  streamDataEvents,
  type ChatStreamEvent,
  type DataFreshnessEvent,
} from "@/lib/api";

const encoder = new TextEncoder();

function eventStream(chunks: string[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) {
        controller.enqueue(encoder.encode(chunk));
      }
      controller.close();
    },
  });
}

describe("chat stream API", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("parses split SSE blocks", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        eventStream([
          'event: meta\ndata: {"provider":"deepseek","model":"deepseek-v4-flash"}\n\n',
          'event: reasoning_delta\ndata: {"text":"先分析"}\n\n',
          'event: tool_activity_start\ndata: {"activityId":"call_1","label":"查看考试数据","status":"running"}\n\n',
          'event: tool_activity_done\ndata: {"activityId":"call_1","label":"查看《数据结构第三次实验》数据","status":"done"}\n\n',
          'event: delta\ndata: {"text":"你',
          '好"}\n\n',
          'event: done\ndata: {"reply":"你好","summary":"s"}\n\n',
        ]),
        {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        },
      ),
    );
    const events: ChatStreamEvent[] = [];

    await streamChatReply({ messages: [], summary: "" }, undefined, (event) => events.push(event));

    expect(events).toEqual([
      { type: "meta", provider: "deepseek", model: "deepseek-v4-flash", requestMode: undefined, summary: undefined },
      { type: "reasoning_delta", text: "先分析" },
      { type: "tool_activity_start", activityId: "call_1", label: "查看考试数据", status: "running" },
      { type: "tool_activity_done", activityId: "call_1", label: "查看《数据结构第三次实验》数据", status: "done" },
      { type: "delta", text: "你好" },
      { type: "done", reply: "你好", summary: "s", provider: undefined, model: undefined, requestMode: undefined },
    ]);
  });

  it("turns missing stream routes into an actionable error", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ detail: "Not Found" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await expect(streamChatReply({ messages: [], summary: "" }, undefined, () => {})).rejects.toMatchObject({
      message: "后端缺少流式聊天接口，请重启 API 服务后重试。",
      status: 404,
      code: "STREAM_ENDPOINT_NOT_FOUND",
    } satisfies Partial<ApiError>);
  });

  it("rejects when the SSE stream emits an error event", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        eventStream([
          'event: meta\ndata: {"provider":"deepseek"}\n\n',
          'event: error\ndata: {"code":"PROVIDER_KEY_MISSING","message":"未配置模型 Key"}\n\n',
        ]),
        {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        },
      ),
    );
    const events: ChatStreamEvent[] = [];

    await expect(streamChatReply({ messages: [], summary: "" }, undefined, (event) => events.push(event))).rejects.toMatchObject({
      message: "未配置模型 Key",
      status: 200,
      code: "PROVIDER_KEY_MISSING",
    } satisfies Partial<ApiError>);
    expect(events).toEqual([
      { type: "meta", provider: "deepseek", model: undefined, requestMode: undefined, summary: undefined },
    ]);
  });

  it("ignores legacy unlabelled tool events", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        eventStream([
          'event: tool_start\ndata: {"type":"tool_start"}\n\n',
          'event: tool_done\ndata: {"type":"tool_done"}\n\n',
          'event: delta\ndata: {"text":"ok"}\n\n',
        ]),
        {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        },
      ),
    );
    const events: ChatStreamEvent[] = [];

    await streamChatReply({ messages: [], summary: "" }, undefined, (event) => events.push(event));

    expect(events).toEqual([{ type: "delta", text: "ok" }]);
  });

  it("does not swallow consumer callback errors", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        eventStream(['event: delta\ndata: {"text":"hello"}\n\n']),
        {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        },
      ),
    );

    await expect(
      streamChatReply({ messages: [], summary: "" }, undefined, () => {
        throw new Error("consumer failed");
      }),
    ).rejects.toThrow("consumer failed");
  });
});

describe("data freshness stream API", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("parses snapshot and data change events", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        eventStream([
          'event: snapshot\ndata: {"latestExamImportedAt":"2026-02-13T09:30:00+00:00"}\n\n',
          'event: data_changed\ndata: {"latestExamImportedAt":"2026-02-14T10:15:00+00:00"}\n\n',
          'event: heartbeat\ndata: {"ts":"2026-02-14T10:15:05+00:00"}\n\n',
        ]),
        {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        },
      ),
    );
    const events: DataFreshnessEvent[] = [];

    await streamDataEvents((event) => events.push(event));

    expect(events).toEqual([
      { type: "snapshot", latestExamImportedAt: "2026-02-13T09:30:00+00:00" },
      { type: "data_changed", latestExamImportedAt: "2026-02-14T10:15:00+00:00" },
      { type: "heartbeat", ts: "2026-02-14T10:15:05+00:00" },
    ]);
  });
});
