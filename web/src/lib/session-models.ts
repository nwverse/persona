// The model panel's data for an Agent's chat (#5032, docs/web/web.md "Model pill and model panel"): the launchers of the
// computer it runs on are the panel's providers, each with the models its agent lists in its own /model menu
// (lib/launcher-models.ts), and the setup the session runs now. Only the session's own launcher switches for now: another
// launcher of the same engine waits for the in-session switch (#5035), another engine needs a new conversation, and an
// agent that can't change its model while it runs (a Codex thread) keeps its effort and speed only.
//
// A list read from the agent takes seconds the first time, so what was read is kept per computer and launcher for the
// page's life: the pill and a later opening show the last list at once, and an opening reads every list again behind it
// (the session's own first, at most three at a time).
import { useSyncExternalStore } from "react";
import { t } from "../i18n";
import { errorText } from "./api";
import { agentId, agentName, sessionAgent } from "./agent-kind";
import { modelShort } from "./context-usage";
import { stateOf } from "./format";
import { forgetCustomModel, launcherAgent, launcherModels, machineLaunchers, modelName, takesTypedModel, withoutCustom, type AgentModel, type LauncherModels } from "./launcher-models";
import { validModelId } from "./model-id";
import type { PanelConfig, PanelModel, PanelProvider, PanelState } from "./model-panel";
import { canSwitchModel, isCurrentModel, modelBusy, sessionLauncher } from "./model-switch";
import { EFFORTS, hasEffortOnly, hasModes } from "./session-actions";
import type { Launcher, Session } from "./types";

/** One launcher's list as read: `list` undefined until a read answered, null from an older hub (it has no list). */
export interface ModelList {
  list?: LauncherModels | null;
  /** A read is on its way. */
  loading?: boolean;
  /** The last read failed and there is no list to show. */
  error?: string;
}

// ── what was read, kept for the page's life ──────────────────────────────────

const lists = new Map<string, ModelList>();
const machines = new Map<string, { launchers?: Launcher[]; loading?: boolean }>();
const reads = new Map<string, Promise<void>>();
const machineReads = new Map<string, Promise<void>>();
// the typed IDs whose Forget is still on its way, per list: a list read meanwhile doesn't bring them back
const forgetting = new Map<string, Set<string>>();
let version = 0;
const subscribers = new Set<() => void>();
const changed = () => {
  version++;
  for (const f of subscribers) f();
};
const subscribe = (f: () => void) => {
  subscribers.add(f);
  return () => void subscribers.delete(f);
};
const listKey = (mid: string, launcher: string) => `${mid}/${launcher}`;

/** Draws again whenever a list or a computer's launchers change. */
export function useModelLists(): number {
  return useSyncExternalStore(subscribe, () => version, () => version);
}

export const modelList = (mid: string, launcher: string): ModelList => lists.get(listKey(mid, launcher)) ?? {};

/** The launchers of `mid` as last read; undefined before the first answer. */
export const machineLauncherList = (mid: string): Launcher[] | undefined => machines.get(mid)?.launchers;

// at most three lists are read at a time (each may start the agent's CLI on the computer); `first` jumps the queue
const MAX_READS = 3;
const queue: (() => void)[] = [];
let running = 0;
function slot(first: boolean): Promise<() => void> {
  return new Promise((go) => {
    const start = () => {
      running++;
      go(() => {
        running--;
        queue.shift()?.();
      });
    };
    if (running < MAX_READS) start();
    else if (first) queue.unshift(start);
    else queue.push(start);
  });
}

/** Reads a launcher's list: once (the pill), or `again` behind the one shown (an opening, Retry). */
export function readModels(mid: string, launcher: string, o: { again?: boolean; first?: boolean } = {}): Promise<void> {
  const key = listKey(mid, launcher);
  const reading = reads.get(key);
  if (reading) return reading;
  const had = lists.get(key);
  if (had?.list !== undefined && !o.again) return Promise.resolve();
  lists.set(key, { list: had?.list, loading: true });
  changed();
  const read = slot(Boolean(o.first)).then(async (done) => {
    try {
      const r = await launcherModels(mid, launcher);
      const gone = forgetting.get(key);
      lists.set(key, { list: r && gone?.size ? { ...r, models: r.models.filter((m) => !(m.custom && gone.has(m.id))) } : r });
    } catch (e) {
      // a list already shown stays (and says nothing): a newer one just didn't come
      const now = lists.get(key);
      lists.set(key, now?.list !== undefined ? { list: now.list } : { error: errorText(e) });
    } finally {
      done();
      reads.delete(key);
      changed();
    }
  });
  reads.set(key, read);
  return read;
}

