import { describe, expect, it } from "vitest";

import {
  createChatThread,
  createClient,
  enqueueAgentRun,
  enqueueSelfAutoAnalysis,
  getAgentRun,
  listChatMessages,
  listChatThreads,
} from "../src";

const THREAD_ID = "123e4567-e89b-42d3-a456-426614174140";
const RUN_ID = "123e4567-e89b-42d3-a456-426614174141";
const REQUEST_ID = "123e4567-e89b-42d3-a456-426614174142";
const MESSAGE_ID = "123e4567-e89b-42d3-a456-426614174143";

const thread = {
  id: THREAD_ID,
  kind: "conversation" as const,
  headRevision: 1,
  createdAt: "2026-07-11T11:00:00Z",
  updatedAt: "2026-07-11T11:00:01Z",
};

const message = {
  id: MESSAGE_ID,
  threadId: THREAD_ID,
  sequence: 1,
  kind: "user" as const,
  content: "Explain my latest result.",
  createdAt: "2026-07-11T11:00:01Z",
};

const run = {
  id: RUN_ID,
  threadId: THREAD_ID,
  clientRequestId: REQUEST_ID,
  kind: "reply" as const,
  inputMessageId: MESSAGE_ID,
  status: "queued" as const,
  attemptCount: 0,
  createdAt: "2026-07-11T11:00:01Z",
  updatedAt: "2026-07-11T11:00:01Z",
};

describe("generated Chat/Agent SDK", () => {
  it("serializes thread cursor and ordered message queries", async () => {
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      const path = new URL(request.url).pathname;
      const body = path.endsWith("/messages")
        ? { items: [message], lastSequence: 1 }
        : { items: [thread], nextCursor: THREAD_ID };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const threads = await listChatThreads({
      client,
      query: { cursor: THREAD_ID, limit: 20 },
    });
    const messages = await listChatMessages({
      client,
      path: { threadId: THREAD_ID },
      query: { afterSequence: 0, limit: 50 },
    });

    expect(threads.data?.nextCursor).toBe(THREAD_ID);
    expect(messages.data?.items[0]?.kind).toBe("user");
    const threadURL = new URL(requests[0]?.url ?? "https://invalid.invalid");
    expect(threadURL.pathname).toBe("/api/v2/students/me/chat/threads");
    expect(threadURL.searchParams.get("cursor")).toBe(THREAD_ID);
    expect(threadURL.searchParams.get("limit")).toBe("20");
    const messageURL = new URL(requests[1]?.url ?? "https://invalid.invalid");
    expect(messageURL.pathname).toBe(
      `/api/v2/students/me/chat/threads/${THREAD_ID}/messages`,
    );
    expect(messageURL.searchParams.get("afterSequence")).toBe("0");
    expect(messageURL.searchParams.get("limit")).toBe("50");
    expect(requests.every(
      (request) => request.headers.get("Authorization") === "Bearer student-access-token",
    )).toBe(true);
  });

  it("serializes the discriminated reply enqueue request and durable run paths", async () => {
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      const body = request.method === "POST"
        ? { run, message, created: true }
        : run;
      return new Response(JSON.stringify(body), {
        status: request.method === "POST" ? 202 : 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const enqueued = await enqueueAgentRun({
      client,
      path: { threadId: THREAD_ID },
      body: {
        clientRequestId: REQUEST_ID,
        kind: "reply",
        content: message.content,
        promptConfigurationKey: "agent.prompt.default",
        modelConfigurationKey: "agent.model.default",
        expectedAnalyticsHeadRevision: null,
      },
    });
    const current = await getAgentRun({ client, path: { runId: RUN_ID } });

    expect(enqueued.data?.created).toBe(true);
    expect(current.data?.status).toBe("queued");
    expect(requests.map((request) => [request.method, new URL(request.url).pathname])).toEqual([
      ["POST", `/api/v2/students/me/chat/threads/${THREAD_ID}/runs`],
      ["GET", `/api/v2/students/me/agent-runs/${RUN_ID}`],
    ]);
    expect(requests[0]?.headers.get("Content-Type")).toBe("application/json");
    expect(requests[0] && JSON.parse(await requests[0].text())).toEqual({
      clientRequestId: REQUEST_ID,
      kind: "reply",
      content: message.content,
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: null,
    });
  });

  it("creates an empty durable thread without a request body", async () => {
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify({ ...thread, headRevision: 0 }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const result = await createChatThread({ client });

    expect(result.data?.headRevision).toBe(0);
    expect(requests[0]?.method).toBe("POST");
    expect(requests[0]?.url).toBe("https://ascendany.invalid/api/v2/students/me/chat/threads");
    expect(await requests[0]?.text()).toBe("");
  });

  it("serializes only server-selected automatic-analysis inputs", async () => {
    const requests: Request[] = [];
    const autoRun = { ...run, kind: "auto_analysis" as const };
    const autoMessage = {
      ...message,
      kind: "auto_analysis_request" as const,
      content: "Analyze the student's current published analytics snapshot and provide a concise, actionable progress review.",
    };
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return new Response(JSON.stringify({ run: autoRun, message: autoMessage, created: true }), {
        status: 202,
        headers: { "Content-Type": "application/json" },
      });
    };
    const client = createClient({
      baseUrl: "https://ascendany.invalid",
      auth: "student-access-token",
      fetch: fetchMock,
    });

    const result = await enqueueSelfAutoAnalysis({
      client,
      body: {
        promptConfigurationKey: "agent.prompt.default",
        modelConfigurationKey: "agent.model.default",
        expectedAnalyticsHeadRevision: 7,
      },
    });

    expect(result.data?.created).toBe(true);
    expect(requests[0]?.method).toBe("POST");
    expect(requests[0]?.url).toBe("https://ascendany.invalid/api/v2/students/me/auto-analysis");
    expect(requests[0] && JSON.parse(await requests[0].text())).toEqual({
      promptConfigurationKey: "agent.prompt.default",
      modelConfigurationKey: "agent.model.default",
      expectedAnalyticsHeadRevision: 7,
    });
  });
});
