// Lightweight unified line diff and sensitive-path hints for FileBrowserPanel (Phase 6B).
// Imported only inside the lazy-loaded FileBrowserPanel chunk (0 bytes on first screen).

export interface DiffLine {
  type: "context" | "add" | "del";
  oldLine?: number;
  newLine?: number;
  text: string;
}

export interface DiffHunk {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: DiffLine[];
}

export interface DiffResult {
  changed: boolean;
  added: number;
  removed: number;
  hunks: DiffHunk[];
  fallback?: boolean;
}

function splitLines(text: string): string[] {
  if (text === "") return [];
  const raw = text.split("\n");
  if (raw.length > 0 && raw[raw.length - 1] === "") raw.pop();
  return raw;
}

const MAX_LCS_CELLS = 250_000;

/** Computes unified diff hunks between two UTF-8 text buffers. */
export function computeLineDiff(before: string, after: string, contextLines = 3): DiffResult {
  if (before === after) {
    return { changed: false, added: 0, removed: 0, hunks: [] };
  }
  const a = splitLines(before);
  const b = splitLines(after);

  // Trim identical prefix & suffix first to keep DP tiny on typical edits.
  let prefix = 0;
  const minLen = Math.min(a.length, b.length);
  while (prefix < minLen && a[prefix] === b[prefix]) prefix++;

  let suffix = 0;
  while (suffix < minLen - prefix && a[a.length - 1 - suffix] === b[b.length - 1 - suffix]) suffix++;

  const aMid = a.slice(prefix, a.length - suffix);
  const bMid = b.slice(prefix, b.length - suffix);

  const ops: DiffLine[] = [];
  for (let i = 0; i < prefix; i++) {
    ops.push({ type: "context", oldLine: i + 1, newLine: i + 1, text: a[i] });
  }

  let fallback = false;
  if (aMid.length * bMid.length <= MAX_LCS_CELLS) {
    const m = aMid.length;
    const n = bMid.length;
    const cols = n + 1;
    const dp = new Int32Array((m + 1) * cols);
    for (let i = m - 1; i >= 0; i--) {
      const row = i * cols;
      const nextRow = (i + 1) * cols;
      for (let j = n - 1; j >= 0; j--) {
        if (aMid[i] === bMid[j]) {
          dp[row + j] = dp[nextRow + j + 1] + 1;
        } else {
          const down = dp[nextRow + j];
          const right = dp[row + j + 1];
          dp[row + j] = down >= right ? down : right;
        }
      }
    }
    let i = 0;
    let j = 0;
    while (i < m && j < n) {
      if (aMid[i] === bMid[j]) {
        ops.push({ type: "context", oldLine: prefix + i + 1, newLine: prefix + j + 1, text: aMid[i] });
        i++;
        j++;
      } else if (dp[(i + 1) * cols + j] >= dp[i * cols + (j + 1)]) {
        ops.push({ type: "del", oldLine: prefix + i + 1, text: aMid[i] });
        i++;
      } else {
        ops.push({ type: "add", newLine: prefix + j + 1, text: bMid[j] });
        j++;
      }
    }
    while (i < m) {
      ops.push({ type: "del", oldLine: prefix + i + 1, text: aMid[i] });
      i++;
    }
    while (j < n) {
      ops.push({ type: "add", newLine: prefix + j + 1, text: bMid[j] });
      j++;
    }
  } else {
    fallback = true;
    for (let i = 0; i < aMid.length; i++) {
      ops.push({ type: "del", oldLine: prefix + i + 1, text: aMid[i] });
    }
    for (let j = 0; j < bMid.length; j++) {
      ops.push({ type: "add", newLine: prefix + j + 1, text: bMid[j] });
    }
  }

  for (let i = 0; i < suffix; i++) {
    const oi = a.length - suffix + i;
    const ni = b.length - suffix + i;
    ops.push({ type: "context", oldLine: oi + 1, newLine: ni + 1, text: a[oi] });
  }

  // Group changed ranges with surrounding context into unified hunks.
  let added = 0;
  let removed = 0;
  const changedIndices: number[] = [];
  for (let i = 0; i < ops.length; i++) {
    if (ops[i].type === "add") {
      added++;
      changedIndices.push(i);
    } else if (ops[i].type === "del") {
      removed++;
      changedIndices.push(i);
    }
  }

  if (changedIndices.length === 0) {
    // Trailing newline-only difference (e.g. "a" vs "a\n").
    return {
      changed: true,
      added: after.endsWith("\n") && !before.endsWith("\n") ? 1 : 0,
      removed: before.endsWith("\n") && !after.endsWith("\n") ? 1 : 0,
      hunks: [
        {
          oldStart: Math.max(1, a.length),
          oldCount: a.length > 0 ? 1 : 0,
          newStart: Math.max(1, b.length),
          newCount: b.length > 0 ? 1 : 0,
          lines: [
            { type: "del", oldLine: a.length || 1, text: before.endsWith("\n") ? "(trailing newline)" : "(no trailing newline)" },
            { type: "add", newLine: b.length || 1, text: after.endsWith("\n") ? "(trailing newline)" : "(no trailing newline)" },
          ],
        },
      ],
    };
  }

  const ranges: [number, number][] = [];
  for (const idx of changedIndices) {
    const start = Math.max(0, idx - contextLines);
    const end = Math.min(ops.length - 1, idx + contextLines);
    if (ranges.length > 0 && start <= ranges[ranges.length - 1][1] + 1) {
      ranges[ranges.length - 1][1] = Math.max(ranges[ranges.length - 1][1], end);
    } else {
      ranges.push([start, end]);
    }
  }

  const hunks: DiffHunk[] = ranges.map(([start, end]) => {
    const slice = ops.slice(start, end + 1);
    let oldStart = 0;
    let newStart = 0;
    let oldCount = 0;
    let newCount = 0;
    for (const line of slice) {
      if (line.type !== "add") {
        if (oldStart === 0 && line.oldLine) oldStart = line.oldLine;
        oldCount++;
      }
      if (line.type !== "del") {
        if (newStart === 0 && line.newLine) newStart = line.newLine;
        newCount++;
      }
    }
    return {
      oldStart: oldStart || 1,
      oldCount,
      newStart: newStart || 1,
      newCount,
      lines: slice,
    };
  });

  return { changed: true, added, removed, hunks, fallback };
}

