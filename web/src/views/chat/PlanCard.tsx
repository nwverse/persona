// The agent's plan (TodoWrite) in the chat, as cindy draws it (TodoListCard.tsx: InlinePlanCard and TodoListCard; the
// owner asked on 2026-11-12 that it follow cindy): a card where the plan was made that opens in place to the whole list,
// and, while that card is out of view, a pill above the composer whose flyout shows the list again (PlanPill).
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useReducer, useRef, useState, type CSSProperties, type RefObject } from "react";
import { createPortal } from "react-dom";
import { t } from "../../i18n";
import { spinStyle } from "../../lib/busy-motion";
import type { Segment, Step } from "../../lib/turns";
import { isPlanStep, planProgress, planTodos, type Todo } from "../../lib/work-groups";
import { cx, EASE, EXIT_MS, useExit } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { MOVE_MS } from "../../ui/motion";

/** The chat's newest plan: the latest TodoWrite of two or more items in its newest exchange that has one, also when the
 *  hub folded it away with the work around it (the fold keeps the list, #5602). `superseded`: a message of the user's
 *  came after the exchange it was made in (cindy's `isPlanUserBoundary`), so it is an earlier turn's plan. */
export function latestPlan(segs: Segment[]): { id: string; todos: Todo[]; superseded: boolean } | undefined {
  let later = false;
  for (let i = segs.length - 1; i >= 0; i--) {
    for (let k = segs[i].steps.length - 1; k >= 0; k--) {
      const st = segs[i].steps[k];
      if (st.kind === "tool" && isPlanStep(st) && !st.result?.is_error && planTodos(st).length >= 2) return { id: st.id, todos: planTodos(st), superseded: later };
      if (st.kind === "fold") {
        const snap = st.item.fold?.tasks?.findLast((t) => t.replace === "todo" && t.entries.length >= 2);
        if (snap) return { id: snap.source_item_id, todos: snap.entries.map((e) => ({ content: e.subject ?? "", status: todoStatus(e.status) })).filter((t) => t.content), superseded: later };
      }
    }
    if (startsTurn(segs[i])) later = true;
  }
  return undefined;
}

/** The step of an exchange that draws its plan card: its latest TodoWrite of two or more items that the tool took
 *  (cindy draws one card per plan, where its latest list was written, and none for a list of one). "" for none. The
 *  other plan updates stay in the summary as ordinary rows, each inspectable (#2631). */
export function planCardStep(steps: Step[]): string {
  for (let i = steps.length - 1; i >= 0; i--) {
    const s = steps[i];
    if (s.kind === "tool" && isPlanStep(s) && !s.result?.is_error && planTodos(s).length >= 2) return s.id;
  }
  return "";
}

/** A message the user wrote starts a turn of their own; what Persona typed in, another session sent, a supervision round
 *  or a message still queued does not (cindy's synthetic rows: an automation, a resume, a subagent). */
function startsTurn(seg: Segment): boolean {
  const u = seg.user;
  return !!u && !u.injected && !u.from && !u.source && !u.queued && !seg.supervision;
}

/** Whether the plan card `id` is out of the scroll view (scrolled past, not reached, or not drawn at all). */
export function usePlanOutOfView(scroller: RefObject<HTMLDivElement | null>, id?: string): boolean {
  const [out, setOut] = useState(false);
  // the chat's scroller can be drawn again in a new element (the page wraps it differently once it knows the agent has a
  // terminal to switch to): watch the one on the page, not the first one seen
  const [sc, setSc] = useState<HTMLDivElement | null>(null);
  useLayoutEffect(() => {
    if (scroller.current !== sc) setSc(scroller.current);
  });
  useEffect(() => {
    if (!sc || !sc.firstElementChild || !id) return setOut(false);
    const find = () => sc.querySelector(`[data-plan-id="${CSS.escape(id)}"]`);
    return typeof IntersectionObserver === "undefined" ? watchByScroll(sc, find, setOut) : watchByIntersection(sc, id, find, setOut);
  }, [sc, id]);
  return out;
}

/** Calls `changed` when the chat's content box (the scroller's first child) changes size, or is replaced by a new one,
 *  which is watched from then on. */
function watchBody(sc: Element, changed: () => void): () => void {
  let body = sc.firstElementChild;
  const ro = new ResizeObserver(changed);
  if (body) ro.observe(body);
  const mo = typeof MutationObserver === "undefined" ? null : new MutationObserver(() => {
    if (sc.firstElementChild === body) return;
    if (body) ro.unobserve(body);
    body = sc.firstElementChild;
    if (body) ro.observe(body);
    changed();
  });
  mo?.observe(sc, { childList: true });
  return () => {
    ro.disconnect();
    mo?.disconnect();
  };
}

