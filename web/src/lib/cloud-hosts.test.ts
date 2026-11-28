// @vitest-environment jsdom
// The cloud hosts client's rules (docs/design/machine-cloud-ops.md): the state's word and tone, the lines, which actions
// go in the row and which in the menu, each action's gate and request, the error reading, and the store that reads
// the providers and bindings once per load and treats any failure of an older hub as "no cloud hosts".
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { formatDate, interpolate, t } from "../i18n";
import { ApiError } from "./api";
import {
  actionLabel,
  actionRequest,
  bindingOf,
  bindRequest,
  canStartAndReconnect,
  cloudFailure,
  cloudRelevant,
  confirmBody,
  consoleUrl,
  currentCloud,
  ensureCloud,
  fetchCloudBindings,
  fetchCloudProviders,
  gateOf,
  hostName,
  lifecycleNotes,
  outcomeText,
  patchBinding,
  placeLine,
  primaryAddress,
  recoveryText,
  reloadCloud,
  resetCloudStore,
  shouldPoll,
  specLine,
  splitActions,
  startAndReconnectRequest,
  stateTone,
  stateWord,
  unboundHosts,
  vendorLabel,
} from "./cloud-hosts";
import type { CloudActionKind, CloudBinding, CloudHost, CloudHostAction, CloudInstance, CloudInstanceState, CloudSafety } from "./extension-wire";
import { CLOUD_INSTANCE_STATES, cloudAccountID, cloudCustomActionID, cloudInstanceID, cloudMachineID, cloudProviderID } from "./extension-wire";

/** A value a newer hub may send that this client's closed types do not name: it reaches the code as it came. */
const offWire = <T,>(raw: string) => raw as unknown as T;

const IID = cloudInstanceID("i-0abc");
const RENEW = cloudCustomActionID("renew");
const instance = (over: Partial<CloudInstance> = {}): CloudInstance => ({ account: cloudAccountID("main"), id: IID, state: "running", ...over });
const host = (actions: CloudHostAction[], over: Partial<CloudHost> = {}, inst: Partial<CloudInstance> = {}): CloudHost => ({
  extension: "example-cloud", provider: cloudProviderID("main"), provider_name: "Example Cloud", vendor: "examplecloud", instance: instance(inst), actions, ...over,
});
const binding = (over: Partial<CloudBinding> = {}): CloudBinding => ({
  machine_id: cloudMachineID("build"), extension: "example-cloud", provider: cloudProviderID("main"), account: cloudAccountID("main"), instance: IID, bound_at: "2026-11-01T00:00:00Z", ...over,
});
const std = (action: CloudActionKind, safety: CloudSafety = "confirm"): CloudHostAction => ({ action, safety });

describe("states", () => {
  it("has a word in every language for all eight states, and reads one it does not know as unknown", () => {
    for (const s of CLOUD_INSTANCE_STATES) expect(stateWord(s)).toBe(t(`machines-cloud.state_${s}`));
    expect(new Set(CLOUD_INSTANCE_STATES.map(stateWord)).size).toBe(8);
    expect(stateWord(offWire<CloudInstanceState>("hibernating"))).toBe(t("machines-cloud.state_unknown"));
    expect(stateWord(undefined)).toBe(t("machines-cloud.state_unknown"));
  });

  it("tones running green, the ways up and down accent, a spot taken back orange and a deleted one red", () => {
    expect(stateTone("running")).toBe("green");
    expect((["starting", "stopping", "rebooting"] as const).map((s) => stateTone(s))).toEqual(["accent", "accent", "accent"]);
    expect(stateTone("preempted")).toBe("orange");
    expect(stateTone("terminated")).toBe("red");
    expect(stateTone("stopped")).toBe("neutral");
    expect(stateTone("unknown")).toBe("neutral");
  });

  it("polls while the instance is on its way or a recovery runs, and not otherwise", () => {
    const h = host([]);
    expect(shouldPoll({ bound: true, host: { ...h, instance: instance({ state: "starting" }) } })).toBe(true);
    expect(shouldPoll({ bound: true, host: h })).toBe(false);
    expect(shouldPoll({ bound: true, host: h, recovery: { phase: "reconnecting", started_at: "x" } })).toBe(true);
    expect(shouldPoll({ bound: true, host: h, recovery: { phase: "done", started_at: "x" } })).toBe(false);
    expect(shouldPoll({ bound: false })).toBe(false);
    expect(shouldPoll(null)).toBe(false);
  });
});

