// The unified model panel's data (#5032, after Cindy's model selector; docs/web/web.md "Model pill and model panel"):
// the providers a chat can run on and their models, the setup that runs now, and what the panel keeps in this browser
// (favorites, recent setups, per-model customizations). The panel (ui/model-panel/ModelPanel.tsx) draws them; each chat
// kind builds them from its own sources (an Agent: its computer's launchers; a Persona chat: the assistant's list).

/** One model of a provider, as the panel shows and picks it. */
export interface PanelModel {
  /** What a pick sends (Claude Code's aliases too: opus, sonnet…). */
  id: string;
  /** Display name (lib/launcher-models.ts modelName). */
  name: string;
  description?: string;
  /** The concrete model an alias stands for under this provider. */
  resolved?: string;
  /** Thinking efforts it takes, weakest first (lib/session-actions.ts EFFORTS / CODEX_EFFORTS ids); empty: no effort control. */
  efforts: string[];
  /** The effort the model runs with when none is set: the slider marks it "Recommended". */
  defaultEffort?: string;
  /** A Fast (priority) speed exists for this model on this engine. */
  fast?: boolean;
  /** Context window in tokens, when known. */
  context?: number;
  /** A model ID typed before for this provider (#1644): listed after the provider's own models, can be forgotten. */
  custom?: boolean;
  /** The provider's default entry: picking it is the same as picking no model. */
  isDefault?: boolean;
}

/** One provider: a launcher (rc-file alias, PATH wrapper, built-in claude, Persona-managed) or a hosted provider. */
export interface PanelProvider {
  /** Launcher name: what a pick sends as the provider. */
  id: string;
  /** What the rail tooltip, group header and second lines call it. */
  name: string;
  /** The engine it runs (AgentMark ids): claude, codex, agy… */
  engine: string;
  /** null: not read yet. */
  models: PanelModel[] | null;
  loading?: boolean;
  /** Reading the list failed: the group says why and offers Retry (onReload). */
  error?: string;
  /** The launcher fixes its own --model: its rows can't be picked, and this says why. */
  locked?: string;
  /** None of its models can be picked here (another engine, out of the computer's scope…): the reason, on each row. */
  unavailable?: string;
  /** It takes a model ID typed by hand: a search that names no listed model offers "Use “…”" in this group. */
  typable?: boolean;
}

/** A setup: what a pick applies, what a favorite or a recent entry keeps. */
export interface PanelConfig {
  provider: string;
  model: string;
  effort?: string;
  fast?: boolean;
}

/** What the chat runs and may switch to right now. */
export interface PanelState {
  /** What runs now; null while unknown. */
  current: PanelConfig | null;
  /** A switch accepted for the next message (#5035, #5033). */
  pending?: PanelConfig | null;
  /** Providers whose models switch without a restart: the Recommended group is the current model, then their other models. */
  recommended: string[];
  /** Why no pick can be applied right now (the Agent is working…): rows are off and this is the note on top. */
  busy?: string;
  /** A warning line on top of the list (a pending switch's consequence). */
  note?: string;
}

/** Where favorites, recent setups and customizations are kept apart: an Agent's providers are not a Persona chat's. */
export type PanelScope = "agent" | "assistant";
