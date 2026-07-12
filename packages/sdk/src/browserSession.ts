import {
  consumeEnrollmentClaim,
  closeLspSession,
  createLspSession,
  loginAccount,
  logoutSession,
  refreshSession,
  type Account,
  type AuthSession,
  type EnrollmentClaimRequest,
  type LoginRequest,
  type LspSession,
} from "./generated";
import { createClient, type Client } from "./generated/client";
import { matchesAttachLspSessionWebSocketPath } from "./generated/runtime-contracts.gen";

const CSRF_TOKEN_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const LSP_ATTACH_TICKET_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const LSP_SESSION_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const LSP_WEBSOCKET_PROTOCOL = "ascendany.lsp.v1";
const LSP_TICKET_PROTOCOL_PREFIX = "ascendany.lsp.ticket.";
const DEFAULT_MIN_ACCESS_VALIDITY_MS = 30_000;

export interface BrowserSessionStorage {
  getItem(key: string): string | null | Promise<string | null>;
  setItem(key: string, value: string): void | Promise<void>;
  removeItem(key: string): void | Promise<void>;
}

export type BrowserSessionSnapshot =
  | { status: "anonymous" }
  | { status: "authenticated"; account: Account; expiresAt: string };

export type BrowserSessionListener = (snapshot: BrowserSessionSnapshot) => void;

export class BrowserSessionError extends Error {
  readonly operation: string;
  readonly status: number | undefined;
  readonly apiError: unknown;

  constructor(operation: string, status: number | undefined, apiError: unknown) {
    super(`Browser session ${operation} failed${status === undefined ? "" : ` with HTTP ${status}`}.`);
    this.name = "BrowserSessionError";
    this.operation = operation;
    this.status = status;
    this.apiError = apiError;
  }
}

export interface BrowserSessionOptions {
  apiOrigin: string;
  storage: BrowserSessionStorage;
  fetch?: typeof fetch;
  now?: () => number;
  minimumAccessValidityMs?: number;
  webSocket?: typeof WebSocket;
}

export type BrowserLspSession = Omit<LspSession, "attachTicket">;

export interface BrowserLspConnection {
  socket: WebSocket;
  session: BrowserLspSession;
  close(): Promise<void>;
}

export class BrowserSession {
  readonly client: Client;

  private readonly apiOrigin: string;
  private readonly storage: BrowserSessionStorage;
  private readonly storageKey: string;
  private readonly now: () => number;
  private readonly minimumAccessValidityMs: number;
  private readonly webSocket: typeof WebSocket | undefined;
  private readonly listeners = new Set<BrowserSessionListener>();
  private active: AuthSession | null = null;
  private operationInFlight: string | null = null;
  private bootstrapInFlight: Promise<BrowserSessionSnapshot> | null = null;
  private refreshInFlight: Promise<Account> | null = null;

  constructor(options: BrowserSessionOptions) {
    this.apiOrigin = canonicalAPIOrigin(options.apiOrigin);
    this.storage = options.storage;
    this.storageKey = `ascendany.v2.csrf:${this.apiOrigin}`;
    this.now = options.now ?? Date.now;
    this.minimumAccessValidityMs = options.minimumAccessValidityMs ?? DEFAULT_MIN_ACCESS_VALIDITY_MS;
    this.webSocket = options.webSocket ?? globalThis.WebSocket;
    if (!Number.isSafeInteger(this.minimumAccessValidityMs) || this.minimumAccessValidityMs < 0) {
      throw new TypeError("minimumAccessValidityMs must be a non-negative safe integer.");
    }
    this.client = createClient({
      baseUrl: this.apiOrigin,
      credentials: "include",
      fetch: options.fetch,
      auth: (security) => security.in === "cookie" ? undefined : this.active?.accessToken,
    });
  }

  snapshot(): BrowserSessionSnapshot {
    if (this.active === null) {
      return { status: "anonymous" };
    }
    return {
      status: "authenticated",
      account: this.active.account,
      expiresAt: this.active.expiresAt,
    };
  }

