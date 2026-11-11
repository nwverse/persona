// New Agent's model choice in the unified model panel (#5104, after Cindy's new-task composer: the user picks a model,
// the engine follows from it and stays visible; docs/web/web.md "New Agent"): the chosen computer's launchers as the
// panel's providers, each led by its "Default" row (no model sent: the model settings' model where the launcher is
// theirs, else the launcher's own), the profile's Agent presets, the setup that runs now, what a pick puts in the form
// and what the pill says. Pure: views/NewAgentModel.tsx draws it, views/NewAgent.tsx holds the form.
import { t } from "../i18n";
import { agentId } from "./agent-kind";
import {
  ALL_EFFORTS, defaultModel, effortChoices, followedBinding, launcherAgent, modelName, takesEffort, typedModelOffered, type AgentModel, type LauncherModels, type PickerTier,
} from "./launcher-models";
import { launcherBlock } from "./launcher-health";
import type { ModelBinding } from "./model-config";
import { cleanSetup, type PanelConfig, type PanelModel, type PanelPreset, type PanelProvider, type PanelState } from "./model-panel";
import { isCurrentModel } from "./model-switch";
import { providerIdNote, providerTitle } from "./provider-names";
import { effortTitle } from "./session-actions";
import { modelLabel, type ModelList, type PillSetup } from "./session-models";
import type { Launcher } from "./types";

/** What the form holds for the model: `launcher` "" follows Model settings › New agent; `model` "" is the launcher's
 *  default (the model settings' model where the launcher is theirs); `effort` "" is the model's own. */
export interface ModelChoice {
  launcher: string;
  model: string;
  effort: string;
}

/** The id of a launcher's "Default" row: its list's default entry (Claude Code's `default`), else `default`. A pick of it
 *  sends no model. */
export const defaultRowId = (list: LauncherModels | null | undefined): string => (list?.models ? defaultModel(list.models)?.id : undefined) ?? "default";

/** The model settings' binding where `launcher` is the launcher they run, else nothing. */
const bindingFor = (launcher: string, configured: ModelBinding | null) => followedBinding(launcher, configured);

/** A list's model by an id it may stand for (its own id, or what an alias resolves to). */
const listed = (list: LauncherModels | null | undefined, id: string): AgentModel | undefined =>
  list?.models.find((m) => m.id === id) ?? list?.models.find((m) => isCurrentModel(m, id));

/** A model's name for people: its list's name, else a short form of the id (a gateway's path without its vendor). */
export function modelTitle(list: LauncherModels | null | undefined, id: string): string {
  const m = listed(list, id);
  return m ? modelName(m) : modelLabel(id);
}

/** The model a default entry stands for, by the name the list gives it elsewhere (Claude Code's "Opus 5.5"), else a short
 *  form of its id. */
function resolvedTitle(models: AgentModel[], d: AgentModel): string {
  const twin = models.find((m) => m !== d && isCurrentModel(m, d.resolved));
  return twin ? modelName(twin) : modelLabel(d.resolved ?? d.id);
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
}

export interface NewAgentPanel {
  providers: PanelProvider[];
  state: PanelState;
  presets: PanelPreset[];
}

/**
 * The panel's data for New Agent: one provider per launcher of the computer in the hub's order (its engine from its
 * list, else what the launcher list says), its "Default" row first, then the other models of its list (a model typed
 * for this start that the list lacks at the end); a launcher that failed its check here (#1643) is listed with its rows
 * off and its reason, never hidden. The row lit is the model picked, else the Default row of the launcher that runs.
 * No Recommended group (a new task has none in Cindy); the presets are offered as rows of their own.
 */