describe("lines", () => {
  it("names it by its display name, else its id", () => {
    expect(hostName(instance({ display_name: "  build box " }))).toBe("build box");
    expect(hostName(instance())).toBe("i-0abc");
  });

  it("reaches it by its first public address, else its first private one", () => {
    expect(primaryAddress(instance({ public_ips: ["203.0.113.7", "2001:db8::7"], private_ips: ["198.51.100.7"] }))).toBe("203.0.113.7");
    expect(primaryAddress(instance({ private_ips: ["198.51.100.7"] }))).toBe("198.51.100.7");
    expect(primaryAddress(instance())).toBe("");
  });

  it("says the place once when the zone already names the region", () => {
    expect(placeLine(instance({ region: "region-a", zone: "region-a-1" }))).toBe("region-a-1");
    expect(placeLine(instance({ region: "north", zone: "z2" }))).toBe("north · z2");
    expect(placeLine(instance({ region: "north" }))).toBe("north");
  });

  it("writes the size as CPUs, memory and disk, each only when known", () => {
    expect(specLine({ cpu: 4, memory_mb: 16384, disk_gb: 100 })).toBe(
      [t("machines-cloud.spec_cpu", { n: "4" }), t("machines-cloud.spec_memory", { size: "16" }), t("machines-cloud.spec_disk", { size: "100" })].join(" · "),
    );
    expect(specLine({ cpu: 4, memory_mb: 16384, disk_gb: 100 })).toBe("4 vCPU · 16 GB · 100 GB");
    expect(specLine({ cpu: 0.5, memory_mb: 512 })).toBe("0.5 vCPU · 512 MB");
    expect(specLine({ memory_mb: 1536 })).toBe("1.5 GB");
    expect(specLine({ size: "small" })).toBe("");
    expect(specLine(undefined)).toBe("");
  });

  it("notes a spot instance, the end of what was paid for, the next renewal and the activity rule", () => {
    const now = Date.parse("2026-11-27T00:00:00Z");
    expect(lifecycleNotes({ spot: true }, now)).toEqual([t("machines-cloud.note_spot")]);
    const notes = lifecycleNotes({ expires_at: "2026-12-31T00:00:00Z", renews_at: "2026-12-30T00:00:00Z", auto_renew: true, keepalive_at: "2026-12-01T00:00:00Z" }, now);
    expect(notes).toHaveLength(3);
    expect(notes[0]).not.toBe(t("machines-cloud.note_spot"));
    expect(lifecycleNotes({ expires_at: "2026-01-01T00:00:00Z" }, now)).toEqual([t("machines-cloud.note_expired", { date: formatDate("2026-01-01T00:00:00Z") })]);
    expect(notes[0]).toBe(t("machines-cloud.note_expires", { date: formatDate("2026-12-31T00:00:00Z") }));
    expect(lifecycleNotes({ auto_renew: true }, now)).toEqual([t("machines-cloud.note_auto_renew")]);
    expect(lifecycleNotes({ expires_at: "not a date" }, now)).toEqual([]);
    expect(lifecycleNotes(undefined, now)).toEqual([]);
  });
});