  subscribe(listener: BrowserSessionListener): () => void {
    this.listeners.add(listener);
    listener(this.snapshot());
    return () => {
      this.listeners.delete(listener);
    };
  }

  async bootstrap(): Promise<BrowserSessionSnapshot> {
    if (this.bootstrapInFlight !== null) {
      return this.bootstrapInFlight;
    }
    if (this.active !== null) {
      throw new BrowserSessionError("bootstrap", undefined, "session_already_active");
    }
    this.beginOperation("bootstrap");
    this.bootstrapInFlight = this.bootstrapOnce().finally(() => {
      this.bootstrapInFlight = null;
      this.endOperation("bootstrap");
    });
    return this.bootstrapInFlight;
  }

  private async bootstrapOnce(): Promise<BrowserSessionSnapshot> {
    const csrfToken = await this.storage.getItem(this.storageKey);
    if (csrfToken === null) {
      return this.snapshot();
    }
    if (!CSRF_TOKEN_PATTERN.test(csrfToken)) {
      await this.clearLocalState();
      throw new BrowserSessionError("bootstrap", undefined, "stored_csrf_invalid");
    }
    await this.rotate(csrfToken, "bootstrap");
    return this.snapshot();
  }

  async login(input: LoginRequest): Promise<Account> {
    this.requireAnonymous("login");
    this.beginOperation("login");
    try {
      const result = await loginAccount({ client: this.client, body: input });
      const session = requireAuthSession("login", result.data, result.response?.status, result.error);
      await this.commit(session);
      return session.account;
    } finally {
      this.endOperation("login");
    }
  }

  async consumeEnrollment(input: EnrollmentClaimRequest): Promise<Account> {
    this.requireAnonymous("consume enrollment");
    this.beginOperation("consume enrollment");
    try {
      const result = await consumeEnrollmentClaim({ client: this.client, body: input });
      const session = requireAuthSession(
        "consume enrollment",
        result.data,
        result.response?.status,
        result.error,
      );
      await this.commit(session);
      return session.account;
    } finally {
      this.endOperation("consume enrollment");
    }
  }

  async ensureAuthenticated(): Promise<Account> {
    const session = this.requireActive("authorize");
    const expiresAt = Date.parse(session.expiresAt);
    if (expiresAt - this.now() > this.minimumAccessValidityMs) {
      return session.account;
    }
    return this.refresh();
  }

  async openLspSession(): Promise<BrowserLspConnection> {
    await this.ensureAuthenticated();
    const result = await createLspSession({ client: this.client });
    const created = requireLspSession(result.data, result.response?.status, result.error);
    const attachTicket = created.attachTicket;
    const session: BrowserLspSession = {
      id: created.id,
      workspaceUri: created.workspaceUri,
      webSocketPath: created.webSocketPath,
      expiresAt: created.expiresAt,
    };
    created.attachTicket = "";
    if (this.webSocket === undefined) {
      return this.failLspConnection(session.id, "websocket_unavailable");
    }
    const target = lspWebSocketURL(this.apiOrigin, session.webSocketPath);
    let socket: WebSocket;
    try {
      socket = new this.webSocket(target, [
        LSP_WEBSOCKET_PROTOCOL,
        LSP_TICKET_PROTOCOL_PREFIX + attachTicket,
      ]);
      await waitForLspWebSocket(socket);
      if (socket.protocol !== LSP_WEBSOCKET_PROTOCOL) {
        socket.close(1008, "LSP protocol negotiation failed");
        return this.failLspConnection(session.id, "websocket_protocol_rejected");
      }
    } catch (error) {
      return this.failLspConnection(session.id, error);
    }
    let closeInFlight: Promise<void> | null = null;
    return {
      socket,
      session,
      close: () => {
        closeInFlight ??= this.closeLspConnection(session.id, socket);
        return closeInFlight;
      },
    };
  }