export function newAgentPanel({ launchers, listOf, choice, configured, runs, tiers }: NewAgentPanelInput): NewAgentPanel {
  const providers = launchers.map((l): PanelProvider => {
    const e = listOf(l.name);
    const list = e.list;
    const agent = launcherAgent(l.name, launchers, list);
    const followed = bindingFor(l.name, configured);
    const p: PanelProvider = { id: l.name, name: providerTitle(l), engine: agentId(agent), models: null };
    const idNote = providerIdNote(l);
    if (idNote) p.idNote = idNote;
    const block = launcherBlock(l);
    if (block) {
      p.unavailable = block.reason;
      p.help = true;
    }
    if (list === undefined && !e.error) {
      p.loading = true;
      return p;
    }
    // a list that couldn't be read says why, with Retry; the launcher still starts with its default, and Claude Code and
    // Codex still take a typed ID (the old picker's "Enter model ID…" beside the error)
    if (list === undefined) p.error = e.error;
    const effortsOf = (m: AgentModel | undefined): string[] => (!takesEffort(agent) ? [] : m && !m.custom ? (m.efforts ?? []) : ALL_EFFORTS);
    const all = list?.models ?? [];
    const d = defaultModel(all);
    const locked = list?.locked;
    // the Default row: what runs with no model sent, in its name (the model settings' model, the model the default entry
    // stands for, the launcher's own --model when it fixes it); a launcher that fixes it says why under it
    const runsModel = locked ? (l.model ? modelLabel(l.model) : "") : followed.model ? modelTitle(list, followed.model) : d?.resolved ? resolvedTitle(all, d) : !d && l.model ? modelLabel(l.model) : "";
    const first: PanelModel = {
      id: defaultRowId(list),
      name: runsModel ? t("lib-launcher-models.default_named", { name: runsModel }) : t("lib-launcher-models.default"),
      efforts: takesEffort(agent) ? effortChoices(list, "", followed) : [],
      providerDefault: true,
    };
    const note = locked || d?.description;
    if (note) first.description = note;
    const defaultEffort = followed.effort || d?.default_effort;
    if (defaultEffort) first.defaultEffort = defaultEffort;
    if (d?.resolved) first.resolved = d.resolved;
    const models: PanelModel[] = [first];
    // a launcher that fixes its own --model takes no pick of a model: its Default row alone
    if (!locked) {
      for (const m of all) {
        if (m === d) continue;
        const row: PanelModel = { id: m.id, name: modelName(m), efforts: effortsOf(m) };
        if (m.description) row.description = m.description;
        if (m.resolved) row.resolved = m.resolved;
        if (m.default_effort) row.defaultEffort = m.default_effort;
        if (m.custom) row.custom = true;
        models.push(row);
      }
      // the ID typed for this start, when the list doesn't name it (yet): its own row, so it is lit
      if (l.name === choice.launcher && choice.model && choice.model !== first.id && !listed(list, choice.model) && typedModelOffered(agent, list) && list !== null) {
        models.push({ id: choice.model, name: choice.model, efforts: effortsOf(undefined), custom: true });
      }
      if (typedModelOffered(agent, list) && list !== null) p.typable = true;
    }
    p.models = models;
    return p;
  });

  // the row lit: the model picked, else the launcher's Default row (which says what it runs)
  const runsList = runs ? listOf(runs).list : undefined;
  const followed = bindingFor(runs, configured);
  const state: PanelState = {
    current: runs ? cleanSetup({ provider: runs, model: choice.model || defaultRowId(runsList), effort: choice.effort || followed.effort }) : null,
    recommended: [],
    starting: true,
  };

  const presets = tiers.map((x): PanelPreset => {
    const list = listOf(x.launcher).list;
    // the row the preset stands on: its model where its launcher's group has it (or takes it typed), else the launcher's
    // Default row; picking it still hands the whole preset over, so what this launcher can't take is said under the box
    const takes = x.model && (list === undefined || (!list?.locked && (listed(list, x.model) || (typedModelOffered(launcherAgent(x.launcher, launchers, list), list) && list !== null))));
    const setup = cleanSetup({ provider: x.launcher, model: (takes ? x.model : "") || defaultRowId(list), effort: x.effort });
    const runsOn = providers.find((p) => p.id === x.launcher);
    const description = x.description || [runsOn?.name ?? x.launcher, x.model && modelTitle(list, x.model), x.effort && effortTitle(x.effort)].filter(Boolean).join(" · ");
    return { key: x.name, name: x.name, ...(description ? { description } : {}), setup };
  });
  return { providers, state, presets };
}

/** What a pick puts in the form: the launcher and model (a provider's Default row is no model), the effort. */
export function formChoice(config: PanelConfig, list: LauncherModels | null | undefined): ModelChoice {
  return { launcher: config.provider, model: config.model === defaultRowId(list) ? "" : config.model, effort: config.effort ?? "" };
}

/** The pill: the engine and provider of the launcher that runs, the model it runs (its list's name for it), the effort
 *  it starts with (the one picked, else the model settings' on their launcher); `loading` while that launcher's list is
 *  read the first time, `failed` when that failed. */
export function newAgentPill(o: { launchers: Launcher[]; runs: string; list: ModelList; choice: ModelChoice; configured: ModelBinding | null; fallback: string }): PillSetup & { loading: boolean; failed: boolean } {
  const { runs, list: e, choice } = o;
  const list = e.list;
  const agent = launcherAgent(runs, o.launchers, list);
  const followed = bindingFor(runs, o.configured);
  const l = o.launchers.find((x) => x.name === runs);
  const id = choice.model || followed.model;
  const d = list ? defaultModel(list.models) : undefined;
  const model = !runs
    ? o.fallback
    : list?.locked && l?.model
      ? modelLabel(l.model)
      : id
        ? modelTitle(list, id)
        : d?.resolved && list
          ? resolvedTitle(list.models, d)
          : list !== undefined
            ? (d ? modelName(d) : l?.model ? modelLabel(l.model) : t("chat-modelpill.default_model"))
            : undefined;
  const out: PillSetup & { loading: boolean; failed: boolean } = {
    engine: agentId(agent),
    provider: l ? providerTitle(l) : runs,
    loading: list === undefined && Boolean(e.loading),
    failed: list === undefined && Boolean(e.error),
  };
  if (model) out.model = model;
  const effort = takesEffort(agent) ? choice.effort || followed.effort : "";
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
