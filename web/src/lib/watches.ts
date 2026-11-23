// Watch cards (docs/assistant/watch-cards.md, #13020): what the owner asked the assistant to check on a schedule, kept
// as cards at the top of Overview and on their project's node. The hub owns them (GET /api/v1/assistant/watches); each
// card's refresh is an ordinary scheduled task (task_id), so "Refresh now" is that task's Run now.
import { api } from "./api";
import { ruleText, type Task } from "./routines";
import { t } from "../i18n";

export type WatchDisplay = "number" | "status" | "table" | "trend";
export type WatchStatus = "ok" | "warn" | "bad";

export interface WatchAlert {
  when: "above" | "below" | "bad" | "change";
  value?: number;
}

export interface WatchReading {
  at: string;
  number?: number;
  status?: WatchStatus;
  text?: string;
  table?: { columns: string[]; rows: string[][] };
  alert?: boolean;
}

export interface WatchPoint {
  at: string;
  number?: number;
  status?: WatchStatus;
  alert?: boolean;
}

export interface Watch {
  id: string;
  title: string;
  question: string;
  display: WatchDisplay;
  unit?: string;
  schedule: { repeat: "daily" | "interval" | "cron"; time?: string; weekdays?: number[]; every_minutes?: number; cron?: string };
  alert?: WatchAlert | null;
  project?: string;
  project_name?: string;
  state: "draft" | "active";
  paused?: boolean;
  task_id?: string;
  latest?: WatchReading | null;
  history?: WatchPoint[] | null;
  cron_text?: string;
  next_run?: string;
  last_run?: string;
  last_status?: string;
  last_error?: string;
  run_machine_id?: string;
  run_session_id?: string;
  no_task?: boolean;
}

/** A change from the card's editor: only what is given changes; `alert: null` removes the alert. */
export interface WatchChange {
  title?: string;
  question?: string;
  schedule?: string;
  display?: WatchDisplay;
  unit?: string;
  alert?: WatchAlert | null;
  project?: string;
  paused?: boolean;
}

const enc = encodeURIComponent;

/** The cards in Overview's order (drafts too); with `project`, that project's confirmed cards only. */
export async function loadWatches(project?: string): Promise<Watch[]> {
  const r = await api<{ watches?: Watch[] | null }>(`/assistant/watches${project ? `?project=${enc(project)}` : ""}`);
  return Array.isArray(r.watches) ? r.watches : [];
}

export async function confirmWatch(id: string): Promise<Watch> {
  return (await api<{ watch: Watch }>(`/assistant/watches/${enc(id)}/confirm`, { method: "POST" })).watch;
}

export async function updateWatch(id: string, change: WatchChange): Promise<Watch> {
  return (await api<{ watch: Watch }>(`/assistant/watches/${enc(id)}`, { method: "PUT", json: change })).watch;
}

export async function deleteWatch(id: string): Promise<void> {
  await api(`/assistant/watches/${enc(id)}`, { method: "DELETE" });
}

export async function reorderWatches(ids: string[]): Promise<void> {
  await api(`/assistant/watches/order`, { method: "PUT", json: { ids } });
}

/** Run the card's task now: the card shows the new reading when the run records it. */
export async function refreshWatch(w: Watch): Promise<void> {
  if (!w.task_id) return;
  await api(`/assistant/tasks/${enc(w.task_id)}/run`, { method: "POST" });
}

/** `ids` with `id` moved by `by` places (−1 earlier, +1 later); unchanged at an end. */
export function moved(ids: string[], id: string, by: number): string[] {
  const i = ids.indexOf(id), j = i + by;
  if (i < 0 || j < 0 || j >= ids.length) return ids;
  const out = [...ids];
  out.splice(i, 1);
  out.splice(j, 0, id);
  return out;
}

/** `ids` with `from` dropped onto `to`'s place. */
export function droppedOn(ids: string[], from: string, to: string): string[] {
  const j = ids.indexOf(to);
  if (from === to || j < 0 || !ids.includes(from)) return ids;
  return moved(ids, from, j - ids.indexOf(from));
}

/** The refresh rule in words, as the Scheduled tasks page says it. */
export function watchRule(w: Watch): string {
  const s = w.schedule ?? { repeat: "daily", time: "09:00" };
  return ruleText({ id: w.task_id ?? w.id, title: w.title, prompt: "", enabled: !w.paused, repeat: s.repeat, time: s.time, weekdays: s.weekdays, every_minutes: s.every_minutes, cron: s.cron, cron_text: w.cron_text } as Task);
}

/** The schedule as the editor's text field holds it: HH:MM, 30m / 2h / 1d, or the cron expression. */
export function scheduleText(w: Watch): string {
  const s = w.schedule;
  if (!s) return "09:00";
  if (s.repeat === "interval") {
    const m = s.every_minutes ?? 0;
    return m % 1440 === 0 ? `${m / 1440}d` : m % 60 === 0 ? `${m / 60}h` : `${m}m`;
  }
  if (s.repeat === "cron") return s.cron ?? "";
  if (s.weekdays?.length) return `${Number((s.time ?? "09:00").split(":")[1])} ${Number((s.time ?? "09:00").split(":")[0])} * * ${s.weekdays.join(",")}`;
  return s.time ?? "09:00";
}

/** When the card pushes, in words. */
export function alertText(a?: WatchAlert | null, unit?: string): string {
  if (!a) return t("watch.alert_never");
  const value = `${a.value ?? 0}${unit ? ` ${unit}` : ""}`;
  if (a.when === "above") return t("watch.alert_above", { value });
  if (a.when === "below") return t("watch.alert_below", { value });
  if (a.when === "bad") return t("watch.alert_bad");
  return t("watch.alert_change");
}

/** What a card's look says about it: its tone (the alert ring, the light) and whether it has an answer yet. */
export function watchTone(w: Watch): "alert" | "bad" | "warn" | "ok" | "empty" {
  const r = w.latest;
  if (!r) return "empty";
  if (r.alert) return "alert";
  if (r.status === "bad") return "bad";
  if (r.status === "warn") return "warn";
  return "ok";
}

/** The trend's numbers, oldest first (readings without one left out). */
export function trendValues(w: Watch): number[] {
  return (w.history ?? []).map((p) => p.number).filter((n): n is number => typeof n === "number" && Number.isFinite(n));
}

/** A sparkline's points in a width × height box, the lowest value at the bottom; one value draws a flat line. */
export function sparkPoints(values: number[], width: number, height: number, pad = 2): string {
  if (values.length === 0) return "";
  const lo = Math.min(...values), hi = Math.max(...values);
  const span = hi - lo || 1;
  const step = values.length > 1 ? (width - pad * 2) / (values.length - 1) : 0;
  return values
    .map((v, i) => {
      const x = values.length > 1 ? pad + i * step : width / 2;
      const y = hi === lo ? height / 2 : pad + (height - pad * 2) * (1 - (v - lo) / span);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}
