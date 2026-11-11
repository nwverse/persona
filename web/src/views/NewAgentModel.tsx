// New Agent's model choice (#5104, after Cindy's new-task composer, which the user asked this part of the web to match;
// docs/web/web.md "New Agent"): the chat's model pill at the right of the first message box's bottom row, opening the
// unified model panel (ui/model-panel) with the chosen computer's launchers as its providers. The user picks a model; the
// launcher and its engine come with it, and the engine's mark stays on the pill and on every row. A "Follow Model
// settings" row on top goes back to the default, the profile's presets are a group of their own, the thinking effort and
// Claude Code's ultracode switch are in the chosen model's flyout. The panel's data is lib/new-agent-panel.ts.
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { PickerTier } from "../lib/launcher-models";
import type { ModelBinding } from "../lib/model-config";
import type { PanelConfig } from "../lib/model-panel";
import { formChoice, newAgentPanel, newAgentPill, type ModelChoice } from "../lib/new-agent-panel";
import { forgetModel, modelList, readModels, useModelLists } from "../lib/session-models";
import type { Launcher } from "../lib/types";
import { toast } from "../ui/atoms";
import { ModelPanel } from "../ui/model-panel/ModelPanel";
import { ModelPill } from "./chat/ModelPill";

export interface NewAgentModelProps {
  mid: string;
  launchers: Launcher[];
  /** This computer's launchers are read (the panel opens only then). */
  ready: boolean;
  choice: ModelChoice;
  /** The launcher the start runs on: the one picked, else the one Model settings › New agent runs; "": none known. */
  runs: string;
  /** Model settings › New agent as it applies now; null: not followed. */
  configured: ModelBinding | null;
  tiers: PickerTier[];
  /** The "Follow Model settings" row; absent without model settings to follow (an older hub). */
  follow?: { active: boolean; detail?: string; warning?: string; label: string };
  /** What the pill says while no launcher is known to run (the model settings run something else, or none is read). */
  fallback: string;
  /** Claude Code's ultracode switch for this start (#3428): in the chosen model's flyout. */
  ultracode?: ReactNode;
  /** The workflow mark on the pill: ultracode is on. */
  ultracodeOn?: boolean;
  onChoice: (choice: ModelChoice) => void;
  onPreset: (name: string) => void;
  onFollow: () => void;
  /** "How to fix" of a launcher that failed its check here (#1643). */
  onHelp: (launcher: string) => void;
  /** "Configure models": the dialog closes, Settings › Model providers opens. */
  onConfigure: () => void;
}

export function NewAgentModel({ mid, launchers, ready, choice, runs, configured, tiers, follow, fallback, ultracode, ultracodeOn, onChoice, onPreset, onFollow, onHelp, onConfigure }: NewAgentModelProps) {
  const version = useModelLists();
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const close = useCallback(() => setAnchor(null), []);
  const shown = Boolean(anchor) && ready;
  // every launcher's list again behind the ones shown, once per opening, the one that runs first (at most three at a
  // time: each may start its agent's CLI on the computer)
  const names = launchers.map((l) => l.name).join("\n");
  useEffect(() => {
    if (!shown) return;
    for (const name of names.split("\n")) if (name) void readModels(mid, name, { again: true, first: name === runs });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per opening, for the computer it opened on
  }, [shown, mid, names]);
  // a different computer closes it (its providers are another computer's)
  const opened = useRef(mid);
  useEffect(() => {
    if (opened.current !== mid) setAnchor(null);
    opened.current = mid;
  }, [mid]);

  const { providers, state, presets } = useMemo(
    () => newAgentPanel({ launchers, listOf: (name) => modelList(mid, name), choice, configured, runs, tiers }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `version` stands for what was read
    [launchers, mid, choice.launcher, choice.model, choice.effort, configured, runs, tiers, version],
  );
  const pill = newAgentPill({ launchers, runs, list: runs ? modelList(mid, runs) : {}, choice, configured, fallback });

  const onPick = async (config: PanelConfig) => {
    onChoice(formChoice(config, modelList(mid, config.provider).list));
    return true;
  };
  const onApplyCurrent = async (patch: { effort?: string; fast?: boolean }) => {
    if (patch.effort !== undefined) onChoice({ ...choice, effort: patch.effort });
    return true;
  };
  const onForget = (provider: string, model: string) => {
    if (provider === choice.launcher && model === choice.model) onChoice({ ...choice, model: "" });
    void forgetModel(mid, provider, model).then((error) => error && toast(error, "error"));
  };

  return (
    <>
      <ModelPill
        current={pill}
        loading={pill.loading || !ready}
        failed={pill.failed}
        open={Boolean(anchor)}
        disabled={!ready}
        onOpen={(el) => setAnchor((a) => (a ? null : el))}
        workflow={ultracodeOn ? {} : undefined}
      />
      {shown && anchor && (
        <ModelPanel
          anchor={anchor}
          onClose={close}
          providers={providers}
          state={state}
          scope="agent"
          onPick={onPick}
          onApplyCurrent={onApplyCurrent}
          onReload={(provider) => void readModels(mid, provider, { again: true, first: provider === runs })}
          onForget={onForget}
          currentExtra={ultracode}
          follow={follow && { ...follow, onFollow: () => (onFollow(), true) }}
          presets={presets}
          onPreset={(key) => (onPreset(key), true)}
          engines
          onHelp={(launcher) => {
            close();
            onHelp(launcher);
          }}
          onConfigure={onConfigure}
        />
      )}
    </>
  );
}
