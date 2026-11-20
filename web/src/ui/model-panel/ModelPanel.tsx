// Shared launcher-first model column (#10249). Selection and setup controls stay in Row and Flyout.
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useReducer, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import "./ModelPanel.css";
import { t } from "../../i18n";
import { afterBack, useCloseOnBack } from "../../lib/back-close";
import {
  engineChoices,
  engineSetup,
  panelSections,
  rowBlock,
  rowCustomizable,
  type PanelConfig,
  type PanelPreset,
  type PanelOverride,
  type PanelProvider,
  type PanelRow,
  type PanelScope,
  type PanelState,
} from "../../lib/model-panel";
import { launcherSections, launcherSearch, newAgentShortcuts, quickPicks, unavailableCatalog, type ShortcutAliases } from "../../lib/launcher-column";
import { navigate } from "../../lib/router";
import type { LauncherQuotaAnswer } from "../../lib/quota";
import { hasFinePointer, keyboardFrame } from "../../lib/viewport";
import { settingsHref } from "../../views/settings/nav";
import { cx, Spinner } from "../atoms";
import { Icon } from "../icons";
import { NARROW_QUERY, returnFocus, SHEET_QUERY, useMedia } from "../overlay";
import { Flyout, type FlyoutMode } from "./Flyout";
import { LauncherRow } from "./LauncherRow";
import { Row } from "./Row";
import { usePanelStore } from "./store";

export interface ModelPanelProps {
  /** The element it opens from (the pill); the panel opens above it, right edges aligned. */
  anchor: HTMLElement;
  onClose: () => void;
  providers: PanelProvider[];
  state: PanelState;
  scope: PanelScope;
  /** Applies a whole setup (a row, a favorite, a recent entry); true when it was applied: the panel then records it in
   *  Recent and closes. false: the caller has said why; the panel stays open with the old setup. */
  onPick: (config: PanelConfig) => Promise<boolean>;
  /** A flyout edit (effort, Fast) on the model that runs now: applied at once, the flyout stays open. */
  onApplyCurrent?: (patch: { effort?: string; fast?: boolean }) => Promise<boolean>;
  /** Reads a provider's list again (its group's Retry). */
  onReload?: (provider: string) => void;
  /** Forgets a typed model ID (#1644). */
  onForget?: (provider: string, model: string) => void;
  /** Drawn inside the flyout of the model that runs now, under its controls (Claude Code's ultracode switch, #3428). */
  currentExtra?: ReactNode;
  /** The launcher's existing quota answer, read by the chat for this computer; unknown providers have no entry. */
  quotas?: Readonly<Record<string, LauncherQuotaAnswer>>;
  /** A strip above the search (a Persona chat's "Following the default" / "Picked for this chat", #5033). */
  header?: ReactNode;
  /** A row on top of the list, in no group (Cindy's "follow" row): following a default instead of a model of one's own
   *  (New Agent: Model settings › New agent). Checked while `active`; a click applies it, and the panel closes once it did. */
  follow?: {
    active: boolean;
    label: string;
    detail?: string;
    warning?: string;
    onFollow: () => boolean | Promise<boolean>;
  };
  /** Ready-made setups listed as a group after the favorites (New Agent's presets); a pick of one goes to `onPreset`. */
  presets?: PanelPreset[];
  newAgent?: { setupKey: (setup: PanelConfig | null, preset?: string) => string | undefined; profile?: string };
  onPreset?: (key: string) => boolean | Promise<boolean>;
  /** The flyout lists the other engines its model runs with here (other providers listing it) and picks one on a click. */
  engines?: boolean;
  /** "How to fix" after the reason under a provider that can't take a pick (New Agent's launcher that failed its check);
   *  the panel closes first. */
  onHelp?: (provider: string) => void;
  /** "Configure models" instead of opening Settings › Model providers from here (a dialog that closes first). */
  onConfigure?: () => void;
  /** A dialog the caller opened about the pick on its way is above the panel: the panel sits under its backdrop and
   *  doesn't close on its clicks, focus or Escape (they are the dialog's). */
  held?: boolean;
}

/** The body's height (rail and list) when the window has room; the panel's own cap (Cindy's max-h). */
const BODY = 428;
const BODY_MIN = 160;
const PANEL_MAX = 560;
const GAP = 6;
const EDGE = 8;
/** A star just saved stays filled this long on the row it was saved from. */
const STAR_FLASH_MS = 700;

interface Geometry {
  left: number;
  top: number;
  body: number;
  /** The first placement's clip from the pill's shape (the opening animation); set once. */
  from?: string;
  shift?: number;
}

