// Developing Persona on the web (#9617, docs/operations/developing-persona.md): what may go into the package
// (scrubbing, route patterns, the same cases as iOS DevModeTests), what the recorder writes a minute and per visible
// period, what the package holds for each choice of categories, and that the error reporter feeds the recorder with the
// hub's reports off.
import JSZip from "jszip";
import { afterEach, describe, expect, it } from "vitest";
import { DevRecorder, line, MAX_AGE_MS, MAX_BYTES, MemoryStore } from "./dev-mode";
import { buildPackage, packageFiles, packageName, readme, type DevDevice } from "./dev-package";
import { devListening, devProbe, setDevSink, type DevSink } from "./dev-probe";
import { devRoute, devWord, MAX_TEXT, scrubText } from "./dev-scrub";
import { ErrorReporter, type ReporterDeps } from "./error-reports";
import type { ErrorReport } from "./extension-wire";

const T0 = Date.parse("2027-01-15T08:00:00Z");

function clock(start = T0) {
  let now = start;
  return { now: () => now, advance: (s: number) => void (now += s * 1000) };
}

const parse = (store: MemoryStore) => store.lines.filter((l) => l.cat === "event").map((l) => JSON.parse(l.text) as Record<string, unknown>);

describe("scrubbing", () => {
  it("drops secrets, addresses and hosts from free text", () => {
    const cases: [string, string][] = [
      ["GET https://hub.example.com:8443/api/v1/sessions?token=abc#x failed", "GET /api/v1/sessions failed"],
      ["open persona://pair?url=https%3A%2F%2Fh&token=s3cr3t now", "open  now"],
      ["header Bearer abc.def-123", "header Bearer <redacted>"],
      ["password=hunter2 and api_key: 'k-1'", "password=<redacted> and api_key: <redacted>"],
      ["cookie=a%3Db; next", "cookie=<redacted>; next"],
      ["mail me@example.com please", "mail <email> please"],
      ["connect to 192.0.2.10 failed", "connect to <ip> failed"],
      ["v6 2001:db8:0:0:0:0:0:1 and fe80::1", "v6 <ip> and <ip>"],
      ["could not resolve myhub.example.net", "could not resolve <host>"],
      ["file /Users/someone/Library/x.json", "file ~/Library/x.json"],
      ["id 0123456789abcdef0123456789abcdef01 end", "id <redacted> end"],
      ["at 10:32:15 the status code 502", "at 10:32:15 the status code 502"],
    ];
    for (const [raw, want] of cases) expect(scrubText(raw), raw).toBe(want);
  });

  it("cuts long text", () => {
    const out = scrubText("长".repeat(2000));
    expect(new TextEncoder().encode(out).length).toBeLessThanOrEqual(MAX_TEXT);
    expect(out.endsWith("…")).toBe(true);
  });

  it("records routes as patterns and only code-chosen words", () => {
    expect(devRoute("get", "/machines/my-mac/sessions/9f1e/items?since=3")).toBe("GET /machines/:id/sessions/:id/items");
    expect(devRoute("POST", "/assistant/chats/abc/messages")).toBe("POST /assistant/chats/:id/messages");
    expect(devWord("sessions_list")).toBe("sessions_list");
    expect(devWord("hello world")).toBe("other");
    expect(devWord("会话")).toBe("other");
  });
});

describe("recorder", () => {
  it("writes a minute line with requests, frames, pages and errors once per route a minute", async () => {
    const c = clock();
    const store = new MemoryStore();
    const r = new DevRecorder(store, c.now, "index-abc", false, () => 42);
    r.page("persona");
    r.request("get", "/machines/m1/sessions/s1/items?since=4", 200, 30, 1000);
    r.request("GET", "/machines/m1/sessions/s1/items", 200, 50, 500);
    r.request("GET", "/sessions", 502, 900, 10);
    r.request("GET", "/sessions", 502, 900, 10);
    r.frame("sessions", 300);
    r.frame("sessions", 300);
    r.frame("item", 50);
    r.socket("dropped");
    r.socket("connected");
    c.advance(20);
    r.page("session");
    c.advance(40);
    r.roll();
    await r.flush();

    const all = parse(store);
    const minute = all.find((l) => l.kind === "minute")!;
    expect(minute.requests).toBe(4);
    expect(minute.build).toBe("index-abc");
    expect(minute.background).toBe(false);
    expect(minute.memory_mb).toBe(42);
    expect(minute.pages).toEqual({ persona: 20, session: 40 });
    const http = minute.http as { route: string; n: number; in: number; max_ms: number; status: Record<string, number> }[];
    const items = http.find((h) => h.route === "GET /machines/:id/sessions/:id/items")!;
    expect(items).toMatchObject({ n: 2, in: 1500, max_ms: 50, status: { "200": 2 } });
    expect((minute.ws as { frames: Record<string, number> }).frames).toEqual({ sessions: 2, item: 1 });
    expect(all.filter((l) => l.kind === "request_error")).toHaveLength(1);
    const text = store.lines.map((l) => l.text).join("\n");
    for (const banned of ["m1", "s1", "since"]) expect(text).not.toContain(banned);
  });

  it("sums a visible period when the page is hidden, and the hidden stretch when it comes back", async () => {
    const c = clock();
    const store = new MemoryStore();
    const r = new DevRecorder(store, c.now, "b");
    r.page("agents");
    r.request("GET", "/sessions", 200, 5, 100);
    c.advance(60);
    r.roll();
    r.frame("sessions", 10);
    c.advance(30);
    r.visibility(false);
    r.visibility(false); // a repeated event changes nothing
    c.advance(600);
    r.visibility(true);
    await r.flush();

    const all = parse(store);
    const periods = all.filter((l) => l.kind === "foreground");
    expect(periods).toHaveLength(1);
    expect(periods[0]).toMatchObject({ seconds: 90, requests: 1, ws_frames: 1, pages: { agents: 90 } });
    const minutes = all.filter((l) => l.kind === "minute");
    expect(minutes).toHaveLength(3);
    expect(minutes[2].background).toBe(true);
  });

  it("keeps errors scrubbed", async () => {
    const store = new MemoryStore();
    const r = new DevRecorder(store, clock().now);
    r.report({ id: "1", source: "web", kind: "error", level: "error", time: "2027-01-15T08:00:00Z", message: "upload to https://hub.example.com/x?token=t failed for me@example.com" });
    r.log("error", "fetch https://hub.example.com/api/v1/sessions?token=t failed");
    await r.flush();
    const error = JSON.parse(store.lines.find((l) => l.cat === "error")!.text) as ErrorReport;
    expect(error.message).toBe("upload to /x failed for <email>");
    expect(store.lines.find((l) => l.cat === "log")!.text).toContain("fetch /api/v1/sessions failed");
  });

  it("drops lines past their age and over the size", async () => {
    const store = new MemoryStore();
    await store.append([{ at: T0 - MAX_AGE_MS - 1, cat: "event", text: "old" }, { at: T0, cat: "event", text: "x".repeat(MAX_BYTES) }, { at: T0, cat: "event", text: "new" }]);
    await store.trim(T0);
    expect(store.lines.map((l) => l.text)).toEqual(["new"]);
  });

  it("writes keys sorted, kind and t among them", () => {
    expect(line(T0, "app", { b: 1, a: 2 })).toBe('{"a":2,"b":1,"kind":"app","t":"2027-01-15T08:00:00.000Z"}');
  });
});