describe("actions", () => {
  const all = [std("console", "read"), std("force_stop", "destructive"), std("reboot"), std("stop"), std("start"), std("hard_reset", "destructive"), std("sync_ip", "read"), std("snapshot_list", "read"), std("snapshot_create"),
    { action: "custom", id: RENEW, title: "Renew", safety: "confirm", confirm_text: "Renew this server for another term?" } satisfies CloudHostAction];

  it("puts Start, Shut down and Restart in the row in that order, and the rest in the menu", () => {
    const { row, menu } = splitActions(host(all));
    expect(row.map((a) => a.action)).toEqual(["start", "stop", "reboot"]);
    expect(menu.map((a) => a.action === "custom" ? `custom:${a.id}` : a.action)).toEqual(["hard_reset", "force_stop", "sync_ip", "snapshot_list", "custom:renew", "console"]);
  });

  it("draws only what the instance offers, and Snapshots for a create alone", () => {
    const { row, menu } = splitActions(host([std("start"), std("snapshot_create")]));
    expect(row.map((a) => a.action)).toEqual(["start"]);
    expect(menu.map((a) => a.action)).toEqual(["snapshot_list"]);
  });

  it("offers the console from the instance's own page when the action is missing", () => {
    expect(splitActions(host([], {}, { console_url: "https://console.example.com/i-0abc" })).menu.map((a) => a.action)).toEqual(["console"]);
    expect(splitActions(host([])).menu).toEqual([]);
  });

  it("gates read at once, confirm with confirm, destructive with the instance's id, and an unknown safety as destructive", () => {
    expect(gateOf(std("sync_ip", "read"))).toBe("read");
    expect(gateOf(std("stop"))).toBe("confirm");
    expect(gateOf(std("force_stop", "destructive"))).toBe("destructive");
    expect(gateOf(std("start", offWire<CloudSafety>("dangerous")))).toBe("destructive");
    expect(actionRequest(std("sync_ip", "read"), IID)).toEqual({ action: "sync_ip" });
    expect(actionRequest(std("stop"), IID)).toEqual({ action: "stop", confirm: true });
    expect(actionRequest(std("force_stop", "destructive"), IID)).toEqual({ action: "force_stop", confirm: true, acknowledge: "i-0abc" });
    expect(actionRequest({ action: "custom", id: RENEW, safety: "confirm" }, IID)).toEqual({ action: "custom", id: "renew", confirm: true });
    expect(actionRequest(std("snapshot_create"), IID, { name: "  before upgrade " })).toEqual({ action: "snapshot_create", confirm: true, name: "before upgrade" });
    expect(actionRequest(std("snapshot_create"), IID, { name: "  " })).toEqual({ action: "snapshot_create", confirm: true });
    expect(startAndReconnectRequest()).toEqual({ action: "start", confirm: true, reconnect: true });
  });

  it("labels the standard actions in plain words and a custom one by its title", () => {
    expect(actionLabel(std("stop"))).toBe(t("machines-cloud.action_stop"));
    expect(actionLabel(std("force_stop"))).toBe(t("machines-cloud.action_force_stop"));
    expect(actionLabel({ action: "custom", id: RENEW, title: "Renew" })).toBe("Renew");
    expect(actionLabel({ action: "custom", id: cloudCustomActionID("probe") })).toBe("probe");
  });

  it("asks a custom action's own sentence, and names the computer and the instance otherwise", () => {
    expect(confirmBody({ action: "custom", id: RENEW, safety: "confirm", confirm_text: "Renew this server?" }, "build", "box")).toBe("Renew this server?");
    expect(confirmBody(std("stop"), "build", "box")).toBe(t("machines-cloud.confirm_stop", { machine: "build", instance: "box" }));
    expect(confirmBody(std("stop"), "build", "box")).toContain("build");
  });

  it("says the outcome in the provider's words, else done or on its way", () => {
    expect(outcomeText(std("start"), { status: "accepted" })).toBe(t("machines-cloud.outcome_accepted", { action: t("machines-cloud.action_start") }));
    expect(outcomeText(std("start"), { status: "done" })).toBe(t("machines-cloud.outcome_done", { action: t("machines-cloud.action_start") }));
    expect(outcomeText(std("custom"), { status: "done", message: " Renewed until March. " })).toBe("Renewed until March.");
  });

  it("opens only an https console", () => {
    expect(consoleUrl({ status: "done", url: "https://console.example.com/signed" }, instance({ console_url: "https://console.example.com/i" }))).toBe("https://console.example.com/signed");
    expect(consoleUrl({ status: "done", url: "javascript:alert(1)" }, instance({ console_url: "https://console.example.com/i" }))).toBe("https://console.example.com/i");
    expect(consoleUrl(undefined, instance({ console_url: "http://console.example.com/i" }))).toBe("");
  });

  it("offers Start and reconnect only for a bound host that offers start and has no problem", () => {
    expect(canStartAndReconnect({ bound: true, host: host([std("start")], {}, { state: "preempted" }) })).toBe(true);
    expect(canStartAndReconnect({ bound: true, host: host([std("stop")]) })).toBe(false);
    expect(canStartAndReconnect({ bound: true, host: host([std("start")]), problem: { extension: "x", provider: cloudProviderID("main"), provider_name: "X", code: "provider_off", error: "off" } })).toBe(false);
    expect(canStartAndReconnect({ bound: false })).toBe(false);
  });

  it("words every recovery phase, with the reason of a failure", () => {
    expect(recoveryText({ phase: "starting", started_at: "x" })).toBe(t("machines-cloud.recovery_starting"));
    expect(recoveryText({ phase: "reconnecting", started_at: "x" })).toBe(t("machines-cloud.recovery_reconnecting"));
    expect(recoveryText({ phase: "done", started_at: "x" })).toBe(t("machines-cloud.recovery_done"));
    const reason = "The server didn't start in time.";
    expect(recoveryText({ phase: "failed", started_at: "x", error: ` ${reason} ` })).toBe(t("machines-cloud.recovery_failed_reason", { error: reason }));
    expect(recoveryText({ phase: "failed", started_at: "x" })).toBe(t("machines-cloud.recovery_failed"));
  });

  // vitest runs from web/ (jsdom gives import.meta.url no file path)
  const WEB = process.cwd();
  it("puts each of the hub's recovery reasons in the failed line as it is, in every language", () => {
    // the hub's reasons (recovery_*) are whole sentences, the English ones with their own period: the line must not
    // double the punctuation or repeat the hint some of them end with
    for (const lang of ["en", "zh-Hans", "ja"]) {
      const web = JSON.parse(readFileSync(join(WEB, `src/i18n/locales/${lang}/machines-cloud.json`), "utf8")) as Record<string, string>;
      const hub = JSON.parse(readFileSync(join(WEB, `../server/internal/i18n/locales/${lang}/hub-extension_cloud.json`), "utf8")) as Record<string, string>;
      const reasons = Object.entries(hub).filter(([k]) => k.startsWith("recovery_")).map(([, v]) => v);
      expect(reasons.length, lang).toBeGreaterThan(0);
      for (const reason of reasons) {
        const line = interpolate(web.recovery_failed_reason!, { error: reason });
        expect(line, `${lang}: ${line}`).not.toMatch(/[.。][.。]/);
      }
      expect(web.recovery_failed_reason, `${lang}: nothing follows the reason but its full stop, so no second hint`).toMatch(/\{error\}[。]?$/);
    }
    const en = JSON.parse(readFileSync(join(WEB, "src/i18n/locales/en/machines-cloud.json"), "utf8")) as Record<string, string>;
    expect(interpolate(en.recovery_failed_reason!, { error: "The server didn't start in time." })).toBe("Couldn't start and reconnect. The server didn't start in time.");
  });
});

