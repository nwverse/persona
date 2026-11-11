// The composer's model pill (#5032, after Cindy's model selector, which the user asked this part of the chat to match;
// docs/web/web.md "Model pill and model panel", docs/design/DESIGN_SYSTEM.md 3.1): at the right of the composer's bottom
// row, before voice / Stop / Send, `[provider][model][engine][effort][Fast][⌄]`. It opens the unified model panel above
// it (ui/model-panel/ModelPanel.tsx), and so does `/model` Enter in the composer. The composer card is a container: under
// 600 px the pill is 148 px wide, under 520 px its text and marks are a step smaller, under 420 px it keeps only the
// provider's tile and the chevron (64 px). An agent that can switch neither its model nor its effort gets the same
// words as a label, not a button.
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type Ref } from "react";
import { t } from "../../i18n";
import { api, errorText, sessionPath } from "../../lib/api";
import { agentName } from "../../lib/agent-kind";
import type { PanelConfig } from "../../lib/model-panel";
import { onModelPicker, sessionLauncher } from "../../lib/model-switch";
import { effortTitle } from "../../lib/session-actions";
import {
  forgetModel, machineLauncherList, modelList, pickPlan, pillView, readForPanel, readModels, sessionEngine, sessionPanel, sessionSwitches, useModelLists, type ModeStep,
} from "../../lib/session-models";
import { useStore } from "../../lib/store";
import { CODEX_ULTRA, ultracodeOffered, ultracodeOn } from "../../lib/ultracode";
import type { Session } from "../../lib/types";
import { AgentMark } from "../../ui/AgentMark";
import { cx, Spinner, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ProviderMark } from "../../ui/model-panel/ProviderMark";
import { lazyOverlay } from "../LazyOverlay";
import { useModeSet } from "./SessionDialogs";
import { UltracodeSwitchRow, UltraMark } from "./Ultracode";

// the panel loads on the pill's first tap, not with every chat's composer
const ModelPanel = lazyOverlay(() => import("../../ui/model-panel/ModelPanel").then((m) => m.ModelPanel));

/** One setup as the pill draws it. */
export interface PillSetup {
  /** The engine (AgentMark ids: claude, codex…). */
  engine: string;
  /** The provider (launcher) name: its tile, and the tooltip. */
  provider: string;
  /** The model's name; absent while not known. */
  model?: string;
  effort?: string;
  fast?: boolean;
}

/** The effort as the pill says it: English shortens "Very high" to "Extra" here only (the tooltip and the panel keep the
 *  full name); other languages say it in full. */
export function pillEffort(id: string): string {
  return id === "xhigh" ? t("chat-modelpill.effort_xhigh_short") : effortTitle(id);
}

// the composer card's widths (Composer.tsx `@container/composer`): narrow < 600, dense < 520, ultra < 420
const ULTRA_HIDDEN = "@max-[420px]/composer:hidden";

