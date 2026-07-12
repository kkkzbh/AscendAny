const PINTIA_PROBLEM_SET_PATH =
  /^\/problem-sets\/[0-9]+(?:\/problems\/type\/[0-9]+)?$/;

export function validatedPintiaProblemSetURL(value: string): string | null {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return null;
  }

  const allowed =
    parsed.protocol === "https:" &&
    parsed.hostname === "pintia.cn" &&
    parsed.port === "" &&
    parsed.username === "" &&
    parsed.password === "" &&
    parsed.search === "" &&
    parsed.hash === "" &&
    PINTIA_PROBLEM_SET_PATH.test(parsed.pathname);
  return allowed ? parsed.href : null;
}

export function denyWindowOpenAndMaybeOpenPintia(
  value: string,
  openExternal: (url: string) => Promise<unknown>,
  reportFailure: (error: unknown) => void,
): { action: "deny" } {
  const validatedURL = validatedPintiaProblemSetURL(value);
  if (validatedURL !== null) {
    void openExternal(validatedURL).catch(reportFailure);
  }
  return { action: "deny" };
}
