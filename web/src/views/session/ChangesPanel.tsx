import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { CHANGES_TREE_INDENT, CHANGES_TREE_INDENT_MAX_SHARE, CHANGES_DIFF_MIN, CHANGES_TREE_DEFAULT, CHANGES_TREE_MIN, CHANGES_TREE_MAX } from "../../lib/layout";
import { ResizeHandle } from "../../ui/ResizeHandle";
import { errorText } from "../../lib/api";
import { relativeTo, type DiffScope } from "../../lib/comments";
import { fileChange, recordedChange, type RecordedChange } from "../../lib/session-changes";
import { scopeBase, type ScopeBase } from "../../lib/change-base";
import { useDiffMode } from "../../lib/code-view";
import { ChangeCounts, FileReview } from "./FileReview";
import { below, toolFile } from "../../lib/files";
import { openPanel, type Panel } from "../../lib/panel";
import { REVIEW_FEATURE } from "../../lib/session-review";
import { hubHas, useStore } from "../../lib/store";
import { changePath, EDIT_TOOLS, turnChanges, type ChangedFile } from "../../lib/turn-changes";
import { exchangeSteps, segments, type Segment } from "../../lib/turns";
import { cx, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { PanelHeader } from "../../ui/PanelHeader";
import { t } from "../../i18n";
import { DiffBlocks, editDiffs } from "../chat/ActionRow";
import { useSharedTranscript } from "../chat/transcript-share";
import type { Transcript } from "../chat/useTranscript";

const TREE_WIDTH_KEY = "persona.changesTreeWidth";
const OPTIONS_KEY = "persona.changes.options";
const contextChoices = [0, 3, 10, 20];
const defaultOptions = { wrap: false, whitespace: false, context: 3 };

function savedOptions(): typeof defaultOptions {
  try {
    const value = JSON.parse(localStorage.getItem(OPTIONS_KEY) ?? "{}");
    return {
      wrap: value?.wrap === true,
      whitespace: value?.whitespace === true,
      context: contextChoices.includes(value?.context) ? value.context : defaultOptions.context,
    };
  } catch {
    return defaultOptions;
  }
}

function storedTreeWidth() {
  try {
    const width = Number(localStorage.getItem(TREE_WIDTH_KEY));
    return Number.isFinite(width) && width > 0 ? Math.max(CHANGES_TREE_MIN, Math.min(CHANGES_TREE_MAX, width)) : CHANGES_TREE_DEFAULT;
  } catch {
    return CHANGES_TREE_DEFAULT;
  }
}

type Opened = Extract<Panel, { kind: "changes" }>;
type Scope = "exchange" | "session";
type ChangeEdit = {
  id: string;
  call?: string;
  turn: string;
  seq: number;
  path: string;
  change: RecordedChange;
  diffs: NonNullable<ReturnType<typeof editDiffs>>;
};

type TreeNode = { dirs: Map<string, TreeNode>; files: ChangedFile[] };

function treeOf(files: ChangedFile[]): TreeNode {
  const root: TreeNode = { dirs: new Map(), files: [] };
  for (const file of files) {
    const parts = file.shown.split(/[\\/]/).filter(Boolean);
    let node = root;
    for (const part of parts.slice(0, -1)) {
      if (!node.dirs.has(part)) node.dirs.set(part, { dirs: new Map(), files: [] });
      node = node.dirs.get(part)!;
    }
    node.files.push(file);
  }
  return root;
}

function FileTree({ root, collapsed, toggle, select, selected }: {
  root: TreeNode;
  collapsed: ReadonlySet<string>;
  toggle: (path: string) => void;
  select: (path: string) => void;
  selected?: string;
}) {
  function draw(node: TreeNode, prefix: string, depth: number): React.ReactNode {
    return (
      <>
        {[...node.dirs.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, child]) => {
          let path = `${prefix}${name}`, label = name;
          while (!child.files.length && child.dirs.size === 1) {
            const [part, next] = [...child.dirs][0]; label += "/" + part; path += "/" + part; child = next;
          }
          const closed = collapsed.has(path);
          return (
            <div key={path}>
              <button type="button" aria-expanded={!closed} onClick={() => toggle(path)} className="flex h-8 w-full items-center gap-1 rounded-control px-2 text-left text-label font-medium text-ink-2 hover:bg-hover [@media(pointer:coarse)]:min-h-11" style={{ paddingLeft: `${8 + depth * 12}px` }}>
                <Icon name="chevronRight" size={12} style={{ transform: closed ? undefined : "rotate(90deg)" }} />
                <span className="truncate" title={path}>{label}</span>
              </button>
              {!closed && draw(child, `${path}/`, depth + 1)}
            </div>
          );
        })}
        {[...node.files].sort((a, b) => a.shown.localeCompare(b.shown)).map((file) => (
          <button key={file.path} type="button" title={file.path} data-change-file={file.path} aria-current={selected === file.path ? "true" : undefined} onClick={() => select(file.path)} className={cx("flex min-h-8 w-full items-center gap-2 rounded-control px-2 py-1 text-left text-label hover:bg-hover [@media(pointer:coarse)]:min-h-11", selected === file.path && "bg-hover text-ink")}>
            <span className="min-w-0 flex-1 overflow-hidden">
              <span className="flex items-center gap-2" style={{ paddingLeft: `min(${(depth + 1) * CHANGES_TREE_INDENT}px, ${CHANGES_TREE_INDENT_MAX_SHARE * 100}%)` }}>
                <Icon name="file" size={13} className="shrink-0 text-ink-3" />
                <span className="min-w-0 flex-1 truncate">{file.shown.split(/[\\/]/).pop()}</span>
              </span>
            </span>
            {file.exact && (
              <span className="shrink-0 whitespace-nowrap font-mono text-submeta tabular-nums">
                <span className="text-diff-add-fg">+{file.add}</span> <span className="text-diff-del-fg">-{file.del}</span>
              </span>
            )}
          </button>
        ))}
      </>
    );
  }
  return draw(root, "", 0);
}