export function ModelPill({
  current,
  pending,
  loading,
  failed,
  disabled,
  open,
  onOpen,
  workflow,
  buttonRef,
}: {
  current: PillSetup;
  /** A switch accepted for the next message (#5035): a clock first, the target's engine named, the tooltip both. */
  pending?: PillSetup | null;
  /** The model list is read for the first time. */
  loading?: boolean;
  /** Reading the model list failed. */
  failed?: boolean;
  /** A switch is on its way: dimmed, takes no clicks. */
  disabled?: boolean;
  open?: boolean;
  /** Opens (or closes) the panel at the pill; absent: the pill is a label. */
  onOpen?: (anchor: HTMLElement) => void;
  /** The workflow mark after the effort (ultracode on, a Codex thread on its "ultra" effort; #3428). */
  workflow?: { label?: string; title?: string };
  buttonRef?: Ref<HTMLButtonElement>;
}) {
  const shown = pending ?? current;
  const unknown = !shown.model;
  const name = shown.model ?? (loading ? t("chat-modelpill.loading") : failed ? t("chat-modelpill.failed") : t("chat-modelpill.default_model"));
  const describe = (x: PillSetup) =>
    [agentName(x.engine), x.model ?? name, x.provider, x.effort && effortTitle(x.effort), x.fast && t("chat-sessiondialogs.speed_fast")].filter(Boolean).join(" · ");
  const title = pending
    ? t("chat-modelpill.current_and_next", { current: describe(current), next: describe(pending) })
    : [describe(current), failed && !unknown ? t("chat-modelpill.failed") : ""].filter(Boolean).join(" · ");
  const effort = shown.effort ? effortTitle(shown.effort) : "";
  const label = pending
    ? t(effort ? "chat-modelpill.aria_pending_effort" : "chat-modelpill.aria_pending", { agent: agentName(pending.engine), model: name, effort })
    : t(effort ? "chat-modelpill.aria_effort" : "chat-modelpill.aria", { model: name, effort });

  const body: ReactNode = (
    <>
      {pending && <Icon name="clock" stroke={2} className="size-[12px] shrink-0 text-ink-3 @max-[520px]/composer:size-[11px]" />}
      {unknown && loading && <Spinner size={13} />}
      {unknown && failed && !loading && <Icon name="warning" stroke={2} className="size-[13px] shrink-0 text-red @max-[520px]/composer:size-[12px]" />}
      <ProviderMark name={shown.provider} className={cx("mr-1.5", failed && !unknown ? "text-red" : "text-ink-2")} />
      {pending && (
        <>
          <span className="shrink-0 text-ink-3 @max-[600px]/composer:hidden">{agentName(pending.engine)}</span>
          <span aria-hidden className="shrink-0 text-ink-3 @max-[600px]/composer:hidden">·</span>
        </>
      )}
      {/* the name gives way first (a far larger shrink than the effort's): narrow, the effort truncates only once the
          name has no room left */}
      <span title={name} className={cx("min-w-0 shrink-[1000] truncate text-ink @max-[600px]/composer:max-w-[108px]", ULTRA_HIDDEN)}>{name}</span>
      {/* the engine's mark is the separator before the effort: no "·" */}
      <span aria-hidden data-pill-engine={shown.engine} className={cx("ml-1 flex shrink-0 items-center @max-[520px]/composer:scale-[0.917]", ULTRA_HIDDEN)}>
        <AgentMark agent={shown.engine} size={12} decorative className="text-ink-2" />
      </span>
      {shown.effort && (
        <span title={effort} data-pill-effort className={cx("shrink-0 whitespace-nowrap text-ink @max-[600px]/composer:min-w-0 @max-[600px]/composer:shrink @max-[600px]/composer:truncate", ULTRA_HIDDEN)}>
          {pillEffort(shown.effort)}
        </span>
      )}
      {shown.fast && <Icon name="bolt" stroke={2} className={cx("ml-0.5 size-[13px] shrink-0 fill-current text-ink-2 @max-[520px]/composer:size-[12px]", ULTRA_HIDDEN)} />}
      {workflow && !pending && <UltraMark size={12} label={workflow.label} title={workflow.title} className={cx("ml-0.5", ULTRA_HIDDEN)} />}
      {!unknown && loading && <span className="ml-0.5 flex shrink-0"><Spinner size={12} /></span>}
      {!unknown && failed && !loading && <Icon name="warning" stroke={2} className="ml-0.5 size-[12px] shrink-0 text-red @max-[520px]/composer:size-[11px]" />}
      {onOpen && <Icon name="chevron" stroke={2} className="size-[14px] shrink-0 text-ink-2 @max-[520px]/composer:size-[13px]" />}
    </>
  );
  const shell = cx(
    // Cindy's bare toolbar pill: no frame at rest, the capsule shows on hover and while its panel is open
    "flex h-[30px] max-w-[min(320px,100%)] min-w-[72px] shrink items-center rounded-full border border-transparent px-2.5 text-[13px] leading-none whitespace-nowrap transition-colors duration-150",
    "@max-[600px]/composer:w-[148px] @max-[520px]/composer:text-[12px] @max-[520px]/composer:[--provider-mark:14.2px] @max-[420px]/composer:w-[64px] @max-[420px]/composer:min-w-[64px]",
  );
  // the clip is inside, so the touch target (touch-hit) can reach past the pill's 30 px
  const inner = "flex min-w-0 flex-1 items-center gap-1 overflow-hidden";
  if (!onOpen) {
    return (
      <span data-model-pill="label" title={title} className={shell}>
        <span className="sr-only">{label}</span>
        <span aria-hidden className={inner}>{body}</span>
      </span>
    );
  }
  return (
    <button
      ref={buttonRef}
      type="button"
      data-model-pill={pending ? "pending" : failed ? "failed" : unknown && loading ? "loading" : "ready"}
      aria-haspopup="listbox"
      aria-expanded={Boolean(open) && !disabled}
      aria-label={label}
      title={title}
      disabled={disabled}
      // the caret stays in the message box (Tab still focuses the pill)
      onMouseDown={(e) => e.preventDefault()}
      onClick={(e) => onOpen(e.currentTarget)}
      className={cx(
        shell,
        "touch-hit hover:border-line-strong hover:bg-hover focus-visible:rounded-full! aria-expanded:border-line-strong aria-expanded:bg-hover",
        disabled && "pointer-events-none opacity-50",
      )}
    >
      <span className={inner}>{body}</span>
    </button>
  );
}

/** POST …/modes {service_tier}: Codex's speed. The session's push after the agent took it owns what is shown (Codex speed
 *  controls, DESIGN_SYSTEM.md), so nothing is drawn ahead of it. */
