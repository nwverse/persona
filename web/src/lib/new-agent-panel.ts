// New Agent's model choice in the unified model panel (#5104, after Cindy's new-task composer: the user picks a model,
// the engine follows from it and stays visible; docs/web/web.md "New Agent"): the chosen computer's launchers as the
// panel's providers, each led by its "Default" row (no model sent: the model settings' model where the launcher is
// theirs, else the launcher's own), the profile's Agent presets, the setup that runs now, what a pick puts in the form
// and what the pill says. Pure: views/NewAgentModel.tsx draws it, views/NewAgent.tsx holds the form.
import { t } from "../i18n";
import { agentId, providerAgent } from "./agent-kind";
import {
  ALL_EFFORTS, defaultDetail, defaultModel, effortChoices, effortRule, followedBinding, launcherAgent, listedModel, modelName, pickerLauncher, takesEffort, typedModelOffered, type AgentModel, type LauncherModels, type PickerTier,
  type UnavailableLauncher,
} from "./launcher-models";
import { launcherBlock } from "./launcher-health";
import { validModelId } from "./model-id";
import type { ModelBinding } from "./model-config";
import { cleanSetup, type PanelConfig, type PanelModel, type PanelPreset, type PanelProvider, type PanelState } from "./model-panel";
import { providerIdNote, providerTitle } from "./provider-names";
import { effortTitle } from "./session-actions";
import { isCurrentModel } from "./model-switch";
import { currentModelRow, modelLabel, type ModelList, type PillSetup } from "./session-models";
import type { Launcher } from "./types";

/** What the form holds for the model: `launcher` "" follows Model settings › New agent; `model` "" is the launcher's
 *  default (the model settings' model where the launcher is theirs); `effort` "" is the model's own. */
export interface ModelChoice {
  launcher: string;
  model: string;
  effort: string;
}

/** What a start sends: the model only when it is not the default and the launcher lets it through, the effort only where
 *  the start takes one (`effortRule`, by the agent and the launcher that runs) and only one the model takes. An ID the
 *  list lacks (typed, #1644) goes as it is to Claude Code and Codex, also when the list couldn't be read (`undefined`); an
 *  older hub's `null` (no picker) sends none. */
export function startPicks(o: { agent: string; list: LauncherModels | null | undefined; model: string; effort: string; followed?: ModelBinding; launcher?: Pick<Launcher, "permission_modes"> }): { model?: string; effort?: string } {
  const out: { model?: string; effort?: string } = {};
  const m = o.list?.models.find((x) => x.id === o.model);
  if (m) {
    // the default entry goes too when the model settings' own model would run instead (a preset picked it, #2225; the
    // picker's "Default" row picks "")
    if ((!m.default || (o.followed?.model && o.followed.model !== m.id)) && !o.list?.locked) out.model = m.id;
  } else if (o.model && o.list !== null && typedModelOffered(o.agent, o.list) && validModelId(o.model)) {
    out.model = o.model;
  }
  const rule = effortRule(o.agent, o.launcher);
  if (o.effort && rule !== "none" && effortChoices(o.list, out.model ?? "", o.followed, rule).includes(o.effort)) out.effort = o.effort;
  return out;
}

/** What of a preset a start with this launcher's list would not pass on: its model when the launcher fixes its own --model
 *  or takes no typed ID, its effort when the model doesn't take it. Empty: all of it (or the list is not in yet). */
export function tierLeftOut(tier: PickerTier, o: { agent: string; list: LauncherModels | null | undefined; followed?: ModelBinding; launcher?: Pick<Launcher, "permission_modes"> }): string[] {
  const picks = startPicks({ agent: o.agent, list: o.list, model: tier.model ?? "", effort: tier.effort ?? "", followed: o.followed, launcher: o.launcher });
  const out: string[] = [];
  // the list's default is what runs with no model sent (startPicks sends it when the model settings would run another)
  if (tier.model && picks.model !== tier.model && !(o.list?.models.some((m) => m.id === tier.model && m.default) && !o.list?.locked)) out.push(tier.model);
  if (tier.effort && picks.effort !== tier.effort) out.push(tier.effort);
  return out;
}

function settingsEffort(agent: string, choice: ModelChoice, followed: ModelBinding): string | undefined {
  return agent === "agy" && choice.launcher ? undefined : followed.effort;
}