/** The browser says when the card enters or leaves the view, so a scroll or a growing chat queries nothing and reads no
 *  layout (reading the rect of a card in an exchange that content-visibility skips lays that exchange out once the chat's
 *  width changed, #2485). Edge-adjacent counts as intersecting, the same rule as the measuring fallback. The card is
 *  looked for again only once the one watched left the DOM, or while none is drawn: the body's ResizeObserver sees a card
 *  that came or went; a card redrawn at the same size in view sends its last report as it leaves; one redrawn at the same
 *  size out of view sends none (it was not intersecting and still is not), so a scroll checks that the watched card is
 *  still in the DOM, which reads no layout, and looks for the new one at most once. */
function watchByIntersection(sc: Element, id: string, find: () => Element | null, setOut: (out: boolean) => void): () => void {
  let el: Element | null = null;
  const current = () => el?.isConnected && el.getAttribute("data-plan-id") === id;
  const refind = () => {
    if (current()) return;
    if (el) io.unobserve(el);
    el = find();
    // not drawn (inside a folded summary, or folded away by the hub with the work around it): the pill is where it shows
    if (!el) return setOut(true);
    io.observe(el);
  };
  // entries of a card no longer watched may still be queued: only the watched card's latest one counts
  const io = new IntersectionObserver((entries) => {
    const last = entries.findLast((e) => e.target === el);
    if (!last) return;
    if (current()) setOut(!last.isIntersecting);
    else refind();
  }, { root: sc });
  // while none is drawn, a scroll looks for nothing: a card that comes back changes the body's size
  const onScroll = () => {
    if (el && !current()) refind();
  };
  refind();
  sc.addEventListener("scroll", onScroll, { passive: true });
  const unwatch = watchBody(sc, refind);
  return () => {
    sc.removeEventListener("scroll", onScroll);
    unwatch();
    io.disconnect();
  };
}

/** Without IntersectionObserver (jsdom): measure the card against the view on every scroll and resize. */
function watchByScroll(sc: Element, find: () => Element | null, setOut: (out: boolean) => void): () => void {
  const check = () => {
    const el = find();
    if (!el) return setOut(true);
    const r = el.getBoundingClientRect();
    const v = sc.getBoundingClientRect();
    setOut(r.bottom < v.top || r.top > v.bottom);
  };
  check();
  sc.addEventListener("scroll", check, { passive: true });
  const unwatch = watchBody(sc, check);
  return () => {
    sc.removeEventListener("scroll", check);
    unwatch();
  };
}

const todoStatus = (s?: string): Todo["status"] => (s === "completed" || s === "in_progress" ? s : "pending");

/** What Close Plan closed, per chat, for this page's life (cindy keeps it in the view's state, not on disk): the plan as
 *  it was then, so the pill comes back once the plan changes (a step done, a new plan), as cindy's snapshot does. */
const closedPlans = new Map<string, string>();

const planSnapshot = (plan: { id: string; todos: Todo[] }) => JSON.stringify([plan.id, plan.todos.map((todo) => [todo.status, todo.content])]);

/** Whether the pill of chat `chat`'s plan was closed with Close Plan, and the close itself. */
export function usePlanClosed(chat: string, plan: { id: string; todos: Todo[] } | undefined): [boolean, () => void] {
  const snapshot = useMemo(() => (plan ? planSnapshot(plan) : ""), [plan]);
  const [, redraw] = useReducer((n: number) => n + 1, 0);
  const close = useCallback(() => {
    if (!snapshot) return;
    closedPlans.set(chat, snapshot);
    redraw();
  }, [chat, snapshot]);
  return [!!snapshot && closedPlans.get(chat) === snapshot, close];
}

// ── the marks (cindy's lucide Circle, CircleCheck and loader arc, in the 24-unit stroke grammar of ui/icons.tsx) ──

const RING = { cx: 12, cy: 12, r: 10 };

function Svg({ size, stroke, className, children }: { size: number; stroke: number; className?: string; children: React.ReactNode }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={stroke} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false" className={className}>
      {children}
    </svg>
  );
}

function DoneMark({ size = 18, stroke = 1.5, className = "text-tool-ink" }: { size?: number; stroke?: number; className?: string }) {
  return (
    <Svg size={size} stroke={stroke} className={cx("shrink-0", className)}>
      <circle {...RING} />
      <path d="m9 12 2 2 4-4" />
    </Svg>
  );
}