function callIn(segment: Segment, call: string): boolean {
  return segment.steps.some((step) => step.kind === "tool" && (step.use.tool_use_id === call || step.use.id === call));
}

type ChangesPanelProps = { mid: string; sid: string; opened: Opened; onClose: () => void };

export function ChangesPanel(props: ChangesPanelProps) {
  const tr = useSharedTranscript(props.mid, props.sid);
  if (!tr) {
    return <div className="flex min-h-0 flex-1 items-center justify-center" data-changes-panel data-transcript-loading><Spinner size={18} /></div>;
  }
  return <ChangesPanelView {...props} tr={tr} />;
}

function ChangesPanelView({ mid, sid, opened, onClose, tr }: ChangesPanelProps & { tr: Transcript }) {
  const session = useStore((state) => (state.sessions[mid] ?? []).find((row) => row.session_id === sid));
  const cwd = session?.cwd;
  const reviews = useStore((state) => hubHas(state.features, REVIEW_FEATURE));
  const all = useMemo(() => segments(tr.items), [tr.items]);
  const call = opened.at?.call;
  const called = useMemo(() => (call ? all.find((segment) => callIn(segment, call)) : undefined), [all, call]);
  const resolvingCall = Boolean(call && !called);
  const candidates = useMemo(() => resolvingCall
    ? all.filter((segment) => segment.folds.some((fold) => (fold.fold?.changes === undefined && Boolean(fold.fold?.tools)) || fold.fold?.changes?.some((file) => file.path && changePath(file.path, cwd) === opened.at?.path)))
    : [], [all, resolvingCall, cwd, opened.at?.path]);
  const turnId = called?.id ?? (call ? undefined : opened.turn ?? all.findLast((segment) => turnChanges(exchangeSteps(segment), cwd))?.id ?? all.at(-1)?.id);
  const exchange = turnId ? all.find((segment) => segment.id === turnId) : undefined;
  const [scope, setScope] = useState<Scope>(opened.scope ?? "exchange");
  const shown = useMemo(() => scope === "session" ? all : resolvingCall ? candidates : exchange ? [exchange] : [], [scope, all, resolvingCall, candidates, exchange]);
  const edits = useMemo(() => {
    const out: ChangeEdit[] = [];
    for (const segment of shown) {
      for (const step of exchangeSteps(segment)) {
        if (step.kind !== "tool" || !step.result || step.result.is_error || (step.result.outcome?.status && step.result.outcome.status !== "ok") || !EDIT_TOOLS.has(step.use.tool_name ?? "")) continue;
        const diffs = editDiffs(step.use);
        const named = toolFile(step.use.tool_name ?? "", step.use.tool_input)?.path;
        if (!named) continue;
        const path = changePath(named, cwd);
        out.push({ id: step.use.id, call: step.use.tool_use_id, turn: segment.id, seq: step.use.seq, path, diffs: diffs ?? [], change: recordedChange(step.use, step.result, path, cwd) });
      }
    }
    return out;
  }, [shown, cwd]);
  const files = useMemo(() => {
    const result = new Map<string, ChangedFile>();
    const add = (path: string) => {
      if (!result.has(path)) result.set(path, { path, shown: relativeTo(cwd, path), add: 0, del: 0, exact: false });
    };
    const paths = new Map(edits.map((edit) => [edit.id, edit.path]));
    for (const segment of shown) for (const step of exchangeSteps(segment)) {
      if (step.kind === "tool") {
        const path = paths.get(step.use.id);
        if (path) add(path);
      } else if (step.kind === "fold") {
        for (const file of step.item.fold?.changes ?? []) if (file.path) add(changePath(file.path, cwd));
      }
    }
    return [...result.values()];
  }, [shown, edits, cwd]);
  const selectedPath = files.some((file) => file.path === opened.at?.path) ? opened.at!.path : files[0]?.path ?? opened.at?.path;
  const [reading, setReading] = useState<{ scope: string; selected?: string; path: string }>();
  const ownerScroll = useRef(false);
  const selectedEdits = useMemo(() => edits.filter((edit) => edit.path === selectedPath), [edits, selectedPath]);
  const editSignature = edits.map((edit) => edit.id).join("|");
  const scopeKey = scope === "session" ? "session" : turnId ?? "exchange";
  const readPath = reading?.scope === scopeKey && reading.selected === selectedPath && files.some((f) => f.path === reading.path)
    ? reading.path : selectedPath;
  const selectedFile = files.find((f) => f.path === readPath);
  const missingTurn = !call && Boolean(opened.turn && !exchange);
  const partialExchange = scope === "exchange" && (missingTurn || Boolean(exchange && !exchange.user)) && (tr.hasMore || tr.loadingEarlier);
  const historyReady = !resolvingCall && !partialExchange && (scope !== "session" || (!tr.hasMore && !tr.loadingEarlier));
  const [mode, setMode] = useDiffMode();
  const [options, setOptions] = useState(savedOptions);
  const [optionsOpen, setOptionsOpen] = useState(false);
  const optionsId = useId();
  const [filter, setFilter] = useState("");
  const [treeOpen, setTreeOpen] = useState(false);
  const [viewedRevision, refreshViewed] = useState(0);
  const transientViewed = useRef(new Map<string, string>());
  const [bases, setBases] = useState<Record<string, ScopeBase>>({});
  const verified = useRef(new Set<string>());
  const priorCommit = useMemo(() => {
    const first = edits[0]?.seq;
    return first === undefined ? undefined : tr.items.findLast((item) => item.seq < first && item.outcome?.git?.commit?.sha)?.outcome?.git?.commit?.sha;
  }, [edits, tr.items]);
  const byFile = useMemo(() => {
    const m = new Map<string, ChangeEdit[]>();
    for (const edit of edits) {
      const previous = m.get(edit.path);
      if (previous) previous.push(edit); else m.set(edit.path, [edit]);
    }
    return m;
  }, [edits]);
  const baseTargets = useMemo(() => [...byFile].flatMap(([path, changes]) => {
    const hash = changes[0].change.beforeHash;
    return hash ? [{path, hash}] : [];
  }), [byFile]);
  useEffect(() => {
    if (!reviews || !cwd || !historyReady || !baseTargets.length) return;
    const ac = new AbortController();
    // One node read at a time, scoped to files with an actual pre-edit fingerprint.
    void (async () => {
      for (const {path, hash} of baseTargets) {
        if (ac.signal.aborted) break;
        const key = JSON.stringify([mid, sid, path, hash]);
        if (verified.current.has(key)) continue;
        const body = await scopeBase(mid, cwd, path, hash, priorCommit, ac.signal);
        if (body && !ac.signal.aborted) {
          verified.current.add(key);
          setBases((previous) => ({ ...previous, [key]: body }));
        }
      }
    })();
    return () => ac.abort();
  }, [mid, sid, cwd, reviews, historyReady, baseTargets, priorCommit]);
  const netByFile = useMemo(() => new Map([...byFile].map(([path, calls]) => {
    const base = bases[JSON.stringify([mid, sid, path, calls[0].change.beforeHash])];
    const recorded = calls.map((call) => call.change);
    const net = fileChange(recorded, base?.text, base?.hunks);
    return [path, {net: base !== undefined && !net.complete ? fileChange(recorded) : net, fromGit: base !== undefined && net.complete}];
  })), [byFile, bases, mid, sid]);
  const reviewed = (path: string) => {
    const last = byFile.get(path)?.at(-1)?.id;
    const storage = JSON.stringify(["changes", mid, sid, scopeKey, path]);
    let saved = transientViewed.current.get(storage);
    try { saved ??= localStorage.getItem(storage) ?? undefined; } catch { /* local only */ }
    return { storage, last, viewed: Boolean(last && last === saved) };
  };
  function markViewed(path: string, yes: boolean) {
    const {storage, last} = reviewed(path);
    if (!last) return;
    transientViewed.current.set(storage, yes ? last : "");
    try {if (yes) localStorage.setItem(storage, last); else localStorage.removeItem(storage);} catch { /* local only */ }
    refreshViewed(viewedRevision + 1);
  }
  const viewedCount = files.filter((f) => reviewed(f.path).viewed).length;
  const [pending, setPending] = useState({ count: 0, path: "" });
  const previous = useRef<{ scope: string; ids: Set<string>; newest: number } | null>(null);
  const pendingLive = useRef(new Map<string, { path: string; seq: number }>());
  const previousCall = useRef(call);
  const diffBody = useRef<HTMLDivElement>(null);
  const scrolledCall = useRef<{ call: string; path?: string } | undefined>(undefined);
  const anchor = useRef<{ path?: string; first: string; top: number; scroll: number }>({ first: "", top: 0, scroll: 0 });
  const [foldRetry, setFoldRetry] = useState(0);
  const [foldLoading, setFoldLoading] = useState(false);
  const foldRequests = useRef(new Set<string>());
  const foldFailures = useRef(new Map<string, string>());
  const foldRetrying = useRef(new Set<string>());
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const treeId = useId();
  const columns = useRef<HTMLDivElement>(null);
  const [treeWidth, setTreeWidth] = useState(storedTreeWidth);
  const [room, setRoom] = useState(CHANGES_TREE_MAX + CHANGES_DIFF_MIN);
  const treeMax = Math.max(CHANGES_TREE_MIN, Math.min(CHANGES_TREE_MAX, room - CHANGES_DIFF_MIN));
  const treeShown = Math.round(Math.max(CHANGES_TREE_MIN, Math.min(treeMax, treeWidth)));

  useLayoutEffect(() => {
    const element = columns.current;
    if (!element) return;
    const measure = () => {
      const width = element.getBoundingClientRect().width;
      if (width > 0) setRoom(width);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [resolvingCall]);

  function changeOptions(patch: Partial<typeof defaultOptions>) {
    const next = { ...options, ...patch };
    setOptions(next);
    try { localStorage.setItem(OPTIONS_KEY, JSON.stringify(next)); } catch { /* page-only preference */ }
  }

  function keepTreeWidth(width: number) {
    setTreeWidth(width);
    try { localStorage.setItem(TREE_WIDTH_KEY, String(width)); } catch { /* page-only choice when storage is unavailable */ }
  }
  function resetTreeWidth() {
    setTreeWidth(CHANGES_TREE_DEFAULT);
    try { localStorage.removeItem(TREE_WIDTH_KEY); } catch { /* as above */ }
  }


  useEffect(() => {
    if (!opened.turn && turnId && !partialExchange) openPanel({ ...opened, turn: turnId });
  }, [opened.turn, turnId, partialExchange]);

  useEffect(() => {
    if (call && call !== previousCall.current) setScope("exchange");
    previousCall.current = call;
  }, [call]);

  useEffect(() => {
    if ((scope === "session" || partialExchange || (resolvingCall && !candidates.length)) && tr.hasMore && !tr.loadingEarlier && !tr.error) void tr.loadEarlier();
  }, [scope, resolvingCall, partialExchange, candidates.length, tr.hasMore, tr.loadingEarlier, tr.error, tr.loadEarlier]);

  const selectedFolds = shown.flatMap((segment) => segment.folds.filter((fold) => {
    const paths = fold.fold?.changes?.flatMap((file) => file.path ? [changePath(file.path, cwd)] : []) ?? [];
    if (!paths.length) return fold.fold?.changes === undefined && Boolean(fold.fold?.tools);
    // A subscription replay can bring the same fold back after its expanded items landed. Only a direct edit from
    // inside this fold proves it is still expanded; an edit elsewhere in the exchange does not.
    return foldFailures.current.has(fold.id) || paths.some((path) => !edits.some((edit) => edit.path === path && edit.seq >= fold.seq && edit.seq <= fold.fold!.through_seq));
  }));
  const pendingFile = (path: string) => !historyReady || selectedFolds.some((fold) =>
    !fold.fold?.changes || fold.fold.changes.some((f) => f.path && changePath(f.path, cwd) === path));
  const filesForTree = files.map((f) => {
    const net = netByFile.get(f.path)?.net;
    return {...f, add:net?.add ?? 0, del:net?.del ?? 0, exact:!pendingFile(f.path) && Boolean(net?.complete && net.regions.every((r) => r.positioned))};
  });
  const totals = filesForTree.reduce((v,f) => ({add:v.add+f.add, del:v.del+f.del, exact:v.exact && f.exact}), {add:0,del:0,exact:true});
  const foldKey = selectedFolds.map((fold) => fold.id).join("|");
  const foldError = selectedFolds.map((fold) => foldFailures.current.get(fold.id)).find(Boolean) ?? "";
  useEffect(() => {
    let active = true;
    const fresh = selectedFolds.filter((fold) => !foldRequests.current.has(fold.id) && (!foldFailures.current.has(fold.id) || foldRetrying.current.has(fold.id)));
    if (!fresh.length) {
      if (!selectedFolds.length) setFoldLoading(false);
      return () => { active = false; };
    }
    for (const fold of fresh) foldRequests.current.add(fold.id);
    setFoldLoading(true);
    const finish = () => {
      // Requested is not the same as expanded: a later replay may need the same fold again.
      for (const fold of fresh) foldRequests.current.delete(fold.id);
    };
    Promise.all(fresh.map((fold) => tr.expandFold(fold)))
      .then((results) => {
        finish();
        for (const fold of fresh) foldRetrying.current.delete(fold.id);
        let incomplete = false;
        fresh.forEach((fold, index) => {
          if (results[index] === false) {
            incomplete = true;
            foldFailures.current.set(fold.id, t("session-changes.fold_failed"));
          } else foldFailures.current.delete(fold.id);
        });
        // Partial items may have restarted this effect. The failure belongs to the fold, not that obsolete effect,
        // so still publish it and let the current render decide whether this fold is selected.
        if (incomplete) setFoldRetry((n) => n + 1);
        if (active) setFoldLoading(false);
      })
      .catch((error) => {
        finish();
        for (const fold of fresh) foldRetrying.current.delete(fold.id);
        for (const fold of fresh) foldFailures.current.set(fold.id, errorText(error));
        setFoldRetry((n) => n + 1);
        if (active) setFoldLoading(false);
      });
    return () => {
      active = false;
      // StrictMode immediately mounts this effect again. Release only this attempt's reservation so that run can
      // restart the read; leaving the fold marked requested would strand it after the first attempt is disowned.
      finish();
    };
  }, [foldKey, foldRetry, tr.expandFold]);

  useEffect(() => {
    const ids = new Set(edits.map((edit) => edit.id));
    // A fold covers everything through its marker even before expansion. That covered tail, rather than the newest
    // visible successful edit, separates history revealed by a read from items appended after what we already hold.
    const newest = tr.items.reduce((latest, item) => Math.max(latest, item.seq, item.fold?.through_seq ?? 0), 0);
    const prior = previous.current;
    if (!prior || prior.scope !== scopeKey || !tr.firstRead) {
      pendingLive.current.clear();
      setPending({ count: 0, path: "" });
    } else {
      // Earlier pages and expanded folds cannot cross the covered tail. Deferring the pill while history loads must not
      // consume a genuinely appended edit into the ordinary seen-ID baseline.
      for (const edit of edits) {
        if (edit.seq > prior.newest && !prior.ids.has(edit.id) && edit.path !== selectedPath) pendingLive.current.set(edit.id, { path: edit.path, seq: edit.seq });
      }
      for (const [id, edit] of pendingLive.current) if (edit.path === selectedPath) pendingLive.current.delete(id);
      const fresh = [...pendingLive.current.values()].sort((a, b) => a.seq - b.seq);
      setPending(historyReady && fresh.length ? { count: fresh.length, path: fresh.at(-1)!.path } : { count: 0, path: "" });
    }
    previous.current = { scope: scopeKey, ids, newest: Math.max(prior?.scope === scopeKey ? prior.newest : 0, newest) };
  }, [editSignature, scopeKey, selectedPath, tr.items, tr.firstRead, historyReady]);

  useLayoutEffect(() => {
    const body = diffBody.current;
    if (!body) return;
    const old = anchor.current;
    if (old.path === selectedPath && old.first && selectedEdits[0]?.id && old.first !== selectedEdits[0].id) {
      const target = [...body.querySelectorAll<HTMLElement>("[data-change-id]")].find((el) => el.dataset.changeId === old.first);
      if (target) body.scrollTop = old.scroll + target.offsetTop - old.top;
    } else if (old.path !== selectedPath) {
      const target = [...body.querySelectorAll<HTMLElement>("[data-review-file]")].find((el) => el.dataset.reviewFile === selectedPath);
      if (target) body.scrollTop = target.offsetTop;
    }
    const first = [...body.querySelectorAll<HTMLElement>("[data-change-id]")].find((el) => el.dataset.changeId === selectedEdits[0]?.id);
    anchor.current = { path: selectedPath, first: selectedEdits[0]?.id ?? "", top: first?.offsetTop ?? 0, scroll: body.scrollTop };
  }, [editSignature, selectedPath]);

  useEffect(() => {
    if (!call) {
      scrolledCall.current = undefined;
      return;
    }
    if (scrolledCall.current?.call === call && scrolledCall.current.path === selectedPath) return;
    const body = diffBody.current;
    const target = body && [...body.querySelectorAll<HTMLElement>("[data-change-call]")].find((el) => el.dataset.changeCall === call || el.dataset.changeId === call);
    if (!body || !target) return;
    body.scrollTop = target.offsetTop;
    scrolledCall.current = { call, path: selectedPath };
  }, [call, editSignature, selectedPath]);

  useEffect(() => {
    if (opened.scope) setScope(opened.scope);
  }, [opened.scope, opened.at?.line, opened.turn]);
  useEffect(() => {
    if (!opened.at?.line) return;
    const row = [...(diffBody.current?.querySelectorAll<HTMLElement>("[data-review-file]") ?? [])].find((el) => el.dataset.reviewFile === opened.at?.path);
    row?.querySelector<HTMLElement>(`[data-side="${opened.at.old ? "old" : "new"}"][data-line="${opened.at.line}"]`)?.scrollIntoView?.({block:"center"});
  }, [opened, editSignature, bases]);

  function selectFile(path: string) {
    ownerScroll.current = false;
    setReading(undefined);
    setTreeOpen(false);
    pendingLive.current.clear();
    setPending({ count: 0, path: "" });
    openPanel({ ...opened, turn: turnId, at: { path } });
    const row = [...(diffBody.current?.querySelectorAll<HTMLElement>("[data-review-file]") ?? [])].find((el) => el.dataset.reviewFile === path);
    row?.scrollIntoView?.({block:"start", inline:"nearest"});
  }

  function followScroll(body: HTMLDivElement) {
    if (!ownerScroll.current) return;
    const rows = [...body.querySelectorAll<HTMLElement>("[data-review-file]")];
    const top = body.getBoundingClientRect().top + 12;
    const atEnd = body.scrollTop > 0 && body.scrollTop + body.clientHeight >= body.scrollHeight - 1;
    const visible = atEnd ? rows.at(-1) : rows.findLast((row) => row.getBoundingClientRect().top <= top) ?? rows[0];
    const path = visible?.dataset.reviewFile;
    if (path && path !== readPath) setReading({ scope: scopeKey, selected: selectedPath, path });
  }

  function retryFolds() {
    foldRequests.current.clear();
    for (const id of foldFailures.current.keys()) foldRetrying.current.add(id);
    setFoldRetry((n) => n + 1);
  }

  if (resolvingCall) {
    const waiting = tr.loadingEarlier || foldLoading || (!tr.error && !foldError && (tr.hasMore || candidates.length > 0));
    return (
      <div className="flex min-h-0 flex-1 flex-col" data-changes-panel data-resolving-call>
        <PanelHeader icon="fileDiff" title={t("session-changes.title")} onClose={onClose} />
        {tr.error && (
          <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-1.5 text-label text-red" role="alert">
            <span className="min-w-0 flex-1 truncate">{tr.error}</span>
            <button type="button" onClick={tr.reload} className="shrink-0 rounded-control px-2 py-1 hover:bg-hover [@media(pointer:coarse)]:min-h-11">{t("session-changes.retry")}</button>
          </div>
        )}
        {foldError && (
          <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-1.5 text-label text-red" role="alert">
            <span className="min-w-0 flex-1">{foldError}</span>
            <button type="button" onClick={retryFolds} className="shrink-0 rounded-control px-2 py-1 hover:bg-hover [@media(pointer:coarse)]:min-h-11">{t("session-changes.retry")}</button>
          </div>
        )}
        <div className="flex min-h-0 flex-1 items-center justify-center gap-2 p-5 text-center text-label text-ink-3" data-change-diff>
          {waiting ? <><Spinner size={16} /> {t("session-changes.loading_history")}</> : !tr.error && !foldError ? t("session-changes.empty") : null}
        </div>
      </div>
    );
  }

  const tree = treeOf(filesForTree.filter((f) => f.shown.toLowerCase().includes(filter.trim().toLowerCase())));
  const position = files.findIndex((f) => f.path === readPath);
  const narrow = room < 700;
  const where: DiffScope = scope === "session" ? {kind:"changes", scope} : {kind:"changes", scope, turn:turnId ?? ""};
  const action = "touch-hit flex h-7 shrink-0 items-center gap-1 rounded-control px-2 text-label font-medium text-ink-2 transition-colors hover:bg-hover hover:text-ink";
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-changes-panel>
      <PanelHeader
        icon="fileDiff"
        title={t("session-changes.title")}
        subtitle={selectedFile?.shown}
        onClose={onClose}
        actions={
          <>
            {readPath && (
              <button type="button" aria-label={t("session-changes.open_file")} title={t("session-changes.open_file")} data-open-file onClick={() => openPanel({ kind: "file", mid, sid, path: readPath, cwd })} className={action}>
                <Icon name="read" size={13} />
                <span className="max-sm:hidden">{t("session-changes.open_file")}</span>
              </button>
            )}
            {reviews && cwd && readPath && below(cwd, readPath) && (
              <button type="button" aria-label={t("session-changes.review_git")} title={t("session-changes.review_git")} data-review-with-git onClick={() => openPanel({ kind: "review", mid, sid, cwd, scope: "branch", at: { path: readPath, file: true } })} className={action}>
                <Icon name="pullRequest" size={13} />
                <span className="max-sm:hidden">{t("session-changes.review_git")}</span>
              </button>
            )}
          </>
        }
      />
      <div className="flex min-h-10 shrink-0 items-center gap-2 border-b border-line px-3 py-1">
        <div role="group" aria-label={t("session-changes.scope_label")} className="flex rounded-control bg-field p-0.5">
          {(["exchange", "session"] as const).map((value) => (
            <button key={value} type="button" data-change-scope={value} aria-pressed={scope === value} onClick={() => { pendingLive.current.clear(); setPending({ count: 0, path: "" }); setScope(value); openPanel({ ...opened, scope: value, turn: turnId }); }} className={cx("min-h-7 rounded-[6px] px-2 text-label whitespace-nowrap [@media(pointer:coarse)]:min-h-11", scope === value ? "bg-surface text-ink shadow-btn" : "text-ink-3 hover:text-ink")}>
              {t(`session-changes.${value}`)}
            </button>
          ))}
        </div>
        {pending.count > 0 && (
          <button type="button" data-new-changes onClick={() => selectFile(pending.path)} className="ml-auto flex min-h-7 shrink-0 items-center rounded-full bg-accent-tint px-2 text-submeta font-medium whitespace-nowrap text-accent-ink [@media(pointer:coarse)]:min-h-11">
            {t("session-changes.new_changes", { count: pending.count })}
          </button>
        )}
      </div>
      {files.length > 0 && <div data-review-toolbar className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-line px-3 py-1.5 text-submeta text-ink-2">
        {totals.exact && <ChangeCounts add={totals.add} del={totals.del}/>}
        <span data-viewed-count className="mr-auto whitespace-nowrap">{t("session-changes.viewed_count", {viewed:viewedCount, count:files.length})}</span>
        <button type="button" className={action} aria-label={t("extensions-review.prev_file")} title={t("extensions-review.prev_file")} disabled={position <= 0} onClick={() => selectFile(files[position-1].path)}><Icon name="chevronLeft" size={14}/></button>
        <button type="button" className={action} aria-label={t("extensions-review.next_file")} title={t("extensions-review.next_file")} disabled={position >= files.length-1} onClick={() => selectFile(files[position+1].path)}><Icon name="chevronRight" size={14}/></button>
        {!narrow && <span role="group" aria-label={t("extensions-review.key_mode")} className="flex rounded-control bg-field p-0.5">{(["unified", "split"] as const).map((layout) => <button key={layout} type="button" data-diff-mode={layout} aria-pressed={layout===mode} className={cx("rounded-[5px] px-2 py-1", layout===mode && "bg-surface text-ink shadow-btn")} onClick={() => setMode(layout)}>{t(`extensions-codeviews.${layout}`)}</button>)}</span>}
        <button type="button" className={cx(action, options.wrap && "bg-accent-tint text-accent-ink")} data-wrap-toggle aria-label={t("extensions-review.wrap")} title={t("extensions-review.wrap")} aria-pressed={options.wrap} onClick={() => changeOptions({ wrap: !options.wrap })}><Icon name="wrap" size={15}/></button>
        <button type="button" className={action} data-review-options aria-label={t("session-changes.display_options")} title={t("session-changes.display_options")}
          aria-expanded={optionsOpen} aria-controls={optionsId} onClick={() => setOptionsOpen(!optionsOpen)}><Icon name="sliders" size={15}/></button>
      </div>}
      {optionsOpen && files.length > 0 && (
        <div id={optionsId} data-review-options-row className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-b border-line px-3 py-1.5 text-label text-ink-2">
          <label className="inline-flex items-center gap-2">{t("session-changes.context_lines")}
            <select value={options.context} onChange={(e) => changeOptions({ context: Number(e.target.value) })}
              className="min-h-8 rounded-control border border-line bg-surface px-2 text-ink">
              {contextChoices.map((number) => <option key={number} value={number}>{number}</option>)}
            </select>
          </label>
          <label className="inline-flex min-h-8 items-center gap-2">
            <input type="checkbox" checked={options.whitespace} onChange={(e) => changeOptions({ whitespace: e.target.checked })} className="accent-[var(--accent)]"/>
            {t("extensions-review.ignore_whitespace")}
          </label>
        </div>
      )}
      {(scope === "session" || partialExchange) && (tr.hasMore || tr.loadingEarlier) && (
        <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-1.5 text-label text-ink-3" data-history-loading>
          <Spinner size={12} /> {t("session-changes.loading_history")}
        </div>
      )}
      {tr.error && (
        <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-1.5 text-label text-red" role="alert">
          <span className="min-w-0 flex-1 truncate">{tr.error}</span>
          <button type="button" onClick={tr.reload} className="shrink-0 rounded-control px-2 py-1 hover:bg-hover [@media(pointer:coarse)]:min-h-11">{t("session-changes.retry")}</button>
        </div>
      )}
      <div ref={columns} className={cx("flex min-h-0 flex-1", narrow ? "flex-col" : "flex-row")}>
        <div className={cx("relative min-h-0 shrink-0", !narrow && "w-(--changes-tree-w)")} style={{ "--changes-tree-w": `${treeShown}px` } as React.CSSProperties}>
          {narrow && <button type="button" data-files-strip className="flex min-h-9 w-full items-center gap-2 border-b border-line px-3 text-left text-label text-ink-2" aria-controls={treeId} aria-expanded={treeOpen} onClick={() => setTreeOpen((o)=>!o)}><Icon name="list" size={14}/>{t("session-changes.files_label")} <span className="ml-auto">{files.length}</span><Icon name="chevronRight" size={12} className={treeOpen ? "rotate-90" : ""}/></button>}
          <nav hidden={narrow && !treeOpen} id={treeId} aria-label={t("session-changes.files_label")} className={cx("overflow-auto py-1", narrow ? "max-h-40 border-b border-line" : "h-full border-r border-line")}>
            <div className="px-2 py-1"><input type="search" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t("session-changes.find_files")} aria-label={t("session-changes.find_files")} className="h-8 w-full min-w-0 rounded-control border border-line bg-field px-2 text-label text-ink"/></div>
            <FileTree root={tree} collapsed={collapsed} selected={readPath} select={selectFile} toggle={(path) => setCollapsed((old) => { const next = new Set(old); if (next.has(path)) next.delete(path); else next.add(path); return next; })} />
          </nav>
          <ResizeHandle edge="right" value={treeShown} min={CHANGES_TREE_MIN} max={treeMax}
            onResize={setTreeWidth} onCommit={keepTreeWidth} onReset={resetTreeWidth}
            label={t("session-changes.resize_label")} title={t("session-changes.resize_title")} controls={treeId}
            className={narrow ? "hidden" : ""} />
        </div>
        <div ref={diffBody} onWheel={() => { ownerScroll.current = true; }} onTouchMove={() => { ownerScroll.current = true; }}
          onPointerDown={(e) => { if (e.target === e.currentTarget) ownerScroll.current = true; }}
          onKeyDown={(e) => { if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End"].includes(e.key)) ownerScroll.current = true; }}
          onScroll={(e) => followScroll(e.currentTarget)} className="relative min-h-0 min-w-0 flex-1 overflow-auto p-2" data-change-diff>
          {foldLoading && <div className="flex items-center gap-2 p-2 text-label text-ink-3"><Spinner size={12} />{t("session-changes.loading_fold")}</div>}
          {foldError && (
            <div className="flex items-center gap-2 p-2 text-label text-red" role="alert">
              <span className="min-w-0 flex-1">{foldError}</span>
              <button type="button" onClick={retryFolds} className="shrink-0 rounded-control px-2 py-1 hover:bg-hover [@media(pointer:coarse)]:min-h-11">{t("session-changes.retry")}</button>
            </div>
          )}
          {files.map((file) => {
            const calls = byFile.get(file.path) ?? [];
            const result = netByFile.get(file.path);
            const waiting = pendingFile(file.path);
            return <FileReview key={JSON.stringify([scopeKey,file.path])} location={{mid,sid,cwd,path:file.path,shown:file.shown,scope:where}} net={result?.net ?? {regions:[],complete:false,add:0,del:0}} fromGit={result?.fromGit ?? false}
              split={!narrow && mode === "split"} wrap={options.wrap} context={options.context} whitespace={options.whitespace} viewed={reviewed(file.path).viewed && !waiting} onViewed={(yes)=>markViewed(file.path,yes)} pending={waiting}
              focus={opened.at?.path === file.path ? opened.at : undefined} historyCount={calls.length} historyOpen={Boolean(call && calls.some((edit) => edit.call === call || edit.id === call))}
              history={calls.map((edit) => <section key={edit.id} data-change-id={edit.id} data-change-call={edit.call} className="my-2"><DiffBlocks diffs={edit.diffs}/></section>)}/>;
          })}
          {!foldLoading && !files.length && (
            <div className="flex h-full min-h-32 flex-col items-center justify-center gap-2 p-5 text-center text-label text-ink-3">
              <Icon name="fileDiff" size={22} />
              {t("session-changes.empty")}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