const KEY_NAMES = new Set(["id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "id_ecdsa_sk", "id_ed25519_sk"]);

/** Client-side mirror of Node's sensitive path classifier for pre-flight confirmation warnings. */
export function classifySensitivePathClient(pathOrName: string): { sensitive: boolean; reason: string } {
  const slashed = pathOrName.trim().replace(/\\/g, "/").toLowerCase();
  if (!slashed) return { sensitive: false, reason: "" };
  const parts = slashed.split("/").filter(Boolean);
  const base = parts[parts.length - 1] ?? "";

  if (
    base === "hub.json" || base.startsWith("hub.json.") ||
    base === "manager.json" || base === "link.json" ||
    base === "secret.key" || base === "hub.key" || base === "handles.key"
  ) {
    return { sensitive: true, reason: "hub_config" };
  }
  if (
    base === ".env" ||
    (base.startsWith(".env.") &&
      !base.endsWith(".example") &&
      !base.endsWith(".sample") &&
      !base.endsWith(".template") &&
      !base.endsWith(".dist"))
  ) {
    return { sensitive: true, reason: "env" };
  }
  if (
    KEY_NAMES.has(base) ||
    base.endsWith(".pem") || base.endsWith(".key") ||
    base.endsWith(".p8") || base.endsWith(".p12") ||
    base.endsWith(".pfx") || base.endsWith(".jks") ||
    base.endsWith(".kdbx") ||
    slashed.includes("/.ssh/") || slashed.endsWith("/.ssh") ||
    slashed.includes("/.gnupg/") || slashed.endsWith("/.gnupg")
  ) {
    return { sensitive: true, reason: "private_key" };
  }
  if (
    base === ".netrc" || base === ".git-credentials" || base === ".npmrc" ||
    base === ".pypirc" || base === ".pgpass" ||
    base === ".credentials.json" || base === "credentials.json" ||
    base === "auth.json" || base === "secrets.json" ||
    base === "secrets.yaml" || base === "secrets.yml" ||
    base.endsWith(".secret") ||
    slashed.includes("/.aws/") || slashed.endsWith("/.aws") ||
    slashed.includes("/.kube/") || slashed.endsWith("/.kube") ||
    slashed.includes("/.docker/") || slashed.endsWith("/.docker")
  ) {
    return { sensitive: true, reason: "credentials" };
  }
  return { sensitive: false, reason: "" };
}
