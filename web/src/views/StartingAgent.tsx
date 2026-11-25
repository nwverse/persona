import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { shortPath } from "../lib/format";
import { deliverLaunch, type AgentLaunch } from "../lib/new-agent";
import { namedLauncher, providerIdNote, providerLabel, providerTitle, useProviderNames, type Named } from "../lib/provider-names";
import { navigate, type Route } from "../lib/router";
import { useStore, putLaunch } from "../lib/store";
import { releaseAttachments, type LocalAttachment } from "../lib/attachments";
import { AttachmentTray } from "../ui/AttachmentTray";
import { Button, Loading } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { t } from "../i18n";

// the question the start's terminal tab stopped on, answered from here (#10365): only for such a failure
const ShellQuestion = lazy(() => import("./ShellQuestionCard"));

export interface StartingAgentDeps {
  /** Optional scope partition (value or getter); switching it resets controlled/uncontrolled edit and retry state and fences stale callbacks. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled machine display name or reader (bypasses `useStore` when supplied). */
  machineName?: string | ((machineId: string) => string | undefined);
  /** Subscribe to machine name changes when `machineName` is a getter. */
  subscribeMachine?: (listener: () => void) => () => void;
  /** Controlled provider names list or getter (bypasses `useProviderNames` and `/model-config`). */
  providerNames?: readonly Named[] | (() => readonly Named[]);
  /** Subscribe to provider names updates when `providerNames` is a getter. */
  subscribeProviderNames?: (listener: () => void) => () => void;
  /** Read the latest launch snapshot right before retry or attachment edits. */
  getLaunch?: (id: string) => AgentLaunch | undefined;
  /** Persist an updated launch before retrying or removing an attachment (defaults to `store.putLaunch`). */
  putLaunch?: (launch: AgentLaunch) => void;
  /** Deliver or retry a launch (defaults to `new-agent.deliverLaunch`). */
  deliverLaunch?: (id: string, clearDraft?: string) => void | Promise<unknown>;
  /** Release preview URLs for removed attachments (defaults to `attachments.releaseAttachments`). */
  releaseAttachments?: (attachments: LocalAttachment[]) => void;
  /** Navigate to the started session or list (defaults to `router.navigate`). */
  navigate?: (route: Route, replace?: boolean) => void;
  /** Confirm retrying an uncertain launch (defaults to `window.confirm`). */
  confirm?: (message: string) => boolean | Promise<boolean>;
  /** Custom shell-question slot when `launch.shellWaiting` is set (bypasses lazy `ShellQuestionCard`). */
  renderShellQuestion?: (launch: AgentLaunch) => ReactNode;
  /** Optional controlled retry draft fields and busy state. */
  text?: string;
  onTextChange?: (text: string) => void;
  launcher?: string;
  onLauncherChange?: (launcher: string) => void;
  retrying?: boolean;
  onRetryingChange?: (retrying: boolean) => void;
}

export interface StartingAgentProps {
  launch: AgentLaunch;
  onToggleSidebar?: () => void;
  deps?: StartingAgentDeps;
}

export interface StartingAgentViewProps {
  launch: AgentLaunch;
  subtitleText: string;
  subtitleTooltip?: string;
  editable: boolean;
  text: string;
  launcher: string;
  loadingLabel: string;
  retrying?: boolean;
  shellQuestion?: ReactNode;
  onToggleSidebar?: () => void;
  onTextChange: (text: string) => void;
  onLauncherChange: (launcher: string) => void;
  onRemoveAttachment?: (id: string) => void;
  onRetry: () => void;
  onViewSession: () => void;
  onViewList: () => void;
}