const ROWS = "[data-panel-row], [data-panel-launcher], [data-panel-unavailable]";
const rowsOf = (list: HTMLElement | null) => (list ? [...list.querySelectorAll<HTMLElement>(ROWS)] : []);

export function ModelPanel({ anchor, onClose, providers, state, scope, onPick, onApplyCurrent, onReload, onForget, quotas, currentExtra, header, follow, presets, newAgent, onPreset, engines, onHelp, onConfigure, held }: ModelPanelProps): ReactNode {
  const store = usePanelStore(scope);
  const listId = useId();
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set(!newAgent && state.current ? [state.current.provider] : []));
  const [showUnavailable, setShowUnavailable] = useState(false);
  const [query, setQuery] = useState("");
  // families unfolded during this opening ("Earlier versions"); a new opening starts folded again
  const [openFamilies, setOpenFamilies] = useState<ReadonlySet<string>>(() => new Set());
  const searching = query.trim() !== "";
  const setupKey = newAgent?.setupKey;
  const hasFollow = Boolean(follow);
  const input = useMemo(
    () => ({
      providers,
      state,
      query,
      favorites: store.favorites,
      recent: store.recent,
      overrides: store.overrides,
      presets,
      openFamilies,
    }),
    [providers, state, query, store.favorites, store.recent, store.overrides, presets, openFamilies],
  );
  const sections = useMemo(() => launcherSections(input), [input]);
  const savedRows = useMemo(() => quickPicks(input, setupKey ? Infinity : 3), [input, setupKey]);
  const found = useMemo(() => (searching ? launcherSearch(input) : []), [input, searching]);
  const presetRows = useMemo(() => panelSections({ ...input, view: { kind: "all" } }).find((s) => s.kind === "presets")?.rows ?? [], [input]);
  const shortcuts = useMemo(() => setupKey && newAgentShortcuts(savedRows, presetRows, setupKey, hasFollow), [savedRows, presetRows, setupKey, hasFollow]);
  const quick = shortcuts?.quick ?? savedRows;
  const visiblePresets = shortcuts?.presets ?? presetRows;
  const foldedShortcuts = useMemo(
    () => setupKey ? [...savedRows.slice(0, 3), ...presetRows].filter((row) => unavailableCatalog(row.provider)) : [],
    [savedRows, presetRows, setupKey],
  );
  const profileHeading = newAgent?.profile ? t("ui-modelpanel.profile_presets", { name: newAgent.profile }) : t("ui-modelpanel.presets");
  const matches = providers.filter((p) =>
    query
      .trim()
      .toLowerCase()
      .split(/\s+/)
      .every((word) => [p.name, p.id, p.engineLabel].some((value) => value?.toLowerCase().includes(word))),
  );
  const available = (p: PanelProvider) => !newAgent || !unavailableCatalog(p);
  const unavailable = sections.filter((s) => !available(s.provider!) && (!searching || matches.includes(s.provider!) || found.some((row) => row.provider.id === s.provider!.id)));
  const toggleLauncher = (id: string) => {
    setFly(null);
    setExpanded((now) => {
      const next = new Set(now);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  };
  const toggleFamily = (key: string) =>
    setOpenFamilies((now) => {
      const next = new Set(now);
      if (!next.delete(key)) next.add(key);
      return next;
    });
  const rows = useMemo(
    () => {
      const visible = (p: PanelProvider) => !setupKey || !unavailableCatalog(p) || showUnavailable;
      if (searching) return found.filter((row) => visible(row.provider));
      const expandedRows = sections.filter((s) => visible(s.provider!) && expanded.has(s.provider!.id)).flatMap((s) => s.rows);
      return [...quick, ...visiblePresets, ...(showUnavailable ? foldedShortcuts : []), ...expandedRows];
    },
    [searching, found, quick, visiblePresets, foldedShortcuts, sections, expanded, setupKey, showUnavailable],
  );

  const panel = useRef<HTMLDivElement>(null);
  const body = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const live = Boolean(onApplyCurrent);
  const phone = useMedia(SHEET_QUERY);
  const narrow = useMedia(NARROW_QUERY);

  // a pick on its way (everything waits); a flyout edit of the model that runs now on its way (its new value shows)
  const [picking, setPicking] = useState(false);
  const [applying, setApplying] = useState<{
    key: string;
    patch: PanelOverride;
  } | null>(null);
  const [fly, setFly] = useState<{
    key: string;
    el: HTMLElement;
    keyboard: boolean;
  } | null>(null);
  const [flash, setFlash] = useState("");
  const closeFly = useCallback(() => setFly(null), []);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const flyRow = fly ? rows.find((r) => r.key === fly.key) : undefined;
  // a row that went (a search, another view, a favorite removed) takes its flyout with it; so does a dialog over the panel
  useEffect(() => {
    if (fly && (held || !flyRow || !fly.el.isConnected)) setFly(null);
  }, [fly, flyRow, held]);

  useEffect(() => {
    if (!flash) return;
    const timer = setTimeout(() => setFlash(""), STAR_FLASH_MS);
    return () => clearTimeout(timer);
  }, [flash]);

  // ── where it sits ──
  const [geo, setGeo] = useState<Geometry | null>(null);
  const [width, setWidth] = useState(0);
  // placed again after every render; this asks for one when only the window moved
  const [, replace] = useReducer((n: number) => n + 1, 0);
  useLayoutEffect(() => {
    const el = panel.current;
    const b = body.current;
    if (!el || !b) return;
    // as wide as it has been: a search or another view never makes it narrower while it is open
    if (el.offsetWidth > width) setWidth(el.offsetWidth);
    const a = anchor.getBoundingClientRect();
    const frame = narrow ? keyboardFrame(innerHeight, window.visualViewport) : undefined;
    const lo = (frame?.top ?? 0) + EDGE;
    const hi = frame ? frame.top + frame.height - EDGE : innerHeight - EDGE;
    const chrome = el.offsetHeight - b.offsetHeight;
    const natural = chrome + BODY;
    const cap = Math.min(PANEL_MAX, innerHeight - 120);
    const above = a.top - GAP - lo;
    const below = hi - (a.bottom + GAP);
    const up = above >= Math.min(natural, cap) || above >= below;
    const room = up ? above : below;
    const bodyH = Math.max(BODY_MIN, Math.min(natural, Math.max(cap, BODY_MIN + chrome), room) - chrome);
    const h = chrome + bodyH;
    const w = Math.max(el.offsetWidth, width);
    const left = Math.max(EDGE, Math.min(a.right - w, innerWidth - w - EDGE));
    let top = up ? a.top - GAP - h : a.bottom + GAP;
    top = Math.max(lo, Math.min(top, hi - h));
    setGeo((was) => {
      if (was && was.left === left && was.top === top && was.body === bodyH) return was;
      if (was) return { ...was, left, top, body: bodyH };
      // the first placement: the opening grows out of the pill (its shape at the panel's edge nearest to it)
      const shift = up ? a.bottom - (top + h) : a.top - top;
      const inset = (v: number, max: number) => Math.max(0, Math.min(max, Math.round(v)));
      const it = inset(a.top - (top + shift), h);
      const ib = inset(top + shift + h - a.bottom, h - it);
      const il = inset(a.left - left, w);
      const ir = inset(left + w - a.right, w - il);
      return {
        left,
        top,
        body: bodyH,
        shift,
        from: `inset(${it}px ${ir}px ${ib}px ${il}px round ${Math.round(a.height / 2)}px)`,
      };
    });
  });

  // the keyboard sliding in on a phone, a window that changes height: placed again; one that changes width: closed
  useEffect(() => {
    let w = innerWidth;
    const resize = () => {
      if (innerWidth !== w) {
        w = innerWidth;
        onClose();
      } else replace();
    };
    const vv = window.visualViewport;
    const again = () => replace();
    addEventListener("resize", resize);
    vv?.addEventListener("resize", again);
    vv?.addEventListener("scroll", again);
    return () => {
      removeEventListener("resize", resize);
      vv?.removeEventListener("resize", again);
      vv?.removeEventListener("scroll", again);
    };
  }, [onClose]);

  // ── closing from outside ──
  const latest = useRef({ fly, onClose, held });
  latest.current = { fly, onClose, held };
  // On a narrow screen Back closes it, its flyout with it, and stays where it opened
  useCloseOnBack(true, onClose, narrow);
  // Going elsewhere from it (Configure models, a provider's How to fix) waits for its entry to come off first: the caller
  // may also close a sheet under it and navigate, which would otherwise leave the sheet's entry under the new page.
  const leave = (go: () => void) => {
    onClose();
    if (narrow) afterBack(go);
    else go();
  };
  const help = onHelp && ((provider: string) => leave(() => onHelp(provider)));
  useEffect(() => {
    const inside = (n: Node | null) => Boolean(n && (panel.current?.contains(n) || anchor.contains(n) || (n instanceof Element && n.closest("[data-model-flyout]"))));
    const down = (e: PointerEvent) => {
      if (!latest.current.held && !inside(e.target as Node | null)) latest.current.onClose();
    };
    const focus = (e: FocusEvent) => {
      if (!latest.current.held && !inside(e.target as Node | null)) latest.current.onClose();
    };
    const key = (e: globalThis.KeyboardEvent) => {
      if (latest.current.held || e.key !== "Escape" || e.isComposing || e.keyCode === 229) return;
      e.preventDefault();
      e.stopPropagation();
      const { fly: open, onClose: close } = latest.current;
      if (open) {
        setFly(null);
        if (open.el.isConnected) open.el.focus({ preventScroll: true });
        return;
      }
      close();
      if (panel.current) returnFocus(panel.current, anchor);
    };
    addEventListener("pointerdown", down, true);
    document.addEventListener("focusin", focus);
    addEventListener("keydown", key, true);
    return () => {
      removeEventListener("pointerdown", down, true);
      document.removeEventListener("focusin", focus);
      removeEventListener("keydown", key, true);
    };
  }, [anchor]);

  // the search has the focus once it shows (a touch screen keeps its keyboard down until the search is tapped)
  const placed = Boolean(geo);
  useEffect(() => {
    if (placed && hasFinePointer()) search.current?.focus({ preventScroll: true });
  }, [placed]);

  // Keep the follow row and launcher summaries visible on opening; a new search starts at the top.
  const viewKey = query.trim().toLowerCase();
  useLayoutEffect(() => {
    if (list.current) list.current.scrollTop = 0;
  }, [viewKey]);

  // Paint the hit row itself: nested groups, scrolling and expansion have different offset parents.
  const hot = useRef<HTMLElement | null>(null);
  const highlight = (row: HTMLElement | null) => {
    if (hot.current === row) return;
    if (hot.current) delete hot.current.dataset.highlighted;
    hot.current = row;
    if (row) row.dataset.highlighted = "";
  };
  const restingRow = (): HTMLElement | null => {
    if (fly?.el.isConnected) return fly.el;
    const a = document.activeElement;
    return a instanceof HTMLElement && a.matches(":focus-visible") ? a.closest(ROWS) : null;
  };
  // Keep the keyboard or flyout highlight when a list read or star redraws its row.
  useLayoutEffect(() => {
    const h = hot.current;
    if (h && !h.isConnected) highlight(null);
    if (!hot.current && fly?.el.isConnected) highlight(fly.el);
  }, [sections, fly]);

  // ── what a row does ──
  // a pick of a row, a preset, an engine in a flyout or the follow row: everything waits for it; once applied, the setup
  // (not the follow row's: it is no model of its own) goes into Recent and the panel closes
  const apply = async (run: () => boolean | Promise<boolean>, setup: PanelConfig | null) => {
    if (picking) return;
    setPicking(true);
    let ok = false;
    try {
      ok = await run();
    } catch {
      ok = false;
    }
    if (ok && setup) store.pushRecent(setup);
    if (!mounted.current) return;
    setPicking(false);
    if (ok) onClose();
  };
  const pick = (row: PanelRow) => apply(() => (row.kind === "preset" && row.preset && onPreset ? onPreset(row.preset.key) : onPick(row.setup)), row.setup);

  const star = (row: PanelRow) => {
    if (row.kind === "favorite" && row.favorite) store.removeFavorite(row.favorite.uid);
    else if (row.kind === "recent" && row.starredUid) store.removeFavorite(row.starredUid);
    else {
      store.addFavorite(row.setup);
      setFlash(row.key);
    }
  };

  const applyLive = async (row: PanelRow, patch: PanelOverride): Promise<boolean> => {
    if (!onApplyCurrent) return false;
    setApplying({ key: row.key, patch });
    let ok = false;
    try {
      ok = await onApplyCurrent(patch);
    } catch {
      ok = false;
    }
    if (mounted.current) setApplying(null);
    return ok;
  };

  const edit = async (row: PanelRow, patch: PanelOverride) => {
    if (row.kind === "favorite" && row.favorite) return store.updateFavorite(row.favorite.uid, patch);
    if (row.kind !== "model") return;
    if (row.current && live && !(await applyLive(row, patch))) return;
    store.setOverride(row.provider.id, row.model.id, patch);
  };

  const reset = async (row: PanelRow) => {
    if (row.kind !== "model") return;
    if (row.current && live) {
      const patch: PanelOverride = {};
      // An agy start uses its native default when the request omits effort.
      const d = state.starting && row.provider.engine === "agy" ? undefined : row.model.defaultEffort;
      if (d && row.setup.effort && row.setup.effort !== d) patch.effort = d;
      // no default known: back to none, the model's own (an Agent not started yet; a running one sends nothing for it)
      else if (!d && row.setup.effort) patch.effort = "";
      if (row.setup.fast) patch.fast = false;
      if (Object.keys(patch).length && !(await applyLive(row, patch))) return;
    }
    store.resetOverride(row.provider.id, row.model.id);
  };

  const openFlyout = (row: PanelRow, el: HTMLElement, toggle: boolean, keyboard: boolean) => {
    if (toggle && fly?.key === row.key) return setFly(null);
    setFly({ key: row.key, el, keyboard });
  };

  // ── keys ──
  const focusRow = (to: HTMLElement | undefined) => {
    if (!to) return;
    if (list.current) delete list.current.dataset.pointer;
    to.focus({ preventScroll: true });
    to.scrollIntoView?.({ block: "nearest" });
    highlight(to.getAttribute("aria-disabled") ? null : to);
  };
  const searchKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing || e.keyCode === 229) return;
    const all = rowsOf(list.current);
    if (e.key === "ArrowDown" || (e.key === "Tab" && !e.shiftKey)) {
      if (!all.length) return;
      e.preventDefault();
      focusRow(all.find((r) => r.hasAttribute("data-current")) ?? all[0]);
    } else if (e.key === "Enter" && searching) {
      // a search and Enter: the first row that can be picked (a typed ID's "Use …" too)
      e.preventDefault();
      const first = rows.find((r) => !rowBlock(r.provider, state, r.current));
      if (first) void pick(first);
    }
  };
  const listKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const all = rowsOf(list.current);
    const at = all.indexOf(e.target as HTMLElement);
    if (at < 0) return;
    e.preventDefault();
    if (e.key === "ArrowUp" && at === 0) return search.current?.focus();
    focusRow(all[Math.max(0, Math.min(all.length - 1, at + (e.key === "ArrowDown" ? 1 : -1)))]);
  };

  const second = (row: PanelRow): { text: string; describes?: boolean } => {
    if (row.kind === "typed") return { text: `${row.provider.name} · ${t("ui-modelpanel.use_typed_note")}` };
    if (row.kind === "preset") {
      const detail = row.preset?.description || row.model.name;
      return { text: detail === row.provider.name || detail.startsWith(`${row.provider.name} · `) ? detail : `${row.provider.name} · ${detail}` };
    }
    if (row.model.custom) return { text: `${row.provider.name} · ${t("ui-modelpanel.typed_before")}` };
    if (!searching && row.kind === "model" && providers.some((p) => p.id !== row.provider.id && p.models?.some((m) => m.name === row.model.name))) return { text: row.provider.name };
    if (!searching && row.kind === "model") return { text: row.model.description ?? "", describes: true };
    return { text: row.provider.name };
  };

  const renderRow = (row: PanelRow) => {
    const customizable = rowCustomizable(row, state, live);
    const removes = row.kind === "favorite" || (row.kind === "recent" && Boolean(row.starredUid));
    const two = second(row);
    return (
      <Row
        key={row.key}
        row={row}
        asButton
        currentCheck={newAgent ? row.current && !follow?.active && row.kind === "model" : undefined}
        aliases={aliasLine(shortcuts?.aliases.get(row.key), profileHeading)}
        second={two.text}
        keepSecond={searching || row.kind === "recent" || row.kind === "favorite"}
        describes={two.describes || row.kind === "preset"}
        blocked={rowBlock(row.provider, state, row.current)}
        customizable={customizable}
        flyoutOpen={fly?.key === row.key}
        starred={removes || flash === row.key}
        starRemoves={removes}
        frozen={picking}
        onPick={() => void pick(row)}
        onStar={() => star(row)}
        onCustomize={(el, toggle) => openFlyout(row, el, toggle, false)}
        onCustomizeKey={(el) => openFlyout(row, el, false, true)}
        onForget={onForget && (() => onForget(row.provider.id, row.model.id))}
      />
    );
  };
  const renderLauncher = (s: (typeof sections)[number]) => (
    <div key={s.key} role="group" aria-label={s.provider!.name} data-panel-section="provider">
      <LauncherRow
        provider={s.provider!}
        state={state}
        quota={quotas?.[s.provider!.id]}
        expanded={!searching && expanded.has(s.provider!.id)}
        frozen={picking}
        onToggle={() => {
          if (searching) { setQuery(""); setExpanded((now) => new Set([...now, s.provider!.id])); }
          else toggleLauncher(s.provider!.id);
        }}
        onReload={onReload}
        onHelp={help}
      />
      {newAgent && !searching && !available(s.provider!) && presetRows.some((r) => r.provider.id === s.provider!.id) && (
        <div role="group" aria-label={profileHeading} data-panel-section="presets">
          <SectionHeading>{profileHeading}</SectionHeading>
          {presetRows.filter((r) => r.provider.id === s.provider!.id).map(renderRow)}
        </div>
      )}
      {!searching && foldedShortcuts.filter((r) => r.kind !== "preset" && r.provider.id === s.provider!.id).map(renderRow)}
      {!searching && expanded.has(s.provider!.id) && (
        <div className="ml-2 border-l border-line pl-1">
          {s.loading && (
            <div role="status" className="flex items-center gap-2 px-3 py-2 text-[12px] text-ink-3">
              <Spinner size={12} />
              {t("ui-modelpanel.loading")}
            </div>
          )}
          {s.blocks
            ? s.blocks.map((b) =>
                b.family ? (
                  <div key={b.key} role="group" aria-label={b.family.label} data-panel-family={b.family.key}>
                    <div className="flex min-w-0 items-center gap-2 px-3 pt-1.5 pb-0.5">
                      <span aria-hidden className="min-w-0 truncate text-submeta leading-[1.4] text-ink-3">
                        {b.family.label}
                      </span>
                      {b.older > 0 && (
                        <button
                          type="button"
                          aria-expanded={Boolean(b.open)}
                          aria-label={
                            b.open
                              ? t("ui-modelpanel.hide_older_of", {
                                  family: b.family.label,
                                })
                              : t("ui-modelpanel.older_of", {
                                  family: b.family.label,
                                  count: b.older,
                                })
                          }
                          data-panel-fold={b.key}
                          disabled={picking}
                          onClick={() => toggleFamily(b.key)}
                          className="ml-auto flex shrink-0 items-center gap-0.5 rounded-control px-1.5 py-0.5 text-submeta leading-[1.4] whitespace-nowrap text-ink-3 transition-colors duration-150 outline-none enabled:hover:bg-hover-2 enabled:hover:text-ink focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed"
                        >
                          {b.open
                            ? t("ui-modelpanel.hide_older")
                            : t("ui-modelpanel.older", {
                                count: b.older,
                              })}
                          <Icon name="chevron" size={11} className={cx("shrink-0 transition-transform duration-150", b.open && "rotate-180")} />
                        </button>
                      )}
                    </div>
                    {b.rows.map(renderRow)}
                  </div>
                ) : (
                  <div key={b.key}>{b.rows.map(renderRow)}</div>
                ),
              )
            : s.rows.map(renderRow)}
        </div>
      )}
    </div>
  );
  const flyMode = (row: PanelRow): FlyoutMode => (row.kind === "favorite" ? "favorite" : row.customized ? "customized" : "recommended");
  const loading = sections.some((s) => s.loading);
  const empty = searching ? !found.length && !matches.length && !loading : !sections.length;
  const notes = [
    state.busy && {
      icon: "info" as const,
      text: state.busy,
      tone: "text-ink-3",
    },
    state.note && {
      icon: "warning" as const,
      text: state.note,
      tone: "text-orange",
    },
  ].filter(Boolean) as {
    icon: "info" | "warning";
    text: string;
    tone: string;
  }[];

  const room = phone ? "calc(100vw - 16px)" : "calc(100vw - 48px)";
  const style: CSSProperties & Record<string, string | number | undefined> = {
    left: geo?.left ?? 0,
    top: geo?.top ?? 0,
    minWidth: width ? `max(300px, min(${width}px, ${room}))` : undefined,
    maxWidth: `min(460px, ${room})`,
    visibility: geo ? undefined : "hidden",
    "--model-panel-from": geo?.from,
    "--model-panel-shift": geo?.shift === undefined ? undefined : `${geo.shift}px`,
    animation: geo ? "model-panel-morph var(--model-panel-morph) var(--model-panel-ease) backwards" : undefined,
  };

  return createPortal(
    <>
      {/* under a dialog's backdrop (z-40) while one asks about the pick, above the page otherwise */}
      <div
        ref={panel}
        role="dialog"
        aria-label={t("ui-modelpanel.list")}
        data-model-panel
        data-held={held || undefined}
        className={cx("fixed flex w-max min-w-[300px] flex-col overflow-hidden rounded-[12px] bg-surface text-ink shadow-overlay", held ? "z-30" : "z-50")}
        style={style}
      >
        <div
          className="flex min-h-0 flex-col"
          style={{
            animation: geo ? "model-panel-content var(--model-panel-fade) var(--model-panel-ease) var(--model-panel-fade-delay) backwards" : undefined,
          }}
        >
          {header && <div className="shrink-0">{header}</div>}
          <div className="flex shrink-0 items-center gap-2 border-b border-line px-3.5 py-3 text-ink-3">
            <Icon name="search" size={14} className="shrink-0" />
            <input
              ref={search}
              type="text"
              value={query}
              onChange={(e) => {
                setFly(null);
                setQuery(e.target.value);
              }}
              onKeyDown={searchKey}
              placeholder={t("ui-modelpanel.search")}
              aria-label={t("ui-modelpanel.search")}
              aria-controls={listId}
              spellCheck={false}
              autoComplete="off"
              className="min-w-0 flex-1 bg-transparent text-[13px] text-ink outline-none placeholder:text-ink-3"
            />
          </div>
          <div ref={body} className="flex min-h-0 min-w-0 shrink" style={{ height: geo?.body ?? BODY }}>
            <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
              <div
                ref={list}
                id={listId}
                role="group"
                aria-label={t("ui-modelpanel.list")}
                aria-busy={picking || undefined}
                onKeyDown={listKey}
                onPointerMove={(e) => {
                  e.currentTarget.dataset.pointer = "";
                  highlight(null);
                }}
                onPointerLeave={(e) => { delete e.currentTarget.dataset.pointer; highlight(restingRow()); }}
                onFocus={(e) => {
                  const row = e.target as HTMLElement;
                  if (row.matches(":focus-visible")) { delete e.currentTarget.dataset.pointer; highlight(row.closest(ROWS)); }
                }}
                onBlur={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) highlight(null); }}
                className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto overscroll-contain p-2 pb-3 [scrollbar-gutter:stable]"
              >
                {follow && <FollowRow follow={follow} aliases={aliasLine(shortcuts?.aliases.get("follow"), profileHeading)} frozen={picking} onPick={() => void apply(follow.onFollow, null)} />}
                {notes.map((n) => (
                  <div key={n.icon} role="note" className={cx("flex w-0 min-w-full items-start gap-1.5 px-2.5 pt-1.5 pb-1 text-[11px] leading-[1.5]", n.tone)}>
                    <Icon name={n.icon} size={12} className="mt-[2.5px] shrink-0" />
                    <span className="min-w-0 break-words">{n.text}</span>
                  </div>
                ))}
                {empty ? (
                  <div className="w-0 min-w-full px-3 py-6 text-center text-[13px] text-ink-3">{t("ui-modelpanel.no_match")}</div>
                ) : (
                  <>
                    {!searching && quick.length > 0 && (
                      <div role="group" aria-label={t("ui-modelpanel.quick_picks")} data-panel-quick className="mb-1 border-b border-line pb-1">
                        {newAgent && <SectionHeading>{quick.some((r) => r.kind === "favorite") ? t("ui-modelpanel.quick_picks") : t("ui-modelpanel.recent")}</SectionHeading>}
                        {quick.map(renderRow)}
                      </div>
                    )}
                    {!searching && visiblePresets.length > 0 && (
                      <div role="group" aria-label={profileHeading} data-panel-section="presets">
                        {newAgent && <SectionHeading>{profileHeading}</SectionHeading>}
                        {visiblePresets.map(renderRow)}
                      </div>
                    )}
                    {!searching && newAgent && sections.some((s) => available(s.provider!)) && <SectionHeading>{t("ui-modelpanel.launchers")}</SectionHeading>}
                    {searching ? (
                      <>
                        {matches.filter(available).map((p) => (
                          <LauncherRow
                            key={p.id}
                            provider={p}
                            state={state}
                            quota={quotas?.[p.id]}
                            expanded={false}
                            frozen={picking}
                            onToggle={() => {
                              setQuery("");
                              setExpanded((now) => new Set([...now, p.id]));
                            }}
                            onReload={onReload}
                            onHelp={help}
                          />
                        ))}
                        {found.filter((r) => available(r.provider)).map(renderRow)}
                        {loading && <div role="status" className="flex items-center gap-2 px-3 py-2 text-label text-ink-3"><Spinner size={12} />{t("ui-modelpanel.loading")}</div>}
                      </>
                    ) : (
                      sections.filter((s) => available(s.provider!)).map(renderLauncher)
                    )}
                    {unavailable.length > 0 && (
                      <div data-panel-unavailable-group className="mt-1 border-t border-line pt-1">
                        <button type="button" data-panel-unavailable aria-expanded={showUnavailable} disabled={picking}
                          onClick={() => setShowUnavailable((v) => !v)}
                          className="flex min-h-11 w-full items-center justify-between rounded-control px-3 text-label font-medium text-ink-2 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent">
                          {t("ui-modelpanel.unavailable_count", { count: unavailable.length })}
                          <Icon name="chevron" size={12} className={cx("shrink-0", showUnavailable && "rotate-180")} />
                        </button>
                        {showUnavailable && unavailable.map(renderLauncher)}
                        {showUnavailable && searching && found.filter((r) => !available(r.provider)).map(renderRow)}
                      </div>
                    )}
                  </>
                )}
              </div>
            </div>
          </div>
          <div className="flex shrink-0 items-center border-t border-line px-3.5 py-[9px]">
            <button
              type="button"
              onClick={() => leave(() => {
                if (onConfigure) onConfigure();
                else navigate(settingsHref("models", null, phone, null, "providers"));
              })}
              className="flex min-w-0 items-center gap-1.5 text-[13px] whitespace-nowrap text-ink-2 transition-colors duration-150 hover:text-ink"
            >
              <Icon name="plus" size={14} className="shrink-0" />
              <span className="truncate">{t("ui-modelpanel.configure")}</span>
            </button>
          </div>
        </div>
      </div>
      {fly && flyRow && panel.current && (
        <Flyout
          rowEl={fly.el}
          panelEl={panel.current}
          row={flyRow}
          setup={applying?.key === flyRow.key ? { ...flyRow.setup, ...applying.patch } : flyRow.setup}
          mode={flyMode(flyRow)}
          starred={flyRow.kind === "favorite" || flash === flyRow.key}
          disabled={picking || Boolean(applying)}
          focusOnOpen={fly.keyboard}
          reposition={sections}
          extra={flyRow.kind === "model" && flyRow.current ? currentExtra : undefined}
          engines={engines ? engineChoices(providers, flyRow, state) : undefined}
          onEngine={(choice) => {
            const setup = engineSetup(choice, flyRow.setup);
            void apply(() => onPick(setup), setup);
          }}
          onEffort={(effort) => void edit(flyRow, { effort })}
          onFast={(on) => void edit(flyRow, { fast: on })}
          onReset={() => void reset(flyRow)}
          onStar={() => star(flyRow)}
          onClose={closeFly}
        />
      )}
    </>,
    document.body,
  );
}

