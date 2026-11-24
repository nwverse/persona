// Compact, collapsible summary of all files edited in a turn: deduplicates by path, aggregates +add / −del line
// counts, lets the reader jump straight to a file in the side panel or inspect its combined diff.
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { t } from "../../i18n";
import { useCloseOnBack } from "../../lib/back-close";
import type { Op } from "../../lib/diff-lines";
import { below, fileName, toolFile } from "../../lib/files";
import { openPanel } from "../../lib/panel";
import { getSession } from "../../lib/store";
import type { WorkNode } from "../../lib/work-groups";
import { Collapse, copyText, cx, Dialog, EASE, IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { NARROW, useMedia } from "../../ui/media-query";
import { DiffBlocks, diffStats, editDiffs, foldDiffs, formatUnifiedDiff } from "./ActionRow";
import { edgeMask, useEdgeFade } from "./edge-fade";

const EDIT_TOOLS = new Set(["Edit", "Write", "MultiEdit", "NotebookEdit"]);

export interface FileDiffEntry {
  path: string;
  add: number;
  del: number;
  diffs: Op[][];
}

export interface TurnDiffSummaryProps {
  nodes: readonly WorkNode[];
  mid: string;
  sid: string;
  cwd?: string;
}

/** Walk a turn's work nodes and aggregate edited files, deduplicating by file path. */
export function summarizeTurnDiffs(nodes: readonly WorkNode[]): FileDiffEntry[] {
  const byPath = new Map<string, FileDiffEntry>();
  const walk = (list: readonly WorkNode[]) => {
    for (const n of list) {
      if (n.kind === "group") {
        walk(n.children);
        continue;
      }
      if (n.step.kind !== "tool") continue;
      const { use } = n.step;
      const name = use.tool_name ?? "";
      if (!EDIT_TOOLS.has(name)) continue;
      const path = toolFile(name, use.tool_input)?.path;
      if (!path) continue;
      const diffs = editDiffs(use) ?? [];
      const stats = diffs.length ? diffStats(diffs) : { add: 0, del: 0 };
      const existing = byPath.get(path);
      if (existing) {
        existing.add += stats.add;
        existing.del += stats.del;
        existing.diffs.push(...diffs);
      } else {
        byPath.set(path, { path, add: stats.add, del: stats.del, diffs: [...diffs] });
      }
    }
  };
  walk(nodes);
  return [...byPath.values()];
}

/** Shorten a file path relative to `cwd`, or to its last two path segments if outside `cwd`. */
export function shortenDiffPath(path: string, cwd?: string): string {
  if (cwd) {
    const rel = below(cwd, path);
    if (rel && rel.length) return rel.join("/");
  }
  const parts = path.replace(/\/+$/, "").split("/").filter(Boolean);
  if (parts.length <= 2) return parts.join("/") || fileName(path);
  return `…/${parts.slice(-2).join("/")}`;
}

function FileDiffIcon() {
  return (
    <svg
      width={14}
      height={14}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.9}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className="shrink-0 text-tool-ink"
    >
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <path d="M14 2v6h6" />
      <path d="M9 13h6M12 10v6M9 19h6" />
    </svg>
  );
}

