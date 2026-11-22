import { Fragment, useEffect, useMemo, useState } from "react";
import { t } from "../../i18n";
import { commentKey, addCodeComment, markedLines } from "../../lib/comment-target";
import { codeQuote, diffLabel, rangeLabel, type DiffScope } from "../../lib/comments";
import { hunkRows, hunkSides, splitRows, languageOf, highlightLines, useHighlighter, type DiffRow } from "../../lib/code-view";
import { ignoreWhitespace, wordSpans, overlay } from "../../lib/code-review";
import { useQuotes } from "../../lib/drafts";
import { visibleHunks, type ChangeHunk, type FileChange } from "../../lib/session-changes";
import { copyText, cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { LineBox } from "./files/LineComment";
import "../extensions/code-view.css";
import "./changes-review.css";

export interface ChangeLocation { mid: string; sid: string; cwd?: string; path: string; shown: string; scope: DiffScope }
type Side = "old" | "new";
type Pick = { side: Side; line: number; end: number };

export function ChangeCounts({ add, del }: {add: number; del: number}) {
  return <span className="inline-flex shrink-0 gap-1.5 whitespace-nowrap font-mono text-submeta tabular-nums" aria-label={t("session-changes.counts", {add, del})}>
    <span className="text-diff-add-fg">+{add}</span><span className="text-diff-del-fg">−{del}</span>
  </span>;
}

function ChangeRows({ hunk, lang, split, wrap, whitespace, select, picked, marks }: {
  hunk: ChangeHunk; lang: string; split: boolean; wrap: boolean; whitespace: boolean;
  select?: (side: Side, line: number, shift: boolean) => void; picked: Pick | null;
  marks: {old: Map<number, number>; new: Map<number, number>};
}) {
  const rows = useMemo(() => {
    const original = hunkRows(hunk);
    return whitespace ? ignoreWhitespace(original) : original;
  }, [hunk, whitespace]);
  const words = useMemo(() => wordSpans(rows), [rows]);
  const indices = useMemo(() => new Map(rows.map((row, i) => [row, i])), [rows]);
  const ready = useHighlighter(Boolean(lang));
  const highlights = useMemo(() => {
    if (!ready) return;
    const sides = hunkSides(hunk);
    return {old: highlightLines(sides.old, lang), new: highlightLines(sides.new, lang)};
  }, [ready, hunk, lang]);
  function gutter(r: DiffRow, side: Side) {
    const n = r[side];
    if (!hunk.positioned) return null;
    if (n === undefined || !select) return <span className="changes-gutter">{n ?? ""}</span>;
    return <button type="button" className="changes-gutter" data-marked={marks[side].has(n) || undefined}
      aria-label={t("session-changes.comment_line", {side: t(side === "old" ? "session-changes.before" : "session-changes.after"), line: n})}
      data-side={side} data-line={n} onClick={(e) => select(side, n, e.shiftKey)}>
      <span>{n}</span><Icon name="plus" size={12} />
    </button>;
  }
  function cell(row: DiffRow | undefined, side?: Side) {
    if (!row) return <div className="min-w-0 bg-field/50" />;
    if (row.kind === "note") return <span className="col-span-full px-3 text-ink-3">{t("session-changes.no_newline")}</span>;
    const source: Side = side ?? (row.kind === "del" ? "old" : "new");
    const line = picked && (!side || side === picked.side) ? row[picked.side] : undefined;
    const lit = line !== undefined && picked && line >= Math.min(picked.line, picked.end) && line <= Math.max(picked.line, picked.end);
    const at = source === "old" ? row.oldAt : row.newAt;
    const pieces = overlay(at === undefined ? null : highlights?.[source]?.[at], row.text, words.get(indices.get(row)!));
    return <div className={cx("flex min-w-0 items-baseline", row.kind === "add" && "bg-diff-add-bg", row.kind === "del" && "bg-diff-del-bg", lit && "outline outline-1 -outline-offset-1 outline-accent")}>
      {(!side || side === "old") && gutter(row, "old")}{(!side || side === "new") && gutter(row, "new")}
      <span aria-hidden className={cx("changes-sign", row.kind === "add" ? "text-diff-add-fg" : "text-diff-del-fg")}>{row.kind === "add" ? "+" : row.kind === "del" ? "−" : " "}</span>
      <code className="flex-1 pr-2">{pieces.map((p, i) =>
        <span key={i} className={cx(p.cls, p.changed && (row.kind === "del" ? "rounded-sm bg-diff-del-word" : "rounded-sm bg-diff-add-word"))}>{p.text}</span>)}</code>
    </div>;
  }
  return <div className="changes-hunk code-hl overflow-x-auto font-mono text-[12px] leading-5" data-layout={split ? "split" : "unified"} data-wrap={wrap}>
    <div className={cx(split && "grid grid-cols-2 divide-x divide-line")}>
      {split ? splitRows(rows).map((r, i) => <Fragment key={i}>{r.note ? <span className="col-span-full px-3 text-ink-3">{t("session-changes.no_newline")}</span> : <>{cell(r.old, "old")}{cell(r.new, "new")}</>}</Fragment>) : rows.map((r, i) => <Fragment key={i}>{cell(r)}</Fragment>)}
    </div>
  </div>;
}

export function FileReview({ net, location, split, wrap, context, whitespace, viewed, onViewed, pending, history, historyCount, historyOpen, fromGit, focus }: {
  net: FileChange; location: ChangeLocation; split: boolean; wrap: boolean; context: number; whitespace: boolean; viewed: boolean; onViewed: (v: boolean) => void;
  pending: boolean; history: React.ReactNode; historyCount: number; historyOpen: boolean; fromGit: boolean;
  focus?: { line?: number; to?: number; old?: boolean };
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [dismissedFocus, setDismissedFocus] = useState<typeof focus>();
  const [extra, setExtra] = useState<Record<string, number>>({});
  const [showHistory, setShowHistory] = useState(historyOpen);
  const [pick, setPick] = useState<Pick | null>(null);
  const hunks = useMemo(() => {
    let result = visibleHunks(net.regions, context, net.gitHunks, extra);
    if (!focus?.line) return result;
    // A saved quote can span previously expanded context. Reveal only its missing edges.
    const side = focus.old ? "old" : "new";
    const openedEdges = { ...extra };
    for (const target of new Set([focus.line, focus.to ?? focus.line])) {
      if (result.some((h) => h.positioned && hunkRows(h).some((row) => row[side] === target))) continue;
      const index = net.regions.findIndex((region) => {
        const start = side === "old" ? region.oldStart : region.newStart;
        const length = region.ops.filter((op) => op.kind !== (side === "old" ? "add" : "del")).length;
        return region.positioned && target >= start && target < start + length;
      });
      if (index < 0) continue;
      let nearest: { distance: number; key: string } | undefined;
      for (const h of result) {
        if (!h.positioned || !h.beforeKey.startsWith(index + "/")) continue;
        const nums = hunkRows(h).flatMap((row) => row[side] === undefined ? [] : [row[side]!]);
        if (!nums.length) continue;
        const above = target < nums[0];
        const distance = above ? nums[0] - target : target - nums[nums.length - 1];
        if (distance > 0 && (!nearest || distance < nearest.distance)) {
          nearest = { distance, key: above ? h.beforeKey : h.afterKey };
        }
      }
      if (nearest) {
        openedEdges[nearest.key] = (openedEdges[nearest.key] ?? 0) + nearest.distance;
        result = visibleHunks(net.regions, context, net.gitHunks, openedEdges);
      }
    }
    return result;
  }, [net, context, extra, focus?.line, focus?.to, focus?.old]);
  const lang = languageOf(undefined, location.path);
  const key = commentKey(location.mid, location.sid);
  const quotes = useQuotes(key ?? "").list;
  const marks = useMemo(() => {
    const here = (s: { path: string; diff?: DiffScope }) => s.path === location.path && Boolean(s.diff && diffLabel(s.diff) === diffLabel(location.scope));
    return {old: markedLines(quotes, here, true), new: markedLines(quotes, here)};
  }, [quotes, location]);
  useEffect(() => { if (historyOpen) setShowHistory(true); }, [historyOpen]);
  const opened = (!viewed && !collapsed) || Boolean(focus?.line && focus !== dismissedFocus);
  const goal = focus?.line ? {line:focus.line, end:focus.to ?? focus.line, side:focus.old ? "old" as const : "new" as const} : null;
  function select(side: Side, line: number, shift: boolean) {
    setPick((prior) => shift && prior?.side === side ? {...prior, end: line} : {side, line, end: line});
  }
  const range = pick ? {from: Math.min(pick.line, pick.end), to: Math.max(pick.line, pick.end)} : undefined;
  const code = pick && range ? hunks.filter((h) => h.positioned).flatMap(hunkRows).filter((r) => {
    const n = r[pick.side]; return n !== undefined && n >= range.from && n <= range.to;
  }) : [];
  // A range across an unrecorded gap cannot safely quote the intervening lines.
  const canComment = range && code.length === range.to - range.from + 1;
  return <section data-review-file={location.path} className="mb-3 min-w-0 rounded-control border border-line bg-surface">
    <header className="sticky top-0 z-[1] flex min-h-10 min-w-0 items-center gap-1.5 rounded-t-control border-b border-line bg-surface px-2 py-1">
      <button type="button" aria-expanded={opened} title={location.shown} onClick={() => {if (viewed) onViewed(false); setDismissedFocus(focus); setCollapsed(opened);}} className="flex min-w-0 flex-1 items-center gap-1.5 rounded-control py-1 text-left text-label font-medium text-ink hover:bg-hover">
        <Icon name="chevronRight" size={13} className={cx("shrink-0", opened && "rotate-90")} /><span className="truncate">{location.shown}</span>
      </button>
      <button type="button" title={t("session-changes.copy_path")} aria-label={t("session-changes.copy_path")} onClick={() => void copyText(location.shown)} className="touch-hit flex size-7 shrink-0 items-center justify-center rounded-control text-ink-3 hover:bg-hover"><Icon name="copy" size={13}/></button>
      {net.complete && net.regions.every((r) => r.positioned) && !pending && <ChangeCounts add={net.add} del={net.del}/>}
      <label className="inline-flex min-h-7 cursor-pointer items-center [@media(pointer:coarse)]:min-h-11 gap-1.5 rounded-control px-1.5 text-submeta text-ink-2">
        <input type="checkbox" data-viewed checked={viewed} disabled={pending} onChange={(e) => {setDismissedFocus(focus); setCollapsed(false); onViewed(e.target.checked);}} className="accent-[var(--accent)]"/>
        {t("extensions-review.viewed")}
      </label>
    </header>
    {opened && <>
      {pending ? <p className="px-3 py-2 text-label text-ink-3">{t("session-changes.loading_history")}</p>
        : !net.complete && !hunks.length ? <p className="px-3 py-2 text-label text-ink-3">{t("session-changes.incomplete")}</p>
        : !hunks.length ? <p className="px-3 py-2 text-label text-ink-3">{t("session-changes.no_net")}</p>
        : <div data-net-diff data-source={fromGit ? "git" : "transcript"}>{!net.complete && <p className="px-3 py-2 text-label text-ink-3">{t("session-changes.incomplete")}</p>}{hunks.map((h, i) => {
          const old = h.lines.filter((l) => l[0] === " " || l[0] === "-").length, next = h.lines.filter((l) => l[0] === " " || l[0] === "+").length;
          const expand = (edge: string) => <button type="button" data-context-edge onClick={() => setExtra((v) => ({ ...v, [edge]: (v[edge] ?? 0) + 20 }))} className="flex min-h-7 w-full items-center gap-2 bg-field px-3 text-left text-submeta text-ink-2 hover:bg-hover"><Icon name="unfold" size={12}/>{t("session-changes.expand_context")}</button>;
          return <div key={i}>{h.moreBefore && expand(h.beforeKey)}
            <div className="break-all border-y border-line/70 bg-field/70 px-3 py-1 font-mono text-submeta text-ink-3">
              {h.positioned ? `@@ -${old ? h.old_start : h.old_start - 1},${old} +${next ? h.new_start : h.new_start - 1},${next} @@ ${h.header ?? ""}` : t("session-changes.unknown_position")}
            </div>
            <ChangeRows hunk={h} split={split} wrap={wrap} whitespace={whitespace} lang={lang} select={key ? select : undefined} picked={pick ?? goal} marks={marks}/>
            {h.moreAfter && expand(h.afterKey)}
          </div>;
        })}</div>}
      {pick && range && key && (canComment ? <LineBox where={`${location.shown}:${rangeLabel(range.from, range.to)}`} onCancel={() => setPick(null)} onAdd={(body) => {
        const lines = code.map((r) => (r.kind === "del" ? "-" : r.kind === "add" ? "+" : " ") + r.text);
        addCodeComment(key, codeQuote({file:location.shown, path:location.path, mid:location.mid, sid:location.sid, cwd:location.cwd, diff:location.scope, old:pick.side === "old", ...range, lines, lang:"diff"}), body);
        setPick(null);
      }}/> : <p role="status" className="px-3 py-2 text-label text-ink-3">{t("session-changes.uncaptured_range")}</p>)}
      {historyCount > 0 && <div className="border-t border-line px-2 py-1">
        <button type="button" data-history-toggle aria-expanded={showHistory} className="flex min-h-8 items-center gap-1.5 rounded-control px-1 text-submeta text-ink-2 hover:bg-hover" onClick={() => setShowHistory((v) => !v)}>
          <Icon name="chevronRight" size={12} className={showHistory ? "rotate-90" : ""}/>{t("session-changes.calls", {count:historyCount})}
        </button>
        {showHistory && history}
      </div>}
    </>}
  </section>;
}