/** Pure presentational view for the agent startup / failed-launch recovery screen. */
export function StartingAgentView({
  launch,
  subtitleText,
  subtitleTooltip,
  editable,
  text,
  launcher,
  loadingLabel,
  retrying = false,
  shellQuestion,
  onToggleSidebar,
  onTextChange,
  onLauncherChange,
  onRemoveAttachment,
  onRetry,
  onViewSession,
  onViewList,
}: StartingAgentViewProps) {
  return (
    <div className="flex h-full min-w-0 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line bg-page/85 px-3 backdrop-blur-md sm:px-4">
        {onToggleSidebar && <button type="button" aria-label={t("startingagent.sidebar")} onClick={onToggleSidebar} className="flex size-7 items-center justify-center rounded-control text-ink-3 hover:bg-hover lg:hidden"><Icon name="sidebar" /></button>}
        <div className="min-w-0 flex-1"><h1 className="truncate text-[14px] font-semibold text-ink">{t("startingagent.title")}</h1><p className="truncate text-[11.5px] text-ink-3" title={subtitleTooltip}>{subtitleText}</p></div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[760px] flex-col gap-7 px-5 pt-6 pb-10 sm:px-8">
          {(launch.text || launch.attachments.length > 0) && <div className="flex flex-col items-end gap-1.5 pl-[12%]">
            <AttachmentTray attachments={launch.attachments} onRemove={editable ? onRemoveAttachment : undefined} />
            {launch.text && <div className="max-w-full rounded-[14px] bg-user-bubble px-3.5 py-2 text-[14px] leading-[1.55] whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{launch.text}</div>}
          </div>}
          {launch.phase === "failed" ? <div role="alert" className="flex flex-col items-start gap-3">
            <p className="text-[13px] text-red">{launch.error}</p>
            {shellQuestion}
            <p className="text-[12px] text-ink-3">{launch.uncertain ? t("startingagent.uncertain") : t("startingagent.kept")}</p>
            {editable && <div className="flex w-full flex-col gap-2">
              <label className="text-[12px] text-ink-2">{t("startingagent.launcher_label")}<input value={launcher} onChange={(e) => onLauncherChange(e.target.value)} className="mt-1 w-full rounded-control bg-inset px-3 py-2 font-mono text-[12px] text-ink shadow-hairline outline-none" /></label>
              <label className="text-[12px] text-ink-2">{t("startingagent.first_message")}<textarea value={text} onChange={(e) => onTextChange(e.target.value)} rows={3} className="mt-1 w-full resize-none rounded-control bg-inset px-3 py-2 text-[13px] text-ink shadow-hairline outline-none" /></label>
            </div>}
            {launch.draft && <pre className="max-h-40 max-w-full overflow-auto rounded-control bg-inset px-3 py-2 text-[12px] whitespace-pre-wrap text-ink-2">{launch.draft}</pre>}
            <div className="flex flex-wrap gap-2">
              <Button icon="retry" disabled={retrying || undefined} onClick={onRetry}>{launch.clearable !== undefined ? t("startingagent.clear_and_send") : t("startingagent.retry")}</Button>
              {launch.session && <Button tone="quiet" onClick={onViewSession}>{t("startingagent.view_session")}</Button>}
              {!launch.session && <Button tone="quiet" onClick={onViewList}>{t("startingagent.view_list")}</Button>}
            </div>
          </div> : <div className="flex flex-col gap-2"><Loading label={loadingLabel} /><p className="text-[12px] text-ink-3">{t("startingagent.background")}</p></div>}
        </div>
      </div>
    </div>
  );
}

function useTrackedString(subscribe: (listener: () => void) => () => void, read: () => string) {
  const stateRef = useRef<{ value: string; version: number } | null>(null);
  const sub = useCallback((listener: () => void) => subscribe(() => {
    const next = read();
    if (!stateRef.current || next !== stateRef.current.value) {
      stateRef.current = { value: next, version: (stateRef.current?.version ?? 0) + 1 };
    }
    listener();
  }), [subscribe, read]);
  const getSnapshot = useCallback(() => {
    const next = read();
    if (!stateRef.current) {
      stateRef.current = { value: next, version: 0 };
    } else if (next !== stateRef.current.value) {
      stateRef.current = { value: next, version: stateRef.current.version + 1 };
    }
    return stateRef.current;
  }, [read]);
  const snap = useSyncExternalStore(sub, getSnapshot, getSnapshot);
  const readToken = useCallback(() => {
    const cur = read();
    const s = stateRef.current;
    return s && s.value === cur ? `${s.version}:${cur}` : `drift:${cur}`;
  }, [read]);
  return { token: `${snap.version}:${snap.value}`, readToken };
}

export function StartingAgent({ launch, onToggleSidebar, deps }: StartingAgentProps) {
  if (deps?.subscribeScopeKey && typeof deps.scopeKey === "function") {
    return <SubscribedScopeBridge launch={launch} onToggleSidebar={onToggleSidebar} deps={deps} readScope={deps.scopeKey} subscribe={deps.subscribeScopeKey} />;
  }
  const scopeDep = deps?.scopeKey;
  const readScopeToken = () => (typeof scopeDep === "function" ? scopeDep() : (scopeDep ?? ""));
  const scopeToken = readScopeToken();
  return <MachineBridge launch={launch} onToggleSidebar={onToggleSidebar} deps={deps} scopeToken={scopeToken} readScopeToken={readScopeToken} />;
}

function SubscribedScopeBridge({
  launch, onToggleSidebar, deps, readScope, subscribe,
}: StartingAgentProps & { readScope: () => string; subscribe: (listener: () => void) => () => void }) {
  const tracked = useTrackedString(subscribe, readScope);
  return <MachineBridge launch={launch} onToggleSidebar={onToggleSidebar} deps={deps} scopeToken={tracked.token} readScopeToken={tracked.readToken} />;
}

interface ScopeResolvedProps extends StartingAgentProps {
  scopeToken: string;
  readScopeToken: () => string;
}

function MachineBridge(props: ScopeResolvedProps) {
  const { deps, launch } = props;
  if (deps?.machineName !== undefined) {
    if (deps.subscribeMachine && typeof deps.machineName === "function") {
      return <SubscribedMachineBridge {...props} readMachineName={deps.machineName} subscribe={deps.subscribeMachine} />;
    }
    const machineName = typeof deps.machineName === "function" ? deps.machineName(launch.machineId) : deps.machineName;
    return <ProviderNamesBridge {...props} machineName={machineName} />;
  }
  return <DefaultMachineBridge {...props} />;
}

function SubscribedMachineBridge(
  props: ScopeResolvedProps & { readMachineName: (machineId: string) => string | undefined; subscribe: (listener: () => void) => () => void },
) {
  const { launch, readMachineName, subscribe } = props;
  const getSnapshot = useCallback(() => readMachineName(launch.machineId), [readMachineName, launch.machineId]);
  const machineName = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return <ProviderNamesBridge {...props} machineName={machineName} />;
}

function DefaultMachineBridge(props: ScopeResolvedProps) {
  const machineName = useStore((s) => s.machines.find((m) => m.id === props.launch.machineId)?.name);
  return <ProviderNamesBridge {...props} machineName={machineName} />;
}

interface MachineResolvedProps extends ScopeResolvedProps {
  machineName?: string;
}

function ProviderNamesBridge(props: MachineResolvedProps) {
  const { deps } = props;
  if (deps?.providerNames !== undefined) {
    if (deps.subscribeProviderNames && typeof deps.providerNames === "function") {
      return <SubscribedProviderNamesBridge {...props} readProviderNames={deps.providerNames} subscribe={deps.subscribeProviderNames} />;
    }
    const providerNames = typeof deps.providerNames === "function" ? deps.providerNames() : deps.providerNames;
    return <StartingAgentBody {...props} providerNames={providerNames} />;
  }
  return <DefaultProviderNamesBridge {...props} />;
}

function SubscribedProviderNamesBridge(
  props: MachineResolvedProps & { readProviderNames: () => readonly Named[]; subscribe: (listener: () => void) => () => void },
) {
  const providerNames = useSyncExternalStore(props.subscribe, props.readProviderNames, props.readProviderNames);
  return <StartingAgentBody {...props} providerNames={providerNames} />;
}

function DefaultProviderNamesBridge(props: MachineResolvedProps) {
  const providerNames = useProviderNames();
  return <StartingAgentBody {...props} providerNames={providerNames} />;
}

interface BodyProps extends MachineResolvedProps {
  providerNames: readonly Named[];
}

function StartingAgentBody({ launch, onToggleSidebar, deps, scopeToken, readScopeToken, machineName, providerNames }: BodyProps) {
  const scopeIdentity = JSON.stringify([scopeToken, launch.id, launch.machineId]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: scopeIdentity, epoch: 1 }));
  const [localText, setLocalText] = useState(launch.text);
  const [localLauncher, setLocalLauncher] = useState(launch.launcher);
  const [localRetrying, setLocalRetrying] = useState(false);

  if (scope.key !== scopeIdentity) {
    setScope({ key: scopeIdentity, epoch: scope.epoch + 1 });
    setLocalText(launch.text);
    setLocalLauncher(launch.launcher);
    setLocalRetrying(false);
  }

  const text = deps?.text !== undefined ? deps.text : localText;
  const launcher = deps?.launcher !== undefined ? deps.launcher : localLauncher;
  const retrying = deps?.retrying !== undefined ? deps.retrying : localRetrying;

  const depsRef = useRef(deps);
  const launchRef = useRef(launch);
  const textRef = useRef(text);
  const launcherRef = useRef(launcher);
  const readScopeTokenRef = useRef(readScopeToken);
  const activeEpochRef = useRef(scope.epoch);
  const lastResetEpoch = useRef(scope.epoch);
  const retrySeqRef = useRef(0);
  const pendingRetryRef = useRef<number | null>(null);

  useLayoutEffect(() => {
    depsRef.current = deps;
    launchRef.current = launch;
    textRef.current = text;
    launcherRef.current = launcher;
    readScopeTokenRef.current = readScopeToken;
  });

  const setTextState = (next: string) => {
    textRef.current = next;
    setLocalText(next);
    depsRef.current?.onTextChange?.(next);
  };
  const setLauncherState = (next: string) => {
    launcherRef.current = next;
    setLocalLauncher(next);
    depsRef.current?.onLauncherChange?.(next);
  };
  const setRetryingState = (next: boolean) => {
    setLocalRetrying(next);
    depsRef.current?.onRetryingChange?.(next);
  };

  useLayoutEffect(() => {
    activeEpochRef.current = scope.epoch;
    if (lastResetEpoch.current !== scope.epoch) {
      lastResetEpoch.current = scope.epoch;
      pendingRetryRef.current = null;
      retrySeqRef.current += 1;
      setTextState(launchRef.current.text);
      setLauncherState(launchRef.current.launcher);
      setRetryingState(false);
    }
    return () => {
      if (activeEpochRef.current === scope.epoch) activeEpochRef.current = 0;
      pendingRetryRef.current = null;
      retrySeqRef.current += 1;
    };
  }, [scope.epoch]);

  useEffect(() => {
    if (
      launch.phase === "ready" && launch.session
      && activeEpochRef.current === scope.epoch && readScopeTokenRef.current() === scopeToken
    ) {
      (depsRef.current?.navigate ?? navigate)({ name: "session", mid: launch.machineId, sid: launch.session.session_id }, true);
    }
  }, [launch, scope.epoch, scopeToken]);

  const isScopeCurrent = (capturedEpoch: number, capturedScope: string, id: string, machineId: string) =>
    activeEpochRef.current === capturedEpoch
    && readScopeTokenRef.current() === capturedScope
    && launchRef.current.id === id
    && launchRef.current.machineId === machineId;

  const liveLaunchEligible = (expected: AgentLaunch): AgentLaunch | null => {
    const latest = depsRef.current?.getLaunch ? depsRef.current.getLaunch(expected.id) : launchRef.current;
    if (!latest || latest.id !== expected.id || latest.machineId !== expected.machineId || latest.phase !== "failed") return null;
    return latest;
  };

  // the launcher by the provider's display name; the line's tooltip says it with its startup ID (#5029)
  const shownLauncher = launch.launcher || launch.displayLauncher || "";
  const shownModel = launch.model || launch.displayModel;
  const runsOn = namedLauncher(shownLauncher, providerNames);
  const subtitle = (name: string) => [machineName ?? launch.machineId, shortPath(launch.cwd), name, shownModel].filter(Boolean).join(" · ");
  const editable = launch.phase === "failed" && !launch.session && !launch.uncertain && !launch.repeatOnly;

  const retry = () => {
    const capturedEpoch = scope.epoch;
    const capturedScope = scopeToken;
    if (retrying || pendingRetryRef.current !== null || !isScopeCurrent(capturedEpoch, capturedScope, launch.id, launch.machineId)) return;
    const currentLaunch = liveLaunchEligible(launch);
    if (!currentLaunch) return;

    const finish = (seq: number) => {
      if (pendingRetryRef.current === seq) {
        pendingRetryRef.current = null;
        if (activeEpochRef.current === capturedEpoch) setRetryingState(false);
      }
    };

    const dispatch = (seq: number) => {
      if (pendingRetryRef.current !== seq || retrySeqRef.current !== seq || !isScopeCurrent(capturedEpoch, capturedScope, currentLaunch.id, currentLaunch.machineId)) {
        finish(seq);
        return;
      }
      const live = liveLaunchEligible(currentLaunch);
      if (!live) {
        finish(seq);
        return;
      }
      const liveEditable = live.phase === "failed" && !live.session && !live.uncertain && !live.repeatOnly;
      const currentLauncher = launcherRef.current;
      const currentText = textRef.current;
      // the model and effort were picked for the launcher it had: another launcher starts with its own
      const other = currentLauncher.trim() !== live.launcher;
      const retireTextStart = live.agentType === "traex" && live.inlinePrompt && (other || !currentText.trim());
      if (liveEditable) {
        (depsRef.current?.putLaunch ?? putLaunch)({
          ...live,
          text: currentText.trim(),
          launcher: currentLauncher.trim(),
          outgoing: undefined,
          ...(other && { model: undefined, effort: undefined, displayLauncher: undefined, displayEngine: undefined, displayModel: undefined }),
          ...(retireTextStart && { agentType: undefined, inlinePrompt: undefined }),
        });
      }
      let delivery: void | Promise<unknown>;
      try {
        delivery = (depsRef.current?.deliverLaunch ?? deliverLaunch)(live.id, live.clearable);
      } catch {
        finish(seq);
        return;
      }
      if (delivery && typeof (delivery as PromiseLike<unknown>).then === "function") {
        void Promise.resolve(delivery).then(
          () => finish(seq),
          () => finish(seq),
        );
      } else {
        finish(seq);
      }
    };

    const seq = ++retrySeqRef.current;
    pendingRetryRef.current = seq;

    if (currentLaunch.uncertain) {
      const message = currentLaunch.session ? t("startingagent.resend_confirm") : t("startingagent.restart_confirm");
      const ask = depsRef.current?.confirm ?? ((msg: string) => confirm(msg));
      let answer: boolean | Promise<boolean>;
      try {
        answer = ask(message);
      } catch {
        finish(seq);
        return;
      }
      if (answer && typeof (answer as PromiseLike<boolean>).then === "function") {
        setRetryingState(true);
        void Promise.resolve(answer).then(
          (approved) => {
            if (!approved) {
              finish(seq);
              return;
            }
            dispatch(seq);
          },
          () => finish(seq),
        );
        return;
      }
      if (!answer) {
        finish(seq);
        return;
      }
    }

    setRetryingState(true);
    dispatch(seq);
  };

  const removeAttachment = (id: string) => {
    if (retrying || pendingRetryRef.current !== null || !isScopeCurrent(scope.epoch, scopeToken, launch.id, launch.machineId)) return;
    const live = liveLaunchEligible(launch);
    if (!live || live.session || live.uncertain || live.repeatOnly) return;
    (depsRef.current?.releaseAttachments ?? releaseAttachments)(live.attachments.filter((a) => a.id === id));
    (depsRef.current?.putLaunch ?? putLaunch)({ ...live, attachments: live.attachments.filter((a) => a.id !== id), outgoing: undefined });
  };

  const viewSession = () => {
    if (!isScopeCurrent(scope.epoch, scopeToken, launch.id, launch.machineId)) return;
    const live = depsRef.current?.getLaunch ? depsRef.current.getLaunch(launch.id) : launchRef.current;
    if (!live || live.id !== launch.id || live.machineId !== launch.machineId || !live.session) return;
    (depsRef.current?.navigate ?? navigate)({ name: "session", mid: live.machineId, sid: live.session.session_id });
  };

  const viewList = () => {
    if (!isScopeCurrent(scope.epoch, scopeToken, launch.id, launch.machineId)) return;
    (depsRef.current?.navigate ?? navigate)({ name: "home" });
  };

  const label = launch.phase === "creating" ? t("startingagent.creating") : launch.phase === "uploading" ? t("startingagent.uploading") : t("startingagent.sending");
  const shellQuestion = launch.phase === "failed" && launch.shellWaiting
    ? (deps?.renderShellQuestion ? deps.renderShellQuestion(launch) : <Suspense fallback={null}><ShellQuestion launch={launch} /></Suspense>)
    : undefined;

  return (
    <StartingAgentView
      launch={launch}
      subtitleText={subtitle(providerTitle(runsOn))}
      subtitleTooltip={providerIdNote(runsOn) ? subtitle(providerLabel(runsOn)) : undefined}
      editable={editable}
      text={text}
      launcher={launcher}
      loadingLabel={label}
      retrying={retrying}
      shellQuestion={shellQuestion}
      onToggleSidebar={onToggleSidebar}
      onTextChange={setTextState}
      onLauncherChange={setLauncherState}
      onRemoveAttachment={editable ? removeAttachment : undefined}
      onRetry={retry}
      onViewSession={viewSession}
      onViewList={viewList}
    />
  );
}
