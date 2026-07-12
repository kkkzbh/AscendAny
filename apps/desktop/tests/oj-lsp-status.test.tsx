import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import type { BrowserLspConnection, BrowserSession } from "@ascendany/sdk";
import { afterEach, describe, expect, it, vi } from "vitest";
import { OjLspStatus } from "../src/components/OjLspStatus";

class TestSocket extends EventTarget {
  readonly messages: string[] = [];
  send(message: string): void { this.messages.push(message); }
  message(value: unknown): void {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(value) }));
  }
}

function setupSession() {
  const socket = new TestSocket();
  const close = vi.fn(async () => undefined);
  const connection = {
    socket: socket as unknown as WebSocket,
    session: {
      id: "11111111-1111-4111-8111-111111111111",
      workspaceUri: "file:///workspace" as const,
      webSocketPath: "/api/v2/lsp/sessions/11111111-1111-4111-8111-111111111111/websocket",
      expiresAt: "2026-07-11T05:00:00Z",
    },
    close,
  } satisfies BrowserLspConnection;
  return {
    close,
    session: { openLspSession: vi.fn(async () => connection) } as unknown as BrowserSession,
    socket,
  };
}

afterEach(cleanup);

describe("desktop OjLspStatus", () => {
  it("shows lifecycle and diagnostics, publishes changes, and cleans up", async () => {
    const { close, session, socket } = setupSession();
    const view = render(<OjLspStatus session={session} source="int main() { return missing; }" />);
    await waitFor(() => expect(socket.messages).toHaveLength(1));
    expect(JSON.parse(socket.messages[0]!)).toMatchObject({ method: "initialize" });

    act(() => socket.message({ jsonrpc: "2.0", id: 1, result: { capabilities: {} } }));
    expect(await screen.findByText("LSP 已连接")).toBeTruthy();
    act(() => socket.message({
      jsonrpc: "2.0",
      method: "textDocument/publishDiagnostics",
      params: {
        uri: "file:///workspace/main.cpp",
        diagnostics: [{
          message: "use of undeclared identifier 'missing'",
          severity: 1,
          range: { start: { line: 0, character: 20 }, end: { line: 0, character: 27 } },
        }],
      },
    }));
    expect(screen.getByText("use of undeclared identifier 'missing'")).toBeTruthy();
    expect(screen.getByText("1:21")).toBeTruthy();

    view.rerender(<OjLspStatus session={session} source="int main() { return 0; }" />);
    await waitFor(() => expect(JSON.parse(socket.messages.at(-1)!)).toMatchObject({
      method: "textDocument/didChange",
      params: { textDocument: { uri: "file:///workspace/main.cpp", version: 2 } },
    }));
    view.unmount();
    await waitFor(() => expect(close).toHaveBeenCalledOnce());
  });
});
