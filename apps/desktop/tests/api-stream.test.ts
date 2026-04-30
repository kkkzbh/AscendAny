import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, streamChatReply, type ChatStreamEvent } from "@/lib/api";

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