/** The id of a launcher's "Default" row, which sends no model: Claude Code's own name for its default entry, which that
 *  row stands in for. A list whose default entry is a model of its own (Codex's) keeps that model as a row of its own, so
 *  a favorite or a recent setup of it is that model, never "no model". */
export const DEFAULT_ROW = "default";

/** A list's model by an id it may stand for (its own id, or what an alias resolves to; a named model before the default
 *  entry standing for the same one). */
const listed = (list: LauncherModels | null | undefined, id: string): AgentModel | undefined => (id && list ? currentModelRow(list.models, id) : undefined);

/** A model's name for people: its list's name, else a short form of the id (a gateway's path without its vendor). */
export function modelTitle(list: LauncherModels | null | undefined, id: string): string {
  const m = listed(list, id);
  return m ? modelName(m) : modelLabel(id);
}

/** The model a default entry stands for, by the name the list gives it elsewhere (Claude Code's "Opus 5.5"), else a short
 *  form of its id. */
function resolvedTitle(models: AgentModel[], d: AgentModel): string {
  const twin = currentModelRow(models, d.resolved ?? d.id);
  return twin && twin !== d ? modelName(twin) : modelLabel(d.resolved ?? d.id);
}

/** What a launcher's Default row runs, by name ("" when nothing says): the launcher's own `--model` when it fixes it,
 *  else Model settings' model on their launcher, else what the default entry stands for, else the launcher's model when
 *  its list has no default entry. The Default row and the pill both say it. */
function defaultRuns(l: Launcher | undefined, list: LauncherModels | null | undefined, followed: ModelBinding): string {
  const d = list ? defaultModel(list.models) : undefined;
  if (list?.locked) return l?.model ? modelLabel(l.model) : "";
  if (followed.model) return modelTitle(list, followed.model);
  if (d?.resolved && list) return resolvedTitle(list.models, d);
  if (d && d.id !== DEFAULT_ROW) return modelName(d);
  return !d && l?.model ? modelLabel(l.model) : "";
}

export interface NewAgentPanelInput {
  launchers: Launcher[];
  listOf: (launcher: string) => ModelList;
  choice: ModelChoice;
  /** Model settings › New agent as it applies now; null: not followed (an older hub, or none set). */
  configured: ModelBinding | null;
  /** The launcher the start runs on ("" when the model settings run something that is not a launcher). */
  runs: string;
  tiers: PickerTier[];
  /** The hub's `unavailable` for this computer (#8109): Persona-managed providers its own-service rule leaves out here,
   *  with why. Absent from an older hub. */
  unavailable?: UnavailableLauncher[];
}

export interface NewAgentPanel {
  providers: PanelProvider[];
  state: PanelState;
  presets: PanelPreset[];
  /** Compare shortcuts using the same launcher binding and supported picks as Start. A null setup means Follow. */
  setupKey: (setup: PanelConfig | null, preset?: string) => string | undefined;
}

/**
 * The panel's data for New Agent: one provider per launcher of the computer in the hub's order (its engine from its
 * list, else what the launcher list says), its "Default" row first, then the other models of its list (a model typed
 * for this start that the list lacks at the end); a launcher that failed its check here (#1643) is listed with its rows
 * off and its reason, never hidden. The row lit is the model picked, else the Default row of the launcher that runs.
 * No Recommended group (a new task has none in Cindy); the presets are offered as rows of their own.
 */
