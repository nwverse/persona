// The composer's "/" palette (#5325), drawn as cindy's slash command palette at the owner's request (2026-11-12): a
// 320 px panel at the composer's left edge, one 36 px row per command with where it comes from on the right, and the
// focused row's description in a 280 px card beside the panel (inside it on a phone). Commands keep their existing
// ranking and cap priority; matching session skills follow them (#6171). Its own chunk is drawn only once "/" is typed.
import { Fragment, Suspense, useEffect, useId, useImperativeHandle, useLayoutEffect, useMemo, useReducer, useRef, useState, type ComponentProps, type ReactNode, type Ref } from "react";
import type { ComposerCommand as Command } from "../../lib/extension-prompts";
import type { MentionSkill } from "../../lib/skill-mentions";
import { cx, EASE } from "../../ui/atoms";
import { t } from "../../i18n";
import { lazyPage } from "../../lib/lazy-page";
import type { MentionSource } from "./ComposerPanel";

const PANEL_W = 320;
const CARD_W = 280;
/** Between the panel and the card. */
const GAP = 8;
/** Kept from the window's edges. */
const EDGE = 8;
const CARD_MAX = 400;

/** Beside the panel: its top and height in the palette; or in the panel, under the rows. */
type Place = { side: "right" | "left"; top: number; maxHeight: number } | { side: "inside" };
export type Entry = { kind: "command"; command: Command } | { kind: "skill"; skill: MentionSkill };

const samePlace = (a: Place | null, b: Place) =>
  a?.side === b.side && (b.side === "inside" || (a.side !== "inside" && a.top === b.top && a.maxHeight === b.maxHeight));

