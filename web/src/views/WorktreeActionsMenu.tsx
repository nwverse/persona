import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { t } from "../i18n";
import { byAttention } from "../lib/format";
import { NO_PINS, isPinned, pinnedSessions, type AgentPins } from "../lib/pins";
import { api, credentialOwner } from "../lib/api";
import { MANAGER_HEADER } from "../lib/manager-choice";
import { openNewAgent } from "../lib/new-agent";
import { navigate } from "../lib/router";
import { sameValue } from "../lib/stable-lines";
import { getState, useStore } from "../lib/store";
import type { Machine, Session } from "../lib/types";
import { refreshWorkspaceCatalog, useWorkspaceCatalog, type WorkspaceCatalogSnapshot } from "../lib/workspace-catalog";
import type { WorkspaceTarget } from "../lib/workspace-target";
import { closeWorktreeActions, worktreeActionsCurrent, type OwnedWorkspaceTarget, type ResolvedWorktreeAction, type WorktreeActionsRequest } from "../lib/worktree-actions";
import { firstDirectWorktreeAgent, resolveWorktreeAction, worktreeActionKey, worktreeSafetyStamp, type WorktreeActionContext } from "../lib/worktree-actions-context";
import { WorktreeMutation, type WorktreeActionPost, type WorktreeMutationState } from "../lib/worktree-actions-state";
import type { WorktreeStatus } from "../lib/worktree";
import { copyText, Menu, toast } from "../ui/atoms";
import type { MenuAt, MenuItem } from "../ui/menu-model";
import type { WorktreeActionsHostProps, WorktreeMutationRegistry } from "./WorktreeActions";
import { DeleteWorktreeDialog, RenameWorktreeDialog } from "./WorktreeActionDialogs";

export type WorktreeActionsViewMode = "menu" | "rename" | "delete" | "close";
export type WorktreeActionsAccess = "loading" | "full" | "readOnly";
export type WorktreeActionsNoticeTone = "ok" | "error" | "info";
export type WorktreeStoreSessions = readonly [Record<string, Session[]>, Session[]];

export interface WorktreeMutationDeps {
  resolve: () => ResolvedWorktreeAction;
  current: () => boolean;
  acknowledged: (action: "rename" | "delete" | "close") => void;
  post?: WorktreeActionPost;
}

export type WorktreeMutationLike = Pick<
  WorktreeMutation,
  "target" | "activate" | "updateDependencies" | "snapshot" | "subscribe" | "cancel" | "invalidate" | "dispose" | "rename" | "preview" | "remove" | "reconcile"
>;

export interface WorktreeActionsMenuViewProps {
  view: WorktreeActionsViewMode;
  target: OwnedWorkspaceTarget;
  anchor: HTMLElement;
  at?: MenuAt;
  header: string;
  headerAsWritten: boolean;
  items: MenuItem[];
  renameName: string;
  machineName?: string;
  model: WorktreeMutationLike;
  onClose: () => void;
  viewKey?: string | number;
  renderMenu?: (props: {
    anchor: HTMLElement;
    point?: { x: number; y: number };
    onClose: () => void;
    header: string;
    headerAsWritten: boolean;
    items: MenuItem[];
    menuKey?: string | number;
  }) => ReactNode;
  renderRenameDialog?: (props: {
    target: OwnedWorkspaceTarget;
    name: string;
    model: WorktreeMutationLike;
    onClose: () => void;
    dialogKey?: string | number;
  }) => ReactNode;
  renderDeleteDialog?: (props: {
    target: OwnedWorkspaceTarget;
    machineName?: string;
    closeOnly: boolean;
    model: WorktreeMutationLike;
    onClose: () => void;
    dialogKey?: string | number;
  }) => ReactNode;
}