/** Reads the computer's launchers (the providers), the last ones shown meanwhile. */
export function readLaunchers(mid: string): Promise<void> {
  const reading = machineReads.get(mid);
  if (reading) return reading;
  const had = machines.get(mid);
  machines.set(mid, { launchers: had?.launchers, loading: true });
  changed();
  const read = machineLaunchers(mid, { purpose: "agent.new" }).then(
    (r) => void machines.set(mid, { launchers: r.launchers ?? [] }),
    () => void machines.set(mid, { launchers: had?.launchers }),
  ).finally(() => {
    machineReads.delete(mid);
    changed();
  });
  machineReads.set(mid, read);
  return read;
}

/** An opening of the panel: every list read again behind the one shown, the session's own first. */
export function readForPanel(s: Session): void {
  const mid = s.machine_id;
  const own = sessionLauncher(s);
  void readModels(mid, own, { again: true, first: true });
  void readLaunchers(mid).then(() => {
    for (const l of machines.get(mid)?.launchers ?? []) if (l.name !== own) void readModels(mid, l.name, { again: true });
  });
}

/** Forgets an ID typed before (#1644): off the list at once, back where it was when the hub says no. "": done; else why not. */
export async function forgetModel(mid: string, launcher: string, id: string): Promise<string> {
  const key = listKey(mid, launcher);
  const before = lists.get(key)?.list;
  if (!before) return "";
  lists.set(key, { list: withoutCustom(before, id).list });
  changed();
  const gone = forgetting.get(key) ?? new Set<string>();
  forgetting.set(key, gone.add(id));
  try {
    await forgetCustomModel(mid, launcher, id);
    return "";
  } catch (e) {
    const now = lists.get(key)?.list;
    lists.set(key, { list: now ? withoutCustom(before, id).restore(now) : before });
    changed();
    return t("composer.forget_failed", { id, error: errorText(e) });
  } finally {
    gone.delete(id);
  }
}

// ── the panel's data, from what was read ─────────────────────────────────────

/** The engine a session runs (AgentMark ids: claude, codex, agy…). */
export const sessionEngine = (s: Session): string => agentId(sessionAgent(s).agent);

/** What the pill and the panel can change on the session now: its model (Claude Code in a terminal, a runtime that says
 *  so), its effort (the composer's chips used to; a Codex thread on its app-server too). Neither: the pill is a label. */
export function sessionSwitches(s: Session): { model: boolean; effort: boolean } {
  const live = stateOf(s) !== "offline";
  return { model: canSwitchModel(s), effort: live && (hasModes(s) || hasEffortOnly(s)) };
}

/** The row of `models` the session runs: the one its model names (an alias or what an alias stands for; the default entry
 *  only when no other row names it); with no model reported yet, the default entry, which is what runs then. */
export function currentModelRow(models: AgentModel[], model: string | undefined): AgentModel | undefined {
  if (!model) return models.find((m) => m.default);
  const hits = models.filter((m) => isCurrentModel(m, model));
  return hits.find((m) => !m.default) ?? hits[0];
}

/** A model ID said briefly where no list names it: Claude's as "opus 5.5", GPT's as their list would, a gateway's path
 *  without its vendor. */
export function modelLabel(id: string): string {
  const short = id.split("/").pop() || id;
  const claude = modelShort(short);
  return claude !== short ? claude : modelName({ id: short });
}

const ALL_EFFORTS = EFFORTS.map((e) => e.id);
const hasFast = (s: Session) => Boolean(s.service_tiers?.some((x) => x.id === "priority"));

/** The efforts a row's slider offers. Antigravity lists one model per thinking level, so its rows have none (a pick
 *  takes the variant's own); the model a thread runs takes the thread's own levels (a Codex thread says them); an ID
 *  typed by hand into Claude Code takes any, as nobody knows. */
function rowEfforts(engine: string, m: AgentModel, listed: boolean, current: boolean, s: Session): string[] {
  if (engine === "agy") return [];
  if (current && s.efforts?.length) return s.efforts;
  if (listed && !m.custom) return m.efforts ?? [];
  return engine === "claude" ? ALL_EFFORTS : [];
}

/** A row of the panel; `listed` false: the model the session runs, which its list doesn't name. */
function panelModel(m: AgentModel, engine: string, current: boolean, s: Session, listed = true): PanelModel {
  const out: PanelModel = { id: m.id, name: modelName(m), efforts: rowEfforts(engine, m, listed, current, s) };
  if (m.description) out.description = m.description;
  if (m.resolved) out.resolved = m.resolved;
  if (m.default_effort) out.defaultEffort = m.default_effort;
  if (m.custom) out.custom = true;
  if (m.default) out.isDefault = true;
  if (current && engine === "codex" && hasFast(s)) out.fast = true;
  if (current && s.context_window) out.context = s.context_window;
  return out;
}

export interface SessionPanel {
  providers: PanelProvider[];
  state: PanelState;
}