export const TurnDiffSummary = memo(function TurnDiffSummary({ nodes, mid, sid, cwd }: TurnDiffSummaryProps) {
  const files = useMemo(() => summarizeTurnDiffs(nodes), [nodes]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState<FileDiffEntry | null>(null);

  if (!files.length) return null;

  let totalAdd = 0;
  let totalDel = 0;
  for (const f of files) {
    totalAdd += f.add;
    totalDel += f.del;
  }
  const resolvedCwd = cwd ?? getSession(mid, sid)?.cwd;

  return (
    <div className="rounded-[8px] border border-line/80 bg-inset/70 px-2.5 py-1.5 text-[12.5px]" data-turn-diff-summary>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full min-w-0 items-center gap-1.5 text-left select-none transition-opacity hover:opacity-80"
      >
        <FileDiffIcon />
        <span className="font-medium text-ink-2">{t("chat-turndiffsummary.files_changed", { count: files.length })}</span>
        <span className="flex shrink-0 items-center gap-1 font-mono text-[12px] font-medium tabular-nums">
          <span className="text-diff-add-fg">+{totalAdd}</span>
          <span className="text-diff-del-fg">−{totalDel}</span>
        </span>
        <span className="flex-1" />
        <Icon
          name="chevronRight"
          size={13}
          stroke={2.2}
          className="shrink-0 text-tool-ink transition-transform duration-150 motion-reduce:transition-none"
          style={{ transform: open ? "rotate(90deg)" : "none" }}
        />
      </button>
      <Collapse open={open} lazy>
        <div className="mt-1.5 flex flex-col gap-0.5 border-t border-line/70 pt-1.5" data-turn-diff-list>
          {files.map((f) => (
            <div key={f.path} className="flex min-w-0 items-center gap-2 rounded-[6px] px-1.5 py-1 transition-colors hover:bg-hover">
              <button
                type="button"
                title={f.path}
                onClick={() => openPanel({ kind: "file", mid, path: f.path, cwd: resolvedCwd })}
                className="group flex min-w-0 flex-1 items-center gap-1.5 text-left"
              >
                <Icon name="read" size={12} className="shrink-0 text-ink-3" />
                <span className="truncate font-mono text-[12px] text-ink-2 group-hover:text-ink">{shortenDiffPath(f.path, resolvedCwd)}</span>
              </button>
              <span className="flex shrink-0 items-center gap-1 font-mono text-[12px] font-medium tabular-nums">
                <span className="text-diff-add-fg">+{f.add}</span>
                <span className="text-diff-del-fg">−{f.del}</span>
              </span>
              {f.diffs.length > 0 && (
                <button
                  type="button"
                  onClick={() => setActive(f)}
                  className="shrink-0 rounded-[5px] px-1.5 py-0.5 text-[11.5px] font-medium text-tool-ink transition-colors hover:bg-hover-2 hover:text-ink"
                  data-view-file-diff
                >
                  {t("chat-turndiffsummary.view_diff")}
                </button>
              )}
            </div>
          ))}
        </div>
      </Collapse>
      {active && <ActionDiffModal onClose={() => setActive(null)} path={active.path} diffs={active.diffs} mid={mid} sid={sid} />}
    </div>
  );
});

function DiffStats({ diffs }: { diffs: Op[][] }) {
  const s = diffStats(diffs);
  return (
    <span className="shrink-0 font-mono text-[12px] font-medium tabular-nums">
      <span className="text-diff-add-fg">+{s.add}</span> <span className="text-diff-del-fg">−{s.del}</span>
    </span>
  );
}

function CopyDiffButton({ diffs, compact, inline }: { diffs: Op[][]; compact?: boolean; inline?: boolean }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | null>(null);
  useEffect(() => () => { if (timer.current !== null) clearTimeout(timer.current); }, []);
  const onCopy = () => {
    void copyText(formatUnifiedDiff(diffs));
    setCopied(true);
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      type="button"
      onClick={onCopy}
      aria-label={copied ? t("chat-turndiffsummary.diff_copied") : t("chat-turndiffsummary.copy_diff")}
      className={cx(
        "flex shrink-0 items-center font-medium whitespace-nowrap transition-colors",
        inline
          ? "h-5 gap-1 rounded-[4px] px-1.5 text-[11px] hover:bg-hover-2 hover:text-ink"
          : cx("gap-1.5 rounded-control shadow-btn hover:bg-hover", compact ? "h-9 px-2.5 text-[13px]" : "h-8 px-3 text-[12.5px]"),
        copied ? "text-green" : "text-ink-2",
      )}
      data-copy-diff
    >
      <Icon name={copied ? "check" : "copy"} size={inline ? 11 : 13} className={copied ? "text-green" : undefined} />
      {copied ? t("chat-turndiffsummary.diff_copied") : compact ? t("chat-turndiffsummary.copy_short") : t("chat-turndiffsummary.copy_diff")}
    </button>
  );
}

function ActionDiffModal({ onClose, path, diffs, mid, sid }: { onClose: () => void; path: string; diffs: Op[][]; mid: string; sid: string }) {
  const narrow = useMedia(NARROW);
  const openFile = () => {
    onClose();
    openPanel({ kind: "file", mid, path, cwd: getSession(mid, sid)?.cwd });
  };
  if (narrow) return <DiffScreen onClose={onClose} path={path} diffs={diffs} onOpenFile={path ? openFile : undefined} />;
  return (
    <Dialog
      open
      onClose={onClose}
      width={860}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{path ? fileName(path) : t("chat-turndiffsummary.diff_title")}</span>
          <DiffStats diffs={diffs} />
        </span>
      }
      footer={
        <div className="flex items-center gap-2">
          <CopyDiffButton diffs={diffs} />
          {path && (
            <button type="button" onClick={openFile} className="flex h-8 items-center gap-1.5 rounded-control px-3 text-[12.5px] font-medium whitespace-nowrap text-ink-2 shadow-btn transition-colors hover:bg-hover">
              <Icon name="read" size={13} />
              {t("chat-turndiffsummary.open_file")}
            </button>
          )}
        </div>
      }
    >
      {path && <div className="mb-2 truncate font-mono text-[11.5px] text-ink-3" title={path}>{path}</div>}
      <div className="flex max-h-[60vh] flex-col gap-2 overflow-auto" data-diff>
        <DiffBlocks diffs={diffs} />
      </div>
    </Dialog>
  );
}

