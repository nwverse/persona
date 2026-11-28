// Cloud hosts for computers (docs/design/machine-cloud-ops.md): the web's client for the manager's cloud routes
// ("Routes for the apps"), a small shared store of the providers and the bindings (so the computers' list and a
// computer's page agree), and every rule that needs no DOM: the state's word and tone, the address and size lines, the
// lifecycle notes, which actions go in the row and which in the menu, and the gate each action needs. An older hub
// answers 404 to every route, and any failure to read the providers or the bindings reads as "no cloud hosts": the
// pages then draw nothing at all. Unit-tested on its own (cloud-hosts.test.ts).
import { useEffect, useSyncExternalStore } from "react";
import { formatDate, formatNumber, t } from "../i18n";
import { api, ApiError, enc, errorText, isAbort, pastHub } from "./api";
import type {
  CloudActionOutcome,
  CloudActionRequest,
  CloudBinding,
  CloudBindings,
  CloudBindRequest,
  CloudError,
  CloudHost,
  CloudHostAction,
  CloudHosts,
  CloudInstance,
  CloudInstanceID,
  CloudInstanceState,
  CloudLifecycle,
  CloudMachine,
  CloudMachineID,
  CloudProvider,
  CloudProviders,
  CloudRecovery,
  CloudSafety,
  CloudSnapshot,
  CloudSnapshots,
  CloudSpec,
  UnknownCloudSafety,
} from "./extension-wire";
import { cloudMachineID, parseCloudActionKind, parseCloudInstanceState, parseCloudRecoveryPhase, parseCloudSafety, parseCloudSnapshotState } from "./extension-wire";

// ── calls ──

const POINT = "/extensions/-/cloud";
const machineBase = (mid: string) => `/machines/${enc(mid)}/cloud`;

/** A cloud route's failure as the pages say it: the server's CloudError fields, or our words for a failure with none. */
export interface CloudFailure {
  code: string;
  error: string;
  /** The gate the action needs, as the hub named it; one this client does not know is kept as it came. */
  safety?: CloudSafety | UnknownCloudSafety;
  detail?: string;
  machine_id?: CloudMachineID;
  status?: number;
}

const str = (v: unknown) => (typeof v === "string" ? v : undefined);

/** Reads a failed call into CloudFailure: the hub's `error` (already in the reader's language) leads; without it, the
 *  shared words for the status (errorText). */
export function cloudFailure(e: unknown): CloudFailure {
  if (e instanceof ApiError) {
    const body = (e.body ?? {}) as Partial<Record<keyof CloudError, unknown>>;
    const code = str(body.code) ?? "";
    const error = str(body.error)?.trim() || (code && fallbackError(code)) || errorText(e);
    const safety = str(body.safety);
    const holder = str(body.machine_id);
    return {
      code,
      error,
      safety: safety === undefined ? undefined : parseCloudSafety(safety),
      detail: str(body.detail)?.trim() || undefined,
      machine_id: holder ? cloudMachineID(holder) : undefined,
      status: e.status,
    };
  }
  return { code: "", error: errorText(e) };
}

const FALLBACK_CODES = new Set(["provider_off", "not_signed_in", "plugin_failed", "deadline", "capacity", "rate_limited", "already_bound", "not_bound", "unsupported", "invalid_state", "refused", "read_only"]);

function fallbackError(code: string): string {
  return FALLBACK_CODES.has(code) ? t(`machines-cloud.err_${code}`) : "";
}

/** The enabled providers. Any failure (an older hub's 404, a hub that cannot be reached) is none. `fresh` does not
 *  join an identical read already under way (one that may have started before a write). */
export async function fetchCloudProviders(signal?: AbortSignal, fresh = false): Promise<CloudProvider[]> {
  try {
    const r = await api<CloudProviders>(POINT, { signal, dedupe: !fresh });
    return Array.isArray(r?.providers) ? r.providers : [];
  } catch (e) {
    if (isAbort(e)) throw e;
    return [];
  }
}

/** Every binding with what the hub last read of it. Any failure is none. `fresh` as for `fetchCloudProviders`. */
export async function fetchCloudBindings(signal?: AbortSignal, fresh = false): Promise<CloudBinding[]> {
  try {
    const r = await api<CloudBindings>(`${POINT}/bindings`, { signal, dedupe: !fresh });
    return Array.isArray(r?.bindings) ? r.bindings : [];
  } catch (e) {
    if (isAbort(e)) throw e;
    return [];
  }
}

