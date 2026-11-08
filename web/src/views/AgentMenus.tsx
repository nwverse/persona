// The Agents list's menus (iOS AgentRowMenu / SpaceMenuItems): a row's ⋯ or right click, a space's ⋯ on its header,
// and the confirmation of "Mark all as seen". What they do is in lib/agent-actions.ts.
import { attachLive, flipMute, flipPin, flipSpaceMute, flipSpacePin, handToPersona } from "../lib/agent-actions";
import { isSpaceMuted, unreadOf, type SpaceLike } from "../lib/agent-filter";
import { errorText } from "../lib/api";
import { resumeCommand, sessionCan, sessionDeletable, sessionMenuActions } from "../lib/format";
import { appArchiveDeps, archiveAgent, archiveOffers, deleteInsteadOfArchive } from "../lib/agent-archives";
import { machineWithHub } from "../lib/mesh";
import { isPinned, isSpacePinned } from "../lib/pins";
import { isMuted } from "../lib/session-actions";
import { getState, markSeen, refresh, refreshTodos, setSpacesSupported, useStore } from "../lib/store";
import { findTodoForSpace, isLegacySpacesRouteError, unlinkAgentFromTodo, unlinkSpaceFromTodo } from "../lib/todos";
import type { Session } from "../lib/types";
import { sessionNode, type MenuNode } from "../lib/node-actions";
import { navigate } from "../lib/router";
import { Button, copyText, Dialog, toast, type MenuItem } from "../ui/atoms";
import type { MenuAt } from "../ui/menu-model";
import { NodeMenu } from "./NodeMenu";
import { t } from "../i18n";
import { markAllSeenLabel } from "../lib/state-labels";

/** What a row's ⋯ (and its right click) offers. `onLinkTodo`: the row can hang on a todo ("Link to to-do…"; "Move to another to-do…" and
 *  "Unlink" when it does). Everything a row offers is here, so a touch screen (no right click; iOS Safari sends no contextmenu on a
 *  long press) reaches it from the ⋯ (#1996). */
export function agentRowMenuItems(s: Session, o: { pinned: boolean; muted: boolean; onRename: (s: Session) => void; onDelete: (s: Session) => void; onLinkTodo?: (s: Session) => void; onCloseAgent?: (s: Session) => void; onArchiveClose?: (s: Session) => void; features?: readonly string[] }): MenuItem[] {
  const { onRename, onDelete, onLinkTodo, onCloseAgent, onArchiveClose } = o;
  // "Live link" and "Close agent" show when the chat's ⋯ shows them (sessionMenuActions), and do the same (#3405)
  const allowed = sessionMenuActions(s);
  return [
    ...(allowed.includes("attach") ? [{ key: "attach", label: t("chat-chatview.attach"), icon: "link" as const, onSelect: () => void attachLive(s) }] : []),
    { key: "pin", label: o.pinned ? t("agentmenus.unpin") : t("agentmenus.pin"), icon: "pin", accel: "p", onSelect: () => void flipPin(s) },
    ...(sessionCan(s, "send") && !s.source_kind ? [{ key: "rename", label: t("agentmenus.rename"), icon: "pencil" as const, accel: "r", onSelect: () => onRename(s) }] : []),
    { key: "mute", label: o.muted ? t("agentmenus.unmute") : t("agentmenus.mute"), icon: o.muted ? "bell" : "bellOff", onSelect: () => void flipMute(s) },
    { key: "persona", label: t("agentmenus.hand_to_persona"), icon: "spark", onSelect: () => void handToPersona(s, machineWithHub(s.machine_id, getState().machines)) },
    // on a todo, the same dialog moves it to another (the hub releases the old link), so it says so (#2348)
    ...(onLinkTodo ? [{ key: "link-todo", label: s.todo ? t("agentmenus.move_to_other_todo") : t("agentmenus.link_to_todo"), icon: "todo" as const, divider: true, onSelect: () => onLinkTodo(s) }] : []),
    ...(onLinkTodo && s.todo ? [{ key: "unlink-todo", label: t("agentmenus.unlink"), icon: "x" as const, onSelect: () => void unlinkTodo(s, s.todo!.id) }] : []),
    ...(s.cwd ? [{ key: "cwd", label: t("agentmenus.copy_folder"), icon: "folder" as const, accel: "c", onSelect: () => copyText(s.cwd) }] : []),
    ...(allowed.includes("resume") ? [{ key: "resume", label: t("agentmenus.copy_resume"), icon: "terminal" as const, onSelect: () => copyText(resumeCommand(s)) }] : []),
    ...(onCloseAgent && allowed.includes("close") ? [{ key: "close", label: t("chat-chatview.close_agent_menu"), icon: "x" as const, danger: true, onSelect: () => onCloseAgent(s) }] : []),
    ...archiveItems(s, o.features ?? getState().features, onArchiveClose),
    ...(deleteInsteadOfArchive(o.features ?? getState().features) && sessionDeletable(s) ? [{ key: "delete", label: t("agentmenus.delete_chat"), icon: "trash" as const, danger: true, onSelect: () => onDelete(s) }] : []),
  ];
}

