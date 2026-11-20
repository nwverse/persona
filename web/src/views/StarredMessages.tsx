import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { t } from "../i18n";
import { conversationPage } from "../lib/conversation-page";
import { shortTime } from "../lib/format";
import { href, navigate, onLinkClick, type Route } from "../lib/router";
import { positionalStar, type Star, useStarMark } from "../lib/starred-rows";
import { checkStarSource, reloadStarred, removeStar, useStarredList } from "../lib/starred";
import { reloadStarCollections, useStarOrganization } from "../lib/starred-collections";
import { filterStars, starSpeaker, type SourceFilter } from "../lib/starred-filter";
import { Button, copyText, IconButton, Loading } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { Markdown } from "../ui/Markdown";
import { SearchField } from "../ui/SearchField";
import { StarredCollectionDialog, starredFieldClass, type StarCollectionAction } from "./StarredCollectionDialog";

const PAGE_SIZE = 24;
const PREVIEW_CHARS = 420;
const PREVIEW_LINES = 6;

export function starRoute(star: Star): Route {
  const { machine_id: mid, session_id: sid, item_id: item } = star;
  const at = positionalStar(item) ? (star.source_fingerprint && `star:${star.source_fingerprint}:${item}`) || undefined : item;
  return star.conversation === "assistant" ? conversationPage(sid, mid, star.source_manager, at) : { name: "session", mid, sid, at, view: "chat" };
}

function snapshotPreview(text: string): string {
  const lines = text.split("\n", PREVIEW_LINES + 1);
  const inView = lines.length > PREVIEW_LINES ? lines.slice(0, PREVIEW_LINES).join("\n") : text;
  return [...inView].slice(0, PREVIEW_CHARS).join("");
}

const noAssignments: Readonly<Record<string, string>> = {};

export function StarredMessages({ onToggleSidebar }: { onToggleSidebar?: () => void }) {
  const list = useStarredList();
  const collections = useStarOrganization();
  return <StarredPage key={collections.scope} list={list} collections={collections} onToggleSidebar={onToggleSidebar} />;
}