/** Every provider's instances; a provider that failed is in `problems`. */
export async function fetchCloudHosts(opts: { refresh?: boolean; signal?: AbortSignal } = {}): Promise<CloudHosts> {
  const r = await api<CloudHosts>(`${POINT}/instances${opts.refresh ? "?refresh=1" : ""}`, { signal: opts.signal, timeout: pastHub(20_000) });
  return { hosts: r?.hosts ?? [], problems: r?.problems ?? [], partial: r?.partial };
}

/** One computer's binding and, when the provider answered, its host now. */
export function fetchMachineCloud(mid: string, opts: { refresh?: boolean; signal?: AbortSignal } = {}): Promise<CloudMachine> {
  return api<CloudMachine>(`${machineBase(mid)}${opts.refresh ? "?refresh=1" : ""}`, { signal: opts.signal, timeout: pastHub(15_000), dedupe: false });
}

export function bindMachineCloud(mid: string, req: CloudBindRequest): Promise<CloudMachine> {
  return api<CloudMachine>(machineBase(mid), { method: "PUT", json: req, timeout: pastHub(15_000) });
}

export function unbindMachineCloud(mid: string): Promise<CloudMachine> {
  return api<CloudMachine>(machineBase(mid), { method: "DELETE", timeout: pastHub(10_000) });
}

export function runCloudAction(mid: string, req: CloudActionRequest): Promise<CloudActionOutcome> {
  return api<CloudActionOutcome>(`${machineBase(mid)}/actions`, { method: "POST", json: req, timeout: pastHub(30_000) });
}

export async function fetchCloudSnapshots(mid: string, signal?: AbortSignal): Promise<CloudSnapshot[]> {
  const r = await api<CloudSnapshots>(`${machineBase(mid)}/snapshots`, { signal, timeout: pastHub(15_000) });
  return r?.snapshots ?? [];
}

// ── the shared store: the providers and the bindings, one read of each per load ──

export interface CloudStore {
  /** null until the first read is in. */
  providers: CloudProvider[] | null;
  bindings: CloudBinding[] | null;
}

const EMPTY: CloudStore = { providers: null, bindings: null };
let store: CloudStore = EMPTY;
let loadedAt = 0;
let inFlight: Promise<void> | null = null;
let generation = 0;
const listeners = new Set<() => void>();

function publish(next: CloudStore) {
  store = next;
  for (const l of listeners) l();
}

/** Reads the providers and the bindings again (after a bind, an unbind or an action). It never joins a read under
 *  way, which may have started before the write: it starts a new one, and an older read's answer is dropped. */
export function reloadCloud(): Promise<void> {
  return load(true);
}

/** One read of both lists as the newest generation; only the newest one's answer is published. */
function load(fresh: boolean): Promise<void> {
  const n = ++generation;
  const run = Promise.all([fetchCloudProviders(undefined, fresh), fetchCloudBindings(undefined, fresh)]).then(
    ([providers, bindings]) => {
      if (n !== generation) return;
      loadedAt = Date.now();
      publish({ providers, bindings });
    },
    () => {
      if (n === generation) publish({ providers: store.providers ?? [], bindings: store.bindings ?? [] });
    },
  );
  const tracked: Promise<void> = run.finally(() => {
    if (inFlight === tracked) inFlight = null;
  });
  inFlight = tracked;
  return tracked;
}

/** Loads the store unless a read finished within `maxAgeMs`: a page and the section inside it share one read. */
export function ensureCloud(maxAgeMs = 5_000): Promise<void> {
  if (inFlight) return inFlight;
  if (store.providers !== null && Date.now() - loadedAt < maxAgeMs) return Promise.resolve();
  return load(false);
}

/** Puts what a computer's own read said of its binding into the store, so the list shows the same state. */
export function patchBinding(mid: string, binding: CloudBinding | undefined) {
  if (store.bindings === null) return;
  const rest = store.bindings.filter((b) => b.machine_id !== mid);
  const before = store.bindings.find((b) => b.machine_id === mid);
  if (!binding && !before) return;
  if (binding && before && sameBinding(before, binding)) return;
  publish({ ...store, bindings: binding ? [...rest, binding] : rest });
}