describe("binding", () => {
  it("asks to bind the host's own provider, account and id", () => {
    expect(bindRequest(host([], {}, { account: cloudAccountID("team"), id: cloudInstanceID("vm-9") }))).toEqual({ extension: "example-cloud", provider: "main", account: "team", instance: "vm-9" });
  });

  it("lists instances no computer is bound to and that can still run", () => {
    const hosts = [host([], { machine_id: cloudMachineID("build") }), host([], {}, { id: cloudInstanceID("a") }), host([], {}, { id: cloudInstanceID("gone"), state: "terminated" })];
    expect(unboundHosts(hosts).map((h) => h.instance.id)).toEqual(["a"]);
  });

  it("badges the vendor, else the provider's name", () => {
    expect(vendorLabel(binding({ vendor: "examplecloud", provider_name: "Example Cloud" }))).toBe("examplecloud");
    expect(vendorLabel(binding({ provider_name: "Example Cloud" }))).toBe("Example Cloud");
    expect(vendorLabel(binding())).toBe("main");
  });
});

describe("errors", () => {
  it("keeps the server's words and fields", () => {
    const f = cloudFailure(new ApiError(409, "already", { error: "Another computer holds it.", code: "already_bound", machine_id: "nas" }));
    expect(f).toEqual({ code: "already_bound", error: "Another computer holds it.", safety: undefined, detail: undefined, machine_id: "nas", status: 409 });
    const g = cloudFailure(new ApiError(502, "x", { error: "The extension failed.", code: "plugin_failed", detail: "vendor said 500" }));
    expect(g.detail).toBe("vendor said 500");
  });

  it("falls back to our words for a known code without an error", () => {
    expect(cloudFailure(new ApiError(409, "", { code: "provider_off" })).error).toBe(t("machines-cloud.err_provider_off"));
    expect(cloudFailure(new ApiError(400, "", { code: "acknowledgement_required", safety: "destructive" })).safety).toBe("destructive");
    expect(cloudFailure(new Error("boom")).code).toBe("");
  });
});