export interface WorktreeActionsMenuDeps {
  /** Optional scope partition (value or getter); switching it cancels dialogs/probes and fences stale callbacks. */
  scopeKey?: string | (() => string);
  /** Subscribe to scope changes when `scopeKey` is a getter. */
  subscribeScopeKey?: (listener: () => void) => () => void;
  /** Controlled credential owner (value or getter; bypasses `api.credentialOwner` when supplied). */
  credentialOwner?: string | (() => string);
  /** Subscribe to credential owner updates when `credentialOwner` is a getter. */
  subscribeCredentialOwner?: (listener: () => void) => () => void;
  /** Check whether `request.id` is still active (defaults to `worktree-actions.worktreeActionsCurrent`). */
  isRequestCurrent?: (id: number) => boolean;
  /** Close `request.id` (defaults to `worktree-actions.closeWorktreeActions`). */
  closeRequest?: (id: number) => void;
  /** Controlled workspace catalog snapshot or reader (bypasses `useWorkspaceCatalog` when supplied). */
  catalog?: WorkspaceCatalogSnapshot | (() => WorkspaceCatalogSnapshot);
  /** Subscribe to workspace catalog updates when `catalog` is a getter. */
  subscribeCatalog?: (listener: () => void) => () => void;
  /** Controlled machines list or reader (bypasses `useStore` and `getState` when supplied). */
  machines?: readonly Machine[] | (() => readonly Machine[]);
  /** Controlled pins or reader (bypasses `useStore` when supplied). */
  pins?: AgentPins | (() => AgentPins);
  /** Controlled `[sessions, sessionSummaries]` tuple or reader (bypasses `useStore` when supplied). */
  storeSessions?: WorktreeStoreSessions | (() => WorktreeStoreSessions);
  /** Subscribe to controlled store updates when getters are supplied. */
  subscribeStore?: (listener: () => void) => () => void;
  /** Custom worktree action resolver (defaults to `resolveWorktreeAction`). */
  resolveAction?: (target: OwnedWorkspaceTarget, context: WorktreeActionContext) => ResolvedWorktreeAction;
  /** Custom direct-agent finder (defaults to `firstDirectWorktreeAgent`). */
  firstDirectAgent?: (target: OwnedWorkspaceTarget, catalog: WorkspaceCatalogSnapshot, sessions: readonly Session[]) => Session | undefined;
  /** Controlled access state (bypasses `/auth/tokens` probe when supplied). */
  access?: WorktreeActionsAccess;
  /** Custom `/auth/tokens` full-device permission probe. */
  checkAccess?: (target: OwnedWorkspaceTarget, signal: AbortSignal) => Promise<WorktreeActionsAccess | boolean>;
  /** Controlled linked-worktree probe result (bypasses `/worktree/status` probe when supplied). */
  linkedWorktree?: boolean | null;
  /** Custom `/worktree/status` linked-worktree probe. */
  checkWorktreeStatus?: (target: OwnedWorkspaceTarget, signal: AbortSignal) => Promise<WorktreeStatus | boolean | null>;
  /** Custom transport passed to `WorktreeMutation` when `createMutation` is omitted. */
  post?: WorktreeActionPost;
  /** Custom mutation model factory (bypasses `new WorktreeMutation` when supplied). */
  createMutation?: (target: OwnedWorkspaceTarget, deps: WorktreeMutationDeps) => WorktreeMutationLike;
  /** Controlled active subview (`"menu" | "rename" | "delete" | "close"`). */
  view?: WorktreeActionsViewMode;
  /** Invoked on view handoffs and scope-reset cancellations. */
  onViewChange?: (next: WorktreeActionsViewMode) => void;
  /** Refresh workspace catalog after acknowledged mutations or child creation (defaults to `refreshWorkspaceCatalog`). */
  refreshWorkspaceCatalog?: () => void | Promise<unknown>;
  /** Show toast notification after acknowledged mutations (defaults to `atoms.toast`). */
  notify?: (message: string, tone: WorktreeActionsNoticeTone) => void;
  /** Navigate to an existing session (defaults to `router.navigate`). */
  navigate?: (to: Parameters<typeof navigate>[0]) => void;
  /** Launch a new agent in the target workspace (defaults to `new-agent.openNewAgent`). */
  openNewAgent?: (opts: { workspace: WorkspaceTarget }) => void;
  /** Open the child worktree composer (defaults to lazy `lib/worktree-creation.openChildWorktree`). */
  openChildWorktree?: (target: WorkspaceTarget, options: { changed?: () => void }) => void | Promise<unknown>;
  /** Copy text to clipboard (defaults to `atoms.copyText`). */
  copyText?: (text: string) => void | Promise<unknown>;
  /** Custom outer view renderer (defaults to `<WorktreeActionsMenuView>`). */
  renderView?: (props: WorktreeActionsMenuViewProps) => ReactNode;
  renderMenu?: WorktreeActionsMenuViewProps["renderMenu"];
  renderRenameDialog?: WorktreeActionsMenuViewProps["renderRenameDialog"];
  renderDeleteDialog?: WorktreeActionsMenuViewProps["renderDeleteDialog"];
}

export interface ActiveWorktreeActionsProps extends WorktreeActionsHostProps {
  request: WorktreeActionsRequest;
  registry: WorktreeMutationRegistry;
  deps?: WorktreeActionsMenuDeps;
}

const EMPTY_STORE_SESSIONS: WorktreeStoreSessions = [{}, []];
const EMPTY_CATALOG: WorkspaceCatalogSnapshot = { owner: "", manager: "", workspaces: [] };
const noopSubscribe = () => () => {};