function SectionHeading({ children }: { children: string }) {
  return <h3 className="px-3 pt-2 pb-1 text-submeta font-semibold text-ink-2">{children}</h3>;
}

function aliasLine(aliases: ShortcutAliases | undefined, profileHeading: string): string | undefined {
  if (!aliases) return undefined;
  return [
    aliases.recent.length && [t("ui-modelpanel.recent"), ...aliases.recent].join(" · "),
    aliases.presets.length && [profileHeading, ...aliases.presets].join(" · "),
  ].filter(Boolean).join(" / ");
}

/** The checked default action remains separate from an explicit model with the same launch parameters. */
function FollowRow({ follow, frozen, aliases, onPick }: { follow: NonNullable<ModelPanelProps["follow"]>; frozen: boolean; aliases?: string; onPick: () => void }) {
  return (
    <>
      <div
        role="button"
        aria-pressed={follow.active}
        aria-disabled={frozen || undefined}
        tabIndex={0}
        data-panel-row="follow"
        data-follow-row
        // followed: the list opens at the top, where this row is (the panel scrolls to the first row marked current)
        data-current={follow.active || undefined}
        onClick={() => !frozen && onPick()}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget || e.nativeEvent.isComposing || (e.key !== "Enter" && e.key !== " ")) return;
          e.preventDefault();
          if (!frozen) onPick();
        }}
        className={cx(
          "group/row relative z-[1] flex min-h-11 w-full shrink-0 items-center gap-2 rounded-control px-3 py-2 text-[14px] leading-[1.43] text-ink outline-none select-none",
          "focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent",
          frozen ? "cursor-not-allowed" : "cursor-pointer",
        )}
      >
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate leading-5 font-medium">{follow.label}</span>
          {(follow.detail || follow.warning) && (
            <span title={[follow.detail, follow.warning].filter(Boolean).join(" · ")} className="flex min-w-0 items-center gap-1 pt-px text-[12px] leading-[1.4] text-ink-2">
              {follow.warning && <Icon name="warning" size={11} className="shrink-0 text-orange" />}
              <span className="truncate">{follow.detail || follow.warning}</span>
            </span>
          )}
          {aliases && <span className="pt-0.5 text-submeta text-ink-2">{aliases}</span>}
        </span>
        {follow.active && <Icon name="check" size={14} stroke={2.5} className="shrink-0 text-ink" />}
      </div>
      <div aria-hidden className="mx-1 my-1 h-px bg-line" />
    </>
  );
}