  async refresh(): Promise<Account> {
    if (this.refreshInFlight !== null) {
      return this.refreshInFlight;
    }
    const session = this.requireActive("refresh");
    this.beginOperation("refresh");
    this.refreshInFlight = this.rotate(session.csrfToken, "refresh").finally(() => {
      this.refreshInFlight = null;
      this.endOperation("refresh");
    });
    return this.refreshInFlight;
  }

  async logout(): Promise<void> {
    let session = this.requireActive("logout");
    this.beginOperation("logout");
    try {
      if (Date.parse(session.expiresAt) - this.now() <= this.minimumAccessValidityMs) {
        await this.rotate(session.csrfToken, "logout refresh");
        session = this.requireActive("logout");
      }
      const result = await logoutSession({
        client: this.client,
        headers: { "X-AscendAny-CSRF": session.csrfToken },
      });
      if (result.response?.status !== 204 || result.error !== undefined) {
        throw new BrowserSessionError("logout", result.response?.status, result.error);
      }
      await this.clearLocalState();
    } finally {
      this.endOperation("logout");
    }
  }

  async forgetLocalSession(): Promise<void> {
    this.beginOperation("forget local session");
    try {
      await this.clearLocalState();
    } finally {
      this.endOperation("forget local session");
    }
  }

  private async rotate(csrfToken: string, operation: string): Promise<Account> {
    const result = await refreshSession({
      client: this.client,
      headers: { "X-AscendAny-CSRF": csrfToken },
    });
    if (result.data === undefined) {
      if (result.response?.status === 401 || result.response?.status === 403) {
        await this.clearLocalState();
      }
      throw new BrowserSessionError(operation, result.response?.status, result.error);
    }
    const session = requireAuthSession(operation, result.data, result.response?.status, result.error);
    await this.commit(session);
    return session.account;
  }

  private async commit(session: AuthSession): Promise<void> {
    validateAuthSession(session, this.now());
    try {
      await this.storage.setItem(this.storageKey, session.csrfToken);
    } catch (error) {
      this.active = null;
      this.emit();
      let cleanupError: unknown;
      try {
        await this.storage.removeItem(this.storageKey);
      } catch (caught) {
        cleanupError = caught;
      }
      throw new BrowserSessionError("persist CSRF rotation", undefined, {
        persistenceError: error,
        cleanupError,
      });
    }
    this.active = session;
    this.emit();
  }

  private async clearLocalState(): Promise<void> {
    this.active = null;
    try {
      await this.storage.removeItem(this.storageKey);
    } finally {
      this.emit();
    }
  }

  private async failLspConnection(sessionId: string, connectionError: unknown): Promise<never> {
    let cleanupError: unknown;
    try {
      const cleanup = await closeLspSession({ client: this.client, path: { sessionId } });
      if (cleanup.response?.status !== 204 || cleanup.error !== undefined) {
        cleanupError = { status: cleanup.response?.status, error: cleanup.error };
      }
    } catch (error) {
      cleanupError = error;
    }
    throw new BrowserSessionError("open LSP session", undefined, { connectionError, cleanupError });
  }

  private async closeLspConnection(sessionId: string, socket: WebSocket): Promise<void> {
    try {
      await this.ensureAuthenticated();
      const result = await closeLspSession({ client: this.client, path: { sessionId } });
      if (result.response?.status !== 204 || result.error !== undefined) {
        throw new BrowserSessionError("close LSP session", result.response?.status, result.error);
      }
    } finally {
      socket.close(1000, "LSP editor disconnected");
    }
  }

  private requireAnonymous(operation: string): void {
    if (this.active !== null) {
      throw new BrowserSessionError(operation, undefined, "session_already_active");
    }
  }

  private requireActive(operation: string): AuthSession {
    if (this.active === null) {
      throw new BrowserSessionError(operation, undefined, "session_not_active");
    }
    return this.active;
  }

  private beginOperation(operation: string): void {
    if (this.operationInFlight !== null) {
      throw new BrowserSessionError(operation, undefined, {
        code: "session_operation_in_progress",
        activeOperation: this.operationInFlight,
      });
    }
    this.operationInFlight = operation;
  }

