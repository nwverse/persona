// The Workbench's thread frame (#13439, docs/web/web.md "Workbench (Labs)"): around the conversation the shell already
// draws (ChatView, unchanged) it adds a thin bar with the project's name and two toggles, the thread's changes on the
// right (the Review panel, or the changes of the conversation where the hub has no Git review) and the thread's terminal
// in a drawer below. Nothing here reads or writes the hub beyond what those panels already do.
import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { repoOf } from "../../lib/agent-repos";
import { CHAT_MIN, PANEL_MIN } from "../../lib/layout";
import { usePanel } from "../../lib/panel";
import { registerContextCommands } from "../../lib/palette";
import { openNewAgent } from "../../lib/new-agent";
import { canShowTerminal } from "../../lib/session-view";
import { getSession, hubHas, useStore } from "../../lib/store";
import { REVIEW_FEATURE } from "../../lib/types";
import { readWorkbenchLayout, takeWorkbenchKey, useWorkbenchLayout, writeWorkbenchLayout } from "../../lib/labs-workbench";
import { cx, IconButton, Loading } from "../../ui/atoms";
import { Empty } from "../session/files/parts";
import { ResizeHandle } from "../../ui/ResizeHandle";
import { PageBoundary } from "../PageBoundary";

// The panels are the usual ones, each its own chunk: this frame stays small.
const ReviewPanel = lazy(() => import("../session/ReviewPanel").then((m) => ({ default: m.ReviewPanel })));
const TerminalPanel = lazy(() => import("../session/TerminalPanel").then((m) => ({ default: m.TerminalPanel })));

export const DIFF_DEFAULT = 520;
export const DRAWER_DEFAULT = 280;
export const DRAWER_MIN = 160;
export const DRAWER_STEP = 16;
/** The drawer's share of the column at most. */
export const DRAWER_MAX_SHARE = 0.6;

/** The changes region's width: the one asked for (or the default) within what leaves the conversation CHAT_MIN. */
export function diffWidth(want: number, room: number): number {
  return Math.round(Math.max(PANEL_MIN, Math.min(want || DIFF_DEFAULT, room > 0 ? room - CHAT_MIN : Infinity)));
}

/** The changes region fits beside the conversation: false folds it away (it keeps its flag) instead of squeezing the chat. */
export const diffFits = (room: number): boolean => room <= 0 || room >= CHAT_MIN + PANEL_MIN;

/** The drawer's height within DRAWER_MIN and its share of the column. */
export function drawerHeight(want: number, column: number): number {
  const max = column > 0 ? Math.max(DRAWER_MIN, column * DRAWER_MAX_SHARE) : Infinity;
  return Math.round(Math.max(DRAWER_MIN, Math.min(want || DRAWER_DEFAULT, max)));
}

function useRoom(ref: React.RefObject<HTMLElement | null>): number {
  const [room, setRoom] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const measure = () => setRoom(el.getBoundingClientRect().width);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return room;
}

function DrawerResize({ height, column, onResize }: { height: number; column: number; onResize: (h: number) => void }) {
  const max = column > 0 ? Math.max(DRAWER_MIN, column * DRAWER_MAX_SHARE) : 2000;
  const drag = useRef<(() => void) | null>(null);
  useEffect(() => () => drag.current?.(), []);
  const start = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const y = e.clientY;
    const from = height;
    const move = (ev: PointerEvent) => onResize(drawerHeight(from + (y - ev.clientY), column));
    const up = () => {
      removeEventListener("pointermove", move);
      removeEventListener("pointerup", up);
      removeEventListener("pointercancel", up);
      drag.current = null;
    };
    drag.current?.();
    drag.current = up;
    addEventListener("pointermove", move);
    addEventListener("pointerup", up);
    addEventListener("pointercancel", up);
  };
  return (
    <div
      role="separator"
      aria-orientation="horizontal"
      aria-label={t("workbench.resize_drawer")}
      aria-valuemin={DRAWER_MIN}
      aria-valuemax={Math.round(max)}
      aria-valuenow={height}
      tabIndex={0}
      onPointerDown={start}
      onDoubleClick={() => onResize(0)}
      onKeyDown={(e) => {
        const dir = e.key === "ArrowUp" ? 1 : e.key === "ArrowDown" ? -1 : 0;
        if (!dir) return;
        e.preventDefault();
        onResize(drawerHeight(height + dir * DRAWER_STEP, column));
      }}
      className="h-1 shrink-0 cursor-row-resize bg-line/0 transition-colors hover:bg-line focus-visible:bg-line"
    />
  );
}

