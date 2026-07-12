import { describe, expect, it } from "vitest";
import { BrowserSession, BrowserSessionError, type BrowserSessionStorage } from "../src";

const API_ORIGIN = "https://api.ascendany.invalid";
const ACCOUNT = {
  id: "123e4567-e89b-42d3-a456-426614174010",
  username: "student_1",
  displayName: "Student One",
  studentNumber: "20260001",
  role: "student" as const,
  authRevision: 1,
};
const LSP_TICKET = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
const LSP_SESSION = {
  id: "11111111-1111-4111-8111-111111111111",
  workspaceUri: "file:///workspace" as const,
  webSocketPath: "/api/v2/lsp/sessions/11111111-1111-4111-8111-111111111111/websocket",
  attachTicket: LSP_TICKET,
  expiresAt: "2026-07-11T05:00:00.000Z",
};

class TestWebSocket extends EventTarget {
  static instances: TestWebSocket[] = [];

  readonly url: string;
  readonly requestedProtocols: string[];
  protocol = "ascendany.lsp.v1";
  readyState = 0;

  constructor(url: string | URL, protocols?: string | string[]) {
    super();
    this.url = String(url);
    this.requestedProtocols = typeof protocols === "string" ? [protocols] : [...(protocols ?? [])];
    TestWebSocket.instances.push(this);
    queueMicrotask(() => {
      this.readyState = 1;
      this.dispatchEvent(new Event("open"));
    });
  }

  close(): void {
    this.readyState = 3;
    this.dispatchEvent(new Event("close"));
  }
}

class FailingWebSocket extends EventTarget {
  protocol = "";
  readyState = 0;

  constructor() {
    super();
    queueMicrotask(() => this.dispatchEvent(new Event("error")));
  }

  close(): void {
    this.readyState = 3;
  }
}

class TestStorage implements BrowserSessionStorage {
  readonly values = new Map<string, string>();
  readonly writes: Array<[string, string]> = [];
  readonly removals: string[] = [];

  getItem(key: string): string | null {
    return this.values.get(key) ?? null;
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value);
    this.writes.push([key, value]);
  }

  removeItem(key: string): void {
    this.values.delete(key);
    this.removals.push(key);
  }
}

