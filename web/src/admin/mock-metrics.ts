// The mock hub's "Activity" (docs/extensions/extensions.md "Runtime status"): counts and logs made up from the extension's id, the same on
// every load (screenshots), and a log that follows itself with a line every few seconds.
import { setLogFollower } from "./api";
import type { ExtensionMetrics, ExtensionOverview, LogLine, LogsPage, MetricsOverview, MetricsWindow } from "./types";

const SPANS: Record<MetricsWindow, { span: number; step: number }> = {
  "1h": { span: 3600, step: 300 },
  "24h": { span: 86400, step: 3600 },
  "7d": { span: 7 * 86400, step: 6 * 3600 },
};

/** A small random generator seeded by text. */
function seeded(text: string): () => number {
  let h = 2166136261;
  for (const c of text) h = Math.imul(h ^ c.charCodeAt(0), 16777619);
  return () => {
    h = Math.imul(h ^ (h >>> 15), 2246822507);
    h = Math.imul(h ^ (h >>> 13), 3266489909);
    return ((h ^= h >>> 16) >>> 0) / 4294967296;
  };
}

// How busy and how healthy each one of the mock's extensions is.
const PROFILE: Record<string, { calls: number; errors: number; p95: number; exits: number; methods: string[] }> = {
  "example-notes": { calls: 40, errors: 0.004, p95: 180, exits: 0, methods: ["connector.status", "connector.archive", "connector.tools.call", "HTTP /search"] },
  "example-calendar": { calls: 0, errors: 0, p95: 0, exits: 0, methods: [] },
  telegram: { calls: 6, errors: 0.12, p95: 900, exits: 2, methods: ["connector.status", "connector.inbox"] },
  voice: { calls: 3, errors: 0.6, p95: 40, exits: 9, methods: ["HTTP /models", "HTTP /ws"] },
};

export function mockMetrics(id: string, window: MetricsWindow, now = Date.now()): ExtensionMetrics {
  const { span, step } = SPANS[window] ?? SPANS["24h"];
  const pr = PROFILE[id] ?? { calls: 8, errors: 0.02, p95: 120, exits: 0, methods: ["connector.status"] };
  const rnd = seeded(`${id}/${window}`);
  const to = Math.ceil(now / 1000 / step) * step;
  const from = to - span;
  const series: ExtensionMetrics["series"] = [];
  let calls = 0;
  let errors = 0;
  for (let t = from; t < to; t += step) {
    const busy = 0.5 + 0.5 * Math.sin((t / 86400) * Math.PI * 2 - 1.2) + rnd() * 0.4;
    const n = Math.round(pr.calls * (step / 300) * busy * (0.7 + rnd() * 0.6));
    const e = Math.round(n * pr.errors * (rnd() * 2));
    calls += n;
    errors += e;
    series.push({ at: new Date(t * 1000).toISOString(), calls: n, errors: e, p95_ms: n ? Math.round(pr.p95 * (0.7 + rnd() * 0.8)) : null });
  }
  const rate = calls ? errors / calls : 0;
  const methods = pr.methods.map((method, i) => {
    const share = i === 0 ? 0.6 : 0.4 / Math.max(1, pr.methods.length - 1);
    const c = Math.round(calls * share);
    const er = Math.round(errors * share);
    const ws = method === "HTTP /ws";
    return {
      method,
      calls: c,
      errors: er,
      error_rate: c ? Math.round((er / c) * 10000) / 10000 : 0,
      p50_ms: ws ? null : Math.round(pr.p95 * 0.3),
      p95_ms: ws ? null : Math.round(pr.p95 * (0.8 + i * 0.3)),
      p99_ms: ws ? null : Math.round(pr.p95 * 2.4),
      codes: er ? { [i % 2 ? "http_5xx" : "timeout"]: er } : undefined,
    };
  });
  const exits = Math.min(pr.exits, window === "1h" ? 1 : pr.exits);
  // i18n-ignore: mock data, an exit reason as a hub writes it
  const reason = id === "voice" ? "连不上 /run/persona/voice.sock：connect: no such file or directory" : "exit status 2";
  return {
    extension: id,
    window,
    from: new Date(from * 1000).toISOString(),
    to: new Date(to * 1000).toISOString(),
    step_seconds: step,
    total: {
      calls,
      errors,
      error_rate: Math.round(rate * 10000) / 10000,
      p50_ms: calls ? Math.round(pr.p95 * 0.3) : null,
      p95_ms: calls ? pr.p95 : null,
      p99_ms: calls ? Math.round(pr.p95 * 2.4) : null,
      codes: errors ? { timeout: Math.ceil(errors / 2), http_5xx: Math.floor(errors / 2) } : undefined,
    },
    methods,
    series,
    starts: calls ? exits + 1 : 0,
    stops: 0,
    exits,
    exit_reasons: exits ? { [reason]: exits } : undefined,
    recent_exits: exits ? [{ at: new Date(now - 42 * 60_000).toISOString(), reason }] : [],
  };
}

export function mockOverview(ids: string[], window: MetricsWindow): MetricsOverview {
  const extensions: ExtensionOverview[] = ids.map((id) => {
    const m = mockMetrics(id, window);
    return { id, calls: m.total.calls, errors: m.total.errors, error_rate: m.total.error_rate, p95_ms: m.total.p95_ms, starts: m.starts, exits: m.exits, last_exit: m.recent_exits.at(-1) };
  });
  const m = mockMetrics(ids[0] ?? "x", window);
  return {
    window,
    from: m.from,
    to: m.to,
    extensions,
    legacy_calls: [
      { route: "GET /api/v1/plugins", calls: 14, since_start: 31 },
      { route: "POST /api/v1/plugins/{id}/-/ui/{ui}", calls: 3, since_start: 9 },
    ],
  };
}

const SAMPLE = [
  ["program", "connected to notes.example.com (12 folders)"],
  ["program", "sync: 3 notes changed since 09:12"],
  ["hub", "connector.status did not answer in 30s"],
  ["program", "request GET /v1/notes?token=*** 200 in 182ms"],
  ["program", "warn: rate limited by notes.example.com, retrying in 4s"],
  ["hub", "the new version does not come up (exited): back to the one before"],
  ["program", "archive: 41 notes written to the knowledge base"],
] as const;

const logs = new Map<string, LogLine[]>();

function logOf(id: string, now = Date.now()): LogLine[] {
  let l = logs.get(id);
  if (!l) {
    l = Array.from({ length: id === "example-calendar" ? 0 : 48 }, (_, i) => {
      const [source, text] = SAMPLE[i % SAMPLE.length];
      return { seq: i, at: new Date(now - (48 - i) * 47_000).toISOString(), source, text };
    });
    logs.set(id, l);
  }
  return l;
}

export function mockLogs(id: string, q: string, since?: number): LogsPage {
  const all = logOf(id);
  const needle = q.toLowerCase();
  const lines = all.filter((l) => (since === undefined || l.seq > since) && (!needle || l.text.toLowerCase().includes(needle)));
  return { lines: lines.slice(-500), next: all.length, kept: 2000 };
}

/** Following in the mock: a made-up line every few seconds. */
export function installMockFollower() {
  setLogFollower((id, since, onLine) => {
    const all = logOf(id);
    for (const l of all) if (l.seq > since) onLine(l);
    let i = 0;
    const t = setInterval(() => {
      const [source, text] = SAMPLE[i++ % SAMPLE.length];
      const l: LogLine = { seq: all.length, at: new Date().toISOString(), source, text };
      all.push(l);
      onLine(l);
    }, 2500);
    return () => clearInterval(t);
  });
}