function useTrackedScope(subscribe: (listener: () => void) => () => void, read: () => string) {
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

function useCachedExternalValue<T>(
  source: T | (() => T) | undefined,
  subscribe: ((listener: () => void) => () => void) | undefined,
): T | undefined {
  const cache = useRef<{ has: boolean; value: T | undefined }>({ has: false, value: undefined });
  const getSnapshot = useCallback((): T | undefined => {
    const next = typeof source === "function" ? (source as () => T)() : source;
    if (cache.current.has && sameValue(cache.current.value, next)) {
      return cache.current.value;
    }
    cache.current = { has: true, value: next };
    return next;
  }, [source]);
  const sub = subscribe && typeof source === "function" ? subscribe : noopSubscribe;
  return useSyncExternalStore(sub, getSnapshot, getSnapshot);
}

function useCachedModelSnapshot(model: WorktreeMutationLike): WorktreeMutationState {
  const cache = useRef<WorktreeMutationState | null>(null);
  const getSnapshot = useCallback(() => {
    const next = model.snapshot();
    if (cache.current && sameValue(cache.current, next)) {
      return cache.current;
    }
    cache.current = next;
    return next;
  }, [model]);
  return useSyncExternalStore(model.subscribe, getSnapshot, getSnapshot);
}

/** Pure presentational view for the active worktree menu and its rename/delete/close dialogs. */
export function WorktreeActionsMenuView({
  view,
  target,
  anchor,
  at,
  header,
  headerAsWritten,
  items,
  renameName,
  machineName,
  model,
  onClose,
  viewKey,
  renderMenu,
  renderRenameDialog,
  renderDeleteDialog,
}: WorktreeActionsMenuViewProps) {
  const menuAnchor = at?.anchor || anchor;
  return <>
    {view === "menu" && (renderMenu
      ? renderMenu({ anchor: menuAnchor, point: at?.point, onClose, header, headerAsWritten, items, menuKey: viewKey })
      : <Menu key={viewKey} anchor={menuAnchor} point={at?.point} onClose={onClose} align="right"
          header={header} headerAsWritten={headerAsWritten} items={items} />)}
    {view === "rename" && (renderRenameDialog
      ? renderRenameDialog({ target, name: renameName, model, onClose, dialogKey: viewKey })
      : <RenameWorktreeDialog key={viewKey} target={target} name={renameName} model={model as WorktreeMutation} onClose={onClose} />)}
    {(view === "delete" || view === "close") && (renderDeleteDialog
      ? renderDeleteDialog({ target, machineName, closeOnly: view === "close", model, onClose, dialogKey: `${viewKey ?? ""}:${view}` })
      : <DeleteWorktreeDialog key={`${viewKey ?? ""}:${view}`} target={target} machineName={machineName} closeOnly={view === "close"} model={model as WorktreeMutation} onClose={onClose} />)}
  </>;
}

interface DefaultStoreBridgeProps {
  needMachines: boolean;
  needPins: boolean;
  needSessions: boolean;
  controlledMachines?: readonly Machine[];
  controlledPins?: AgentPins;
  controlledStoreSessions?: WorktreeStoreSessions;
  render: (resolved: {
    machines: readonly Machine[];
    pins: AgentPins;
    storeSessions: WorktreeStoreSessions;
  }) => ReactNode;
}

function DefaultWorktreeStoreBridge({
  needMachines,
  needPins,
  needSessions,
  controlledMachines,
  controlledPins,
  controlledStoreSessions,
  render,
}: DefaultStoreBridgeProps) {
  const storeMachines = useStore((state) => state.machines);
  const storePins = useStore((state) => state.pins || NO_PINS);
  const storeMap = useStore((state) => state.sessions);
  const storeSummaries = useStore((state) => state.sessionSummaries);
  const resolvedSessions = useMemo<WorktreeStoreSessions>(
    () => (needSessions ? [storeMap, storeSummaries] : (controlledStoreSessions ?? EMPTY_STORE_SESSIONS)),
    [needSessions, storeMap, storeSummaries, controlledStoreSessions],
  );
  return <>{render({
    machines: needMachines ? storeMachines : (controlledMachines ?? []),
    pins: needPins ? storePins : (controlledPins ?? NO_PINS),
    storeSessions: resolvedSessions,
  })}</>;
}

function DefaultWorktreeCatalogBridge({ render }: { render: (catalog: WorkspaceCatalogSnapshot) => ReactNode }) {
  const catalog = useWorkspaceCatalog(true);
  return <>{render(catalog)}</>;
}

interface WorktreeActionsBodyProps {
  request: WorktreeActionsRequest;
  onOpenWorkspace?: (target: WorkspaceTarget) => void;
  sessions?: readonly Session[];
  readOnly: boolean;
  deps?: WorktreeActionsMenuDeps;
  depsRef: { current?: WorktreeActionsMenuDeps };
  catalog: WorkspaceCatalogSnapshot;
  machines: readonly Machine[];
  pins: AgentPins;
  storeSessions: WorktreeStoreSessions;
  view: WorktreeActionsViewMode;
  access: WorktreeActionsAccess;
  linkedWorktree: boolean | null | undefined;
  model: WorktreeMutationLike;
  state: WorktreeMutationState;
  scopeEpoch: number;
  stampRef: { current: { epoch: number; value: string } };
  liveCatalogRef: { current: WorkspaceCatalogSnapshot };
  liveMachinesRef: { current: readonly Machine[] };
  resolveRef: { current: () => ResolvedWorktreeAction };
  isCurrent: () => boolean;
  isOwnerScopeCurrent: () => boolean;
  close: () => void;
  changeView: (next: "rename" | "delete" | "close") => void;
  pendingChildRef: { current: number };
  pendingCopyRef: { current: number };
}

function WorktreeActionsBody({
  request,
  onOpenWorkspace,
  sessions,
  readOnly,
  deps,
  depsRef,
  catalog,
  machines,
  pins,
  storeSessions,
  view,
  access,
  linkedWorktree,
  model,
  state,
  scopeEpoch,
  stampRef,
  liveCatalogRef,
  liveMachinesRef,
  resolveRef,
  isCurrent,
  isOwnerScopeCurrent,
  close,
  changeView,
  pendingChildRef,
  pendingCopyRef,
}: WorktreeActionsBodyProps) {
  const { target, anchor } = request;
  const writable = access === "full" && !readOnly;
  const resolveAction = deps?.resolveAction ?? resolveWorktreeAction;
  const context = resolveAction(target, { catalog, machines, writable, linkedWorktree: linkedWorktree === true });

  useLayoutEffect(() => {
    liveCatalogRef.current = catalog;
    liveMachinesRef.current = machines;
  }, [catalog, machines, liveCatalogRef, liveMachinesRef]);

  useLayoutEffect(() => {
    if (access === "loading") return;
    const next = context.workspace ? worktreeSafetyStamp(context.workspace) : "";
    if (stampRef.current.epoch !== scopeEpoch) {
      stampRef.current = { epoch: scopeEpoch, value: next };
    } else if (!stampRef.current.value && next) {
      stampRef.current = { epoch: scopeEpoch, value: next };
    }
    const retainedRemoval = state.dispatched && context.canReconcile && !context.workspace;
    if ((!context.writable && !retainedRemoval) || (stampRef.current.value && next !== stampRef.current.value && !retainedRemoval)) {
      model.invalidate();
    }
  }, [model, scopeEpoch, stampRef, access, context.writable, context.workspace, context.canReconcile, state.dispatched]);

  const ordered = useMemo(() => {
    if (sessions) return sessions;
    const rows = [...Object.values(storeSessions[0]).flat(), ...storeSessions[1]];
    const seen = new Set<string>();
    const unique = rows.filter((session) => {
      const k = JSON.stringify([session.manager, session.machine_id, session.host, session.session_id]);
      if (seen.has(k)) return false;
      seen.add(k);
      return true;
    });
    return [...pinnedSessions(unique, pins), ...unique.filter((session) => !isPinned(session, pins)).sort(byAttention)];
  }, [sessions, storeSessions, pins]);

  const agent = (deps?.firstDirectAgent ?? firstDirectWorktreeAgent)(target, catalog, ordered);
  const run = (action: () => void, writing = false) => {
    if (!isCurrent()) return;
    const checked = resolveRef.current();
    if (!checked.workspace || (writing && !checked.writable)) return;
    close();
    action();
  };

  const items: MenuItem[] = [
    {
      key: "open",
      label: t("worktree-actions.open"),
      icon: "folder",
      disabled: !context.workspace || (!agent && !context.writable),
      onSelect: () => run(() => {
        if (agent) (depsRef.current?.navigate ?? navigate)({ name: "session", mid: agent.machine_id, sid: agent.session_id });
        else (depsRef.current?.openNewAgent ?? openNewAgent)({ workspace: target });
      }, !agent),
    },
    {
      key: "new-agent",
      label: t("worktree-actions.new_agent"),
      icon: "agent",
      disabled: !context.writable,
      onSelect: () => run(() => (depsRef.current?.openNewAgent ?? openNewAgent)({ workspace: target }), true),
    },
    ...(context.canCreate ? [{
      key: "child",
      label: t("worktree-actions.new_child"),
      icon: "git" as const,
      inline: true,
      onSelect: async () => {
        if (!isCurrent() || pendingChildRef.current === scopeEpoch) return;
        pendingChildRef.current = scopeEpoch;
        try {
          // Recheck the originating target after loading the child composer.
          const openChild = depsRef.current?.openChildWorktree
            ?? (await import("../lib/worktree-creation")).openChildWorktree;
          if (!isCurrent() || !resolveRef.current().canCreate) return;
          close();
          await openChild(target, {
            changed: () => {
              if (!isOwnerScopeCurrent()) return;
              if (resolveRef.current().writable) {
                try {
                  void Promise.resolve((depsRef.current?.refreshWorkspaceCatalog ?? refreshWorkspaceCatalog)()).catch(() => {});
                } catch { /* non-blocking refresh */ }
              }
            },
          });
        } catch (error) {
          if (isCurrent()) throw error;
        } finally {
          if (pendingChildRef.current === scopeEpoch) pendingChildRef.current = 0;
        }
      },
    }] : []),
    ...(context.canRename && !state.dispatched ? [{
      key: "rename",
      label: t("worktree-actions.rename"),
      icon: "pencil" as const,
      keepOpen: true,
      onSelect: () => changeView("rename"),
    }] : []),
    {
      key: "copy-name",
      label: t("worktree-actions.copy_name"),
      icon: "copy",
      divider: true,
      inline: true,
      disabled: !context.workspace,
      onSelect: async () => {
        if (!isCurrent() || pendingCopyRef.current === scopeEpoch) return;
        pendingCopyRef.current = scopeEpoch;
        try {
          const text = resolveRef.current().workspace?.title || target.label || target.cwd;
          await (depsRef.current?.copyText ?? copyText)(text);
        } catch (error) {
          if (isCurrent()) throw error;
        } finally {
          if (pendingCopyRef.current === scopeEpoch) pendingCopyRef.current = 0;
        }
      },
    },
    {
      key: "copy-path",
      label: t("worktree-actions.copy_path"),
      icon: "copy",
      inline: true,
      disabled: !context.workspace || !target.cwd,
      onSelect: async () => {
        if (!isCurrent() || pendingCopyRef.current === scopeEpoch) return;
        pendingCopyRef.current = scopeEpoch;
        try {
          await (depsRef.current?.copyText ?? copyText)(target.cwd);
        } catch (error) {
          if (isCurrent()) throw error;
        } finally {
          if (pendingCopyRef.current === scopeEpoch) pendingCopyRef.current = 0;
        }
      },
    },
    ...(context.parent && onOpenWorkspace ? [{
      key: "parent",
      label: t("worktree-actions.open_parent"),
      icon: "folder" as const,
      onSelect: () => {
        if (!isCurrent()) return;
        const parent = resolveRef.current().parent;
        if (parent) {
          close();
          onOpenWorkspace(parent);
        }
      },
    }] : []),
    ...(target.host === "herdr" && (!state.dispatched || state.preview?.close_only) ? [{
      key: "close",
      label: t(state.dispatched ? "worktree-actions.check_status" : "worktree-actions.close_workspace"),
      icon: "x" as const,
      divider: true,
      danger: true,
      keepOpen: true,
      disabled: state.dispatched ? !context.canReconcile : !context.canClose,
      onSelect: () => changeView("close"),
    }] : []),
    ...(["orca", "herdr", "paseo"].includes(target.host) && !(state.dispatched && state.preview?.close_only) ? [{
      key: "delete",
      label: t(state.dispatched ? "worktree-actions.check_status" : "worktree-actions.delete"),
      icon: "trash" as const,
      divider: true,
      danger: true,
      keepOpen: true,
      disabled: state.dispatched ? !context.canReconcile : !context.canDelete,
      onSelect: () => changeView("delete"),
    }] : []),
    ...(target.host === "herdr" && linkedWorktree === false ? [{ key: "linked-only", label: t("worktree-actions.linked_only"), note: true, onSelect: () => {} }] : []),
    ...(target.host === "herdr" && linkedWorktree === null ? [{ key: "git-unavailable", label: t("worktree-actions.unavailable"), note: true, onSelect: () => {} }] : []),
    ...(!context.writable ? [{ key: "availability", label: access === "loading" ? t("worktree-actions.checking_access") : context.reason, note: true, onSelect: () => {} }] : []),
    ...(context.workspace?.is_main || context.hasChildren ? [{ key: "protected", label: t(context.hasChildren ? "worktree-actions.has_children" : "worktree-actions.protected"), note: true, onSelect: () => {} }] : []),
  ];

  const viewProps: WorktreeActionsMenuViewProps = {
    view,
    target,
    anchor,
    at: request.at,
    header: context.workspace?.title || target.label || t("worktree-actions.worktree"),
    headerAsWritten: Boolean(context.workspace?.title || target.label),
    items,
    renameName: context.workspace?.title || target.label || "",
    machineName: context.machine?.name,
    model,
    onClose: close,
    viewKey: scopeEpoch,
    renderMenu: deps?.renderMenu,
    renderRenameDialog: deps?.renderRenameDialog,
    renderDeleteDialog: deps?.renderDeleteDialog,
  };
  if (deps?.renderView) return <>{deps.renderView(viewProps)}</>;
  return <WorktreeActionsMenuView {...viewProps} />;
}

export function ActiveWorktreeActions({
  request,
  registry,
  onOpenWorkspace,
  sessions,
  readOnly = false,
  deps,
}: ActiveWorktreeActionsProps) {
  const { target, id, anchor } = request;
  const scopeDep = deps?.scopeKey;
  const readScope = useCallback(() => (typeof scopeDep === "function" ? scopeDep() : (scopeDep ?? "")), [scopeDep]);
  const scopeSubscribe = deps?.subscribeScopeKey && typeof scopeDep === "function" ? deps.subscribeScopeKey : noopSubscribe;
  const trackedScope = useTrackedScope(scopeSubscribe, readScope);

  const ownerDep = deps?.credentialOwner;
  const readControlledOwner = useCallback(
    () => (typeof ownerDep === "function" ? ownerDep() : typeof ownerDep === "string" ? ownerDep : ""),
    [ownerDep],
  );
  const ownerSubscribe = deps?.subscribeCredentialOwner && typeof ownerDep === "function" ? deps.subscribeCredentialOwner : noopSubscribe;
  const trackedOwner = useTrackedScope(ownerSubscribe, readControlledOwner);

  const key = worktreeActionKey(target);
  const registryKey = deps?.scopeKey !== undefined ? `${trackedScope.value}\0${key}` : key;
  // Keep scope/identity tracking and dialog/probe state in the stable outer component across bridge switches.
  const actionScope = JSON.stringify([trackedScope.token, trackedOwner.token, id, key]);
  const [scope, setScope] = useState<{ key: string; epoch: number }>(() => ({ key: actionScope, epoch: 1 }));
  const effectiveEpoch = scope.key === actionScope ? scope.epoch : scope.epoch + 1;
  if (scope.key !== actionScope) {
    setScope({ key: actionScope, epoch: effectiveEpoch });
  }

  const [localViewState, setLocalViewState] = useState<{ epoch: number; view: WorktreeActionsViewMode }>(() => ({
    epoch: effectiveEpoch,
    view: "menu",
  }));
  const [accessState, setAccessState] = useState<{ epoch: number; value: WorktreeActionsAccess }>(() => ({
    epoch: effectiveEpoch,
    value: "loading",
  }));
  const [linkedState, setLinkedState] = useState<{ epoch: number; value: boolean | null | undefined }>(() => ({
    epoch: effectiveEpoch,
    value: undefined,
  }));

  const view: WorktreeActionsViewMode = deps?.view !== undefined
    ? deps.view
    : (localViewState.epoch === effectiveEpoch ? localViewState.view : "menu");
  const access: WorktreeActionsAccess = deps?.access !== undefined
    ? deps.access
    : (accessState.epoch === effectiveEpoch ? accessState.value : "loading");
  const linkedWorktree: boolean | null | undefined = deps?.linkedWorktree !== undefined
    ? deps.linkedWorktree
    : (linkedState.epoch === effectiveEpoch ? linkedState.value : undefined);

  const controlledCatalog = useCachedExternalValue(deps?.catalog, deps?.subscribeCatalog);
  const controlledMachines = useCachedExternalValue(deps?.machines, deps?.subscribeStore);
  const controlledPins = useCachedExternalValue(deps?.pins, deps?.subscribeStore);
  const controlledStoreSessions = useCachedExternalValue(deps?.storeSessions, deps?.subscribeStore);

  const activeEpochRef = useRef(effectiveEpoch);
  const prevCommittedEpochRef = useRef(0);
  const depsRef = useRef(deps);
  const requestRef = useRef(request);
  const readOnlyRef = useRef(readOnly);
  const accessRef = useRef(access);
  const linkedWorktreeRef = useRef(linkedWorktree);
  const readScopeTokenRef = useRef(trackedScope.readToken);
  const readOwnerTokenRef = useRef(trackedOwner.readToken);
  const liveCatalogRef = useRef<WorkspaceCatalogSnapshot>(controlledCatalog ?? EMPTY_CATALOG);
  const liveMachinesRef = useRef<readonly Machine[]>(controlledMachines ?? []);
  const stampRef = useRef<{ epoch: number; value: string }>({ epoch: effectiveEpoch, value: "" });
  const pendingChildRef = useRef(0);
  const pendingCopyRef = useRef(0);

  const readOwnerNow = () => {
    const cur = depsRef.current?.credentialOwner;
    if (cur !== undefined) return typeof cur === "function" ? cur() : cur;
    return credentialOwner();
  };
  const readMachinesNow = (): readonly Machine[] => {
    const cur = depsRef.current?.machines;
    if (cur !== undefined) return typeof cur === "function" ? cur() : cur;
    return getState().machines;
  };
  const readCatalogNow = (): WorkspaceCatalogSnapshot => {
    const cur = depsRef.current?.catalog;
    if (cur !== undefined) return typeof cur === "function" ? cur() : cur;
    return liveCatalogRef.current;
  };

  const capturedEpoch = effectiveEpoch;
  const capturedScopeToken = trackedScope.token;
  const capturedOwnerToken = trackedOwner.token;

  const isOwnerScopeCurrent = () =>
    readScopeTokenRef.current() === capturedScopeToken
    && readOwnerTokenRef.current() === capturedOwnerToken
    && target.owner === readOwnerNow();

  const isCurrent = () =>
    activeEpochRef.current === capturedEpoch
    && isOwnerScopeCurrent()
    && (depsRef.current?.isRequestCurrent ?? worktreeActionsCurrent)(id);

  const close = () => {
    if (!isCurrent()) return;
    (depsRef.current?.closeRequest ?? closeWorktreeActions)(id);
    // Menu -> dialog handoffs restore the original row, not a menu item that has just unmounted.
    if (anchor.isConnected) anchor.focus({ preventScroll: true });
  };

  const resolveNow = (): ResolvedWorktreeAction => {
    const cur = depsRef.current;
    const reqTarget = requestRef.current.target;
    const writable = accessRef.current === "full" && !readOnlyRef.current;
    const resolver = cur?.resolveAction ?? resolveWorktreeAction;
    return resolver(reqTarget, {
      catalog: readCatalogNow(),
      machines: readMachinesNow(),
      writable,
      linkedWorktree: linkedWorktreeRef.current === true,
    });
  };

  const resolveRef = useRef(resolveNow);
  const activeModelRef = useRef<WorktreeMutationLike | null>(null);

  const makeBoundModelDeps = (
    modelEpoch: number,
    modelScopeToken: string,
    modelOwnerToken: string,
    modelId: number,
    modelTarget: OwnedWorkspaceTarget,
    modelAnchor: HTMLElement,
    getModel: () => WorktreeMutationLike | null,
    post?: WorktreeActionPost,
  ): WorktreeMutationDeps => {
    const isModelCurrent = () =>
      activeEpochRef.current === modelEpoch
      && activeModelRef.current === getModel()
      && readScopeTokenRef.current() === modelScopeToken
      && readOwnerTokenRef.current() === modelOwnerToken
      && modelTarget.owner === readOwnerNow()
      && (depsRef.current?.isRequestCurrent ?? worktreeActionsCurrent)(modelId);
    return {
      resolve: () => {
        const cur = depsRef.current;
        const writable = accessRef.current === "full" && !readOnlyRef.current;
        const resolver = cur?.resolveAction ?? resolveWorktreeAction;
        return resolver(modelTarget, {
          catalog: readCatalogNow(),
          machines: readMachinesNow(),
          writable,
          linkedWorktree: linkedWorktreeRef.current === true,
        });
      },
      current: isModelCurrent,
      acknowledged: (action) => {
        if (!isModelCurrent()) return;
        try {
          void Promise.resolve((depsRef.current?.refreshWorkspaceCatalog ?? refreshWorkspaceCatalog)()).catch(() => {});
        } catch { /* non-blocking refresh */ }
        const msg = t(action === "rename" ? "worktree-actions.renamed" : action === "close" ? "worktree-actions.closed" : "worktree-actions.removed");
        if (depsRef.current?.notify) depsRef.current.notify(msg, "ok");
        else toast(msg, "ok");
        (depsRef.current?.closeRequest ?? closeWorktreeActions)(modelId);
        if (modelAnchor.isConnected) modelAnchor.focus({ preventScroll: true });
      },
      ...(post ? { post } : {}),
    };
  };

  const acquireModel = (
    modelTarget: OwnedWorkspaceTarget,
    regKey: string,
    reg: WorktreeMutationRegistry,
    currentDeps: WorktreeActionsMenuDeps | undefined,
    previousModel?: WorktreeMutationLike,
  ): WorktreeMutationLike => {
    const prior = reg.get(regKey);
    if (prior && !["invalid", "removed"].includes(prior.snapshot().phase)) {
      if (previousModel && prior === previousModel) {
        prior.cancel();
        if (prior.snapshot().dispatched) return prior;
        prior.dispose();
        reg.delete(regKey);
      } else {
        return prior;
      }
    } else {
      prior?.dispose();
    }
    let createdRef: WorktreeMutationLike | null = null;
    const modelDeps = makeBoundModelDeps(
      effectiveEpoch,
      capturedScopeToken,
      capturedOwnerToken,
      id,
      modelTarget,
      anchor,
      () => createdRef,
      currentDeps?.post,
    );
    const created: WorktreeMutationLike = currentDeps?.createMutation
      ? currentDeps.createMutation(modelTarget, modelDeps)
      : new WorktreeMutation(modelTarget, modelDeps);
    createdRef = created;
    reg.set(regKey, created as WorktreeMutation);
    return created;
  };

  const [modelEntry, setModelEntry] = useState<{ epoch: number; regKey: string; model: WorktreeMutationLike }>(() => ({
    epoch: effectiveEpoch,
    regKey: registryKey,
    model: acquireModel(target, registryKey, registry, deps),
  }));
  if (modelEntry.epoch !== effectiveEpoch || modelEntry.regKey !== registryKey) {
    setModelEntry({
      epoch: effectiveEpoch,
      regKey: registryKey,
      model: acquireModel(target, registryKey, registry, deps, modelEntry.model),
    });
  }
  const model = modelEntry.model;

  useLayoutEffect(() => {
    depsRef.current = deps;
    requestRef.current = request;
    readOnlyRef.current = readOnly;
    accessRef.current = access;
    linkedWorktreeRef.current = linkedWorktree;
    readScopeTokenRef.current = trackedScope.readToken;
    readOwnerTokenRef.current = trackedOwner.readToken;
    if (controlledCatalog !== undefined) liveCatalogRef.current = controlledCatalog;
    if (controlledMachines !== undefined) liveMachinesRef.current = controlledMachines;
    resolveRef.current = resolveNow;
    activeModelRef.current = model;
  });

  useLayoutEffect(() => {
    const previous = prevCommittedEpochRef.current;
    prevCommittedEpochRef.current = effectiveEpoch;
    activeEpochRef.current = effectiveEpoch;
    activeModelRef.current = model;
    if (previous !== 0 && previous !== effectiveEpoch) {
      depsRef.current?.onViewChange?.("menu");
    }
    return () => {
      if (activeEpochRef.current === effectiveEpoch) activeEpochRef.current = 0;
      if (activeModelRef.current === model) activeModelRef.current = null;
      if (pendingChildRef.current === effectiveEpoch) pendingChildRef.current = 0;
      if (pendingCopyRef.current === effectiveEpoch) pendingCopyRef.current = 0;
    };
  }, [effectiveEpoch, model]);

  useLayoutEffect(() => {
    model.updateDependencies(
      makeBoundModelDeps(
        effectiveEpoch,
        capturedScopeToken,
        capturedOwnerToken,
        id,
        target,
        anchor,
        () => model,
        deps?.post,
      ),
    );
  }, [model, effectiveEpoch, capturedScopeToken, capturedOwnerToken, id, target, anchor, deps?.post]);

  const state = useCachedModelSnapshot(model);

  const controlledAccess = deps?.access;
  const customCheckAccess = deps?.checkAccess;
  useEffect(() => {
    if (controlledAccess !== undefined) return;
    const runEpoch = effectiveEpoch;
    const runScopeToken = trackedScope.token;
    const runOwnerToken = trackedOwner.token;
    const runTarget = requestRef.current.target;
    const controller = new AbortController();
    const isProbeCurrent = () =>
      !controller.signal.aborted
      && activeEpochRef.current === runEpoch
      && readScopeTokenRef.current() === runScopeToken
      && readOwnerTokenRef.current() === runOwnerToken
      && (depsRef.current?.isRequestCurrent ?? worktreeActionsCurrent)(id)
      && runTarget.owner === readOwnerNow();
    void (async () => {
      try {
        if (customCheckAccess) {
          const result = await customCheckAccess(runTarget, controller.signal);
          if (!isProbeCurrent()) return;
          const next: WorktreeActionsAccess = result === true || result === "full" ? "full" : "readOnly";
          setAccessState({ epoch: runEpoch, value: next });
          return;
        }
        // This route is full-device-only on existing hubs. No role is guessed from a token's spelling or saved device id.
        const result = await api<{ tokens: { id: string; role?: string }[]; current: string }>("/auth/tokens", {
          signal: controller.signal, headers: { [MANAGER_HEADER]: runTarget.manager },
        });
        if (!isProbeCurrent()) return;
        const full = Array.isArray(result.tokens) && typeof result.current === "string"
          && (result.current === "" || result.tokens.some((token) => token.id === result.current && token.role === "full"));
        setAccessState({ epoch: runEpoch, value: full ? "full" : "readOnly" });
      } catch {
        if (isProbeCurrent()) setAccessState({ epoch: runEpoch, value: "readOnly" });
      }
    })();
    return () => controller.abort();
  }, [controlledAccess, customCheckAccess, effectiveEpoch, id, target.manager, target.owner, trackedScope.token, trackedOwner.token]);

  const controlledLinked = deps?.linkedWorktree;
  const customCheckStatus = deps?.checkWorktreeStatus;
  useEffect(() => {
    if (controlledLinked !== undefined) return;
    if (target.host !== "herdr" || access !== "full") return;
    const runEpoch = effectiveEpoch;
    const runScopeToken = trackedScope.token;
    const runOwnerToken = trackedOwner.token;
    const runTarget = requestRef.current.target;
    const controller = new AbortController();
    const isProbeCurrent = () =>
      !controller.signal.aborted
      && activeEpochRef.current === runEpoch
      && readScopeTokenRef.current() === runScopeToken
      && readOwnerTokenRef.current() === runOwnerToken
      && (depsRef.current?.isRequestCurrent ?? worktreeActionsCurrent)(id)
      && runTarget.owner === readOwnerNow();
    void (async () => {
      try {
        if (customCheckStatus) {
          const res = await customCheckStatus(runTarget, controller.signal);
          if (!isProbeCurrent()) return;
          const next = typeof res === "boolean" || res === null ? res : Boolean(res.worktree && res.path === runTarget.cwd);
          setLinkedState({ epoch: runEpoch, value: next });
          return;
        }
        const status = await api<WorktreeStatus>(`/machines/${encodeURIComponent(runTarget.machineId)}/worktree/status`, {
          method: "POST", json: { kind: runTarget.host, cwd: runTarget.cwd }, signal: controller.signal,
          headers: { [MANAGER_HEADER]: runTarget.manager },
        });
        if (!isProbeCurrent()) return;
        setLinkedState({ epoch: runEpoch, value: status.worktree && status.path === runTarget.cwd });
      } catch {
        if (isProbeCurrent()) setLinkedState({ epoch: runEpoch, value: null });
      }
    })();
    return () => controller.abort();
  }, [controlledLinked, customCheckStatus, effectiveEpoch, id, target.host, target.machineId, target.cwd, target.manager, target.owner, access, trackedScope.token, trackedOwner.token]);

  useEffect(() => {
    registry.set(registryKey, model as WorktreeMutation);
    model.activate();
    return () => {
      model.cancel();
      if (!model.snapshot().dispatched) {
        model.dispose();
        if (registry.get(registryKey) === model) registry.delete(registryKey);
      }
    };
  }, [model, registry, registryKey, effectiveEpoch]);

  const changeView = (next: "rename" | "delete" | "close") => {
    if (!isCurrent()) return;
    const checked = resolveRef.current();
    const dispatched = model.snapshot().dispatched;
    if (next === "rename" && (dispatched || !checked.canRename)) return;
    if (next === "close" && (dispatched ? !checked.canReconcile : !checked.canClose)) return;
    if (next === "delete" && (dispatched ? !checked.canReconcile : !checked.canDelete)) return;
    if (anchor.isConnected) anchor.focus({ preventScroll: true });
    if (depsRef.current?.view === undefined) setLocalViewState({ epoch: capturedEpoch, view: next });
    depsRef.current?.onViewChange?.(next);
  };

  const needDefaultCatalog = controlledCatalog === undefined;
  const needDefaultMachines = controlledMachines === undefined;
  const needDefaultPins = sessions === undefined && controlledPins === undefined;
  const needDefaultSessions = sessions === undefined && controlledStoreSessions === undefined;
  const needDefaultStore = needDefaultMachines || needDefaultPins || needDefaultSessions;

  const renderBody = (
    catalog: WorkspaceCatalogSnapshot,
    storeResolved: { machines: readonly Machine[]; pins: AgentPins; storeSessions: WorktreeStoreSessions },
  ) => (
    <WorktreeActionsBody
      request={request}
      onOpenWorkspace={onOpenWorkspace}
      sessions={sessions}
      readOnly={readOnly}
      deps={deps}
      depsRef={depsRef}
      catalog={catalog}
      machines={storeResolved.machines}
      pins={storeResolved.pins}
      storeSessions={storeResolved.storeSessions}
      view={view}
      access={access}
      linkedWorktree={linkedWorktree}
      model={model}
      state={state}
      scopeEpoch={capturedEpoch}
      stampRef={stampRef}
      liveCatalogRef={liveCatalogRef}
      liveMachinesRef={liveMachinesRef}
      resolveRef={resolveRef}
      isCurrent={isCurrent}
      isOwnerScopeCurrent={isOwnerScopeCurrent}
      close={close}
      changeView={changeView}
      pendingChildRef={pendingChildRef}
      pendingCopyRef={pendingCopyRef}
    />
  );

  const renderWithStore = (catalog: WorkspaceCatalogSnapshot) => {
    if (needDefaultStore) {
      return (
        <DefaultWorktreeStoreBridge
          needMachines={needDefaultMachines}
          needPins={needDefaultPins}
          needSessions={needDefaultSessions}
          controlledMachines={controlledMachines}
          controlledPins={controlledPins}
          controlledStoreSessions={controlledStoreSessions}
          render={(storeResolved) => renderBody(catalog, storeResolved)}
        />
      );
    }
    return renderBody(catalog, {
      machines: controlledMachines ?? [],
      pins: controlledPins ?? NO_PINS,
      storeSessions: controlledStoreSessions ?? EMPTY_STORE_SESSIONS,
    });
  };

  if (needDefaultCatalog) {
    return <DefaultWorktreeCatalogBridge render={renderWithStore} />;
  }
  return renderWithStore(controlledCatalog!);
}