function DiffScreen({ onClose, path, diffs, onOpenFile }: { onClose: () => void; path: string; diffs: Op[][]; onOpenFile?: () => void }) {
  useCloseOnBack(true, onClose);
  const [all, setAll] = useState(false);
  const { shown, hidden } = useMemo(() => (all ? { shown: diffs, hidden: 0 } : foldDiffs(diffs)), [all, diffs]);
  const body = useRef<HTMLDivElement>(null);
  const { edges, measure } = useEdgeFade(body, [shown]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [onClose]);
  const mask = edgeMask(edges);
  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label={path ? t("chat-turndiffsummary.diff_title_for", { name: fileName(path) }) : t("chat-turndiffsummary.diff_title")}
      className="fixed inset-x-0 z-40 flex flex-col bg-surface pt-[env(safe-area-inset-top)]"
      style={{ top: "var(--kb-top)", height: "var(--kb-h)", animation: `slide-up 220ms ${EASE} both` }}
      data-diff-screen
    >
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-2">
        <IconButton icon="x" label={t("ui-atoms.close")} onClick={onClose} size={36} className="touch-hit" />
        <span className="min-w-0 flex-1 truncate text-[15px] font-semibold text-ink" title={path || undefined}>
          {path ? fileName(path) : t("chat-turndiffsummary.diff_title")}
        </span>
        <DiffStats diffs={diffs} />
        <CopyDiffButton diffs={diffs} compact />
        {onOpenFile && (
          <button type="button" onClick={onOpenFile} className="flex h-9 shrink-0 items-center gap-1.5 rounded-control px-3 text-[13px] font-medium whitespace-nowrap text-ink-2 shadow-btn transition-colors hover:bg-hover">
            <Icon name="read" size={13} />
            {t("chat-turndiffsummary.open_file")}
          </button>
        )}
      </div>
      {path && <div className="shrink-0 truncate px-4 pt-2 font-mono text-[11.5px] text-ink-3">{path}</div>}
      <div ref={body} onScroll={measure} className="flex min-h-0 flex-1 flex-col gap-2 overflow-auto overscroll-contain px-3 pt-2 pb-[max(12px,env(safe-area-inset-bottom))]" style={{ maskImage: mask, WebkitMaskImage: mask }} data-diff>
        <DiffBlocks diffs={shown} />
        {hidden > 0 && (
          <button type="button" onClick={() => setAll(true)} className="sticky left-0 flex h-10 w-fit shrink-0 items-center gap-1.5 rounded-control px-3 text-[13px] font-medium whitespace-nowrap text-accent-ink" data-diff-more>
            <Icon name="chevron" size={13} stroke={2.2} />
            {t("chat-turndiffsummary.diff_expand_more", { count: hidden })}
          </button>
        )}
      </div>
    </div>,
    document.body,
  );
}