async function setSpeed(s: Session, tier: string): Promise<boolean> {
  try {
    await api(`${sessionPath(s.machine_id, s.session_id)}/modes`, { method: "POST", json: { service_tier: tier } });
    return true;
  } catch (e) {
    toast(t("chat-sessiondialogs.change_failed", { error: errorText(e) }), "error");
    return false;
  }
}

/** An Agent's pill: what the session runs, and its panel. Loads the session's own model list once, for the name its list
 *  gives the model and to say when that list can't be read. */
export const AgentModelPill = memo(function AgentModelPill({ session }: { session: Session }) {
  useModelLists();
  const mid = session.machine_id;
  const own = sessionLauncher(session);
  const switches = sessionSwitches(session);
  const opens = switches.model || switches.effort;
  useEffect(() => {
    if (opens) void readModels(mid, own, { first: true });
  }, [opens, mid, own]);
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const button = useRef<HTMLButtonElement>(null);
  const key = `${mid}/${session.session_id}`;
  // `/model` Enter in the composer opens it where the agent switches models (the composer sends the text otherwise)
  useEffect(() => (switches.model ? onModelPicker(key, () => setAnchor(button.current)) : undefined), [switches.model, key]);
  useEffect(() => {
    if (!opens) setAnchor(null);
  }, [opens]);
  const close = useCallback(() => setAnchor(null), []);

  const view = pillView(session, modelList(mid, own));
  if (!opens && !view.model) return null;
  const engine = sessionEngine(session);
  const workflow = ultracodeOn(session) ? {} : engine === "codex" && session.effort === CODEX_ULTRA ? { label: t("ultracode.codex_ultra_title"), title: t("ultracode.codex_ultra_title") } : undefined;
  return (
    <>
      <ModelPill
        current={view}
        loading={view.loading}
        failed={view.failed}
        open={Boolean(anchor)}
        onOpen={opens ? (el) => setAnchor((a) => (a ? null : el)) : undefined}
        workflow={workflow}
        buttonRef={button}
      />
      {anchor && opens && <SessionModelPanel session={session} anchor={anchor} onClose={close} />}
    </>
  );
});

/** The panel for an Agent's chat: its computer's launchers as providers (lib/session-models.ts), picks applied through
 *  POST …/modes one at a time (the model, then the effort, then the speed), each drawn at once and put back if it fails. */
function SessionModelPanel({ session, anchor, onClose }: { session: Session; anchor: HTMLElement; onClose: () => void }) {
  const version = useModelLists();
  const { set, latest } = useModeSet(session);
  const mid = session.machine_id;
  const own = sessionLauncher(session);
  const ultracode = useStore((s) => ultracodeOffered(session, s.features));
  // every list again behind the one shown, once per opening
  useEffect(() => readForPanel(latest.current), [latest, mid, own]);
  const launchers = machineLauncherList(mid);
  // eslint-disable-next-line react-hooks/exhaustive-deps -- `version` stands for what was read
  const { providers, state } = useMemo(() => sessionPanel(session, launchers, (name) => modelList(mid, name)), [session, launchers, mid, version]);

  const apply = useCallback(async (steps: ModeStep[]) => {
    for (const step of steps) {
      const ok = "service_tier" in step ? await setSpeed(latest.current, step.service_tier) : await set(step);
      if (!ok) return false;
    }
    return true;
  }, [latest, set]);
  const onPick = useCallback(async (config: PanelConfig) => {
    const steps = pickPlan(latest.current, config, modelList(mid, own).list);
    return steps ? apply(steps) : false;
  }, [apply, latest, mid, own]);
  const onApplyCurrent = useCallback(async (patch: { effort?: string; fast?: boolean }) => {
    const s = latest.current;
    const steps: ModeStep[] = [];
    if (patch.effort && patch.effort !== s.effort) steps.push({ effort: patch.effort });
    if (patch.fast !== undefined) steps.push({ service_tier: patch.fast ? "priority" : "default" });
    return apply(steps);
  }, [apply, latest]);
  const onReload = useCallback((provider: string) => void readModels(mid, provider, { again: true, first: provider === own }), [mid, own]);
  const onForget = useCallback((provider: string, model: string) => {
    void forgetModel(mid, provider, model).then((error) => error && toast(error, "error"));
  }, [mid]);

  return (
    <ModelPanel
      anchor={anchor}
      onClose={onClose}
      providers={providers}
      state={state}
      scope="agent"
      onPick={onPick}
      onApplyCurrent={onApplyCurrent}
      onReload={onReload}
      onForget={onForget}
      currentExtra={ultracode ? <UltracodeSwitchRow session={session} /> : undefined}
    />
  );
}
