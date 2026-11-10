// Line diff for the revision view (T-034, D-093). Own code, no dependency
// (D-035): common prefix and suffix are cut, the middle is an LCS table.
// Documents are small; a middle above MAX_CELLS falls back to "all removed,
// all added", which is still correct, only less precise.

export type DiffOp = "same" | "add" | "del";

export interface DiffLine {
  op: DiffOp;
  text: string;
  /** Line number in the old text (same, del). */
  old?: number;
  /** Line number in the new text (same, add). */
  new?: number;
}

const MAX_CELLS = 4_000_000;

/** Splits a text into lines; a final newline does not make an empty last line. */
export function splitLines(text: string): string[] {
  if (text === "") return [];
  const parts = text.split("\n");
  if (parts[parts.length - 1] === "") parts.pop();
  return parts;
}

/** The line diff from `a` to `b`. Within a change, removed lines come first. */
export function diffLines(a: string, b: string): DiffLine[] {
  const x = splitLines(a);
  const y = splitLines(b);
  let start = 0;
  while (start < x.length && start < y.length && x[start] === y[start]) start++;
  let endX = x.length;
  let endY = y.length;
  while (endX > start && endY > start && x[endX - 1] === y[endY - 1]) {
    endX--;
    endY--;
  }

  const out: DiffLine[] = [];
  for (let i = 0; i < start; i++)
    out.push({ op: "same", text: x[i] ?? "", old: i + 1, new: i + 1 });

  const n = endX - start;
  const m = endY - start;
  if (n * m > MAX_CELLS) {
    for (let i = start; i < endX; i++) out.push({ op: "del", text: x[i] ?? "", old: i + 1 });
    for (let j = start; j < endY; j++) out.push({ op: "add", text: y[j] ?? "", new: j + 1 });
  } else {
    // lcs[i][j] = LCS length of x[start+i..endX) and y[start+j..endY).
    const w = m + 1;
    const lcs = new Uint32Array((n + 1) * w);
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i * w + j] =
          x[start + i] === y[start + j]
            ? (lcs[(i + 1) * w + j + 1] ?? 0) + 1
            : Math.max(lcs[(i + 1) * w + j] ?? 0, lcs[i * w + j + 1] ?? 0);
      }
    }
    let i = 0;
    let j = 0;
    while (i < n || j < m) {
      const xi = start + i;
      const yj = start + j;
      if (i < n && j < m && x[xi] === y[yj]) {
        out.push({ op: "same", text: x[xi] ?? "", old: xi + 1, new: yj + 1 });
        i++;
        j++;
      } else if (j >= m || (i < n && (lcs[(i + 1) * w + j] ?? 0) >= (lcs[i * w + j + 1] ?? 0))) {
        out.push({ op: "del", text: x[xi] ?? "", old: xi + 1 });
        i++;
      } else {
        out.push({ op: "add", text: y[yj] ?? "", new: yj + 1 });
        j++;
      }
    }
  }

  for (let k = 0; k < x.length - endX; k++) {
    out.push({ op: "same", text: x[endX + k] ?? "", old: endX + k + 1, new: endY + k + 1 });
  }
  return out;
}

export type DiffRow = DiffLine | { op: "skip"; count: number };

/**
 * Hides long runs of unchanged lines: keeps `context` lines around each
 * change and replaces the rest by one "skip" row.
 */
export function withContext(lines: DiffLine[], context = 3): DiffRow[] {
  const keep = lines.map(() => false);
  lines.forEach((l, i) => {
    if (l.op === "same") return;
    for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++)
      keep[k] = true;
  });
  const out: DiffRow[] = [];
  let run: DiffLine[] = [];
  const flush = () => {
    // A single hidden line costs as much space as its skip row: show it.
    if (run.length === 1) out.push(...run);
    else if (run.length > 1) out.push({ op: "skip", count: run.length });
    run = [];
  };
  lines.forEach((l, i) => {
    if (keep[i]) {
      flush();
      out.push(l);
    } else {
      run.push(l);
    }
  });
  flush();
  return out;
}

/** Counts of added and removed lines. */
export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const l of lines) {
    if (l.op === "add") added++;
    else if (l.op === "del") removed++;
  }
  return { added, removed };
}