export function WorkbenchThread({ mid, sid, children }: { mid: string; sid: string; children: React.ReactNode }) {
  const layout = useWorkbenchLayout();
  const session = useStore(() => getSession(mid, sid));
  const git = useStore((s) => hubHas(s.features, REVIEW_FEATURE));
  const cwd = session?.cwd ?? "";
  const project = repoOf(cwd, session?.repo_root);
  const canTerm = canShowTerminal(session);
  const frame = useRef<HTMLDivElement>(null);
  const column = useRef<HTMLDivElement>(null);
  const room = useRoom(frame);
  // the shell's own right panel (Team, Files, Review, Side) takes the right edge while it is open: never two at once
  const shellPanel = usePanel();
  const columnHeight = useRoomHeight(column);

  const toggleDiff = useCallback(() => writeWorkbenchLayout({ diff: !readWorkbenchLayout().diff }), []);
  const toggleDrawer = useCallback(() => writeWorkbenchLayout({ drawer: !readWorkbenchLayout().drawer }), []);
  const canTermNow = useRef(canTerm);
  canTermNow.current = canTerm;

  const cwdNow = useRef(cwd);
  cwdNow.current = cwd;
  const repoNow = useRef(project.repo);
  repoNow.current = project.repo;

  // the usual chords keep their keys and mean the same here: ⌘B the right panel, ⌘J the terminal
  useEffect(() => {
    const offs = [
      takeWorkbenchKey("right-panel", () => void toggleDiff()),
      takeWorkbenchKey("terminal-chat", () => (canTermNow.current ? void toggleDrawer() : false)),
      registerContextCommands((ctx) =>
        ctx.pane
          ? [
              { id: "workbench:diff", section: t("workbench.section"), title: t("workbench.cmd_diff"), icon: "sidebar", run: toggleDiff },
              ...(canTermNow.current ? [{ id: "workbench:terminal", section: t("workbench.section"), title: t("workbench.cmd_terminal"), icon: "terminal" as const, run: toggleDrawer }] : []),
              ...(cwdNow.current ? [{ id: "workbench:new-thread", section: t("workbench.section"), title: t("workbench.cmd_new_thread"), icon: "compose" as const, run: () => openNewAgent({ machineId: mid, cwd: repoNow.current || cwdNow.current }) }] : []),
            ]
          : [],
      ),
    ];
    return () => offs.reverse().forEach((off) => off());
  }, [toggleDiff, toggleDrawer, mid]);
  const showDiff = layout.diff && diffFits(room) && !shellPanel;
  const showDrawer = layout.drawer && canTerm;
  const diffW = diffWidth(layout.diffWidth, room);
  const drawerH = drawerHeight(layout.drawerHeight, columnHeight);
  const wait = <div className="flex flex-1 items-center justify-center"><Loading /></div>;

  return (
    <div ref={frame} data-workbench-thread-frame className="flex h-full min-h-0 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-line px-3">
        <span className="min-w-0 flex-1 truncate text-label text-ink-3">{project.name}</span>
        <IconButton icon="sidebar" size={26} iconSize={14} label={t("workbench.toggle_diff")} pressed={showDiff} onClick={toggleDiff} />
        {canTerm && <IconButton icon="terminal" size={26} iconSize={14} label={t("workbench.toggle_terminal")} pressed={layout.drawer} onClick={toggleDrawer} />}
      </div>
      <div className="flex min-h-0 flex-1">
        <div ref={column} className="flex min-w-0 flex-1 flex-col">
          <div className="relative min-h-0 flex-1">{children}</div>
          {showDrawer && (
            <section aria-label={t("workbench.drawer_label")} data-workbench-drawer className="flex shrink-0 flex-col border-t border-line bg-page" style={{ height: drawerH }}>
              <DrawerResize height={drawerH} column={columnHeight} onResize={(h) => writeWorkbenchLayout({ drawerHeight: h })} />
              <div className="flex min-h-0 flex-1 flex-col">
                <PageBoundary page={`workbench-terminal:${mid}/${sid}`}>
                  <Suspense fallback={wait}>
                    <TerminalPanel key={`${mid}/${sid}`} mid={mid} sid={sid} cwd={cwd || undefined} embedded onClose={toggleDrawer} />
                  </Suspense>
                </PageBoundary>
              </div>
            </section>
          )}
        </div>
        {showDiff && (
          <aside
            id="workbench-diff"
            aria-label={t("workbench.diff_label")}
            data-workbench-diff
            className={cx("relative flex shrink-0 flex-col border-l border-line bg-page")}
            style={{ width: diffW }}
          >
            <ResizeHandle
              edge="left"
              value={diffW}
              min={PANEL_MIN}
              max={diffWidth(Infinity, room)}
              onCommit={(w) => writeWorkbenchLayout({ diffWidth: w })}
              onReset={() => writeWorkbenchLayout({ diffWidth: 0 })}
              label={t("workbench.resize_diff")}
              controls="workbench-diff"
            />
            <PageBoundary page={`workbench-diff:${mid}/${sid}`}>
              <Suspense fallback={wait}>
                {git && cwd ? <ReviewPanel key={`${mid}/${sid}`} mid={mid} sid={sid} cwd={cwd} onClose={toggleDiff} />
                  : <Empty icon="fileDiff" title={t("workbench.diff_unavailable")} />}
              </Suspense>
            </PageBoundary>
          </aside>
        )}
      </div>
    </div>
  );
}

function useRoomHeight(ref: React.RefObject<HTMLElement | null>): number {
  const [h, setH] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const measure = () => setH(el.getBoundingClientRect().height);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return h;
}