function StarredPage({ list, collections, onToggleSidebar }: {
  list: ReturnType<typeof useStarredList>;
  collections: ReturnType<typeof useStarOrganization>;
  onToggleSidebar?: () => void;
}) {
  const [query, setQuery] = useState("");
  const [source, setSource] = useState<SourceFilter>("all");
  const [collection, setCollection] = useState("all");
  const [limit, setLimit] = useState(PAGE_SIZE);
  const [selected, setSelected] = useState<ReadonlySet<string> | null>(null);
  const [action, setAction] = useState<StarCollectionAction>();
  const [retrying, setRetrying] = useState(false);
  const search = useRef<HTMLDivElement>(null);
  const organization = collections.data;
  const canOrganize = list.canWrite && organization?.can_write === true;
  const activeCollection = organization?.collections.find((item) => item.id === collection);
  const activeFilter = activeCollection?.id ?? (organization && collection === "unfiled" ? "unfiled" : "all");
  const assignments = organization?.assignments ?? noAssignments;
  const found = useMemo(() => filterStars(list.stars, query, source, activeFilter, assignments), [list.stars, query, source, activeFilter, assignments]);
  const shown = found.slice(0, limit);
  const selection = canOrganize ? selected : null;
  const selectedShown = selection ? shown.filter((star) => selection.has(star.id)).map((star) => star.id) : [];
  const allShownSelected = shown.length > 0 && selectedShown.length === shown.length;

  const retry = async (collectionsOnly = false) => {
    setRetrying(true);
    try { await (collectionsOnly ? reloadStarCollections() : reloadStarred()); }
    finally { setRetrying(false); }
  };
  const restoreFocus = useCallback(() => {
    requestAnimationFrame(() => {
      if (document.activeElement === document.body) search.current?.querySelector("input")?.focus();
    });
  }, []);
  const resetResults = () => { setLimit(PAGE_SIZE); setSelected(null); };
  const toggle = useCallback((id: string) => {
    setSelected((current) => {
      if (!current) return current;
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);
  const move = useCallback((id: string) => setAction({ type: "move", ids: [id] }), []);
  const finishAction = () => {
    if (action?.type === "delete" && action.id === collection) {
      setCollection("all");
      resetResults();
    }
    if (action?.type === "move") setSelected(null);
    setAction(undefined);
    restoreFocus();
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-page">
      <header className="flex shrink-0 items-center gap-3 border-b border-line bg-surface px-4 py-3">
        {onToggleSidebar && <IconButton icon="sidebar" label={t("app.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />}
        <h1 className="truncate text-base font-semibold text-ink">{t("starred.title")}</h1>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-4 px-4 py-5 sm:px-6">
          {list.status === "loading" && !list.error && <Loading />}
          {list.status === "unsupported" && <Notice icon="star" title={t("starred.unsupported")} detail={t("starred.unsupported_hint")} />}
          {list.error && <StarredError message={list.error} retrying={retrying} onRetry={() => void retry()} />}
          {list.status === "ready" && (
            <>
              <div ref={search}>
                <SearchField size="md" value={query} onChange={(text) => { setQuery(text); resetResults(); }} placeholder={t("starred.search")} />
              </div>
              <div className="flex flex-wrap items-end gap-2">
                {organization && <label className="min-w-36 flex-1 text-label text-ink-2">
                  <span className="mb-1 block">{t("starred.collection")}</span>
                  <select className={starredFieldClass} value={activeFilter} onChange={(event) => { setCollection(event.target.value); resetResults(); }}>
                    <option value="all">{t("starred.all_messages")}</option>
                    <option value="unfiled">{t("starred.unfiled")}</option>
                    {organization.collections.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </label>}
                <label className="min-w-36 flex-1 text-label text-ink-2">
                  <span className="mb-1 block">{t("starred.source")}</span>
                  <select className={starredFieldClass} value={source} onChange={(event) => { setSource(event.target.value as SourceFilter); resetResults(); }}>
                    <option value="all">{t("starred.all_sources")}</option>
                    <option value="assistant">{t("starred.assistant")}</option>
                    <option value="agent">{t("starred.agent")}</option>
                  </select>
                </label>
              </div>
              {collections.error && <StarredError message={collections.error} retrying={retrying} onRetry={() => void retry(true)} />}
              {canOrganize && <div className="flex flex-wrap items-center gap-2">
                <Button disabled={collections.writing} onClick={() => setAction({ type: "create" })}>{t("starred.new_collection")}</Button>
                {activeCollection && <>
                  <Button tone="quiet" disabled={collections.writing} onClick={() => setAction({ type: "rename", id: activeCollection.id, name: activeCollection.name })}>{t("starred.rename_collection")}</Button>
                  <Button tone="quiet" disabled={collections.writing} onClick={() => setAction({ type: "delete", id: activeCollection.id, name: activeCollection.name })}>{t("starred.delete_collection")}</Button>
                </>}
                {shown.length > 0 && <Button tone="quiet" onClick={() => setSelected(selection ? null : new Set())}>
                  {t(selection ? "common.cancel" : "starred.select_messages")}
                </Button>}
              </div>}
              {selection && shown.length > 0 && <div className="flex flex-wrap items-center gap-3 rounded-control bg-inset px-3 py-2">
                <label className="flex min-h-11 items-center gap-2 text-label text-ink-2 pointer-fine:min-h-8">
                  <input type="checkbox" checked={allShownSelected}
                    ref={(input) => { if (input) input.indeterminate = !allShownSelected && selectedShown.length > 0; }}
                    onChange={() => setSelected(allShownSelected ? new Set() : new Set(shown.map((star) => star.id)))} />
                  {t("starred.select_shown")}
                </label>
                <span role="status" className="text-label text-ink-3">{t("starred.selected", { count: selectedShown.length })}</span>
                <Button disabled={!selectedShown.length || collections.writing} onClick={() => setAction({ type: "move", ids: selectedShown, bulk: true })}>{t("starred.move_selected")}</Button>
              </div>}
              {!found.length ? (query.trim() || source !== "all"
                ? <Notice icon="search" title={t("starred.no_matches")} />
                : <Notice icon="star" title={t(activeFilter === "all" ? "starred.empty" : "starred.empty_collection")}
                    detail={activeFilter === "all" ? t("starred.empty_hint") : undefined} />)
                : <ol aria-label={t("starred.title")} className="flex min-w-0 flex-col gap-3">
                    {shown.map((star) => <StarCard key={star.id} star={star} canWrite={list.canWrite} afterRemove={restoreFocus}
                      onMove={canOrganize && !selection ? move : undefined} onSelect={selection ? toggle : undefined} selected={selection?.has(star.id)} />)}
                  </ol>}
              {found.length > limit && <div className="self-center"><Button onClick={() => setLimit((n) => n + PAGE_SIZE)}>{t("starred.more")}</Button></div>}
            </>
          )}
        </div>
      </div>
      {action && organization && <StarredCollectionDialog action={action} organization={organization} scope={collections.scope}
        canWrite={canOrganize} busy={collections.writing} stars={list.stars} onClose={() => setAction(undefined)} onSaved={finishAction} />}
    </div>
  );
}

function StarredError({ message, retrying, onRetry }: { message: string; retrying: boolean; onRetry: () => void }) {
  return <div role="alert" className="flex flex-wrap items-center justify-between gap-2 rounded-card border border-line bg-surface px-4 py-3">
    <p className="min-w-0 break-words text-sm text-ink-2">{message}</p>
    <Button icon="retry" onClick={onRetry} busy={retrying}>{t("starred.retry")}</Button>
  </div>;
}

function Notice({ icon, title, detail }: { icon: "star" | "search"; title: string; detail?: string }) {
  return (
    <div className="flex flex-col items-center gap-2 px-4 py-12 text-center">
      <Icon name={icon} size={28} className="text-ink-3" />
      <h2 className="text-base font-semibold text-ink">{title}</h2>
      {detail && <p className="max-w-sm text-sm text-ink-2">{detail}</p>}
    </div>
  );
}

const StarCard = memo(function StarCard({ star, canWrite, afterRemove, onMove, onSelect, selected }: {
  star: Star; canWrite: boolean; afterRemove: () => void;
  onMove?: (id: string) => void; onSelect?: (id: string) => void; selected?: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const openRequest = useRef<symbol | undefined>(undefined);
  useEffect(() => () => { openRequest.current = undefined; }, []);
  const [sourceStatus, setSourceStatus] = useState<"checking" | "unavailable" | "failed">();
  const mark = useStarMark(star);
  const preview = useMemo(() => snapshotPreview(star.text), [star.text]);
  const title = star.title || t("starred.conversation");
  const speaker = starSpeaker(star);
  const source = starRoute(star);
  const positional = positionalStar(star.item_id);
  const unavailable = sourceStatus === "unavailable" || positional && !star.source_fingerprint;
  const openOriginal = (event: React.MouseEvent<HTMLAnchorElement>) => {
    if (!positional) {
      onLinkClick(event, source);
      return;
    }
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    event.preventDefault();
    if (sourceStatus === "checking") return;
    const request = Symbol();
    openRequest.current = request;
    setSourceStatus("checking");
    void checkStarSource(star).then((result) => {
      if (openRequest.current !== request || result === "retired") return;
      openRequest.current = undefined;
      setSourceStatus(result === "verified" ? undefined : result);
      if (result === "verified") navigate(source);
    });
  };
  const date = star.written_at || star.starred_at;
  return (
    <li data-star-id={star.id} className="min-w-0 rounded-card border border-line bg-surface">
      <article className="flex min-w-0 flex-col gap-3 px-4 py-3">
        <header className="flex min-w-0 items-start gap-2">
          {onSelect && <label className="-my-1.5 flex min-h-11 min-w-11 shrink-0 items-center justify-center pointer-fine:min-h-8 pointer-fine:min-w-8">
            <input type="checkbox" aria-label={t("starred.select_message", { title })} checked={selected ?? false} onChange={() => onSelect(star.id)} />
          </label>}
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold text-ink" title={title}>{title}</h2>
            <p className="mt-0.5 flex flex-wrap items-baseline gap-x-2 text-xs text-ink-2">
              <span className="break-words">{speaker}</span>
              <time dateTime={date}>{shortTime(date)}</time>
            </p>
          </div>
        </header>
        <div className="min-w-0 break-words">
          <Markdown text={expanded ? star.text : preview} small compact lineBreaks askImages />
          {star.omitted_chars ? <p className="mt-2 text-xs text-ink-2">{t("starred.truncated", { count: star.omitted_chars })}</p> : null}
          {preview !== star.text && (
            <button type="button" onClick={() => setExpanded(!expanded)} aria-expanded={expanded} className="mt-1 rounded-control px-1 py-1 text-xs font-medium text-accent-ink hover:bg-hover">
              {t(expanded ? "starred.show_less" : "starred.show_snapshot")}
            </button>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          {!unavailable && <a href={href(source)} onClick={openOriginal} aria-busy={sourceStatus === "checking" || undefined}
            className="inline-flex min-h-11 pointer-fine:min-h-8 items-center gap-1.5 whitespace-nowrap rounded-control px-2.5 text-sm font-medium text-accent-ink hover:bg-hover">
            <Icon name="external" />{t(sourceStatus === "checking" ? "common.loading" : "starred.open")}
          </a>}
          <Button icon="copy" className="min-h-11 pointer-fine:min-h-8" onClick={() => void copyText(star.text)}>{t("starred.copy")}</Button>
          {onMove && <Button className="min-h-11 pointer-fine:min-h-8" onClick={() => onMove(star.id)}>{t("starred.move")}</Button>}
          {canWrite && <Button icon="star" className="min-h-11 pointer-fine:min-h-8" busy={mark.pending === "remove"} onClick={() => void removeStar(star).then((removed) => { if (removed) afterRemove(); })}>{t("starred.unstar")}</Button>}
        </div>
        {(unavailable || sourceStatus === "failed") && <p role="status" className="text-xs text-ink-2">
          {t(unavailable ? "starred.source_unavailable" : "starred.source_failed")}
        </p>}
      </article>
    </li>
  );
});
