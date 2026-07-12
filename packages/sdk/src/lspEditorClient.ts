import type { BrowserLspConnection, BrowserSession } from "./browserSession";

export const CPP20_DOCUMENT_URI = "file:///workspace/main.cpp";

export type LspEditorConnectionState = "connecting" | "ready" | "disconnected" | "error";

export interface LspPosition {
  line: number;
  character: number;
}

export interface LspRange {
  start: LspPosition;
  end: LspPosition;
}

export interface LspEditorDiagnostic {
  message: string;
  range: LspRange;
  severity?: number;
  source?: string;
  code?: string | number;
}

export interface LspEditorSnapshot {
  state: LspEditorConnectionState;
  diagnostics: readonly LspEditorDiagnostic[];
  error: string | null;
}

export type LspEditorListener = (snapshot: LspEditorSnapshot) => void;

const INITIALIZE_REQUEST_ID = 1;

export class Cpp20LspEditorClient {
  private readonly browserSession: BrowserSession;
  private readonly listeners = new Set<LspEditorListener>();
  private connection: BrowserLspConnection | null = null;
  private source = "";
  private version = 1;
  private snapshotValue: LspEditorSnapshot = {
    state: "disconnected",
    diagnostics: [],
    error: null,
  };
  private connectInFlight: Promise<void> | null = null;
  private closeInFlight: Promise<void> | null = null;
  private closed = false;

  constructor(browserSession: BrowserSession) {
    this.browserSession = browserSession;
  }

  snapshot(): LspEditorSnapshot {
    return this.snapshotValue;
  }

  subscribe(listener: LspEditorListener): () => void {
    this.listeners.add(listener);
    listener(this.snapshotValue);
    return () => this.listeners.delete(listener);
  }

  connect(initialSource: string): Promise<void> {
    if (this.connectInFlight !== null || this.connection !== null || this.closed) {
      throw new Error("C++20 LSP editor client can only connect once.");
    }
    this.source = initialSource;
    this.update({ state: "connecting", diagnostics: [], error: null });
    this.connectInFlight = this.connectOnce().finally(() => {
      this.connectInFlight = null;
    });
    return this.connectInFlight;
  }

  change(source: string): void {
    this.source = source;
    if (this.snapshotValue.state !== "ready" || this.connection === null) return;
    this.version += 1;
    this.send({
      jsonrpc: "2.0",
      method: "textDocument/didChange",
      params: {
        textDocument: { uri: CPP20_DOCUMENT_URI, version: this.version },
        contentChanges: [{ text: source }],
      },
    });
  }

  close(): Promise<void> {
    this.closed = true;
    this.closeInFlight ??= this.closeOnce(true);
    return this.closeInFlight;
  }

  private async connectOnce(): Promise<void> {
    try {
      const connection = await this.browserSession.openLspSession();
      if (this.closed) {
        await connection.close();
        return;
      }
      this.connection = connection;
      connection.socket.addEventListener("message", this.onMessage);
      connection.socket.addEventListener("close", this.onSocketClose);
      connection.socket.addEventListener("error", this.onSocketError);
      this.send({
        jsonrpc: "2.0",
        id: INITIALIZE_REQUEST_ID,
        method: "initialize",
        params: {
          processId: null,
          rootUri: connection.session.workspaceUri,
          capabilities: { textDocument: { publishDiagnostics: {} } },
          workspaceFolders: [{ uri: connection.session.workspaceUri, name: "workspace" }],
        },
      });
    } catch (error) {
      if (!this.closed) this.fail(error);
      throw error;
    }
  }

  private readonly onMessage = (event: MessageEvent<unknown>): void => {
    try {
      const message = parseServerMessage(event.data);
      if (message.id === INITIALIZE_REQUEST_ID) {
        if (message.error !== undefined || !isRecord(message.result)) {
          throw new Error("LSP initialize response was rejected.");
        }
        if (this.snapshotValue.state !== "connecting") return;
        this.send({ jsonrpc: "2.0", method: "initialized", params: {} });
        this.send({
          jsonrpc: "2.0",
          method: "textDocument/didOpen",
          params: {
            textDocument: {
              uri: CPP20_DOCUMENT_URI,
              languageId: "cpp",
              version: this.version,
              text: this.source,
            },
          },
        });
        this.update({ state: "ready", diagnostics: [], error: null });
        return;
      }
      if (message.method === "textDocument/publishDiagnostics") {
        const diagnostics = parseDiagnostics(message.params);
        this.update({ ...this.snapshotValue, diagnostics });
      }
    } catch (error) {
      this.fail(error);
      this.closeAfterFailure();
    }
  };

