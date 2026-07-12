import { describe, expect, it, vi } from "vitest";
import {
  CPP20_DOCUMENT_URI,
  Cpp20LspEditorClient,
  type BrowserLspConnection,
  type BrowserSession,
} from "../src";

class EditorSocket extends EventTarget {
  readonly messages: string[] = [];

  send(message: string): void {
    this.messages.push(message);
  }

  message(value: unknown): void {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(value) }));
  }
}

function testConnection() {
  const socket = new EditorSocket();
  const close = vi.fn(async () => undefined);
  const connection = {
    socket: socket as unknown as WebSocket,
    session: {
      id: "11111111-1111-4111-8111-111111111111",
      workspaceUri: "file:///workspace" as const,
      webSocketPath: "/api/v2/lsp/sessions/11111111-1111-4111-8111-111111111111/websocket",
      expiresAt: "2026-07-11T05:00:00.000Z",
    },
    close,
  } satisfies BrowserLspConnection;
  return { close, connection, socket };
}

function parsedMessages(socket: EditorSocket): Array<Record<string, unknown>> {
  return socket.messages.map((message) => JSON.parse(message) as Record<string, unknown>);
}

describe("Cpp20LspEditorClient", () => {
  it("initializes main.cpp, publishes changes and exposes diagnostics", async () => {
    const { close, connection, socket } = testConnection();
    const openLspSession = vi.fn(async () => connection);
    const session = { openLspSession } as unknown as BrowserSession;
    const client = new Cpp20LspEditorClient(session);
    const snapshots = [client.snapshot()];
    client.subscribe((snapshot) => snapshots.push(snapshot));

    await client.connect("int main() { return missing; }\n");

    expect(openLspSession).toHaveBeenCalledOnce();
    expect(parsedMessages(socket)[0]).toMatchObject({
      jsonrpc: "2.0",
      id: 1,
      method: "initialize",
      params: {
        processId: null,
        rootUri: "file:///workspace",
        workspaceFolders: [{ uri: "file:///workspace", name: "workspace" }],
      },
    });
    expect(client.snapshot().state).toBe("connecting");

    socket.message({ jsonrpc: "2.0", id: 1, result: { capabilities: {} } });

    expect(client.snapshot().state).toBe("ready");
    expect(parsedMessages(socket).slice(1)).toMatchObject([
      { jsonrpc: "2.0", method: "initialized", params: {} },
      {
        jsonrpc: "2.0",
        method: "textDocument/didOpen",
        params: {
          textDocument: {
            uri: CPP20_DOCUMENT_URI,
            languageId: "cpp",
            version: 1,
            text: "int main() { return missing; }\n",
          },
        },
      },
    ]);

    client.change("int main() { return 0; }\n");
    expect(parsedMessages(socket).at(-1)).toMatchObject({
      method: "textDocument/didChange",
      params: {
        textDocument: { uri: CPP20_DOCUMENT_URI, version: 2 },
        contentChanges: [{ text: "int main() { return 0; }\n" }],
      },
    });

    socket.message({
      jsonrpc: "2.0",
      method: "textDocument/publishDiagnostics",
      params: {
        uri: CPP20_DOCUMENT_URI,
        diagnostics: [{
          message: "use of undeclared identifier 'missing'",
          severity: 1,
          source: "clang",
          range: {
            start: { line: 0, character: 20 },
            end: { line: 0, character: 27 },
          },
        }],
      },
    });

    expect(client.snapshot()).toMatchObject({
      state: "ready",
      diagnostics: [{
        message: "use of undeclared identifier 'missing'",
        severity: 1,
        range: { start: { line: 0, character: 20 } },
      }],
    });
    expect(snapshots.some((snapshot) => snapshot.state === "connecting")).toBe(true);

    await client.close();
    expect(close).toHaveBeenCalledOnce();
    expect(client.snapshot()).toEqual({ state: "disconnected", diagnostics: [], error: null });
  });

  it("closes a session that finishes opening after editor cleanup", async () => {
    const { close, connection, socket } = testConnection();
    let resolveConnection!: (connection: BrowserLspConnection) => void;
    const pending = new Promise<BrowserLspConnection>((resolve) => {
      resolveConnection = resolve;
    });
    const session = { openLspSession: vi.fn(() => pending) } as unknown as BrowserSession;
    const client = new Cpp20LspEditorClient(session);

    const connecting = client.connect("int main() {}\n");
    const closing = client.close();
    resolveConnection(connection);
    await Promise.all([connecting, closing]);

    expect(close).toHaveBeenCalledOnce();
    expect(socket.messages).toEqual([]);
    expect(client.snapshot().state).toBe("disconnected");
  });

  it("fails closed when diagnostics violate the workspace contract", async () => {
    const { close, connection, socket } = testConnection();
    const session = { openLspSession: vi.fn(async () => connection) } as unknown as BrowserSession;
    const client = new Cpp20LspEditorClient(session);
    await client.connect("int main() {}\n");
    socket.message({ jsonrpc: "2.0", id: 1, result: { capabilities: {} } });

    socket.message({
      jsonrpc: "2.0",
      method: "textDocument/publishDiagnostics",
      params: { uri: "file:///etc/passwd", diagnostics: [] },
    });

    expect(client.snapshot()).toMatchObject({ state: "error", diagnostics: [] });
    await vi.waitFor(() => expect(close).toHaveBeenCalledOnce());
  });
});
