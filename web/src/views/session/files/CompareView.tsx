// Two versions of a changed file side by side in the Git panel (docs/web/web.md "Compare", #1437; iOS / Mac: #1299): pictures
// next to each other or under a slider, documents (PDF / docx / pptx / xlsx) as a diff of their text by page, slide or
// sheet. A version comes from git/blob (written into the node's cache) or the working tree.
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { api, enc, errorText, fileDownloadURL, isAbort } from "../../../lib/api";
import { diffLines, docxUnits, flatten, hunks, pairRows, pdfUnits, pptxUnits, ROW_SEP, shownText, workbookUnits, type Hunk, type Unit } from "../../../lib/compare";
import { extOf } from "../../../lib/files";
import { openPanel } from "../../../lib/panel";
import { Button, cx, Loading } from "../../../ui/atoms";
import { Empty, Segmented } from "./parts";
import { t } from "../../../i18n";
import { tRich } from "../../../i18n/rich";

const IMAGE_EXTS = ["png", "jpg", "jpeg", "gif", "webp", "bmp", "avif", "svg", "ico", "heic"];
const DOC_EXTS = ["pdf", "docx", "pptx", "xlsx", "xlsm", "xls", "ods"];

/** Whether the Git panel compares this file itself (instead of a text diff). */
export function compareKind(path: string): "image" | "doc" | null {
  const ext = extOf(path);
  return IMAGE_EXTS.includes(ext) ? "image" : DOC_EXTS.includes(ext) ? "doc" : null;
}

/** HEAD, INDEX, a commit or "<commit>^"; WORKTREE is the file as it is on disk. */
export type Rev = string;

interface Side {
  label: string;
  path?: string; // on the machine; missing: the file does not exist in that version
}

async function resolve(mid: string, cwd: string, root: string, path: string, rev: Rev, signal: AbortSignal, current?: () => boolean): Promise<Side> {
  if (rev === "WORKTREE") return { label: t("session-files-compareview.worktree"), path: `${root.replace(/\/$/, "")}/${path}` };
  const b = await api<{ path?: string; missing?: boolean }>(`/machines/${enc(mid)}/git/blob?cwd=${enc(cwd)}&path=${enc(path)}&rev=${enc(rev)}`, { signal, isAuthContextCurrent: () => !signal.aborted && (current?.() ?? true) });
  const label = rev === "HEAD" ? "HEAD" : rev === "INDEX" ? t("session-files-compareview.index") : rev.endsWith("^") ? t("session-files-compareview.before_commit", { hash: rev.slice(0, 7) }) : rev.slice(0, 7);
  if (!b.missing && !b.path) throw new Error(t("session-files-compareview.version_unavailable"));
  return { label, path: b.missing ? undefined : b.path };
}

export function CompareView({ mid, cwd, root, path, beforePath = path, before, after, deleted, refreshed, isAuthContextCurrent }: { mid: string; cwd: string; root: string; path: string; beforePath?: string; before: Rev; after: Rev; deleted?: boolean; refreshed?: number; isAuthContextCurrent?: () => boolean }) {
  const [result, setResult] = useState<{ key: string; sides?: [Side, Side]; error?: string } | null>(null);
  const [retry, setRetry] = useState(0);
  const [mode, setMode] = useState<"side" | "slide">("side");
  const [at, setAt] = useState(50);
  const docScroll = useRef(0);
  const key = JSON.stringify([mid, cwd, root, path, beforePath, before, after, deleted, refreshed, retry]);
  useEffect(() => {
    const ac = new AbortController();
    const active = () => !ac.signal.aborted && (isAuthContextCurrent?.() ?? true);
    Promise.all([resolve(mid, cwd, root, beforePath, before, ac.signal, isAuthContextCurrent), deleted && after === "WORKTREE" ? Promise.resolve({ label: t("session-files-compareview.worktree") }) : resolve(mid, cwd, root, path, after, ac.signal, isAuthContextCurrent)])
      .then((sides) => active() && setResult({ key, sides }))
      .catch((e) => active() && !isAbort(e) && setResult({ key, error: errorText(e) }));
    return () => ac.abort();
  }, [mid, cwd, root, path, beforePath, before, after, deleted, key, isAuthContextCurrent]);
  const current = result?.key === key ? result : null;
  if (current?.error) return <Empty icon="info" title={t("session-files-compareview.versions_failed")} detail={current.error} action={<Button icon="retry" className="touch-hit" onClick={() => setRetry((n) => n + 1)}>{t("session-gitpanel.retry")}</Button>} />;
  if (!current?.sides)
    return (
      <div className="flex flex-1 items-center justify-center">
        <Loading label={t("session-files-compareview.loading_versions")} />
      </div>
    );
  const oldExt = extOf(beforePath);
  return compareKind(path) === "image" ? <ImageCompare mid={mid} sides={current.sides} mode={mode} onMode={setMode} at={at} onAt={setAt} /> : <DocCompare mid={mid} sides={current.sides} beforeExt={DOC_EXTS.includes(oldExt) ? oldExt : extOf(path)} afterExt={extOf(path)} scrollTop={docScroll} isAuthContextCurrent={isAuthContextCurrent} />;
}

