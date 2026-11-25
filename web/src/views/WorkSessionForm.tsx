import { useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { t } from "../i18n";
import { agentName, providerAgent } from "../lib/agent-kind";
import { api, ApiError, credentialOwner, enc, errorText, pastHub, subscribeCredentialOwner } from "../lib/api";
import { pickAttachment, releaseAttachments, type LocalAttachment } from "../lib/attachments";
import { useScopedMachines } from "../lib/current-assistant";
import { shortPath } from "../lib/format";
import { launcherBlock } from "../lib/launcher-health";
import { machineLaunchers, modelName, type AgentModel, type MachineLaunchers } from "../lib/launcher-models";
import { hubLabel } from "../lib/mesh";
import { launchAgent } from "../lib/new-agent";
import { providerLabel } from "../lib/provider-names";
import { getState } from "../lib/store";
import type { Launcher, Machine } from "../lib/types";
import { AttachmentTray } from "../ui/AttachmentTray";
import { Button, Dialog, Spinner, toast } from "../ui/atoms";
import { useAddComputer } from "./machines/AddComputerEntry";

export interface WorkSessionDirs {
  path: string;
  parent?: string;
  entries: { name: string; path: string; is_git?: boolean }[];
  recent?: { path: string; name: string }[];
}

export interface WorkSessionStartOptions {
  agent_type: string;
  model_mode: "selectable" | "fixed" | "default";
  models?: AgentModel[];
  fixed_model?: string;
  prompt_required: boolean;
  inline_prompt: boolean;
  attachments: boolean;
}

export interface WorkSessionLaunchInput {
  machineId: string;
  cwd: string;
  launcher: string;
  agentType: string;
  workSession: true;
  model?: string;
  inlinePrompt: boolean;
  text: string;
  attachments: LocalAttachment[];
}

export interface WorkSessionAddComputer {
  open: () => void;
  dialog?: ReactNode;
}

export interface WorkSessionFormDeps {
  /** Optional scope partition (value or getter); switching it resets controlled/uncontrolled form state and fences in-flight loads. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled machines list or getter (bypasses `useScopedMachines` and default `store.getState().machines`). */
  machines?: readonly Machine[] | (() => readonly Machine[]);
  /** Subscribe to machine list updates when `machines` is a getter. */
  subscribeMachines?: (listener: () => void) => () => void;
  /** Read the live machine snapshot right before submission (defaults to controlled `machines` when supplied, else `store.getState().machines`). */
  getLiveMachine?: (machineId: string) => Machine | undefined;
  /** Controlled credential owner value or getter (bypasses `api.credentialOwner` and `subscribeCredentialOwner`). */
  credentialOwner?: string | (() => string);
  /** Subscribe to credential owner updates when `credentialOwner` is a getter. */
  subscribeCredentialOwner?: (listener: () => void) => () => void;
  /** Controlled "Add computer" handler/dialog (bypasses `useAddComputer`). */
  addComputer?: WorkSessionAddComputer | (() => void);
  /** Load directory entries for a machine and path (defaults to hub `/machines/:mid/dirs`). */
  loadDirs?: (machineId: string, path: string, signal: AbortSignal) => Promise<WorkSessionDirs>;
  /** Load launchers for a machine (defaults to `machineLaunchers`). */
  loadLaunchers?: (machineId: string, signal: AbortSignal) => Promise<Pick<MachineLaunchers, "launchers">>;
  /** Load start options for a machine and launcher (defaults to hub `/machines/:mid/launchers/:launcher/start-options`). */
  loadStartOptions?: (machineId: string, launcher: string, signal: AbortSignal) => Promise<WorkSessionStartOptions>;
  /** Launch the chosen work session (defaults to `launchAgent`). */
  launchAgent?: (input: WorkSessionLaunchInput) => void;
  /** Create a local attachment from a picked file (defaults to `pickAttachment`). */
  pickAttachment?: (file: File) => LocalAttachment;
  /** Release preview URLs for removed or discarded attachments (defaults to `releaseAttachments`). */
  releaseAttachments?: (attachments: LocalAttachment[]) => void;
  /** Format load or attachment errors (defaults to `workSessionError`). */
  formatError?: (error: unknown) => string;
  /** Show notifications (defaults to `atoms.toast`). */
  notify?: (message: string, tone: "ok" | "error" | "info") => void;
  /** Optional controlled form fields and change callbacks. */
  mid?: string;
  onMidChange?: (mid: string) => void;
  cwd?: string;
  onCwdChange?: (cwd: string) => void;
  browse?: string;
  onBrowseChange?: (browse: string) => void;
  harness?: string;
  onHarnessChange?: (harness: string) => void;
  launcher?: string;
  onLauncherChange?: (launcher: string) => void;
  model?: string;
  onModelChange?: (model: string) => void;
  prompt?: string;
  onPromptChange?: (prompt: string) => void;
  attachments?: LocalAttachment[];
  onAttachmentsChange?: (attachments: LocalAttachment[]) => void;
}

export interface WorkSessionFormProps {
  open: boolean;
  onClose: () => void;
  deps?: WorkSessionFormDeps;
}

export interface WorkSessionFormViewProps {
  open: boolean;
  machines: readonly Machine[];
  mid: string;
  machine?: Machine;
  offline: boolean;
  hasTarget: boolean;
  cwd: string;
  browse: string;
  listed?: WorkSessionDirs;
  dirsLoading: boolean;
  projectReady: boolean;
  launchersLoaded: boolean;
  groups: ReadonlyMap<string, readonly Launcher[]>;
  harness: string;
  launcher: string;
  matching: readonly Launcher[];
  validLauncher: boolean;
  optionsLoading: boolean;
  opts?: WorkSessionStartOptions;
  models: readonly AgentModel[];
  model: string;
  compatible: boolean;
  staleModel: boolean;
  canStart: boolean;
  error?: string;
  prompt: string;
  attachments: LocalAttachment[];
  addComputerDialog?: ReactNode;
  onClose: () => void;
  onStart: () => void;
  onChooseDevice: (id: string) => void;
  onAddComputer: () => void;
  onCwdChange: (cwd: string) => void;
  onBrowseChange: (path: string) => void;
  onChooseHarness: (agent: string) => void;
  onChooseLauncher: (name: string) => void;
  onModelChange: (model: string) => void;
  onRetry: () => void;
  onPromptChange: (prompt: string) => void;
  onAddFiles: (files: FileList) => void;
  onRemoveAttachment: (id: string) => void;
}

const input = "h-9 w-full min-w-0 rounded-control bg-inset px-3 text-[13px] text-ink shadow-hairline outline-none focus:shadow-[0_0_0_1px_var(--line-strong)] disabled:opacity-50";
const label = "flex min-w-0 flex-col gap-1.5 text-[12px] font-medium text-ink-2";

type Load<T> = { key: string; data?: T; error?: string };

function useTarget<T>(
  key: string,
  read: (signal: AbortSignal) => Promise<T>,
  isScopeCurrent: () => boolean,
  formatError: (error: unknown) => string,
): Load<T> | undefined {
  const [result, setResult] = useState<Load<T>>();
  const readRef = useRef(read);
  const isScopeCurrentRef = useRef(isScopeCurrent);
  const formatErrorRef = useRef(formatError);
  const seqRef = useRef(0);
  useLayoutEffect(() => {
    readRef.current = read;
    isScopeCurrentRef.current = isScopeCurrent;
    formatErrorRef.current = formatError;
  });
  useLayoutEffect(() => {
    const seq = ++seqRef.current;
    setResult(undefined);
    if (!key || !isScopeCurrentRef.current()) {
      return () => { seqRef.current += 1; };
    }
    const controller = new AbortController();
    const active = () => !controller.signal.aborted && seqRef.current === seq && isScopeCurrentRef.current();
    let pending: Promise<T>;
    try {
      pending = Promise.resolve(readRef.current(controller.signal));
    } catch (error) {
      if (active()) setResult({ key, error: formatErrorRef.current(error) });
      return () => {
        seqRef.current += 1;
        controller.abort();
      };
    }
    void pending.then(
      (data) => { if (active()) setResult({ key, data }); },
      (error) => { if (active()) setResult({ key, error: formatErrorRef.current(error) }); },
    );
    return () => {
      seqRef.current += 1;
      controller.abort();
    };
  }, [key]);
  return result?.key === key ? result : undefined;
}

/** What a launcher starts, as the hub's start options say it (agent_type). */
const agentType = (l: Launcher) => l.agent_type || l.agent || "claude";

function workSessionError(e: unknown) {
  if (e instanceof ApiError && e.code === "work_launcher_unavailable") return t("work-session.launcher_unavailable");
  return errorText(e);
}

const defaultLoadDirs = (mid: string, path: string, signal: AbortSignal) =>
  api<WorkSessionDirs>(`/machines/${enc(mid)}/dirs?path=${enc(path)}`, { signal });
const defaultLoadLaunchers = (mid: string, signal: AbortSignal) => machineLaunchers(mid, { signal });
const defaultLoadStartOptions = (mid: string, launcher: string, signal: AbortSignal) =>
  api<WorkSessionStartOptions>(`/machines/${enc(mid)}/launchers/${enc(launcher)}/start-options`, { signal, timeout: pastHub(85_000) });

/** Pure presentational view for the Work Session dialog. */
export function WorkSessionFormView({
  open,
  machines,
  mid,
  machine,
  offline,
  hasTarget,
  cwd,
  browse,
  listed,
  dirsLoading,
  projectReady,
  launchersLoaded,
  groups,
  harness,
  launcher,
  matching,
  validLauncher,
  optionsLoading,
  opts,
  models,
  model,
  compatible,
  staleModel,
  canStart,
  error,
  prompt,
  attachments,
  addComputerDialog,
  onClose,
  onStart,
  onChooseDevice,
  onAddComputer,
  onCwdChange,
  onBrowseChange,
  onChooseHarness,
  onChooseLauncher,
  onModelChange,
  onRetry,
  onPromptChange,
  onAddFiles,
  onRemoveAttachment,
}: WorkSessionFormViewProps) {
  const files = useRef<HTMLInputElement>(null);
  const entries = listed?.entries ?? [];
  const recent = !browse ? (listed?.recent ?? []).filter((d) => d.path !== listed?.path && !entries.some((e) => e.path === d.path)) : [];
  const visible = cwd === listed?.path || entries.some((d) => d.path === cwd) || recent.some((d) => d.path === cwd);

  return <>
    <Dialog open={open} onClose={onClose} title={t("personaassistant.start_work_session")} width={520} footer={<>
      <Button tone="quiet" onClick={onClose}>{t("common.cancel")}</Button>
      <Button tone="primary" disabled={!canStart} onClick={onStart}>{t("newagent.start")}</Button>
    </>}>
      <div className="flex flex-col gap-4">
        <label className={label}>{t("newagent.computer")}
          <select className={input} value={mid} onChange={(e) => onChooseDevice(e.target.value)}>
            <option value="">{t("work-session.choose")}</option>
            {mid && !machine && <option value={mid} disabled>{mid}</option>}
            {machines.map((m) => <option key={m.id} value={m.id} disabled={!m.online || m.trust === "pending"}>
              {[m.name, hubLabel(m, machines as Machine[]), !m.online ? t("machines.offline") : ""].filter(Boolean).join(" · ")}
            </option>)}
          </select>
        </label>
        {machines.length === 0 && <div><Button icon="plus" onClick={() => { onClose(); onAddComputer(); }}>{t("machines.add")}</Button></div>}
        {offline && <p role="status" className="text-[13px] text-orange">{t(machine?.trust === "pending" ? "work-session.unconfirmed" : "work-session.offline")}</p>}
        <div className="flex flex-col gap-1.5">
          <label className={label}>{t("work-session.project")}
            <select className={input} value={cwd} disabled={!hasTarget || !listed} onChange={(e) => onCwdChange(e.target.value)}>
              <option value="">{t("work-session.choose")}</option>
              {!visible && cwd && <option value={cwd}>{shortPath(cwd)}</option>}
              {listed?.path && <option value={listed.path}>{shortPath(listed.path)}</option>}
              {recent.length > 0 && <optgroup label={t("newagent.recent")}>{recent.map((d) => <option key={d.path} value={d.path}>{shortPath(d.path)}</option>)}</optgroup>}
              {entries.map((d) => <option key={d.path} value={d.path}>{d.name}{d.is_git ? " · Git" : ""}</option>)}
            </select>
          </label>
          {hasTarget && <div className="flex flex-wrap items-center gap-1">
            <Button tone="quiet" icon="chevronLeft" disabled={!listed?.parent || listed.parent === listed.path} onClick={() => onBrowseChange(listed!.parent!)}>{t("newagent.parent_folder")}</Button>
            <Button tone="quiet" icon="folder" disabled={!cwd || !projectReady || cwd === listed?.path} onClick={() => onBrowseChange(cwd)}>{t("work-session.browse")}</Button>
            {dirsLoading && <Spinner size={12} />}
          </div>}
        </div>
        <label className={label}>{t("work-session.harness")}
          <select className={input} value={harness} disabled={!projectReady || !groups.size} onChange={(e) => onChooseHarness(e.target.value)}>
            <option value="">{t("work-session.choose")}</option>
            {harness && !groups.has(harness) && <option value={harness} disabled>{agentName(harness)}</option>}
            {[...groups.keys()].map((agent) => <option value={agent} key={agent}>{agentName(agent)}</option>)}
          </select>
        </label>
        {projectReady && launchersLoaded && !groups.size && <p role="status" className="text-[13px] text-ink-3">{t("work-session.no_launchers")}</p>}
        <label className={label}>{t("work-session.launcher")}
          <select className={input} value={launcher} disabled={!projectReady || !matching.length} onChange={(e) => onChooseLauncher(e.target.value)}>
            <option value="">{t("work-session.choose")}</option>
            {launcher && !validLauncher && <option value={launcher} disabled>{launcher}</option>}
            {matching.map((l) => <option value={l.name} key={l.name}>{providerLabel(l)}</option>)}
          </select>
        </label>
        <label className={label}>{t("work-session.model")}
          <select aria-label={t("work-session.model")} className={input} value={model}
            disabled={!compatible || (opts?.model_mode !== "selectable" && !staleModel)} onChange={(e) => onModelChange(e.target.value)}>
            <option value="">{!opts ? t(optionsLoading ? "work-session.checking" : "work-session.choose")
              : opts.model_mode === "fixed" ? t("work-session.fixed", { model: opts.fixed_model ?? "" })
              : t("work-session.default")}</option>
            {model && !models.some((m) => m.id === model) && <option value={model} disabled>{model}</option>}
            {models.map((m) => <option key={m.id} value={m.id}>{modelName(m)}{m.resolved ? ` · ${m.resolved}` : ""}</option>)}
          </select>
          {opts?.model_mode === "default" && <span className="font-normal text-ink-3">{t("work-session.no_override")}</span>}
          {opts?.model_mode === "selectable" && <span className="font-normal text-ink-3">{t("work-session.one_start")}</span>}
          {staleModel && <span role="status" className="font-normal text-orange">{t("work-session.model_changed")}</span>}
        </label>
        {opts && !compatible && <p role="alert" className="text-[13px] text-red">{t("work-session.harness_changed")}</p>}
        {error && <div role="alert" className="flex flex-wrap items-center gap-2 text-[13px] text-red">
          <span>{error}</span><Button tone="quiet" icon="retry" onClick={onRetry}>{t("startingagent.retry")}</Button>
        </div>}
        <label className={label}>{t(opts?.prompt_required ? "newagent.first_message" : "newagent.first_message_optional")}
          <textarea aria-label={t("newagent.first_message")} value={prompt} rows={3}
            className="w-full resize-none rounded-control bg-inset px-3 py-2 text-[13px] font-normal text-ink shadow-hairline outline-none"
            onChange={(e) => onPromptChange(e.target.value)}
            onDragOver={(e) => { if (opts?.attachments && e.dataTransfer.types.includes("Files")) e.preventDefault(); }}
            onDrop={(e) => { if (opts?.attachments && e.dataTransfer.files.length) { e.preventDefault(); onAddFiles(e.dataTransfer.files); } }}
            onPaste={(e) => { if (opts?.attachments && e.clipboardData.files.length) { e.preventDefault(); onAddFiles(e.clipboardData.files); } }}
            onKeyDown={(e) => {
              if (!e.nativeEvent.isComposing && e.keyCode !== 229 && e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                onStart();
              }
            }} />
        </label>
        {attachments.length > 0 && <AttachmentTray attachments={attachments} onRemove={onRemoveAttachment} />}
        {opts?.attachments && <div>
          <input ref={files} type="file" multiple hidden onChange={(e) => { if (e.currentTarget.files) onAddFiles(e.currentTarget.files); e.currentTarget.value = ""; }} />
          <Button icon="plus" tone="quiet" onClick={() => files.current?.click()}>{t("newagent.add_files")}</Button>
        </div>}
        {opts && !opts.attachments && attachments.length > 0 && <p role="status" className="text-[13px] text-orange">{t("work-session.remove_attachments")}</p>}
      </div>
    </Dialog>
    {addComputerDialog}
  </>;
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
  return { value: snap.value, token: `${snap.version}:${snap.value}`, readToken };
}

export function WorkSessionForm({ open, onClose, deps }: WorkSessionFormProps) {
  if (deps?.subscribeScopeKey && typeof deps.scopeKey === "function") {
    return <SubscribedScopeBridge open={open} onClose={onClose} deps={deps} readScope={deps.scopeKey} subscribe={deps.subscribeScopeKey} />;
  }
  const scopeDep = deps?.scopeKey;
  const readScopeToken = () => (typeof scopeDep === "function" ? scopeDep() : (scopeDep ?? ""));
  const scopeToken = readScopeToken();
  return <MachinesBridge open={open} onClose={onClose} deps={deps} scopeToken={scopeToken} readScopeToken={readScopeToken} />;
}

function SubscribedScopeBridge({
  open, onClose, deps, readScope, subscribe,
}: WorkSessionFormProps & { readScope: () => string; subscribe: (listener: () => void) => () => void }) {
  const tracked = useTrackedString(subscribe, readScope);
  return <MachinesBridge open={open} onClose={onClose} deps={deps} scopeToken={tracked.token} readScopeToken={tracked.readToken} />;
}

interface ScopeResolvedProps extends WorkSessionFormProps {
  scopeToken: string;
  readScopeToken: () => string;
}

function MachinesBridge(props: ScopeResolvedProps) {
  const { deps } = props;
  if (deps?.machines !== undefined) {
    if (deps.subscribeMachines && typeof deps.machines === "function") {
      return <SubscribedMachinesBridge {...props} deps={deps} readMachines={deps.machines} subscribe={deps.subscribeMachines} />;
    }
    const machinesDep = deps.machines;
    const machines = typeof machinesDep === "function" ? machinesDep() : machinesDep;
    const readLiveMachine = (id: string) =>
      deps.getLiveMachine ? deps.getLiveMachine(id) : (typeof machinesDep === "function" ? machinesDep() : machinesDep).find((m) => m.id === id);
    return <OwnerBridge {...props} machines={machines} readLiveMachine={readLiveMachine} />;
  }
  return <DefaultMachinesBridge {...props} />;
}

function SubscribedMachinesBridge(
  props: ScopeResolvedProps & { deps: WorkSessionFormDeps; readMachines: () => readonly Machine[]; subscribe: (listener: () => void) => () => void },
) {
  const { deps, readMachines, subscribe } = props;
  const machines = useSyncExternalStore(subscribe, readMachines, readMachines);
  const readLiveMachine = useCallback(
    (id: string) => (deps.getLiveMachine ? deps.getLiveMachine(id) : readMachines().find((m) => m.id === id)),
    [deps.getLiveMachine, readMachines],
  );
  return <OwnerBridge {...props} machines={machines} readLiveMachine={readLiveMachine} />;
}

function DefaultMachinesBridge(props: ScopeResolvedProps) {
  const machines = useScopedMachines();
  const getLive = props.deps?.getLiveMachine;
  const readLiveMachine = useCallback(
    (id: string) => (getLive ? getLive(id) : getState().machines.find((m) => m.id === id)),
    [getLive],
  );
  return <OwnerBridge {...props} machines={machines} readLiveMachine={readLiveMachine} />;
}

interface MachinesResolvedProps extends ScopeResolvedProps {
  machines: readonly Machine[];
  readLiveMachine: (machineId: string) => Machine | undefined;
}

function OwnerBridge(props: MachinesResolvedProps) {
  const { deps } = props;
  if (deps?.subscribeCredentialOwner) {
    return <SubscribedOwnerBridge {...props} deps={deps} subscribe={deps.subscribeCredentialOwner} />;
  }
  if (deps?.credentialOwner !== undefined) {
    const ownerDep = deps.credentialOwner;
    const readOwnerToken = () => (typeof ownerDep === "function" ? ownerDep() : ownerDep);
    const ownerToken = readOwnerToken();
    return <AddComputerBridge {...props} ownerToken={ownerToken} readOwnerToken={readOwnerToken} />;
  }
  return <DefaultOwnerBridge {...props} />;
}

function SubscribedOwnerBridge(
  props: MachinesResolvedProps & { deps: WorkSessionFormDeps; subscribe: (listener: () => void) => () => void },
) {
  const { deps, subscribe } = props;
  const ownerDep = deps.credentialOwner;
  const readOwner = useCallback(
    () => (typeof ownerDep === "function" ? ownerDep() : typeof ownerDep === "string" ? ownerDep : credentialOwner()),
    [ownerDep],
  );
  const tracked = useTrackedString(subscribe, readOwner);
  return <AddComputerBridge {...props} ownerToken={tracked.token} readOwnerToken={tracked.readToken} />;
}

function DefaultOwnerBridge(props: MachinesResolvedProps) {
  const owner = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  return <AddComputerBridge {...props} ownerToken={owner} readOwnerToken={credentialOwner} />;
}

interface OwnerResolvedProps extends MachinesResolvedProps {
  ownerToken: string;
  readOwnerToken: () => string;
}

function AddComputerBridge(props: OwnerResolvedProps) {
  const addDep = props.deps?.addComputer;
  if (addDep !== undefined) {
    const adding: WorkSessionAddComputer = typeof addDep === "function" ? { open: addDep, dialog: null } : addDep;
    return <WorkSessionFormBody {...props} adding={adding} />;
  }
  return <DefaultAddComputerBridge {...props} />;
}

function DefaultAddComputerBridge(props: OwnerResolvedProps) {
  const adding = useAddComputer();
  return <WorkSessionFormBody {...props} adding={adding} />;
}

interface BodyProps extends OwnerResolvedProps {
  adding: WorkSessionAddComputer;
}

function WorkSessionFormBody({
  open, onClose, deps, scopeToken, readScopeToken, machines, readLiveMachine, ownerToken, readOwnerToken, adding,
}: BodyProps) {
  const scopeIdentity = JSON.stringify([scopeToken, ownerToken]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: scopeIdentity, epoch: 1 }));
  const [resetEpoch, setResetEpoch] = useState(1);

  const [localMid, setLocalMid] = useState("");
  const [localCwd, setLocalCwd] = useState("");
  const [localBrowse, setLocalBrowse] = useState("");
  const [localHarness, setLocalHarness] = useState("");
  const [localLauncher, setLocalLauncher] = useState("");
  const [localModel, setLocalModel] = useState("");
  const [localPrompt, setLocalPrompt] = useState("");
  const [localAttachments, setLocalAttachments] = useState<LocalAttachment[]>([]);
  const [revision, setRevision] = useState(0);

  if (scope.key !== scopeIdentity) {
    setScope({ key: scopeIdentity, epoch: scope.epoch + 1 });
    setLocalMid("");
    setLocalCwd("");
    setLocalBrowse("");
    setLocalHarness("");
    setLocalLauncher("");
    setLocalModel("");
    setLocalPrompt("");
    setLocalAttachments([]);
    setRevision(0);
  }

  const resetting = scope.epoch !== resetEpoch;
  const mid = resetting ? "" : deps?.mid !== undefined ? deps.mid : localMid;
  const cwd = resetting ? "" : deps?.cwd !== undefined ? deps.cwd : localCwd;
  const browse = resetting ? "" : deps?.browse !== undefined ? deps.browse : localBrowse;
  const harness = resetting ? "" : deps?.harness !== undefined ? deps.harness : localHarness;
  const launcher = resetting ? "" : deps?.launcher !== undefined ? deps.launcher : localLauncher;
  const model = resetting ? "" : deps?.model !== undefined ? deps.model : localModel;
  const prompt = resetting ? "" : deps?.prompt !== undefined ? deps.prompt : localPrompt;
  const attachments = resetting ? [] : deps?.attachments !== undefined ? deps.attachments : localAttachments;

  const depsRef = useRef(deps);
  const openRef = useRef(open);
  const onCloseRef = useRef(onClose);
  const readScopeTokenRef = useRef(readScopeToken);
  const readOwnerTokenRef = useRef(readOwnerToken);
  const readLiveMachineRef = useRef(readLiveMachine);
  const attachmentsRef = useRef(attachments);
  const promptRef = useRef(prompt);
  const keptLocalFiles = useRef<LocalAttachment[]>(deps?.attachments === undefined ? localAttachments : []);
  const submitted = useRef(false);
  const activeEpochRef = useRef(scope.epoch);
  const lastResetEpoch = useRef(scope.epoch);

  useLayoutEffect(() => {
    depsRef.current = deps;
    openRef.current = open;
    onCloseRef.current = onClose;
    readScopeTokenRef.current = readScopeToken;
    readOwnerTokenRef.current = readOwnerToken;
    readLiveMachineRef.current = readLiveMachine;
    attachmentsRef.current = attachments;
    promptRef.current = prompt;
    if (!resetting) keptLocalFiles.current = deps?.attachments === undefined ? localAttachments : [];
  });

  const releaseFiles = (list: LocalAttachment[]) => {
    if (!list.length) return;
    (depsRef.current?.releaseAttachments ?? releaseAttachments)(list);
  };
  const setMidState = (next: string) => {
    setLocalMid(next);
    depsRef.current?.onMidChange?.(next);
  };
  const setCwdState = (next: string) => {
    setLocalCwd(next);
    depsRef.current?.onCwdChange?.(next);
  };
  const setBrowseState = (next: string) => {
    setLocalBrowse(next);
    depsRef.current?.onBrowseChange?.(next);
  };
  const setHarnessState = (next: string) => {
    setLocalHarness(next);
    depsRef.current?.onHarnessChange?.(next);
  };
  const setLauncherState = (next: string) => {
    setLocalLauncher(next);
    depsRef.current?.onLauncherChange?.(next);
  };
  const setModelState = (next: string) => {
    setLocalModel(next);
    depsRef.current?.onModelChange?.(next);
  };
  const setPromptState = (next: string) => {
    promptRef.current = next;
    setLocalPrompt(next);
    depsRef.current?.onPromptChange?.(next);
  };
  const setAttachmentsState = (next: LocalAttachment[] | ((current: LocalAttachment[]) => LocalAttachment[])) => {
    const resolved = typeof next === "function" ? next(attachmentsRef.current) : next;
    attachmentsRef.current = resolved;
    if (depsRef.current?.attachments === undefined) keptLocalFiles.current = resolved;
    setLocalAttachments(resolved);
    depsRef.current?.onAttachmentsChange?.(resolved);
  };

  useLayoutEffect(() => () => releaseFiles(keptLocalFiles.current), []);

  useLayoutEffect(() => {
    activeEpochRef.current = scope.epoch;
    if (lastResetEpoch.current !== scope.epoch) {
      lastResetEpoch.current = scope.epoch;
      submitted.current = false;
      if (keptLocalFiles.current.length) {
        releaseFiles(keptLocalFiles.current);
        keptLocalFiles.current = [];
      }
      setMidState("");
      setBrowseState("");
      setCwdState("");
      setHarnessState("");
      setLauncherState("");
      setModelState("");
      setPromptState("");
      setAttachmentsState([]);
      setRevision(0);
      setResetEpoch(scope.epoch);
    }
    return () => {
      if (activeEpochRef.current === scope.epoch) activeEpochRef.current = 0;
    };
  }, [scope.epoch]);

  useLayoutEffect(() => {
    if (open) submitted.current = false;
  }, [open, scope.epoch]);

  const isScopeCurrent = () =>
    activeEpochRef.current === scope.epoch && readScopeTokenRef.current() === scopeToken && readOwnerTokenRef.current() === ownerToken;
  const formatError = (error: unknown) => (depsRef.current?.formatError ?? workSessionError)(error);

  const machine = machines.find((m) => m.id === mid);
  const offline = !!mid && (!machine?.online || machine.trust === "pending");
  const target = !resetting && open && machine?.online && machine.trust !== "pending"
    ? JSON.stringify([scopeToken, ownerToken, scope.epoch, mid, machine.manager, machine.engine, machine.engine_default, revision])
    : "";
  const dirs = useTarget(
    target && JSON.stringify([target, browse]),
    (signal) => (depsRef.current?.loadDirs ?? defaultLoadDirs)(mid, browse, signal),
    isScopeCurrent,
    formatError,
  );
  const project = useTarget(
    target && cwd && JSON.stringify([target, cwd]),
    (signal) => (depsRef.current?.loadDirs ?? defaultLoadDirs)(mid, cwd, signal),
    isScopeCurrent,
    formatError,
  );
  const launcherList = useTarget(
    target,
    (signal) => (depsRef.current?.loadLaunchers ?? defaultLoadLaunchers)(mid, signal),
    isScopeCurrent,
    formatError,
  );
  // The Harness choices, by what each launcher starts: each of Paseo's providers is one of its own, by its name, never
  // the engine's "paseo" (providerAgent, #8142); a start still sends the launcher's own agent type.
  const groups = new Map<string, Launcher[]>();
  for (const l of launcherList?.data?.launchers ?? []) {
    if (launcherBlock(l)) continue;
    const agent = providerAgent(agentType(l), l.name);
    if (!groups.has(agent)) groups.set(agent, []);
    groups.get(agent)!.push(l);
  }
  const matching = groups.get(harness) ?? [];
  const chosen = matching.find((l) => l.name === launcher);
  const validLauncher = Boolean(chosen);
  const kind = chosen ? agentType(chosen) : harness;
  const optionsKey = target && project?.data && validLauncher ? JSON.stringify([target, cwd, harness, launcher]) : "";
  const options = useTarget(
    optionsKey,
    (signal) => (depsRef.current?.loadStartOptions ?? defaultLoadStartOptions)(mid, launcher, signal),
    isScopeCurrent,
    formatError,
  );
  const opts = options?.data;
  const models = opts?.model_mode === "selectable" ? (opts.models ?? []) : [];
  const compatible = opts?.agent_type === kind;
  // Reconnecting may change what the same launcher can run. A removed model
  // should be picked again rather than silently replaced with the default.
  const staleModel = !!opts && !!model && !models.some((m) => m.id === model);
  const canStart = !!target && !!project?.data && validLauncher && compatible && !staleModel
    && (!opts?.prompt_required || !!prompt.trim()) && (opts?.attachments || attachments.length === 0);

  const chooseLauncher = (name: string) => {
    setLauncherState(name);
    setModelState("");
  };
  const chooseHarness = (agent: string) => {
    setHarnessState(agent);
    const list = groups.get(agent) ?? [];
    chooseLauncher(list.length === 1 ? list[0].name : "");
  };
  const chooseDevice = (id: string) => {
    setMidState(id);
    setBrowseState("");
    setCwdState("");
    setHarnessState("");
    chooseLauncher("");
  };
  const close = () => {
    submitted.current = true;
    onCloseRef.current();
  };
  const add = (list: FileList) => {
    if (!isScopeCurrent()) return;
    const pick = depsRef.current?.pickAttachment ?? pickAttachment;
    const notify = depsRef.current?.notify ?? toast;
    const picked: LocalAttachment[] = [];
    for (const file of Array.from(list)) {
      try { picked.push(pick(file)); }
      catch (e) { notify(formatError(e), "error"); }
    }
    if (picked.length) setAttachmentsState((current) => [...current, ...picked]);
  };
  const removeAttachment = (id: string) => {
    if (!isScopeCurrent()) return;
    const removed = attachmentsRef.current.filter((a) => a.id === id);
    releaseFiles(removed);
    setAttachmentsState((current) => current.filter((a) => a.id !== id));
  };
  const start = () => {
    if (submitted.current || !openRef.current || !isScopeCurrent()) return;
    const live = readLiveMachineRef.current(mid);
    const currentPrompt = promptRef.current;
    const currentAttachments = attachmentsRef.current;
    if (
      !canStart || !opts || !project?.data || !live?.online || live.trust === "pending"
      || live.manager !== machine?.manager || live.engine !== machine?.engine || live.engine_default !== machine?.engine_default
      || (opts.prompt_required && !currentPrompt.trim()) || (!opts.attachments && currentAttachments.length > 0)
    ) return;
    submitted.current = true;
    const runLaunch = depsRef.current?.launchAgent ?? launchAgent;
    runLaunch({
      machineId: mid,
      cwd: project.data.path,
      launcher,
      agentType: kind,
      workSession: true,
      model: model || undefined,
      inlinePrompt: opts.inline_prompt,
      text: currentPrompt.trim(),
      attachments: currentAttachments,
    });
    keptLocalFiles.current = []; // The launch owns the attachment previews after this point.
    setAttachmentsState([]);
    setPromptState("");
    onCloseRef.current();
  };

  const err = dirs?.error || project?.error || launcherList?.error || options?.error;

  return (
    <WorkSessionFormView
      open={open}
      machines={machines}
      mid={mid}
      machine={machine}
      offline={offline}
      hasTarget={Boolean(target)}
      cwd={cwd}
      browse={browse}
      listed={dirs?.data}
      dirsLoading={Boolean(target) && !dirs}
      projectReady={Boolean(project?.data)}
      launchersLoaded={Boolean(launcherList?.data)}
      groups={groups}
      harness={harness}
      launcher={launcher}
      matching={matching}
      validLauncher={validLauncher}
      optionsLoading={Boolean(optionsKey) && !options}
      opts={opts}
      models={models}
      model={model}
      compatible={compatible}
      staleModel={staleModel}
      canStart={canStart}
      error={err}
      prompt={prompt}
      attachments={attachments}
      addComputerDialog={adding.dialog}
      onClose={close}
      onStart={start}
      onChooseDevice={chooseDevice}
      onAddComputer={adding.open}
      onCwdChange={setCwdState}
      onBrowseChange={setBrowseState}
      onChooseHarness={chooseHarness}
      onChooseLauncher={chooseLauncher}
      onModelChange={setModelState}
      onRetry={() => setRevision((n) => n + 1)}
      onPromptChange={setPromptState}
      onAddFiles={add}
      onRemoveAttachment={removeAttachment}
    />
  );
}
