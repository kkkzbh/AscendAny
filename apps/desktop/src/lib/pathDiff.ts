export type PathDiffKind = "unchanged" | "removed" | "added";

export interface PathDiffEntry {
  kind: PathDiffKind;
  point: string;
  /** Index in the resulting path (post-update) for `unchanged` and `added`. */
  toIndex: number | null;
  /** Index in the previous path for `unchanged` and `removed`. */
  fromIndex: number | null;
}

/**
 * LCS-based node-level diff for learning paths.
 *
 * Mirrors the row-level diff we already use for streaming notes, so the
 * UI can drive the same "removed → added" entrance/exit animations.
 */
export function diffPaths(before: string[], after: string[]): PathDiffEntry[] {
  const m = before.length;
  const n = after.length;
  const lcs: number[][] = [];
  for (let i = 0; i <= m; i += 1) {
    lcs.push(new Array<number>(n + 1).fill(0));
  }
  for (let i = m - 1; i >= 0; i -= 1) {
    const row = lcs[i] as number[];
    const next = lcs[i + 1] as number[];
    for (let j = n - 1; j >= 0; j -= 1) {
      if (before[i] === after[j]) {
        row[j] = (next[j + 1] ?? 0) + 1;
      } else {
        row[j] = Math.max(next[j] ?? 0, row[j + 1] ?? 0);
      }
    }
  }
  const out: PathDiffEntry[] = [];
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    const a = before[i] as string;
    const b = after[j] as string;
    if (a === b) {
      out.push({ kind: "unchanged", point: a, fromIndex: i, toIndex: j });
      i += 1;
      j += 1;
      continue;
    }
    const down = (lcs[i + 1] as number[])[j] ?? 0;
    const right = (lcs[i] as number[])[j + 1] ?? 0;
    if (down >= right) {
      out.push({ kind: "removed", point: a, fromIndex: i, toIndex: null });
      i += 1;
    } else {
      out.push({ kind: "added", point: b, fromIndex: null, toIndex: j });
      j += 1;
    }
  }
  while (i < m) {
    out.push({
      kind: "removed",
      point: before[i] as string,
      fromIndex: i,
      toIndex: null,
    });
    i += 1;
  }
  while (j < n) {
    out.push({
      kind: "added",
      point: after[j] as string,
      fromIndex: null,
      toIndex: j,
    });
    j += 1;
  }
  return out;
}