/** One turn of the arc, while the turn runs (cindy's spinner cycle). */
const ARC_TURN_MS = 1000;

/** The step in progress: an arc on the same circle as a pending step's, with a gap. It turns only while the turn runs
 *  (an interrupted plan must not look busy), and never with reduced motion: unlike the spinners it does not carry
 *  BUSY_MOTION (lib/busy-motion.ts), as cindy's arc stands still there; its shape alone says "in progress". The turning
 *  is on the box around the SVG, the SVG itself stays still (cindy's Spinner). */
function ActiveMark({ live }: { live?: boolean }) {
  return (
    <span className="inline-flex shrink-0 items-center justify-center text-ink" data-plan-arc={live ? "turning" : "still"} style={live ? spinStyle(ARC_TURN_MS) : undefined}>
      <Svg size={18} stroke={1.5}>
        <path d="M22 12a10 10 0 1 1-6.91-9.51" />
      </Svg>
    </span>
  );
}

function PendingMark() {
  return (
    <Svg size={18} stroke={1.5} className="shrink-0 text-ink">
      <circle {...RING} />
    </Svg>
  );
}

function TodoMark({ status, live }: { status: Todo["status"]; live?: boolean }) {
  return status === "completed" ? <DoneMark /> : status === "in_progress" ? <ActiveMark live={live} /> : <PendingMark />;
}

/** The pill's: how far along the current step is, a still ring (it says where the plan is, not that something runs). Its
 *  change moves (MOVE_MS); with reduced motion the stylesheet takes every transition to 1 ms, so it jumps. */
function ProgressRing({ progress }: { progress: number }) {
  const size = 16;
  const stroke = 2;
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const p = Math.min(1, Math.max(0, progress));
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true" focusable="false" className="shrink-0" data-plan-ring>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--tool-ink)" strokeWidth={stroke} opacity={0.45} />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke="var(--ink)"
        strokeWidth={stroke}
        strokeLinecap="round"
        strokeDasharray={c}
        strokeDashoffset={c * (1 - p)}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
        style={{ transition: `stroke-dashoffset ${MOVE_MS}ms ${EASE}` }}
      />
    </svg>
  );
}

// ── the card ──

/** The plan in the chat (cindy's InlinePlanCard): a head, `⌄ ☰ 2/5 · <the step in progress, else the last>`, that opens
 *  and closes the whole list in place. It starts open, and is not remembered; closed, a 2 px bar under the head says how
 *  far along it is. Like every tool call it sits inside its exchange's work summary (super rule 2; cindy draws it in the
 *  open, #5162). */