  private readonly onSocketClose = (): void => {
    this.connection = null;
    if (!this.closed) {
      this.update({ state: "disconnected", diagnostics: [], error: null });
    }
  };

  private readonly onSocketError = (): void => {
    if (!this.closed) {
      this.fail(new Error("LSP WebSocket failed."));
      this.closeAfterFailure();
    }
  };

  private send(message: object): void {
    if (this.connection === null) throw new Error("LSP connection is unavailable.");
    this.connection.socket.send(JSON.stringify(message));
  }

  private async closeOnce(markDisconnected: boolean): Promise<void> {
    const connection = this.connection;
    this.connection = null;
    if (connection !== null) {
      connection.socket.removeEventListener("message", this.onMessage);
      connection.socket.removeEventListener("close", this.onSocketClose);
      connection.socket.removeEventListener("error", this.onSocketError);
      try {
        await connection.close();
      } finally {
        if (markDisconnected) {
          this.update({ state: "disconnected", diagnostics: [], error: null });
        }
      }
      return;
    }
    if (markDisconnected) {
      this.update({ state: "disconnected", diagnostics: [], error: null });
    }
  }

  private fail(error: unknown): void {
    this.update({
      state: "error",
      diagnostics: [],
      error: error instanceof Error ? error.message : "LSP connection failed.",
    });
  }

  private closeAfterFailure(): void {
    this.closed = true;
    this.closeInFlight ??= this.closeOnce(false);
    void this.closeInFlight.catch((error: unknown) => {
      const cleanupMessage = error instanceof Error ? error.message : "LSP session cleanup failed.";
      this.update({ ...this.snapshotValue, error: `${this.snapshotValue.error ?? "LSP connection failed."} ${cleanupMessage}` });
    });
  }

  private update(snapshot: LspEditorSnapshot): void {
    this.snapshotValue = snapshot;
    for (const listener of this.listeners) listener(snapshot);
  }
}

interface ServerMessage {
  id?: number | string | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: unknown;
}

function parseServerMessage(data: unknown): ServerMessage {
  if (typeof data !== "string") throw new Error("LSP server messages must be text.");
  const parsed: unknown = JSON.parse(data);
  if (!isRecord(parsed) || parsed.jsonrpc !== "2.0") {
    throw new Error("LSP server message is invalid.");
  }
  if (parsed.id !== undefined && parsed.id !== null) {
    const validNumber = typeof parsed.id === "number" && Number.isSafeInteger(parsed.id) && parsed.id >= 0;
    const validString = typeof parsed.id === "string" && parsed.id.length > 0 && parsed.id.length <= 128;
    if (!validNumber && !validString) throw new Error("LSP server response ID is invalid.");
  }
  if (parsed.method !== undefined && typeof parsed.method !== "string") {
    throw new Error("LSP server method is invalid.");
  }
  return parsed as ServerMessage;
}

function parseDiagnostics(params: unknown): readonly LspEditorDiagnostic[] {
  if (!isRecord(params) || params.uri !== CPP20_DOCUMENT_URI || !Array.isArray(params.diagnostics)) {
    throw new Error("LSP diagnostics payload is invalid.");
  }
  return params.diagnostics.map((value) => {
    if (!isRecord(value) || typeof value.message !== "string" || !validRange(value.range)) {
      throw new Error("LSP diagnostic is invalid.");
    }
    const severity = value.severity;
    if (severity !== undefined && (typeof severity !== "number" || !Number.isSafeInteger(severity) || severity < 1 || severity > 4)) {
      throw new Error("LSP diagnostic severity is invalid.");
    }
    const source = value.source;
    if (source !== undefined && typeof source !== "string") {
      throw new Error("LSP diagnostic source is invalid.");
    }
    const code = value.code;
    if (code !== undefined && typeof code !== "string" && (typeof code !== "number" || !Number.isSafeInteger(code))) {
      throw new Error("LSP diagnostic code is invalid.");
    }
    return {
      message: value.message,
      range: value.range,
      ...(severity === undefined ? {} : { severity }),
      ...(source === undefined ? {} : { source }),
      ...(code === undefined ? {} : { code: code as string | number }),
    };
  });
}

function validRange(value: unknown): value is LspRange {
  return isRecord(value) && validPosition(value.start) && validPosition(value.end);
}

function validPosition(value: unknown): value is LspPosition {
  return isRecord(value)
    && typeof value.line === "number"
    && Number.isSafeInteger(value.line)
    && value.line >= 0
    && typeof value.character === "number"
    && Number.isSafeInteger(value.character)
    && value.character >= 0;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