/** "Archive" and "Archive and close…" (lib/agent-archives.ts): a busy agent shows them disabled, saying why, so the
 *  user knows they exist and what to wait for. The chat's ⋯ offers the same. `onArchiveClose` asks first. */
export function archiveItems(s: Session, features: readonly string[], onArchiveClose?: (s: Session) => void): MenuItem[] {
  const offers = archiveOffers(s, features);
  return [
    ...(offers.archive !== "no"
      ? [{ key: "archive", label: t("agentmenus.archive"), icon: "archive" as const, divider: true, disabled: offers.archive === "busy", detail: offers.archive === "busy" ? t("agentmenus.archive_busy") : undefined, onSelect: () => void archiveAgent(s, false, appArchiveDeps) }]
      : []),
    ...(onArchiveClose && offers.close !== "no"
      ? [{ key: "archive-close", label: t("agentmenus.archive_close"), icon: "archive" as const, disabled: offers.close === "busy", detail: offers.close === "busy" ? t("agentmenus.archive_close_busy") : undefined, onSelect: () => onArchiveClose(s) }]
      : []),
  ];
}

async function unlinkTodo(s: Session, todo: string) {
  try {
    await unlinkAgentFromTodo(todo, s.machine_id, s.session_id);
    await refreshTodos();
    await refresh();
    toast(t("agentmenus.unlinked"), "ok");
  } catch (e) {
    toast(t("agentmenus.unlink_failed", { error: errorText(e) }), "error");
  }
}

/** The row's menu, then what any pane offers (copy its ids, split, git, close): src/lib/node-actions.ts. The path and the
 *  rename are the row's own above. `at`: where a right click opened it (the pointer); without it, beside `anchor`. */
export function AgentRowMenu({ s, anchor, at, onClose, ...o }: { s: Session; anchor: HTMLElement; at?: MenuAt; onClose: () => void; onRename: (s: Session) => void; onDelete: (s: Session) => void; onLinkTodo?: (s: Session) => void; onCloseAgent?: (s: Session) => void; onArchiveClose?: (s: Session) => void }) {
  const pinned = useStore((st) => isPinned(s, st.pins));
  const muted = useStore((st) => isMuted(s, st.mutes));
  const features = useStore((st) => st.features);
  const online = useStore((st) => st.machines.find((m) => m.id === s.machine_id)?.online !== false);
  return <NodeMenu node={sessionNode(s, online)} at={at ?? { anchor }} onClose={onClose} before={agentRowMenuItems(s, { pinned, muted, features, ...o })} omit={["node-copy-path", "node-rename"]} />;
}