describe("package", () => {
  const device: DevDevice = { platform: "web", os: "macos", os_version: "15", browser: "Chrome 142", build: "index-abc", hub_version: "1.2.3", utc_offset_minutes: 480 };

  async function recorded() {
    const c = clock();
    const store = new MemoryStore();
    const r = new DevRecorder(store, c.now, "index-abc");
    r.request("GET", "/sessions", 200, 1, 1);
    c.advance(60);
    r.roll();
    r.page("settings");
    r.report({ id: "1", source: "web", kind: "error", level: "error", time: "2027-01-15T08:00:00Z", message: "boom" });
    r.log("warn", "careful");
    await r.flush();
    return { store, now: c.now() };
  }

  it("holds what was picked", async () => {
    const { store, now } = await recorded();
    const all = await packageFiles(store, new Set(["logs", "errors", "energy"]), "2027-01-15T08:00:00.000Z", device, now);
    expect(Object.keys(all.files).sort()).toEqual(["README.md", "errors.jsonl", "events.jsonl", "log.txt", "manifest.json"]);
    expect(all.summary).toMatchObject({ errors: 1, logLines: 1 });
    const manifest = JSON.parse(all.files["manifest.json"]);
    expect(manifest).toMatchObject({ format: "persona-diagnostics", version: 1, platform: "web", browser: "Chrome 142", categories: ["device", "logs", "errors", "energy"] });
    expect(manifest.window.to).toBe(new Date(now).toISOString());

    const energy = await packageFiles(store, new Set(["energy"]), null, device, now);
    expect(Object.keys(energy.files).sort()).toEqual(["README.md", "events.jsonl", "manifest.json"]);
    expect(energy.files["events.jsonl"]).toContain('"kind":"minute"');
    expect(energy.files["events.jsonl"]).not.toContain('"kind":"page"');
    expect(energy.files["README.md"]).toContain("Left out by the owner: logs, errors.");
  });

  it("is a zip with its files in a folder of its name", async () => {
    const { store, now } = await recorded();
    const file = await buildPackage(store, new Set(["energy"]), null, device, now);
    expect(file.name).toBe(packageName(device, new Date(now)));
    expect(file.name).toMatch(/^persona-diagnostics-web-\d{8}-\d{4}\.zip$/);
    const zip = await JSZip.loadAsync(await file.arrayBuffer());
    const folder = file.name.replace(/\.zip$/, "");
    expect(zip.file(`${folder}/README.md`)).toBeTruthy();
    expect(zip.file(`${folder}/manifest.json`)).toBeTruthy();
    expect(readme(new Set(["logs", "errors", "energy"]))).not.toContain("Left out");
  });
});

describe("probe and error reports", () => {
  afterEach(() => setDevSink(null));

  const deps = (): ReporterDeps => ({
    storage: null,
    send: async () => ({ ok: true, enabled: false }),
    now: () => T0,
    uuid: () => "u",
    origin: "https://hub.test",
    platform: {},
    setTimer: () => {},
    clearTimer: () => {},
  });

  it("does nothing without a sink", () => {
    expect(devListening()).toBe(false);
    expect(() => devProbe.request("GET", "/x", 200, 1, 1)).not.toThrow();
  });

  it("hands the recorder an error with the hub's reports off", () => {
    const got: ErrorReport[] = [];
    const sink: DevSink = { request() {}, frame() {}, socket() {}, page() {}, report: (r) => void got.push(r) };
    const reporter = new ErrorReporter(deps());
    reporter.setFeatures([]); // the hub takes no error reports
    reporter.capture(new Error("before"));
    expect(got).toHaveLength(0);
    setDevSink(sink);
    reporter.capture(new Error("while recording"));
    expect(got.map((r) => r.message)).toEqual(["Error: while recording"]);
  });
});