function sameBinding(a: CloudBinding, b: CloudBinding): boolean {
  return a.instance === b.instance && a.account === b.account && a.provider === b.provider && a.extension === b.extension && a.state === b.state && a.public_ip === b.public_ip && a.display_name === b.display_name;
}

/** For tests: forgets every read. */
export function resetCloudStore() {
  generation++;
  inFlight = null;
  loadedAt = 0;
  store = EMPTY;
  for (const l of listeners) l();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => void listeners.delete(fn);
}
const snapshot = () => store;

/** What the store holds now, outside a component. */
export const currentCloud = (): CloudStore => store;

/** The providers and the bindings; `load` (default) reads them when this mounts unless a read is fresh. */
export function useCloudStore(load = true): CloudStore {
  const s = useSyncExternalStore(subscribe, snapshot, snapshot);
  useEffect(() => {
    if (load) void ensureCloud();
  }, [load]);
  return s;
}

export function bindingOf(s: CloudStore, mid: string): CloudBinding | undefined {
  return s.bindings?.find((b) => b.machine_id === mid);
}

/** Whether the cloud part of a page has anything to draw for this computer: a binding, or a provider to link one. */
export function cloudRelevant(s: CloudStore, mid: string): boolean {
  return !!bindingOf(s, mid) || (s.providers?.length ?? 0) > 0;
}

// ── states ──

export type CloudTone = "green" | "orange" | "red" | "accent" | "neutral";

/** The instance's state as a plain word; a state this client does not know reads as "unknown". */
export function stateWord(state: CloudInstanceState | undefined): string {
  const known = state === undefined ? "unknown" : parseCloudInstanceState(state);
  return t(`machines-cloud.state_${typeof known === "string" ? known : "unknown"}`);
}

export function stateTone(state: CloudInstanceState | undefined): CloudTone {
  switch (state) {
    case "running":
      return "green";
    case "starting":
    case "stopping":
    case "rebooting":
      return "accent";
    case "preempted":
      return "orange";
    case "terminated":
      return "red";
    default:
      return "neutral";
  }
}

/** On its way somewhere: the section reads it again every few seconds. */
export function transitional(state: CloudInstanceState | undefined): boolean {
  return state === "starting" || state === "stopping" || state === "rebooting";
}

export function recoveryRunning(r: CloudRecovery | undefined): boolean {
  return r?.phase === "starting" || r?.phase === "reconnecting";
}

/** Whether a computer's cloud read should be read again soon: its instance is on its way, or a recovery runs. */
export function shouldPoll(cm: CloudMachine | null | undefined): boolean {
  if (!cm?.bound) return false;
  return transitional(cm.host?.instance.state) || recoveryRunning(cm.recovery);
}

/** The snapshot's state as a plain word; one this client does not know is shown as it came. */
export function snapshotStateWord(state: string): string {
  const known = parseCloudSnapshotState(state);
  return typeof known === "string" ? t(`machines-cloud.snapshot_${known}`) : known.unknown;
}

// ── lines ──

export function hostName(instance: Pick<CloudInstance, "display_name" | "id">): string {
  return instance.display_name?.trim() || instance.id;
}

/** The address to reach it by: its first public one, else its first private one. */
export function primaryAddress(instance: Pick<CloudInstance, "public_ips" | "private_ips">): string {
  return instance.public_ips?.find(Boolean) ?? instance.private_ips?.find(Boolean) ?? "";
}

/** Region and zone as one place: the zone alone when it already names the region. */
export function placeLine(instance: Pick<CloudInstance, "region" | "zone">): string {
  const region = instance.region?.trim() ?? "";
  const zone = instance.zone?.trim() ?? "";
  if (region && zone && zone.startsWith(region)) return zone;
  return [region, zone].filter(Boolean).join(" · ");
}

const amount = (n: number) => formatNumber(n, { maximumFractionDigits: 1 });

