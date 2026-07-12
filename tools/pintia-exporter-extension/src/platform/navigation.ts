const PINTIA_ORIGIN = "https://pintia.cn";

export interface PintiaNavigationTarget {
  requestedUrl: string;
  accepts(actualUrl: string): boolean;
}

export function exactPintiaNavigationTarget(path: string): PintiaNavigationTarget {
  return exactUrlNavigationTarget(pintiaUrl(path));
}

export function exactUrlNavigationTarget(requestedUrl: string): PintiaNavigationTarget {
  return { requestedUrl, accepts: (actualUrl) => actualUrl === requestedUrl };
}

export function programmingProblemsNavigationTarget(problemSetId: string): PintiaNavigationTarget {
  if (!/^\d+$/.test(problemSetId)) {
    throw new Error("Pintia problem set id must contain only decimal digits.");
  }
  const requestedUrl = pintiaUrl(`/problem-sets/${problemSetId}/problems`);
  const routePattern = new RegExp(
    `^/problem-sets/${problemSetId}/problems(?:/type/(\\d+))?$`,
  );
  return {
    requestedUrl,
    accepts(actualUrl) {
      let parsed: URL;
      try {
        parsed = new URL(actualUrl);
      } catch {
        return false;
      }
      if (parsed.origin !== PINTIA_ORIGIN || actualUrl.includes("#")) {
        return false;
      }
      const route = parsed.pathname.match(routePattern);
      if (route === null) {
        return false;
      }
      if (parsed.search === "") {
        return !actualUrl.includes("?");
      }
      return route[1] !== undefined &&
        parsed.search === "?paperIndex=1";
    },
  };
}

export function navigationReached(target: PintiaNavigationTarget, actualUrl: string | undefined): boolean {
  return actualUrl !== undefined && target.accepts(actualUrl);
}

function pintiaUrl(path: string): string {
  if (!path.startsWith("/") || path.includes("?") || path.includes("#")) {
    throw new Error("Pintia navigation path must be an absolute pathname without query or fragment.");
  }
  return `${PINTIA_ORIGIN}${path}`;
}