export function PlanCard({ id, todos, live }: { id?: string; todos: Todo[]; live?: boolean }) {
  const [open, setOpen] = useState(true);
  if (!todos.length) return null;
  const done = todos.filter((todo) => todo.status === "completed").length;
  const total = todos.length;
  const head = todos.find((todo) => todo.status === "in_progress")?.content ?? todos[total - 1].content;
  return (
    <div className="flex w-full justify-start" data-plan-card data-plan-id={id}>
      <div className="max-w-full min-w-0 overflow-hidden rounded-[12px] border border-line bg-surface">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
          className="flex w-full min-w-0 cursor-pointer items-center gap-2 px-[14px] py-[10px] transition-opacity select-none hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset [@media(pointer:coarse)]:min-h-10"
        >
          <Icon name={open ? "chevron" : "chevronRight"} size={14} stroke={2} className="shrink-0 text-tool-ink" />
          <Icon name="todo" size={16} stroke={2} className="shrink-0 text-ink" />
          <span className="shrink-0 font-mono text-[13px] leading-none font-medium text-ink tabular-nums" data-plan-count>
            {done}/{total}
          </span>
          <span className="shrink-0 text-[13px] leading-none text-tool-ink">·</span>
          <span className="mt-px min-w-0 truncate text-[13px] leading-none text-ink" data-plan-head>
            {head}
          </span>
        </button>
        {open ? (
          <div className="border-t border-line px-[14px] pt-[10px] pb-[12px]">
            <div className="flex flex-col gap-[2px]" data-plan-list>
              {todos.map((todo, i) => (
                <div key={`${todo.content}:${i}`} className="flex min-h-[30px] items-center gap-[10px]" data-todo={todo.status}>
                  <TodoMark status={todo.status} live={live} />
                  <span className={cx("min-w-0 text-[13px] leading-[1.45] break-words", todo.status === "completed" ? "text-tool-ink" : todo.status === "in_progress" ? "font-medium text-ink" : "text-ink")}>{todo.content}</span>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="h-[2px] w-full bg-line" data-plan-progress>
            <div className="h-full bg-ink" style={{ width: `${(done / total) * 100}%`, transition: `width ${MOVE_MS}ms ${EASE}` }} />
          </div>
        )}
      </div>
    </div>
  );
}

// ── the pill above the composer ──

type Reveal = "closed" | "hover" | "pinned";

/** The flyout's gap above the pill, and the band over it that keeps the pointer "over" while it crosses (cindy's mb-2
 *  and h-3). */
const FLYOUT_GAP = 8;
const HOVER_BRIDGE = 12;
/** cindy's flyout widths: at least 220, at most 420 and the screen less its margins. */
const FLYOUT_MIN = 220;
const FLYOUT_MAX = 420;
const SCREEN_MARGIN = 16;

interface Place {
  /** The flyout's center and bottom edge, on screen. */
  x: number;
  bottom: number;
  maxWidth: number;
  /** The pill's own box: the hover band sits on its top edge. */
  pill: { left: number; top: number; width: number };
}

/** Where the flyout goes while it shows: centered on the composer's column (cindy centers it on the composer, so a pill
 *  pushed aside by something next to it still opens in the middle), its bottom a little above the pill. It is drawn on
 *  the page's body, since what holds the pill scrolls and would clip it. Placed again when the window or the column
 *  under it changes. */
function useFlyoutPlace(shown: boolean, pill: RefObject<HTMLElement | null>): Place | null {
  const [place, setPlace] = useState<Place | null>(null);
  useLayoutEffect(() => {
    const el = pill.current;
    if (!shown || !el) return;
    const column = el.closest("[data-chat-dock]") ?? el.parentElement ?? el;
    const measure = () => {
      const r = el.getBoundingClientRect();
      const c = column.getBoundingClientRect();
      const cs = getComputedStyle(column);
      const inner = c.width - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
      const x = c.left + (parseFloat(cs.paddingLeft) || 0) + inner / 2;
      const screen = document.documentElement.clientWidth || innerWidth;
      setPlace({ x, bottom: r.top - FLYOUT_GAP, maxWidth: Math.max(0, Math.min(FLYOUT_MAX, inner > 0 ? inner : FLYOUT_MAX, screen - 2 * SCREEN_MARGIN)), pill: { left: r.left, top: r.top, width: r.width } });
    };
    measure();
    const vv = window.visualViewport;
    // the box over the composer scrolls once what is in it is taller than it may be: the pill moves with it
    const box = el.closest("[data-scroll-box]");
    addEventListener("resize", measure);
    vv?.addEventListener("resize", measure);
    box?.addEventListener("scroll", measure, { passive: true });
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    ro?.observe(column);
    return () => {
      removeEventListener("resize", measure);
      vv?.removeEventListener("resize", measure);
      box?.removeEventListener("scroll", measure);
      ro?.disconnect();
    };
  }, [shown, pill]);
  return shown ? place : null;
}

/** Above the composer while the newest plan's card is out of view (cindy's TodoListCard): a still ring of how far along
 *  the current step is (a check once all are done) and "Step 3 of 5". Hovering opens the whole list in a flyout above it
 *  and leaving closes it; a click pins it open, and a second click, a click outside or Esc closes it. The flyout's ✕
 *  (Close Plan, `onClose`) hides the pill for this plan; the agent's plan itself is untouched. `live`: the turn runs, so
 *  the step in progress turns. */
export function PlanPill({ todos, live, onClose }: { todos: Todo[]; live?: boolean; onClose?: () => void }) {
  const flyoutId = useId();
  const wrap = useRef<HTMLDivElement>(null);
  const pill = useRef<HTMLButtonElement>(null);
  const flyout = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const [mode, setMode] = useState<Reveal>("closed");
  const revealed = mode !== "closed";
  const exit = useExit(revealed, EXIT_MS.card, () => [card.current?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: EXIT_MS.card, easing: "ease-in", fill: "forwards" })]);
  const place = useFlyoutPlace(exit.mounted, pill);

  useEffect(() => {
    if (mode !== "pinned") return;
    // the flyout is on the body, not inside the pill's box: a press on it is not outside
    const inside = (n: EventTarget | null) => n instanceof Node && (!!wrap.current?.contains(n) || !!flyout.current?.contains(n));
    const down = (e: PointerEvent) => {
      if (!inside(e.target)) setMode("closed");
    };
    // before the chat's own Esc (which stops a running turn) and the composer's: this Esc only closes the flyout
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.isComposing) return;
      e.stopPropagation();
      setMode("closed");
    };
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("keydown", key, true);
    return () => {
      document.removeEventListener("pointerdown", down, true);
      document.removeEventListener("keydown", key, true);
    };
  }, [mode]);

  if (!todos.length) return null;
  const p = planProgress(todos);
  // The flyout is drawn on the body, so the browser's Tab order would reach its ✕ only at the end of the page: Tab goes
  // from the pill to the ✕ while it shows, and from the ✕ on to what comes after the pill (cindy's DOM order).
  const tabIn = (e: React.KeyboardEvent) => {
    if (e.key !== "Tab" || e.shiftKey || !revealed || !closeButton.current) return;
    e.preventDefault();
    closeButton.current.focus();
  };
  const tabOut = (e: React.KeyboardEvent) => {
    if (e.key !== "Tab") return;
    // back to the pill; going forward the browser then moves on from it, as if the ✕ came right after it
    pill.current?.focus();
    if (e.shiftKey) e.preventDefault();
  };
  return (
    <div className="flex justify-center">
      <div
        ref={wrap}
        className="inline-flex items-center justify-center"
        onMouseEnter={() => setMode((m) => (m === "closed" ? "hover" : m))}
        onMouseLeave={() => setMode((m) => (m === "hover" ? "closed" : m))}
      >
        <button
          ref={pill}
          type="button"
          onClick={() => setMode((m) => (m === "pinned" ? "closed" : "pinned"))}
          onKeyDown={tabIn}
          aria-controls={flyoutId}
          aria-expanded={revealed}
          className="touch-hit flex cursor-pointer items-center gap-2 rounded-full border border-line bg-surface px-[14px] py-[6px] whitespace-nowrap transition-opacity select-none hover:opacity-80"
          data-plan-pill
        >
          {p.allDone ? <DoneMark size={14} stroke={2} className="text-ink" /> : <ProgressRing progress={p.total ? p.step / p.total : 0} />}
          <span className="text-[13px] leading-none text-ink tabular-nums">{t("chat-plancard.step_of", { step: p.step, total: p.total })}</span>
        </button>
        {exit.mounted &&
          place &&
          createPortal(
            <>
              {/* the band between the pill and the flyout: crossing it, the pointer is still over the pill */}
              <div aria-hidden className="fixed z-30" style={{ left: place.pill.left, top: place.pill.top - HOVER_BRIDGE, width: place.pill.width, height: HOVER_BRIDGE }} />
              <div
                ref={flyout}
                id={flyoutId}
                data-plan-flyout={exit.leaving ? "leaving" : mode}
                className="fixed z-30 w-max"
                style={{ left: place.x, top: place.bottom, transform: "translate(-50%, -100%)", minWidth: Math.min(FLYOUT_MIN, place.maxWidth), maxWidth: place.maxWidth } as CSSProperties}
              >
                <div
                  ref={card}
                  aria-hidden={!revealed}
                  className="relative w-full overflow-hidden rounded-[12px] border border-line bg-surface"
                  style={{ animation: `pop-in 160ms ${EASE} both`, transformOrigin: "bottom center" }}
                >
                  {onClose && revealed && (
                    <button
                      ref={closeButton}
                      type="button"
                      onClick={onClose}
                      onKeyDown={tabOut}
                      aria-label={t("chat-plancard.close")}
                      title={t("chat-plancard.close")}
                      className="absolute top-2 right-2 z-10 flex size-7 items-center justify-center rounded-full text-tool-ink transition-colors hover:bg-tool-hover hover:text-ink active:scale-[0.98]"
                      data-plan-close
                    >
                      <Icon name="x" size={14} stroke={2} />
                    </button>
                  )}
                  <div className={cx("flex max-h-[280px] flex-col gap-[2px] overflow-y-auto overscroll-contain py-[10px] pl-[14px]", onClose ? "pr-10" : "pr-[14px]")} data-plan-list>
                    {todos.map((todo, i) => (
                      <div key={i} className="flex h-[30px] items-center gap-[10px]" data-todo={todo.status}>
                        <TodoMark status={todo.status} live={live} />
                        <span className={cx("mt-px min-w-0 truncate text-[13px]", todo.status === "completed" ? "text-tool-ink" : todo.status === "in_progress" ? "font-semibold text-ink" : "text-ink")}>{todo.content}</span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            </>,
            document.body,
          )}
      </div>
    </div>
  );
}