/** "4 vCPU · 16 GB · 100 GB": its CPUs, memory and disk, each only when known. */
export function specLine(spec: CloudSpec | undefined): string {
  if (!spec) return "";
  const parts: string[] = [];
  if (spec.cpu && spec.cpu > 0) parts.push(t("machines-cloud.spec_cpu", { n: amount(spec.cpu) }));
  if (spec.memory_mb && spec.memory_mb > 0) {
    parts.push(spec.memory_mb < 1024 ? t("machines-cloud.spec_memory_mb", { size: amount(spec.memory_mb) }) : t("machines-cloud.spec_memory", { size: amount(spec.memory_mb / 1024) }));
  }
  if (spec.disk_gb && spec.disk_gb > 0) parts.push(t("machines-cloud.spec_disk", { size: amount(spec.disk_gb) }));
  return parts.join(" · ");
}

const validDate = (iso: string | undefined) => (iso && !Number.isNaN(Date.parse(iso)) ? iso : undefined);

/** What ends or interrupts it, one note each: a spot instance, the end of what was paid for, the next renewal, the
 *  free tier's activity rule. */
export function lifecycleNotes(lifecycle: CloudLifecycle | undefined, now = Date.now()): string[] {
  if (!lifecycle) return [];
  const notes: string[] = [];
  if (lifecycle.spot) notes.push(t("machines-cloud.note_spot"));
  const expires = validDate(lifecycle.expires_at);
  if (expires) notes.push(t(Date.parse(expires) < now ? "machines-cloud.note_expired" : "machines-cloud.note_expires", { date: formatDate(expires) }));
  const renews = validDate(lifecycle.renews_at);
  if (renews) notes.push(t(lifecycle.auto_renew ? "machines-cloud.note_renews_auto" : "machines-cloud.note_renews", { date: formatDate(renews) }));
  else if (lifecycle.auto_renew) notes.push(t("machines-cloud.note_auto_renew"));
  const keepalive = validDate(lifecycle.keepalive_at);
  if (keepalive) notes.push(t("machines-cloud.note_keepalive", { date: formatDate(keepalive) }));
  return notes;
}

// ── actions ──

/** The three power actions drawn as buttons in the row, in this order; everything else goes in the menu. */
export const ROW_ACTIONS = ["start", "stop", "reboot"] as const;
const MENU_ORDER = ["hard_reset", "force_stop", "sync_ip", "snapshot_list", "custom", "console"] as const;

export function actionKey(a: Pick<CloudHostAction, "action" | "id">): string {
  return a.action === "custom" ? `custom:${a.id ?? ""}` : a.action;
}

/** The host's actions split into the row's buttons and the menu's rows. Snapshots is one menu row for both
 *  snapshot_list and snapshot_create (the list and "Take snapshot" are one dialog); console is offered when the host
 *  has the action or at least a console address. */
export function splitActions(host: CloudHost): { row: CloudHostAction[]; menu: CloudHostAction[] } {
  const actions = host.actions ?? [];
  const row = ROW_ACTIONS.map((k) => actions.find((a) => a.action === k)).filter((a): a is CloudHostAction => !!a);
  const menu: CloudHostAction[] = [];
  for (const kind of MENU_ORDER) {
    if (kind === "custom") {
      menu.push(...actions.filter((a) => a.action === "custom" && a.id));
      continue;
    }
    if (kind === "snapshot_list") {
      const snap = actions.find((a) => a.action === "snapshot_list") ?? actions.find((a) => a.action === "snapshot_create");
      if (snap) menu.push({ action: "snapshot_list", safety: "read" });
      continue;
    }
    const found = actions.find((a) => a.action === kind);
    if (found) menu.push(found);
    else if (kind === "console" && host.instance.console_url) menu.push({ action: "console", safety: "read" });
  }
  return { row, menu };
}

export function hasAction(host: CloudHost | undefined, kind: CloudHostAction["action"]): boolean {
  return !!host?.actions?.some((a) => a.action === kind);
}

/** The gate an action passes, as the interface draws it. A safety this client does not know is the strictest. */
export function gateOf(a: Pick<CloudHostAction, "safety">): CloudSafety {
  const safety = parseCloudSafety(a.safety);
  return typeof safety === "string" ? safety : "destructive";
}

/** The request the routes take for `a` once its gate is passed: confirm for confirm and destructive, the instance's id
 *  as the acknowledgement for destructive. */
