import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const outputName = "runtime-contracts.gen.ts";

function requireObject(value, label) {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${label} must be an object.`);
  }
  return value;
}

export async function generateRuntimeContracts(generatedDirectory) {
  const sourcePath = join(generatedDirectory, "source.json");
  const source = requireObject(JSON.parse(await readFile(sourcePath, "utf8")), "OpenAPI source");
  const paths = requireObject(source.paths, "OpenAPI paths");
  const matches = Object.entries(paths).filter(([, pathItem]) => {
    const item = requireObject(pathItem, "OpenAPI path item");
    if (item.get === undefined) return false;
    return requireObject(item.get, "OpenAPI GET operation").operationId === "attachLspSession";
  });
  if (matches.length !== 1) {
    throw new Error("OpenAPI must define exactly one attachLspSession GET operation.");
  }
  const [pathTemplate] = matches[0];
  if (typeof pathTemplate !== "string" || pathTemplate.split("{sessionId}").length !== 2) {
    throw new Error("attachLspSession path must contain exactly one {sessionId} parameter.");
  }

  const components = requireObject(source.components, "OpenAPI components");
  const schemas = requireObject(components.schemas, "OpenAPI schemas");
  const lspSession = requireObject(schemas.LSPSession, "OpenAPI LSPSession schema");
  const properties = requireObject(lspSession.properties, "OpenAPI LSPSession properties");
  const webSocketPath = requireObject(properties.webSocketPath, "OpenAPI webSocketPath schema");
  if (typeof webSocketPath.pattern !== "string" || webSocketPath.pattern.length === 0) {
    throw new Error("OpenAPI LSPSession.webSocketPath must define a pattern.");
  }
  new RegExp(webSocketPath.pattern, "u");

  const output = `// Generated from contracts/openapi/ascendany-v2.yaml. Do not edit.\n\n`
    + `export const ATTACH_LSP_SESSION_PATH_TEMPLATE = ${JSON.stringify(pathTemplate)} as const;\n`
    + `export const LSP_SESSION_WEBSOCKET_PATH_PATTERN = new RegExp(${JSON.stringify(webSocketPath.pattern)}, "u");\n\n`
    + `export function formatAttachLspSessionWebSocketPath(sessionId: string): string {\n`
    + `  return ATTACH_LSP_SESSION_PATH_TEMPLATE.replace("{sessionId}", sessionId);\n`
    + `}\n\n`
    + `export function matchesAttachLspSessionWebSocketPath(path: string, sessionId: string): boolean {\n`
    + `  return LSP_SESSION_WEBSOCKET_PATH_PATTERN.test(path)\n`
    + `    && path === formatAttachLspSessionWebSocketPath(sessionId);\n`
    + `}\n`;
  await writeFile(join(generatedDirectory, outputName), output, "utf8");
}