  private endOperation(operation: string): void {
    if (this.operationInFlight !== operation) {
      throw new BrowserSessionError(operation, undefined, "session_operation_ownership_lost");
    }
    this.operationInFlight = null;
  }

  private emit(): void {
    const snapshot = this.snapshot();
    for (const listener of this.listeners) {
      listener(snapshot);
    }
  }
}

function requireLspSession(data: LspSession | undefined, status: number | undefined, error: unknown): LspSession {
  if (data === undefined) {
    throw new BrowserSessionError("open LSP session", status, error);
  }
  if (
    !LSP_SESSION_ID_PATTERN.test(data.id) ||
    data.workspaceUri !== "file:///workspace" ||
    !matchesAttachLspSessionWebSocketPath(data.webSocketPath, data.id) ||
    !LSP_ATTACH_TICKET_PATTERN.test(data.attachTicket) ||
    !Number.isFinite(Date.parse(data.expiresAt))
  ) {
    throw new BrowserSessionError("open LSP session", status, "invalid_lsp_session_response");
  }
  return data;
}

function lspWebSocketURL(apiOrigin: string, path: string): string {
  const target = new URL(path, apiOrigin);
  target.protocol = target.protocol === "https:" ? "wss:" : "ws:";
  return target.href;
}

function waitForLspWebSocket(socket: WebSocket): Promise<void> {
  if (socket.readyState === 1) {
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      socket.removeEventListener("open", handleOpen);
      socket.removeEventListener("error", handleError);
      socket.removeEventListener("close", handleClose);
    };
    const handleOpen = () => {
      cleanup();
      resolve();
    };
    const handleError = () => {
      cleanup();
      reject(new Error("LSP WebSocket connection failed."));
    };
    const handleClose = () => {
      cleanup();
      reject(new Error("LSP WebSocket closed before opening."));
    };
    socket.addEventListener("open", handleOpen, { once: true });
    socket.addEventListener("error", handleError, { once: true });
    socket.addEventListener("close", handleClose, { once: true });
  });
}

function canonicalAPIOrigin(value: string): string {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch (error) {
    throw new TypeError(`apiOrigin must be an absolute URL: ${String(error)}`);
  }
  const loopbackHTTP = parsed.protocol === "http:" && isLoopbackHostname(parsed.hostname);
  if (
    (parsed.protocol !== "https:" && !loopbackHTTP) ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.pathname !== "/" ||
    parsed.search !== "" ||
    parsed.hash !== "" ||
    value !== parsed.origin
  ) {
    throw new TypeError("apiOrigin must be one canonical HTTPS origin or canonical HTTP loopback origin.");
  }
  return parsed.origin;
}

function isLoopbackHostname(hostname: string): boolean {
  if (hostname === "localhost" || hostname === "[::1]") {
    return true;
  }
  const octets = hostname.split(".");
  if (octets.length !== 4 || octets.some((octet) => !/^\d{1,3}$/.test(octet))) {
    return false;
  }
  const values = octets.map(Number);
  return values[0] === 127 && values.every((octet) => octet >= 0 && octet <= 255);
}

function requireAuthSession(
  operation: string,
  session: AuthSession | undefined,
  status: number | undefined,
  apiError: unknown,
): AuthSession {
  if (session === undefined) {
    throw new BrowserSessionError(operation, status, apiError);
  }
  return session;
}

function validateAuthSession(session: AuthSession, now: number): void {
  const expiresAt = Date.parse(session.expiresAt);
  if (
    session.accessToken.trim() === "" ||
    !Number.isFinite(expiresAt) ||
    expiresAt <= now ||
    !CSRF_TOKEN_PATTERN.test(session.csrfToken) ||
    session.account.id.trim() === "" ||
    session.account.username.trim() === "" ||
    session.account.displayName.trim() === "" ||
    !Number.isSafeInteger(session.account.authRevision) ||
    session.account.authRevision < 1
  ) {
    throw new BrowserSessionError("validate server session", undefined, "invalid_auth_session_contract");
  }
}
