// Overview by project (#13019, docs/web/web.md "Overview by project"): one card per top-level project of the knowledge
// map's project tree, drawn from the same reads as the tree's node panel (views/knowledge/ProjectTree.tsx: the project
// list with parents and milestones, a project's detail for its notes) and counted by the same function
// (lib/project-overview.ts projectFacts), so a card never says more, or other numbers, than the node. Nothing is
// summarized again. Under the cards the to-dos in no project: Temporary and Unsorted, with the way to the sort sheet
// (views/todos/SortTodos.tsx). A chunk of its own: Overview's first screen carries only its lazy import (views/Home.tsx).
import { lazy, Suspense, useEffect, useState } from "react";
import { formatDate, t } from "../../i18n";
import { api, enc, isAbort } from "../../lib/api";
import { useScopedTodos } from "../../lib/current-assistant";
import { mainConversationMachine } from "../../lib/feed-cache";
import { type KBDetailResponse } from "../../lib/kb";
import { milestoneStateLabel, type TreeEntry } from "../../lib/kb-tree";
import { looseTodos, projectFacts, topProjects } from "../../lib/project-overview";
import { href, navigate, onLinkClick, type Route } from "../../lib/router";
import { useStore } from "../../lib/store";
import type { Todo } from "../../lib/todos";
import { Button, cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { QuestionSummary } from "../chat/QuestionSummary";
import { useTreeData, yesterdayNotes, type TreeData } from "../knowledge/ProjectTree";

const SortTodos = lazy(() => import("../todos/SortTodos"));

/** How many to-do titles a Temporary or Unsorted block lists before "and N more". */
export const LOOSE_ROWS = 5;

/** The knowledge map's project tree with one project selected (/kb/map/<slug>). */
export const projectRoute = (slug: string): Route => ({ name: "kb", kind: "map", id: slug });

const CARD_GRID = "grid items-stretch gap-3 [grid-template-columns:repeat(auto-fill,minmax(min(100%,320px),1fr))]";

export default function ProjectOverview() {
  const tree = useTreeData(true, 0);
  const todos = useScopedTodos();
  const [sorting, setSorting] = useState(false);
  const list = tree.list;
  const tops = list ? topProjects(list, tree.parents) : [];
  const { temporary, unsorted } = looseTodos(todos);
  return (
    <div className="flex flex-col gap-8" data-project-overview="">
      <section aria-labelledby="overview-by-project">
        <Heading id="overview-by-project" title={t("overview-projects.by_project")} count={list ? tops.length : undefined}
          right={tops.length > 0 && <Link to={{ name: "kb", kind: "map" }}>{t("overview-projects.open_tree")}</Link>} />
        {tree.error && !list ? (
          <p className="text-label text-ink-3" data-project-overview-error="">{t("overview-projects.failed", { error: tree.error })}</p>
        ) : !list ? null : tops.length === 0 ? (
          <NoProjects />
        ) : (
          <div className={CARD_GRID}>
            {tops.map((e) => <ProjectCard key={e.id} e={e} data={tree} todos={todos} />)}
          </div>
        )}
      </section>
      <div className="grid gap-8 @min-[60rem]:grid-cols-2">
        <LooseBlock kind="temporary" title={t("overview-projects.temporary")} hint={t("overview-projects.temporary_hint")} empty={t("overview-projects.temporary_empty")} rows={temporary} />
        <LooseBlock kind="unsorted" title={t("overview-projects.unsorted")} hint={t("overview-projects.unsorted_hint")} empty={t("overview-projects.unsorted_empty")} rows={unsorted}
          action={unsorted.length > 0 && (
            <button type="button" onClick={() => setSorting(true)} data-overview-sort="" className="touch-hit text-label font-medium whitespace-nowrap text-accent-ink hover:underline">
              {t("overview-projects.sort", { count: unsorted.length })}
            </button>
          )} />
      </div>
      <MainQuestions />
      {sorting && (
        <Suspense fallback={null}>
          <SortTodos onClose={() => setSorting(false)} />
        </Suspense>
      )}
    </div>
  );
}

/** The main chat's questions in progress and unread, as its question rail marks them (#13021, lib/question-reads.ts
 *  useQuestionSummary): the rail publishes its marks while the main chat is mounted, and this reads them. Overview
 *  does not load the main chat's lines, so while that chat is closed it draws nothing rather than a count of its own
 *  (docs/web/web.md "Overview by project"). Opens the main chat. */
function MainQuestions() {
  const assistant = useStore((s) => s.assistant);
  const mid = mainConversationMachine(assistant);
  const sid = assistant?.main;
  if (!mid || !sid) return null;
  return <QuestionSummary scope={`${mid}/${sid}`} onOpen={() => navigate({ name: "persona", sid })} />;
}

function Heading({ id, title, count, right }: { id?: string; title: string; count?: number; right?: React.ReactNode }) {
  return (
    <div className="mb-2.5 flex items-baseline gap-2">
      <h2 id={id} className="text-meta font-medium text-ink">{title}</h2>
      {count !== undefined && <span className="text-label text-ink-3 tabular-nums">{count}</span>}
      <span className="flex-1" />
      {right}
    </div>
  );
}

function Link({ to, children }: { to: Route; children: React.ReactNode }) {
  return <a href={href(to)} onClick={(e) => onLinkClick(e, to)} className="touch-hit text-label whitespace-nowrap text-ink-3 hover:text-ink-2">{children}</a>;
}

/** The latest of the project's own notes from yesterday, as its node panel lists them (its parent's are not inherited). */
function useLatestNote(id: string, updated: string | undefined): string | null | undefined {
  const [note, setNote] = useState<string | null | undefined>(undefined);
  useEffect(() => {
    const ac = new AbortController();
    api<KBDetailResponse>(`/kb/projects/${enc(id)}`, { signal: ac.signal })
      .then((r) => setNote(yesterdayNotes(r.detail.log).at(-1) ?? null))
      .catch((x) => { if (!isAbort(x)) setNote(null); });
    return () => ac.abort();
  }, [id, updated]);
  return note;
}

/** One top-level project: its name, the projects under it, its milestones, its open to-dos (and its subtree's), and
 *  its latest note from yesterday. The card opens the project tree with the project selected. */
export function ProjectCard({ e, data, todos }: { e: TreeEntry; data: TreeData; todos: readonly Todo[] }) {
  const facts = projectFacts(data.list ?? [], data.parents, todos, e.id);
  const note = useLatestNote(e.id, e.updated);
  const to = projectRoute(e.id);
  const shown = facts.children.slice(0, 4);
  return (
    <div data-project-card={e.id} className="relative isolate flex h-full flex-col gap-2.5 rounded-card bg-surface px-3.5 py-3 shadow-card transition-colors hover:bg-hover">
      <div className="flex min-w-0 items-center gap-2">
        <Icon name="layers" size={14} className="shrink-0 text-ink-3" />
        {/* the title link's overlay spans the card: a click anywhere on it opens the tree */}
        <a href={href(to)} onClick={(ev) => onLinkClick(ev, to)} className="min-w-0 flex-1 truncate text-card-title font-semibold text-ink after:absolute after:inset-0 after:z-[-1] after:content-['']">
          {e.name}
        </a>
        <span data-card-todos={facts.openTodos} className={cx("inline-flex h-5 shrink-0 items-center rounded-full px-1.5 text-submeta font-medium whitespace-nowrap tabular-nums", facts.openTodos ? "bg-accent-tint text-accent-ink" : "bg-field text-ink-3")}>
          {t("overview-projects.open_todos", { count: facts.openTodos })}
        </span>
      </div>
      {e.one_line && <p className="-mt-1 truncate text-label text-ink-2">{e.one_line}</p>}
      <div className="flex min-w-0 items-center gap-1.5 text-label text-ink-3" data-card-children={facts.children.length}>
        <Icon name="folder" size={12} className="shrink-0" />
        {facts.children.length ? (
          <span className="min-w-0 truncate">
            {t("overview-projects.sub_projects", { count: facts.children.length })}
            {" · "}
            {shown.map((c) => c.name).join(t("overview-projects.name_separator"))}
            {facts.children.length > shown.length && t("overview-projects.and_more", { count: facts.children.length - shown.length })}
          </span>
        ) : <span>{t("overview-projects.no_sub_projects")}</span>}
      </div>
      {facts.milestones.length > 0 && (
        <ul className="flex flex-col gap-1" data-card-milestones={facts.milestones.length}>
          {facts.milestones.map((m) => (
            <li key={m.id} className="flex min-w-0 items-center gap-2 text-label text-ink">
              <Icon name="flag" size={12} className={cx("shrink-0", m.state === "accepted" ? "text-green" : m.state === "not_accepted" ? "text-orange" : "text-accent-ink")} />
              <span className="min-w-0 flex-1 truncate">{m.title}</span>
              {m.target && <span className="shrink-0 text-submeta text-ink-3 tabular-nums">{formatDate(`${m.target}T12:00:00`, { month: "short", day: "numeric" })}</span>}
              <span className={cx("shrink-0 text-submeta", m.state === "accepted" ? "text-green" : m.state === "not_accepted" ? "text-orange" : "text-ink-3")}>{milestoneStateLabel(m.state)}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-auto truncate border-t border-line pt-2 text-label text-ink-3" data-card-note="">
        <span className="font-medium text-ink-2">{t("overview-projects.yesterday")}</span>
        {" "}
        {note === undefined ? "…" : note || t("overview-projects.no_yesterday")}
      </p>
    </div>
  );
}

function NoProjects() {
  return (
    <div className="flex flex-col items-start gap-3 rounded-card bg-surface p-4 shadow-card" data-project-overview-empty="">
      <span className="flex size-8 items-center justify-center rounded-full bg-field text-ink-2"><Icon name="layers" size={15} /></span>
      <div>
        <p className="text-meta font-medium text-ink">{t("overview-projects.empty_title")}</p>
        <p className="mt-0.5 text-label text-ink-2">{t("overview-projects.empty_hint")}</p>
      </div>
      <Button tone="secondary" icon="branch" onClick={() => navigate({ name: "kb", kind: "map" })}>{t("overview-projects.open_tree")}</Button>
    </div>
  );
}

/** Temporary or Unsorted: how many, the first few titles (each opens its to-do) and, for Unsorted, the sort sheet. */
function LooseBlock({ kind, title, hint, empty, rows, action }: { kind: string; title: string; hint: string; empty: string; rows: Todo[]; action?: React.ReactNode }) {
  const shown = rows.slice(0, LOOSE_ROWS);
  return (
    <section data-loose={kind} aria-label={title}>
      <Heading title={title} count={rows.length} right={action} />
      <div className="rounded-card bg-surface px-3.5 py-2.5 shadow-card">
        <p className="text-label text-ink-3">{rows.length ? hint : empty}</p>
        {shown.length > 0 && (
          <ul className="mt-1.5">
            {shown.map((td) => {
              const to: Route = { name: "todos", id: td.id };
              return (
                <li key={td.id} className="border-t border-line first:border-t-0">
                  <a href={href(to)} onClick={(ev) => onLinkClick(ev, to)} className="flex min-w-0 items-center gap-2 py-1.5 text-meta text-ink hover:text-accent-ink">
                    <Icon name="todo" size={12} className="shrink-0 text-ink-3" />
                    <span className="truncate">{td.title}</span>
                  </a>
                </li>
              );
            })}
          </ul>
        )}
        {rows.length > shown.length && (
          <Link to={{ name: "todos" }}>{t("overview-projects.more", { count: rows.length - shown.length })}</Link>
        )}
      </div>
    </section>
  );
}