export function actionRequest(a: CloudHostAction, instanceId: CloudInstanceID, extra: { name?: string; reconnect?: boolean } = {}): CloudActionRequest {
  const gate = gateOf(a);
  const req: CloudActionRequest = { action: a.action };
  if (a.action === "custom" && a.id) req.id = a.id;
  if (gate !== "read") req.confirm = true;
  if (gate === "destructive") req.acknowledge = instanceId;
  if (extra.name?.trim()) req.name = extra.name.trim();
  if (extra.reconnect) req.reconnect = true;
  return req;
}

/** A standard action this client knows (any kind but custom), or undefined for one it does not. */
function standardKind(action: string) {
  const kind = parseCloudActionKind(action);
  return typeof kind === "string" && kind !== "custom" ? kind : undefined;
}

/** The button's words: plain ones for the standard actions, the manifest's title for a custom one. */
export function actionLabel(a: Pick<CloudHostAction, "action" | "id" | "title">): string {
  if (a.action === "custom") return a.title?.trim() || a.id || t("machines-cloud.action_custom");
  const kind = standardKind(a.action);
  return kind ? t(`machines-cloud.action_${kind}`) : a.action;
}

/** What the confirmation says will happen, naming the computer and the instance. */
export function confirmBody(a: CloudHostAction, machine: string, instance: string): string {
  if (a.action === "custom") return a.confirm_text?.trim() || t("machines-cloud.confirm_custom", { action: actionLabel(a), machine, instance });
  const kind = standardKind(a.action);
  return kind && kind !== "sync_ip" && kind !== "console" && kind !== "snapshot_list"
    ? t(`machines-cloud.confirm_${kind}`, { machine, instance })
    : t("machines-cloud.confirm_custom", { action: actionLabel(a), machine, instance });
}

/** The outcome as the row says it: the provider's own line when it gave one, else ours. */
export function outcomeText(a: Pick<CloudHostAction, "action" | "id" | "title">, outcome: CloudActionOutcome): string {
  if (outcome.message?.trim()) return outcome.message.trim();
  return outcome.status === "accepted" ? t("machines-cloud.outcome_accepted", { action: actionLabel(a) }) : t("machines-cloud.outcome_done", { action: actionLabel(a) });
}

/** The console's address: the action's answer, else the instance's own page. https only. */
export function consoleUrl(outcome: CloudActionOutcome | undefined, instance: Pick<CloudInstance, "console_url"> | undefined): string {
  for (const u of [outcome?.url, instance?.console_url]) {
    if (u && /^https:\/\//i.test(u)) return u;
  }
  return "";
}

// ── binding ──

export function bindRequest(host: CloudHost): CloudBindRequest {
  return { extension: host.extension, provider: host.provider, account: host.instance.account, instance: host.instance.id };
}

export function hostKey(host: Pick<CloudHost, "extension" | "provider" | "instance">): string {
  return [host.extension, host.provider, host.instance.account, host.instance.id].join("/");
}

/** Instances no computer is bound to and that can still run (not terminated). */
export function unboundHosts(hosts: readonly CloudHost[]): CloudHost[] {
  return hosts.filter((h) => !h.machine_id && h.instance.state !== "terminated");
}

/** The vendor badge's words: the vendor id the extension chose, else the provider's name. */
export function vendorLabel(b: Pick<CloudBinding, "vendor" | "provider_name" | "provider">): string {
  return b.vendor?.trim() || b.provider_name?.trim() || b.provider;
}

// ── start and reconnect ──

/** Whether the offline sheet offers Start and reconnect: bound, and the host offers start now. */
export function canStartAndReconnect(cm: CloudMachine | null | undefined): boolean {
  return !!cm?.bound && !cm.problem && hasAction(cm.host, "start");
}

export function startAndReconnectRequest(): CloudActionRequest {
  return { action: "start", confirm: true, reconnect: true };
}

/** The recovery's phase in words; failed carries its reason. */
export function recoveryText(r: CloudRecovery): string {
  if (r.phase === "failed") return r.error?.trim() ? t("machines-cloud.recovery_failed_reason", { error: r.error.trim() }) : t("machines-cloud.recovery_failed");
  const phase = parseCloudRecoveryPhase(r.phase);
  return typeof phase === "string" ? t(`machines-cloud.recovery_${phase}`) : phase.unknown;
}