/** A command as its row names it: without the "/" already typed. */
const commandName = (c: Command) => c.word.replace(/^\//, "");
/** What picking a command types, when that is more than the command itself (Persona's text commands, quick prompts). */
const typed = (c: Command) => (c.text.trim() === c.word ? "" : c.text.trim());
const entryName = (e: Entry) => e.kind === "command" ? commandName(e.command) : e.skill.name;
const entryKey = (e: Entry) => e.kind === "command" ? `command:${e.command.key ?? e.command.word}` : `skill:${e.skill.id}`;

function Description({ entry, id, className }: { entry: Entry; id?: string; className?: string }) {
  const desc = entry.kind === "command" ? entry.command.desc : entry.skill.description;
  const text = entry.kind === "command" ? typed(entry.command) : "";
  return (
    <div id={id} className={className}>
      {desc && <p className="text-ink-2">{desc}</p>}
      {text && <p className={cx("line-clamp-4 whitespace-pre-line text-ink-3", desc && "mt-1")}>{t("chat-slashpalette.types", { text })}</p>}
    </div>
  );
}

export interface SlashPaletteHandle {
  /** A key pressed in the message box: arrows move, Enter / Tab pick. False leaves the key to the composer. */
  key: (key: string) => boolean;
}

export type SlashPaletteInput = {
  /** Commands already ranked by the composer, with the existing whole-list cap applied first. */
  rows: Command[];
  active: number;
  onHover: (i: number) => void;
  onPick: (c: Command) => void;
  /** A press outside `within` (the composer) closes it. */
  onClose: () => void;
  within: React.RefObject<HTMLElement | null>;
  /** What follows the slash; the catalog is read once for this opening, not for each query. */
  query?: string;
  source?: MentionSource;
  machineId?: string;
  onSkillPick?: (skill: MentionSkill) => void;
  handle?: Ref<SlashPaletteHandle>;
};

export type SlashPaletteViewProps = SlashPaletteInput & {
  entries: Entry[];
  status?: ReactNode;
  /** No final empty message before the first skill answer has had a chance to arrive. */
  skillsLoading?: boolean;
  renderSkill?: (skill: MentionSkill, common: ComponentProps<"button">) => ReactNode;
};

export function SlashPaletteView({ entries, active, onHover, onPick, onClose, within, source, onSkillPick, handle, status, skillsLoading = false, renderSkill }: SlashPaletteViewProps) {
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const [glide, setGlide] = useState<{ top: number; height: number } | null>(null);
  const [place, setPlace] = useState<Place | null>(null);
  // the list scrolled or the window changed size: measure again
  const [, remeasure] = useReducer((n: number) => n + 1, 0);
  const current = Math.min(active, Math.max(entries.length - 1, 0));
  const focused = entries[current];

  const choose = (entry: Entry) => {
    if (entry.kind === "command") onPick(entry.command);
    else onSkillPick?.(entry.skill);
  };
  useImperativeHandle(handle, () => ({
    key: (key: string): boolean => {
      if (!entries.length) return false;
      if (key === "ArrowDown" || key === "ArrowUp") {
        onHover((current + (key === "ArrowDown" ? 1 : entries.length - 1)) % entries.length);
        return true;
      }
      if (key === "Enter" || key === "Tab") {
        choose(entries[current]);
        return true;
      }
      return false;
    },
  }));

  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    const down = (e: Event) => {
      if (!within.current?.contains(e.target as Node)) close.current();
    };
    document.addEventListener("pointerdown", down, true);
    addEventListener("resize", remeasure);
    return () => {
      document.removeEventListener("pointerdown", down, true);
      removeEventListener("resize", remeasure);
    };
  }, [within]);

  // the focused row into the list's view, on a key or a new filter only: the wheel may leave it
  useLayoutEffect(() => {
    const l = list.current;
    const row = refs.current[current];
    if (!l || !row) return;
    if (row.offsetTop < l.scrollTop) l.scrollTop = row.offsetTop;
    else if (row.offsetTop + row.offsetHeight > l.scrollTop + l.clientHeight) l.scrollTop = row.offsetTop + row.offsetHeight - l.clientHeight;
  }, [current, entries]);

  // After every render, since the rows, the focus, the list's scroll and the card's own height all move the card; a
  // place that did not change renders nothing more.
  useLayoutEffect(() => {
    const r = root.current;
    const p = panel.current;
    const l = list.current;
    if (!r || !p) return;
    const row = refs.current[current];
    const at = row ? { top: row.offsetTop, height: row.offsetHeight } : null;
    setGlide((cur) => (cur?.top === at?.top && cur?.height === at?.height ? cur : at));
    // the panel's left edge does not move while it grows in (it grows from its bottom left corner)
    const box = r.getBoundingClientRect();
    const right = box.left + PANEL_W + GAP + CARD_W + EDGE <= innerWidth;
    const left = box.left - GAP - CARD_W >= EDGE;
    let next: Place = { side: "inside" };
    if (right || left) {
      // in the palette's own coordinates: the card starts a little above the focused row and never ends below the
      // panel, nor above the window
      const bottom = p.offsetTop + p.offsetHeight;
      const ceiling = EDGE - box.top;
      const rowTop = row && l ? p.offsetTop + l.offsetTop + row.offsetTop - l.scrollTop : p.offsetTop;
      const maxHeight = Math.max(1, Math.min(CARD_MAX, bottom - ceiling));
      const height = Math.min(card.current?.scrollHeight ?? 0, maxHeight);
      const top = Math.round(Math.max(ceiling, Math.min(rowTop - 6, bottom - height)));
      next = { side: right ? "right" : "left", top, maxHeight: Math.round(maxHeight) };
    }
    setPlace((cur) => (samePlace(cur, next) ? cur : next));
  });

  const described = (e: Entry) => Boolean((e.kind === "command" ? e.command.desc || typed(e.command) : e.skill.description));
  return (
    <div ref={root} data-slash-palette className="absolute bottom-full left-0 z-10 mb-2" style={{ animation: `pop-in 150ms ${EASE} both`, transformOrigin: "bottom left" }}>
      <div
        ref={panel}
        className="relative flex flex-col rounded-xl bg-surface p-1.5 shadow-overlay"
        style={{ width: PANEL_W, maxWidth: `calc(100vw - ${2 * EDGE}px)`, maxHeight: `min(${CARD_MAX}px, calc(var(--kb-h) * 0.5))` }}
      >
        {entries.length > 0 ? (
          <div ref={list} role="listbox" aria-label={t(source?.skills ? "chat-slashpalette.label_with_skills" : "chat-slashpalette.label")} onScroll={remeasure} className="relative min-h-0 overflow-y-auto overscroll-contain">
            <span aria-hidden className="pointer-events-none absolute inset-x-0 rounded-control bg-hover" style={{ top: glide?.top ?? 0, height: glide?.height ?? 0, opacity: glide ? 1 : 0, transition: `top 200ms ${EASE}, height 200ms ${EASE}` }} />
            {entries.map((entry, i) => {
              const common = {
                ref: (el: HTMLButtonElement | null) => { refs.current[i] = el; },
                type: "button" as const,
                role: "option",
                "aria-selected": i === current,
                "aria-describedby": described(entry) ? `${id}-${i}` : undefined,
                // the box keeps the focus; a click picks
                onMouseDown: (e: React.MouseEvent) => e.preventDefault(),
                // Late skill rows under a resting pointer do not take the active row; a real move still hovers them.
                onMouseEnter: entry.kind === "command" ? () => onHover(i) : undefined,
                onMouseMove: entry.kind === "skill" ? () => onHover(i) : undefined,
                onClick: () => choose(entry),
              };
              if (entry.kind === "skill") return <Fragment key={entryKey(entry)}>{renderSkill?.(entry.skill, common)}</Fragment>;
              const c = entry.command;
              return (
                <button
                  key={entryKey(entry)}
                  {...common}
                  className={cx(
                    "relative flex h-9 w-full items-center justify-between gap-3 rounded-control px-2.5 text-left text-[14px] leading-[1.43] text-ink [@media(pointer:coarse)]:h-11",
                    i === current ? "font-medium" : "font-normal",
                  )}
                >
                  <span className="min-w-0 truncate">{commandName(c)}</span>
                  {c.source && <span className="max-w-[50%] shrink-0 truncate text-[12px] font-normal text-ink-3">{c.source}</span>}
                </button>
              );
            })}
          </div>
        ) : !skillsLoading ? (
          <div role="status" className="flex h-10 items-center justify-center text-[13px] text-ink-3">
            {t(source?.skills ? "chat-slashpalette.no_match_skills" : "chat-slashpalette.no_match")}
          </div>
        ) : null}
        {place?.side === "inside" && focused && described(focused) && (
          <div data-slash-card aria-hidden className="mt-1.5 shrink-0 border-t border-line px-2.5 pt-2 pb-1 text-[13px] leading-[1.5]">
            <Description entry={focused} />
          </div>
        )}
        {status}
        {/* each row's description for assistive technology; the card and the line above only show the focused one */}
        <div hidden>
          {entries.map((entry, i) => described(entry) && <Description key={entryKey(entry)} entry={entry} id={`${id}-${i}`} />)}
        </div>
      </div>
      {focused && place && place.side !== "inside" && (
        <div
          ref={card}
          data-slash-card
          aria-hidden
          onMouseDown={(e) => e.preventDefault()}
          className="absolute overflow-y-auto rounded-xl bg-surface p-3.5 shadow-overlay"
          style={{ width: CARD_W, left: place.side === "right" ? PANEL_W + GAP : -(GAP + CARD_W), top: place.top, maxHeight: place.maxHeight }}
        >
          <div className="truncate text-[14px] font-medium text-ink">{entryName(focused)}</div>
          <Description entry={focused} className="mt-2 text-[13px] leading-[1.5]" />
        </div>
      )}
    </div>
  );
}

// Only the skill-aware second stage loads on picker intent. Its Suspense fallback is the command palette, so neither a
// catalog nor a chunk request holds back the commands.
const SlashSkillPalette = lazyPage(() => import("./SlashSkillPalette").then((m) => m.SlashSkillPalette));

export function SlashPalette(props: SlashPaletteInput) {
  const commandEntries = useMemo<Entry[]>(() => props.rows.map((command) => ({ kind: "command", command })), [props.rows]);
  const commands = <SlashPaletteView {...props} entries={commandEntries} skillsLoading={Boolean(props.source?.skills)} />;
  if (!props.source?.skills) return commands;
  return (
    <Suspense fallback={commands}>
      <SlashSkillPalette {...props} View={SlashPaletteView} />
    </Suspense>
  );
}