/** The panel's providers and state for `s`: `launchers` the computer's (undefined before they are read), `listOf` what
 *  was read for each. The session's own launcher is always there, at its place in the list or first. */
export function sessionPanel(s: Session, launchers: Launcher[] | undefined, listOf: (launcher: string) => ModelList): SessionPanel {
  const own = sessionLauncher(s);
  const engine = sessionEngine(s);
  const { model: modelSwitch } = sessionSwitches(s);
  const all = launchers ?? [];
  const names = all.some((l) => l.name === own) ? all.map((l) => l.name) : [own, ...all.map((l) => l.name)];
  const ownList = listOf(own).list;
  const row = currentModelRow(ownList?.models ?? [], s.model);

  const providers = names.map((name): PanelProvider => {
    const e = listOf(name);
    const mine = name === own;
    const kind = mine ? engine : agentId(launcherAgent(name, all, e.list));
    const p: PanelProvider = { id: name, name, engine: kind, models: null };
    if (e.list === undefined) {
      if (e.error) p.error = e.error;
      else p.loading = true;
    } else {
      const models = (e.list?.models ?? []).map((m) => panelModel(m, kind, mine && m === row, s));
      // the model it runs, when the list doesn't name it (a list that couldn't be read, an older hub, a model it lacks)
      if (mine && !row && s.model) models.push(panelModel({ id: s.model, name: modelLabel(s.model) }, kind, true, s, false));
      p.models = models;
    }
    if (!mine) {
      p.unavailable = kind === engine ? t("chat-modelpill.other_provider") : t("chat-modelpill.other_engine", { from: agentName(engine), to: agentName(kind) });
    } else if (!modelSwitch) {
      p.unavailable = t("chat-modelpill.model_fixed", { agent: agentName(engine) });
    } else {
      if (e.list?.locked) p.locked = t("composer.locked");
      if (takesTypedModel(engine) && !e.list?.locked) p.typable = true;
    }
    return p;
  });

  const model = row?.id ?? s.model;
  const state: PanelState = {
    current: model ? { provider: own, model, ...(s.effort ? { effort: s.effort } : {}), ...(s.service_tier === "priority" ? { fast: true } : {}) } : null,
    recommended: modelSwitch ? [own] : [],
  };
  const busy = modelSwitch ? modelBusy(s) : "";
  if (busy) state.busy = busy;
  return { providers, state };
}

/** One POST …/modes of a pick. */
export type ModeStep = { model: string } | { effort: string } | { service_tier: string };

/** What applying `config` sends, in order: the model first (the efforts a model takes depend on it), then the effort,
 *  then the speed; each only when it differs from the session's. null: it can't be applied here (another launcher, a
 *  model this agent can't switch to while it runs, an ID the hub would refuse). An Antigravity variant brings its own
 *  thinking level. */
export function pickPlan(s: Session, config: PanelConfig, list: LauncherModels | null | undefined): ModeStep[] | null {
  if (config.provider !== sessionLauncher(s)) return null;
  const models = list?.models ?? [];
  const row = models.find((m) => m.id === config.model);
  const current = currentModelRow(models, s.model);
  const same = row ? row === current || isCurrentModel(row, s.model) : config.model === s.model;
  if (!same && (!sessionSwitches(s).model || (!row && !validModelId(config.model)))) return null;
  const steps: ModeStep[] = [];
  if (!same) steps.push({ model: config.model });
  const effort = config.effort || (sessionEngine(s) === "agy" ? row?.default_effort : undefined);
  if (effort && effort !== s.effort) steps.push({ effort });
  if (config.fast !== undefined && hasFast(s)) {
    const tier = config.fast ? "priority" : "default";
    if (tier !== (s.service_tier ?? "default")) steps.push({ service_tier: tier });
  }
  return steps;
}

/** What the pill says: the engine, the launcher and the model the session runs (its list's name for it once read), the
 *  effort and speed; `loading` while the list is read the first time, `failed` when that failed. */
export interface PillView {
  engine: string;
  provider: string;
  /** Absent: not known yet (no reply yet and no list to say what the launcher runs by default). */
  model?: string;
  effort?: string;
  fast?: boolean;
  loading: boolean;
  failed: boolean;
}

export function pillView(s: Session, e: ModelList): PillView {
  const row = currentModelRow(e.list?.models ?? [], s.model);
  const model = row ? modelName(row) : s.model ? modelLabel(s.model) : undefined;
  const out: PillView = { engine: sessionEngine(s), provider: sessionLauncher(s), loading: e.list === undefined && Boolean(e.loading), failed: e.list === undefined && Boolean(e.error) };
  if (model) out.model = model;
  if (s.effort) out.effort = s.effort;
  if (s.service_tier === "priority") out.fast = true;
  return out;
}