function authSession(sequence: number) {
  return {
    accessToken: `header.payload.signature-${sequence}`,
    expiresAt: "2026-07-11T05:00:00.000Z",
    csrfToken: String(sequence).padStart(43, "A"),
    account: ACCOUNT,
  };
}

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("BrowserSession", () => {
  it("keeps access credentials in memory and persists only the rotating CSRF token", async () => {
    const storage = new TestStorage();
    const requests: Request[] = [];
    const fetchMock: typeof fetch = async (input) => {
      const request = input instanceof Request ? input : new Request(input);
      requests.push(request);
      return jsonResponse(authSession(1));
    };
    const session = new BrowserSession({
      apiOrigin: API_ORIGIN,
      storage,
      fetch: fetchMock,
      now: () => Date.parse("2026-07-11T04:00:00.000Z"),
    });

    await session.login({ username: "student_1", password: "long-enough-password" });

    expect(session.snapshot()).toEqual({
      status: "authenticated",
      account: ACCOUNT,
      expiresAt: "2026-07-11T05:00:00.000Z",
    });
    expect([...storage.values.values()]).toEqual([authSession(1).csrfToken]);
    expect(JSON.stringify([...storage.values])).not.toContain("header.payload.signature");
    expect(requests[0]?.credentials).toBe("include");
    expect(requests[0]?.headers.has("Cookie")).toBe(false);
  });

  it("consumes the LSP attach ticket in WebSocket protocols without persisting or returning it", async () => {
    TestWebSocket.instances = [];
    const storage = new TestStorage();
    const requests: Request[] = [];
    const session = new BrowserSession({
      apiOrigin: API_ORIGIN,
      storage,
      webSocket: TestWebSocket as unknown as typeof WebSocket,
      now: () => Date.parse("2026-07-11T04:00:00.000Z"),
      fetch: async (input) => {
        const request = input instanceof Request ? input : new Request(input);
        requests.push(request);
        if (request.url.endsWith("/api/v2/auth/login")) {
          return jsonResponse(authSession(1));
        }
        if (request.url.endsWith("/api/v2/lsp/sessions")) {
          return jsonResponse(LSP_SESSION, 201);
        }
        if (request.method === "DELETE" && request.url.endsWith(`/api/v2/lsp/sessions/${LSP_SESSION.id}`)) {
          return new Response(null, { status: 204 });
        }
        throw new Error(`unexpected request ${request.method} ${request.url}`);
      },
    });
    await session.login({ username: "student_1", password: "long-enough-password" });

    const connection = await session.openLspSession();

    expect(connection.session).toEqual({
      id: LSP_SESSION.id,
      workspaceUri: LSP_SESSION.workspaceUri,
      webSocketPath: LSP_SESSION.webSocketPath,
      expiresAt: LSP_SESSION.expiresAt,
    });
    expect("attachTicket" in connection.session).toBe(false);
    expect(TestWebSocket.instances).toHaveLength(1);
    expect(TestWebSocket.instances[0]?.url).toBe(
      "wss://api.ascendany.invalid/api/v2/lsp/sessions/11111111-1111-4111-8111-111111111111/websocket",
    );
    expect(TestWebSocket.instances[0]?.requestedProtocols).toEqual([
      "ascendany.lsp.v1",
      `ascendany.lsp.ticket.${LSP_TICKET}`,
    ]);
    expect(requests[1]?.headers.get("Authorization")).toContain("Bearer ");
    expect(JSON.stringify([...storage.values])).not.toContain(LSP_TICKET);
    expect(JSON.stringify(connection.session)).not.toContain(LSP_TICKET);
    await connection.close();
    await connection.close();
    expect(TestWebSocket.instances[0]?.readyState).toBe(3);
    expect(requests.filter((request) => request.method === "DELETE")).toHaveLength(1);
  });

  it("deletes the server LSP session when the WebSocket cannot open", async () => {
    const storage = new TestStorage();
    const requests: Request[] = [];
    const session = new BrowserSession({
      apiOrigin: API_ORIGIN,
      storage,
      webSocket: FailingWebSocket as unknown as typeof WebSocket,
      now: () => Date.parse("2026-07-11T04:00:00.000Z"),
      fetch: async (input) => {
        const request = input instanceof Request ? input : new Request(input);
        requests.push(request);
        if (request.url.endsWith("/api/v2/auth/login")) {
          return jsonResponse(authSession(1));
        }
        if (request.method === "POST" && request.url.endsWith("/api/v2/lsp/sessions")) {
          return jsonResponse(LSP_SESSION, 201);
        }
        if (request.method === "DELETE" && request.url.endsWith(`/api/v2/lsp/sessions/${LSP_SESSION.id}`)) {
          return new Response(null, { status: 204 });
        }
        throw new Error(`unexpected request ${request.method} ${request.url}`);
      },
    });
    await session.login({ username: "student_1", password: "long-enough-password" });

    await expect(session.openLspSession()).rejects.toBeInstanceOf(BrowserSessionError);
    expect(requests.map((request) => `${request.method} ${new URL(request.url).pathname}`)).toEqual([
      "POST /api/v2/auth/login",
      "POST /api/v2/lsp/sessions",
      `DELETE /api/v2/lsp/sessions/${LSP_SESSION.id}`,
    ]);
    expect(JSON.stringify([...storage.values])).not.toContain(LSP_TICKET);
  });

  it("bootstraps from the persisted CSRF token and commits its rotation", async () => {
    const storage = new TestStorage();
    const key = `ascendany.v2.csrf:${API_ORIGIN}`;
    storage.values.set(key, authSession(1).csrfToken);
    const requests: Request[] = [];
    const session = new BrowserSession({
      apiOrigin: API_ORIGIN,
      storage,
      now: () => Date.parse("2026-07-11T04:00:00.000Z"),
      fetch: async (input) => {
        const request = input instanceof Request ? input : new Request(input);
        requests.push(request);
        return jsonResponse(authSession(2));
      },
    });

    await expect(session.bootstrap()).resolves.toMatchObject({ status: "authenticated" });
    expect(requests).toHaveLength(1);
    expect(new URL(requests[0]!.url).pathname).toBe("/api/v2/auth/refresh");
    expect(requests[0]?.headers.get("X-AscendAny-CSRF")).toBe(authSession(1).csrfToken);
    expect(storage.values.get(key)).toBe(authSession(2).csrfToken);
  });

  it("coalesces concurrent refresh calls into one credential rotation", async () => {
    const storage = new TestStorage();
    let refreshCalls = 0;
    const session = new BrowserSession({
      apiOrigin: API_ORIGIN,
      storage,
      now: () => Date.parse("2026-07-11T04:00:00.000Z"),
      fetch: async (input) => {
        const request = input instanceof Request ? input : new Request(input);
        if (request.url.endsWith("/login")) {
          return jsonResponse(authSession(1));
        }
        refreshCalls++;
        await Promise.resolve();
        return jsonResponse(authSession(2));
      },
    });
    await session.login({ username: "student_1", password: "long-enough-password" });

    const [first, second, third] = await Promise.all([
      session.refresh(),
      session.refresh(),
      session.refresh(),
    ]);

    expect([first, second, third]).toEqual([ACCOUNT, ACCOUNT, ACCOUNT]);
    expect(refreshCalls).toBe(1);
    expect(storage.writes.at(-1)?.[1]).toBe(authSession(2).csrfToken);
  });

  it("clears local state after an authoritative refresh rejection", async () => {
    const storage = new TestStorage();
    const key = `ascendany.v2.csrf:${API_ORIGIN}`;
    storage.values.set(key, authSession(1).csrfToken);
    const session = new BrowserSession({
      apiOrigin: API_ORIGIN,
      storage,
      fetch: async () => jsonResponse({ code: "csrf_rejected" }, 403),
    });

    try {
      await session.bootstrap();
      expect.unreachable("bootstrap must reject an authoritative CSRF failure");
    } catch (error) {
      expect(error).toBeInstanceOf(BrowserSessionError);
      expect(error).toMatchObject({ operation: "bootstrap", status: 403 });
    }
    expect(session.snapshot()).toEqual({ status: "anonymous" });
    expect(storage.values.has(key)).toBe(false);
  });

  it("rejects non-canonical production and non-loopback HTTP API origins", () => {
    const storage = new TestStorage();
    for (const apiOrigin of [
      "https://api.ascendany.invalid/",
      "http://api.ascendany.invalid",
      "https://user@api.ascendany.invalid",
      "https://api.ascendany.invalid/path",
    ]) {
      expect(() => new BrowserSession({ apiOrigin, storage }), apiOrigin).toThrow(TypeError);
    }
    expect(() => new BrowserSession({ apiOrigin: "http://127.0.0.1:8080", storage })).not.toThrow();
  });
});
