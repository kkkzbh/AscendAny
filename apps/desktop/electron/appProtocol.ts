import { net, protocol } from "electron";
import fs from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import {
  DESKTOP_APP_HOST,
  DESKTOP_APP_SCHEME,
  resolveDesktopAssetPath,
} from "./appProtocolPath";

export function registerDesktopAppSchemePrivileges(): void {
  protocol.registerSchemesAsPrivileged([
    {
      scheme: DESKTOP_APP_SCHEME,
      privileges: {
        standard: true,
        secure: true,
        supportFetchAPI: true,
      },
    },
  ]);
}

export function registerDesktopAppProtocol(distRoot: string): void {
  const root = path.resolve(distRoot);
  const handleRequest = async (request: Request) => {
    if (request.method !== "GET") {
      return new Response(null, { status: 405, headers: { Allow: "GET" } });
    }
    const assetPath = resolveDesktopAssetPath(root, request.url);
    if (assetPath === null) {
      return new Response(null, { status: 404 });
    }
    try {
      const [realRoot, realAsset, asset] = await Promise.all([
        fs.realpath(root),
        fs.realpath(assetPath),
        fs.stat(assetPath),
      ]);
      if (!asset.isFile() || !realAsset.startsWith(`${realRoot}${path.sep}`)) {
        return new Response(null, { status: 404 });
      }
      return (await net.fetch(pathToFileURL(realAsset).toString())) as unknown as Response;
    } catch {
      return new Response(null, { status: 404 });
    }
  };
  // Electron 33's declaration resolves protocol.handle's Response from lib.dom,
  // while net.fetch resolves the same runtime object from undici-types.
  protocol.handle(
    DESKTOP_APP_SCHEME,
    handleRequest as unknown as Parameters<typeof protocol.handle>[1],
  );
}

export function isDesktopAppURL(value: string): boolean {
  try {
    const parsed = new URL(value);
    return parsed.protocol === `${DESKTOP_APP_SCHEME}:` && parsed.hostname === DESKTOP_APP_HOST;
  } catch {
    return false;
  }
}
