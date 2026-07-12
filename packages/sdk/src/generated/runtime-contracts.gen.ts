// Generated from contracts/openapi/ascendany-v2.yaml. Do not edit.

export const ATTACH_LSP_SESSION_PATH_TEMPLATE = "/api/v2/lsp/sessions/{sessionId}/websocket" as const;
export const LSP_SESSION_WEBSOCKET_PATH_PATTERN = new RegExp("^/api/v2/lsp/sessions/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/websocket$", "u");

export function formatAttachLspSessionWebSocketPath(sessionId: string): string {
  return ATTACH_LSP_SESSION_PATH_TEMPLATE.replace("{sessionId}", sessionId);
}

export function matchesAttachLspSessionWebSocketPath(path: string, sessionId: string): boolean {
  return LSP_SESSION_WEBSOCKET_PATH_PATTERN.test(path)
    && path === formatAttachLspSessionWebSocketPath(sessionId);
}