function OpenButtons({ mid, sides }: { mid: string; sides: [Side, Side] }) {
  return (
    <div className="flex shrink-0 gap-1.5">
      {sides.map((s, i) =>
        s.path ? (
          <Button key={i} tone="quiet" onClick={() => openPanel({ kind: "file", mid, path: s.path! })}>
            {i === 0 ? t("session-files-compareview.open_old") : t("session-files-compareview.open_new")}
          </Button>
        ) : null,
      )}
    </div>
  );
}

// ── pictures ──────────────────────────────────────────────

function ImageCompare({ mid, sides, mode, onMode, at, onAt }: { mid: string; sides: [Side, Side]; mode: "side" | "slide"; onMode: (mode: "side" | "slide") => void; at: number; onAt: (at: number) => void }) {
  const src = (s: Side) => (s.path ? fileDownloadURL(mid, s.path, "", { inline: true }) : "");
  const both = sides.every((s) => s.path);
  const checker = { backgroundImage: "conic-gradient(var(--field) 25%, var(--inset) 0 50%, var(--field) 0 75%, var(--inset) 0)", backgroundSize: "20px 20px" };
  return (
    <>
      <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-line px-3 py-1.5">
        {both && <Segmented label={t("session-files-compareview.mode")} value={mode} onChange={onMode} options={[["side", t("session-files-compareview.side_by_side")], ["slide", t("session-files-compareview.slider")]]} />}
        <span className="min-w-0 flex-1 truncate text-[11.5px] text-ink-3">
          {sides[0].label} → {sides[1].label}
        </span>
        <OpenButtons mid={mid} sides={sides} />
      </div>
      {mode === "side" || !both ? (
        <div className="grid min-h-0 flex-1 grid-cols-2 gap-px bg-line">
          {sides.map((s, i) => (
            <div key={i} className="flex min-h-0 flex-col bg-surface">
              <div className={cx("shrink-0 px-3 py-1.5 text-[11.5px] font-medium", i === 0 ? "text-red" : "text-green")}>
                {i === 0 ? t("session-files-compareview.old") : t("session-files-compareview.new")} · {s.label}
              </div>
              <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-4" style={checker}>
                {s.path ? <img src={src(s)} alt="" className="max-h-full max-w-full shadow-hairline" /> : <span className="text-[12.5px] text-ink-3">{t("session-files-compareview.not_in_version")}</span>}
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden p-4" style={checker}>
            <div className="relative max-h-full max-w-full">
              <img src={src(sides[1])} alt={t("session-files-compareview.new")} className="block max-h-[70vh] max-w-full" />
              <img src={src(sides[0])} alt={t("session-files-compareview.old")} className="absolute inset-0 block h-full w-full object-contain" style={{ clipPath: `inset(0 ${100 - at}% 0 0)` }} />
              <div className="pointer-events-none absolute top-0 bottom-0 w-[2px] bg-accent" style={{ left: `${at}%` }} />
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-3 border-t border-line px-4 py-2 text-[11.5px] whitespace-nowrap">
            <span className="text-red">{t("session-files-compareview.old")}</span>
            <input type="range" min={0} max={100} value={at} onChange={(e) => onAt(Number(e.target.value))} aria-label={t("session-files-compareview.divider")} className="flex-1 accent-[var(--accent)]" />
            <span className="text-green">{t("session-files-compareview.new")}</span>
          </div>
        </div>
      )}
    </>
  );
}

// ── documents ─────────────────────────────────────────────

async function unitsOf(mid: string, side: Side, ext: string, signal: AbortSignal): Promise<Unit[]> {
  if (!side.path) return [];
  const res = await fetch(fileDownloadURL(mid, side.path, ""), { signal });
  if (!res.ok) throw new Error(t("session-files-compareview.read_failed", { label: side.label, status: res.status }));
  const data = await res.arrayBuffer();
  if (ext === "pdf") return pdfUnits(data);
  const parse = (s: string) => new DOMParser().parseFromString(s, "application/xml");
  if (ext === "docx" || ext === "pptx") {
    const { default: JSZip } = await import("jszip");
    const zip = await JSZip.loadAsync(data);
    return ext === "docx" ? docxUnits(zip, parse) : pptxUnits(zip, parse);
  }
  const XLSX = await import("xlsx");
  return workbookUnits(XLSX, XLSX.read(new Uint8Array(data), { type: "array" }));
}

function DocCompare({ mid, sides, beforeExt, afterExt, scrollTop, isAuthContextCurrent }: { mid: string; sides: [Side, Side]; beforeExt: string; afterExt: string; scrollTop: RefObject<number>; isAuthContextCurrent?: () => boolean }) {
  const [state, setState] = useState<{ hunks?: Hunk[]; added?: number; removed?: number; error?: string; tooBig?: boolean }>({});
  const [retry, setRetry] = useState(0);
  const scroller = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const ac = new AbortController();
    setState({});
    Promise.all([unitsOf(mid, sides[0], beforeExt, ac.signal), unitsOf(mid, sides[1], afterExt, ac.signal)])
      .then(([a, b]) => {
        if (ac.signal.aborted || !(isAuthContextCurrent?.() ?? true)) return;
        const ops = diffLines(flatten(a), flatten(b));
        if (!ops) return setState({ tooBig: true });
        setState({ hunks: hunks(ops), added: ops.filter((o) => o.kind === "add").length, removed: ops.filter((o) => o.kind === "del").length });
      })
      .catch((e) => !ac.signal.aborted && (isAuthContextCurrent?.() ?? true) && !isAbort(e) && setState({ error: errorText(e) }));
    return () => ac.abort();
  }, [mid, sides, beforeExt, afterExt, retry, isAuthContextCurrent]);
  useLayoutEffect(() => {
    if (scroller.current) scroller.current.scrollTop = scrollTop.current;
  }, [scrollTop, state.hunks]);
  const bar = (
    <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-line px-3 py-1.5 text-[11.5px] whitespace-nowrap text-ink-3 tabular-nums">
      <span className="min-w-0 flex-1 truncate">
        {sides[0].label} → {sides[1].label}
        {state.hunks && (
          <>
            {" · "}
            {tRich("session-files-compareview.summary", {
              added: <span className="text-green">+{state.added}</span>,
              removed: <span className="text-red">−{state.removed}</span>,
              count: state.hunks.length,
            })}
          </>
        )}
      </span>
      <OpenButtons mid={mid} sides={sides} />
    </div>
  );
  if (state.error)
    return (
      <>
        {bar}
        <Empty icon="info" title={t("session-files-compareview.compare_failed")} detail={state.error} action={<Button icon="retry" className="touch-hit" onClick={() => setRetry((n) => n + 1)}>{t("session-gitpanel.retry")}</Button>} />
      </>
    );
  if (state.tooBig)
    return (
      <>
        {bar}
        <Empty icon="info" title={t("session-files-compareview.too_big")} detail={t("session-files-compareview.open_both")} />
      </>
    );
  if (!state.hunks)
    return (
      <>
        {bar}
        <div className="flex flex-1 items-center justify-center">
          <Loading label={t("session-files-compareview.loading_text")} />
        </div>
      </>
    );
  return (
    <>
      {bar}
      {state.hunks.length === 0 ? (
        <Empty icon="check" title={t("session-files-compareview.no_text_change")} detail={t("session-files-compareview.no_text_change_detail")} />
      ) : (
        <div ref={scroller} onScroll={(event) => { scrollTop.current = event.currentTarget.scrollTop; }} className="min-h-0 flex-1 overflow-y-auto py-2">
          {state.hunks.map((h, i) => (
            <section key={i} className="mb-2">
              {h.skippedBefore > 0 && <div className="px-4 py-1 text-[11.5px] text-ink-3">{t("session-files-compareview.skipped", { count: h.skippedBefore })}</div>}
              <div className="sticky top-0 z-10 bg-surface/95 px-4 py-1 text-[12px] font-semibold text-ink-2 backdrop-blur">{h.unit}</div>
              {pairRows(h.ops).map((o, j) =>
                o.kind === "row" ? (
                  <div key={j} className="flex gap-2 bg-orange-tint/60 px-4 py-[3px] text-[13px] leading-[1.55] [overflow-wrap:anywhere]">
                    <span className="w-3 shrink-0 font-mono text-orange select-none">~</span>
                    <span className="min-w-0 text-ink-2">
                      {o.cells.map((c, k) => (
                        <span key={k}>
                          {k > 0 && <span className="text-ink-3">{ROW_SEP}</span>}
                          {c.old === c.new ? (
                            c.new
                          ) : (
                            <span className="text-ink">
                              <del className="text-red decoration-red/50">{c.old}</del>
                              <span className="px-0.5 text-ink-3">→</span>
                              <ins className="text-green no-underline">{c.new}</ins>
                            </span>
                          )}
                        </span>
                      ))}
                    </span>
                  </div>
                ) : (
                  <div
                    key={j}
                    className={cx(
                      "flex gap-2 px-4 py-[3px] text-[13px] leading-[1.55] whitespace-pre-wrap [overflow-wrap:anywhere]",
                      o.kind === "add" ? "bg-diff-add-bg text-ink" : o.kind === "del" ? "bg-diff-del-bg text-ink line-through decoration-red/40" : "text-ink-2",
                    )}
                  >
                    <span className={cx("w-3 shrink-0 font-mono select-none", o.kind === "add" ? "text-green" : o.kind === "del" ? "text-red" : "text-ink-3")}>{o.kind === "add" ? "+" : o.kind === "del" ? "−" : ""}</span>
                    <span className="min-w-0">{shownText(o.text)}</span>
                  </div>
                ),
              )}
            </section>
          ))}
        </div>
      )}
    </>
  );
}
