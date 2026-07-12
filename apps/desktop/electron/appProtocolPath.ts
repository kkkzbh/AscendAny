import path from "node:path";

export const DESKTOP_APP_SCHEME = "ascendany-app";
export const DESKTOP_APP_HOST = "bundle";
export const DESKTOP_APP_ORIGIN = `${DESKTOP_APP_SCHEME}://${DESKTOP_APP_HOST}`;
export const DESKTOP_APP_ENTRY_URL = `${DESKTOP_APP_ORIGIN}/index.html`;

export function resolveDesktopAssetPath(distRoot: string, requestURL: string): string | null {
  const root = path.resolve(distRoot);
  let parsed: URL;
  try {
    parsed = new URL(requestURL);
  } catch {
    return null;
  }
  if (
    parsed.protocol !== `${DESKTOP_APP_SCHEME}:` ||
    parsed.hostname !== DESKTOP_APP_HOST ||
    parsed.port !== "" ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.search !== "" ||
    parsed.hash !== ""
  ) {
    return null;
  }

  let decodedPath: string;
  try {
    decodedPath = decodeURIComponent(parsed.pathname);
  } catch {
    return null;
  }
  if (!decodedPath.startsWith("/") || decodedPath.includes("\\") || decodedPath.includes("\0")) {
    return null;
  }
  const segments = decodedPath.split("/");
  if (segments.some((segment) => segment === "." || segment === "..")) {
    return null;
  }
  const relativePath = decodedPath === "/" ? "index.html" : decodedPath.slice(1);
  if (relativePath === "" || relativePath.endsWith("/")) {
    return null;
  }
  const candidate = path.resolve(root, relativePath);
  if (!candidate.startsWith(`${root}${path.sep}`)) {
    return null;
  }
  return candidate;
}