export function newAgentPanel({ launchers, listOf, choice, configured, runs, tiers, unavailable = [] }: NewAgentPanelInput): NewAgentPanel {
  const providers = launchers.map((l): PanelProvider => {
    const e = listOf(l.name);
    const list = e.list;
    const agent = launcherAgent(l.name, launchers, list);
    const followed = followedBinding(l.name, configured);
    // one of Paseo's providers is its own engine, by its name: its mark where the app has one (#8142)
    const p: PanelProvider = { id: l.name, name: providerTitle(l), engine: agentId(providerAgent(agent, l.name)), models: null };
    p.defaultModel = l.model;
    p.isDefault = Boolean(configured?.provider === `launcher.${l.name}`);
    const idNote = providerIdNote(l);
    if (idNote) p.idNote = idNote;
    if (l.pi) p.alsoNote = t("lib-new-agent-panel.pi_too"); // docs/integrations/pi.md "Persona's model providers"
    const block = launcherBlock(l);
    if (block) p.unavailable = block.reason;
    // a launcher that can't start here isn't asked for its list (its rows are off anyway): its Default row stands for it
    if (list === undefined && !e.error && !block) {
      p.loading = true;
      return p;
    }
    // a list that couldn't be read says why, with Retry; the launcher still starts with its default, and Claude Code and
    // Codex still take a typed ID (the old picker's "Enter model ID…" beside the error)
    if (list === undefined && e.error) p.error = e.error;
    // the efforts a start on it takes: its models' own, all of them for a model whose levels nobody lists, except on a
    // launcher that lists its permission modes (Paseo's other providers, #8261), which takes only what its model lists
    const rule = effortRule(agent, l);
    const effortsOf = (m: AgentModel | undefined): string[] => (rule === "none" ? [] : m && !m.custom ? (m.efforts ?? []) : rule === "listed" ? [] : ALL_EFFORTS);
    const all = list?.models ?? [];
    const d = defaultModel(all);
    // Claude Code's `default` entry is folded into the Default row; a real model marked default stays a row of its own
    const folded = d?.id === DEFAULT_ROW ? d : undefined;
    const locked = list?.locked;
    // A real, lone default is a catalog choice. The synthetic Default offered for an empty reply is not one.
    if (list) p.catalogCount = locked ? Math.min(all.length, 1) : all.length - Number(Boolean(folded && all.length > 1));
    if (e.loading && (p.error || p.catalogCount === 0)) p.loading = true;
    // the Default row: what runs with no model sent, in its name (the model settings' model, the model the default entry
    // stands for, the launcher's own --model when it fixes it); a launcher that fixes it says why under it
    const runsModel = defaultRuns(l, list, followed);
    p.defaultModel = runsModel;
    const first: PanelModel = {
      id: DEFAULT_ROW,
      name: runsModel ? t("lib-launcher-models.default_named", { name: runsModel }) : t("lib-launcher-models.default"),
      efforts: effortChoices(list, "", followed, rule),
      providerDefault: true,
    };
    const note = locked || folded?.description;
    if (note) first.description = note;
    const defaultEffort = settingsEffort(agent, choice, followed) || d?.default_effort;
    if (defaultEffort) first.defaultEffort = defaultEffort;
    if (folded?.resolved) first.resolved = folded.resolved;
    const models: PanelModel[] = [first];
    // a launcher that fixes its own --model takes no pick of a model: its Default row alone
    if (!locked) {
      for (const m of all) {
        if (m === folded) continue;
        const row: PanelModel = { id: m.id, name: modelName(m), efforts: effortsOf(m) };
        if (m.description) row.description = m.description;
        if (m.resolved) row.resolved = m.resolved;
        if (m.aliases?.length) row.aliases = m.aliases;
        if (m.default_effort) row.defaultEffort = m.default_effort;
        if (m.custom) row.custom = true;
        models.push(row);
      }
      if (typedModelOffered(agent, list) && list !== null) p.typable = true;
      // the ID typed for this start, when the list doesn't name it (yet): its own row, so it is lit (not one the hub
      // remembers yet: no "Typed before", nothing to forget)
      if (p.typable && l.name === choice.launcher && choice.model && choice.model !== first.id && !listed(list, choice.model)) {
        models.push({ id: choice.model, name: choice.model, description: t("newagent.typed_now"), efforts: effortsOf(undefined) });
      }
    }
    p.models = models;
    return p;
  });
  // a provider left out on this computer (#8109: its own-service rule, where Claude Code is signed in) is listed off,
  // with why, after the ones that start here: the panel says it rather than leaving it missing
  for (const u of unavailable) {
    if (providers.some((p) => p.id === u.name)) continue;
    providers.push({ id: u.name, name: providerTitle(u), engine: agentId(u.agent || "claude"), models: null, unavailable: u.reason });
  }

  // the row lit: the model picked, else the launcher's Default row (which says what it runs)
  const followed = followedBinding(runs, configured);
  const agent = launcherAgent(runs, launchers, listOf(runs).list);
  const inheritedEffort = settingsEffort(agent, choice, followed);
  const state: PanelState = {
    current: runs ? cleanSetup({ provider: runs, model: choice.model || DEFAULT_ROW, effort: choice.effort || inheritedEffort }) : null,
    recommended: [],
    starting: true,
  };
  if (!choice.effort && inheritedEffort) state.currentEffortDisplayOnly = agent === "agy" ? "all" : "agy";

  const presets = tiers.map((x): PanelPreset => {
    const list = listOf(x.launcher).list;
    const runsOn = providers.find((p) => p.id === x.launcher);
    // the row the preset stands on: its model where its launcher's group has it or takes it typed (a group not read yet
    // is not judged), else the launcher's Default row; picking it still hands the whole preset over, so what this
    // launcher can't take is said under the box
    const own = runsOn?.models;
    const takes = x.model && (!own || runsOn?.typable || own.some((m) => !m.providerDefault && isCurrentModel(m, x.model)));
    const setup = cleanSetup({ provider: x.launcher, model: (takes ? x.model : "") || DEFAULT_ROW, effort: x.effort });
    const description = x.description || [runsOn?.name ?? x.launcher, x.model && modelTitle(list, x.model), x.effort && effortTitle(x.effort)].filter(Boolean).join(" · ");
    return { key: x.name, name: x.name, ...(description ? { description } : {}), setup };
  });
  const setupKey = (setup: PanelConfig | null, preset?: string): string | undefined => {
    const tier = preset ? tiers.find((x) => x.name === preset) : undefined;
    const picked = tier ? { launcher: tier.launcher, model: tier.model ?? "", effort: tier.effort ?? "" }
      : setup ? formChoice(setup) : { launcher: "", model: "", effort: "" };
    const binding = followedBinding(picked.launcher, configured);
    const provider = pickerLauncher(picked.launcher, binding);
    const launcher = launchers.find((l) => l.name === provider);
    const list = listOf(provider).list;
    if (!launcher || list === undefined) return undefined;
    const agent = launcherAgent(provider, launchers, list);
    const picks = startPicks({ agent, launcher, list, model: picked.model, effort: picked.effort, followed: binding });
    const models = list?.models ?? [];
    const id = list?.locked ? launcher.model : picks.model || defaultDetail(models, binding) || (!defaultModel(models) ? launcher.model : "");
    const model = id ? listedModel(models, id) : defaultModel(models);
    const effort = picks.effort || settingsEffort(agent, picked, binding) || model?.default_effort;
    return JSON.stringify([provider, model?.resolved || model?.id || id || "", effort || ""]);
  };
  return { providers, state, presets, setupKey };
}