export function SpaceMenu({
  sp,
  anchor,
  at,
  fold,
  onClose,
  onRename,
  onMarkSeen,
  onLinkTodo,
  openPage = true,
}: {
  sp: SpaceLike;
  anchor: HTMLElement;
  /** Where a right click opened it (the pointer); without it, beside `anchor`. */
  at?: MenuAt;
  /** The space's fold, when it has one to toggle. */
  fold?: MenuNode["fold"];
  onClose: () => void;
  onRename: (s: Session) => void;
  onMarkSeen: (list: Session[]) => void;
  onLinkTodo?: (sp: SpaceLike) => void;
  /** "Open space page" (/space/…) leads the menu; the space's own page leaves it out. */
  openPage?: boolean;
}) {
  const pins = useStore((st) => st.pins);
  const mutes = useStore((st) => st.mutes);
  const todos = useStore((st) => st.todos);
  const todosSupported = useStore((st) => st.todosSupported);
  const todosLoaded = useStore((st) => st.todosLoaded);
  const spacesSupported = useStore((st) => st.spacesSupported);
  const pinned = isSpacePinned(sp.machineId, sp.name, pins);
  const muted = isSpaceMuted(sp, mutes);
  const unread = unreadOf(sp.sessions);
  const first = sp.sessions[0];
  const online = useStore((st) => st.machines.find((m) => m.id === sp.machineId)?.online !== false);

  const supportSpaces = todosLoaded && todosSupported !== false && spacesSupported !== false;
  const linkedTodo = supportSpaces ? findTodoForSpace(todos, sp.machineId, sp.name) : undefined;

  const page = { name: "space" as const, mid: sp.machineId, space: sp.name };
  const items: MenuItem[] = [
    ...(openPage && first?.space_id ? [{ key: "open", label: t("agentmenus.open_space"), icon: "layers" as const, onSelect: () => navigate(page) }] : []),
    ...(unread.length ? [{ key: "seen", label: markAllSeenLabel(unread.length), icon: "check" as const, onSelect: () => onMarkSeen(unread) }] : []),
    ...(first?.space_id && !first.source_kind ? [{ key: "rename", label: t("agentmenus.rename_space"), icon: "pencil" as const, onSelect: () => onRename(first) }] : []),
    ...(supportSpaces
      ? linkedTodo
        ? [
            {
              key: "relink-todo",
              label: t("agentmenus.move_to_other_todo"),
              icon: "todo" as const,
              onSelect: () => onLinkTodo?.(sp),
            },
            {
              key: "unlink-todo",
              label: t("agentmenus.remove_from_todo"),
              icon: "x" as const,
              onSelect: async () => {
                try {
                  await unlinkSpaceFromTodo(linkedTodo.id, sp.machineId, sp.name);
                  await refreshTodos();
                  await refresh();
                  toast(t("agentmenus.removed_from_todo"), "ok");
                } catch (e) {
                  if (isLegacySpacesRouteError(e)) {
                    setSpacesSupported(false);
                  }
                  toast(t("agentmenus.remove_failed", { error: errorText(e) }), "error");
                  void refreshTodos();
                }
              },
            },
          ]
        : [
            {
              key: "link-todo",
              label: t("agentmenus.attach_to_todo"),
              icon: "todo" as const,
              onSelect: () => onLinkTodo?.(sp),
            },
          ]
      : []),
    { key: "pin", label: pinned ? t("agentmenus.unpin_space") : t("agentmenus.pin_space"), icon: "pin", onSelect: () => void flipSpacePin(sp.machineId, sp.name) },
    { key: "mute", label: muted ? t("agentmenus.unmute_space") : t("agentmenus.mute_space"), icon: muted ? "bell" : "bellOff", onSelect: () => void flipSpaceMute(sp.machineId, sp.name) },
  ];
  const node: MenuNode = { kind: "workspace", mid: sp.machineId, online, host: first?.space_kind, label: sp.name, workspaceId: first?.workspace_id ?? first?.space_id, paneId: first?.pane_id, cwd: first?.cwd, fold };
  return <NodeMenu node={node} at={at ?? { anchor }} onClose={onClose} header={t("agentmenus.space_header", { name: sp.name })} before={items} omit={["node-rename"]} />;
}

/** "Mark all as seen", asked first: it changes what the other devices show too. */
export function MarkSeenDialog({ list, onClose, onDone }: { list: Session[] | null; onClose: () => void; onDone?: () => void }) {
  const n = list?.length ?? 0;
  return (
    <Dialog
      open={Boolean(list)}
      onClose={onClose}
      title={markAllSeenLabel()}
      width={380}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            tone="primary"
            icon="check"
            onClick={() => {
              if (list) markSeen(list);
              onDone?.();
              onClose();
            }}
          >
            {markAllSeenLabel(n)}
          </Button>
        </>
      }
    >
      <p className="text-[13px] leading-relaxed text-ink-2">{t("agentmenus.mark_all_seen_body", { count: n })}</p>
    </Dialog>
  );
}