describe("the store", () => {
  const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } });
  let reads: string[] = [];
  let routes: Record<string, () => Response> = {};
  beforeEach(() => {
    reads = [];
    routes = {};
    resetCloudStore();
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://x").pathname;
      reads.push(path);
      return Promise.resolve(routes[path]?.() ?? json({ error: "not found" }, 404));
    }));
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    resetCloudStore();
  });

  it("reads an older hub's 404 and any failure as no providers and no bindings", async () => {
    expect(await fetchCloudProviders()).toEqual([]);
    expect(await fetchCloudBindings()).toEqual([]);
    routes["/api/v1/extensions/-/cloud"] = () => json({ error: "down" }, 503);
    expect(await fetchCloudProviders()).toEqual([]);
  });

  it("reads each list once per load, shares it, and patches one computer's binding", async () => {
    routes["/api/v1/extensions/-/cloud"] = () => json({ providers: [{ extension: "example-cloud", id: "main", name: "Example Cloud", vendor: "examplecloud" }] });
    routes["/api/v1/extensions/-/cloud/bindings"] = () => json({ bindings: [binding({ state: "stopped" })] });
    await Promise.all([ensureCloud(), ensureCloud()]);
    await ensureCloud();
    expect(reads.filter((p) => p.endsWith("/cloud"))).toHaveLength(1);
    expect(reads.filter((p) => p.endsWith("/bindings"))).toHaveLength(1);
    expect(bindingOf(currentCloud(), "build")?.state).toBe("stopped");
    expect(cloudRelevant(currentCloud(), "other")).toBe(true);
    patchBinding("build", binding({ state: "running" }));
    expect(bindingOf(currentCloud(), "build")?.state).toBe("running");
    patchBinding("build", undefined);
    expect(bindingOf(currentCloud(), "build")).toBeUndefined();
    await reloadCloud();
    expect(reads.filter((p) => p.endsWith("/bindings"))).toHaveLength(2);
  });

  it("starts a new read for a reload after a write and drops the answer of an older read still under way", async () => {
    const held: Array<{ path: string; answer: (r: Response) => void }> = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://x").pathname;
      reads.push(path);
      return new Promise<Response>((answer) => held.push({ path, answer }));
    }));
    const answer = (i: number, state: CloudBinding["state"]) => {
      const h = held[i];
      h.answer(h.path.endsWith("/bindings") ? json({ bindings: [binding({ state })] }) : json({ providers: [] }));
    };
    const older = ensureCloud(); // a page's read, started before the write
    await vi.waitFor(() => expect(held).toHaveLength(2));
    const newer = reloadCloud(); // the reload after the write
    await vi.waitFor(() => expect(held).toHaveLength(4));
    expect(reads.filter((p) => p.endsWith("/bindings"))).toHaveLength(2);
    answer(2, "running");
    answer(3, "running");
    await newer;
    expect(bindingOf(currentCloud(), "build")?.state).toBe("running");
    answer(0, "stopped"); // the list from before the write comes in last
    answer(1, "stopped");
    await older;
    expect(bindingOf(currentCloud(), "build")?.state).toBe("running");
  });
});
