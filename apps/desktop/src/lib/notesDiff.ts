export type DiffSegmentKind = "unchanged" | "removed" | "added";

export interface DiffSegment {
  kind: DiffSegmentKind;
  lines: string[];
}

function splitLines(value: string): string[] {
  if (value === "") {
    return [];
  }
  return value.split("\n");
}

export function diffLines(before: string, after: string): DiffSegment[] {
  const a = splitLines(before);
  const b = splitLines(after);
  const m = a.length;
  const n = b.length;
  const lcs: number[][] = [];
  for (let i = 0; i <= m; i += 1) {
    lcs.push(new Array<number>(n + 1).fill(0));
  }
  for (let i = m - 1; i >= 0; i -= 1) {
    const row = lcs[i] as number[];
    const next = lcs[i + 1] as number[];
    for (let j = n - 1; j >= 0; j -= 1) {
      if (a[i] === b[j]) {
        row[j] = (next[j + 1] ?? 0) + 1;
      } else {
        row[j] = Math.max(next[j] ?? 0, row[j + 1] ?? 0);
      }
    }
  }
  const segments: DiffSegment[] = [];
  const push = (kind: DiffSegmentKind, line: string): void => {
    const last = segments[segments.length - 1];
    if (last && last.kind === kind) {
      last.lines.push(line);
    } else {
      segments.push({ kind, lines: [line] });
    }
  };
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    const aLine = a[i] as string;
    const bLine = b[j] as string;
    if (aLine === bLine) {
      push("unchanged", aLine);
      i += 1;
      j += 1;
      continue;
    }
    const down = (lcs[i + 1] as number[])[j] ?? 0;
    const right = (lcs[i] as number[])[j + 1] ?? 0;
    if (down >= right) {
      push("removed", aLine);
      i += 1;
    } else {
      push("added", bLine);
      j += 1;
    }
  }
  while (i < m) {
    push("removed", a[i] as string);
    i += 1;
  }
  while (j < n) {
    push("added", b[j] as string);
    j += 1;
  }
  return segments;
}

export function summarizeDiff(segments: DiffSegment[]): {
  unchanged: number;
  removed: number;
  added: number;
} {
  let unchanged = 0;
  let removed = 0;
  let added = 0;
  for (const segment of segments) {
    if (segment.kind === "unchanged") {
      unchanged += segment.lines.length;
    } else if (segment.kind === "removed") {
      removed += segment.lines.length;
    } else {
      added += segment.lines.length;
    }
  }
  return { unchanged, removed, added };
}

export function isMajorRewrite(segments: DiffSegment[]): boolean {
  const { unchanged, removed, added } = summarizeDiff(segments);
  const total = unchanged + Math.max(removed, added);
  if (total === 0) {
    return false;
  }
  const changed = removed + added;
  return changed / Math.max(total + Math.min(removed, added), 1) > 0.6;
}