/** What a pick puts in the form: the launcher and model (a provider's Default row is no model), the effort. */
export function formChoice(config: PanelConfig): ModelChoice {
  return { launcher: config.provider, model: config.model === DEFAULT_ROW ? "" : config.model, effort: config.effort ?? "" };
}

/** The pill: the engine and provider of the launcher that runs, the model it runs (its list's name for it), the effort
 *  it starts with (the one picked, else the model settings' on their launcher); `loading` while that launcher's list is
 *  read the first time, `failed` when that failed. */
export function newAgentPill(o: { launchers: Launcher[]; runs: string; list: ModelList; choice: ModelChoice; configured: ModelBinding | null; fallback: string }): PillSetup & { loading: boolean; failed: boolean } {
  const { runs, list: e, choice } = o;
  const list = e.list;
  const agent = launcherAgent(runs, o.launchers, list);
  const followed = followedBinding(runs, o.configured);
  const l = o.launchers.find((x) => x.name === runs);
  const d = list ? defaultModel(list.models) : undefined;
  // the model picked, else what the launcher's Default row runs; with nothing to say once the list is in, its default
  // entry's name or "Default model"
  const named = !list?.locked && choice.model ? modelTitle(list, choice.model) : defaultRuns(l, list, followed);
  const model = !runs ? o.fallback : named || (list !== undefined ? (d ? modelName(d) : t("chat-modelpill.default_model")) : undefined);
  const out: PillSetup & { loading: boolean; failed: boolean } = {
    engine: agentId(providerAgent(agent, runs)),
    provider: l ? providerTitle(l) : runs,
    loading: list === undefined && Boolean(e.loading),
    failed: list === undefined && Boolean(e.error),
  };
  if (model) out.model = model;
  const effort = takesEffort(agent, l) ? choice.effort || settingsEffort(agent, choice, followed) : "";
  if (effort) out.effort = effort;
  return out;
}

/** Whether a choice made on one computer can stay on another (#5104, Cindy: a choice the target can't run falls back to
 *  its default): following the model settings always can; a launcher picked by hand only where that computer has it and
 *  can start it. */
export function choiceCarries(choice: ModelChoice, launchers: Launcher[]): boolean {
  if (!choice.launcher) return true;
  const l = launchers.find((x) => x.name === choice.launcher);
  return Boolean(l) && !launcherBlock(l);
}
