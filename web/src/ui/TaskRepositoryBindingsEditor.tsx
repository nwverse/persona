import { useEffect, useMemo, useRef, useState } from "react";
import { t } from "../i18n";
import { ApiError, errorText } from "../lib/api";
import {
  cleanRepositoryDraft, loadRepositoryCatalog, loadTaskRepositories, repositoryDraft, repositoryRequestContext, saveTaskRepositories, validRepositoryAlias,
  type CreateRepositoryBindings, type RepositoryCatalog, type RepositoryRequestContext, type RequestedRef, type TaskRepository, type TaskRepositoryInput, type TaskRepositorySet,
} from "../lib/task-repositories";
import { Button, Loading, toast } from "./atoms";
import { settingsInputClass } from "../views/settings/SettingsParts";

function useRequestContext(manager: string, owner: string) {
  const alive = useRef(true);
  const identity = useRef("");
  identity.current = `${manager}:${owner}`;
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  return useMemo(() => repositoryRequestContext(manager, owner, () => alive.current && identity.current === `${manager}:${owner}`), [manager, owner]);
}

export type TaskRepositoryBindingsEditorProps = {
  catalog: RepositoryCatalog; value: TaskRepositoryInput[]; snapshots?: TaskRepository[]; pending?: boolean;
  onChange: (value: TaskRepositoryInput[]) => void;
};
/** Pure metadata editor reused by to-dos and downstream task workspace panels. */
export function TaskRepositoryBindingsEditor({ catalog, value, snapshots = [], pending, onChange }: TaskRepositoryBindingsEditorProps) {
  const latest = useRef({ value, pending });
  latest.current = { value, pending };
  const available = catalog.repositories.filter((r) => !value.some((v) => v.repo_id === r.id));
  const change = (index: number, patch: Partial<TaskRepositoryInput>) => onChange(value.map((r, i) => i === index ? { ...r, ...patch } : r));
  const remove = (index: number) => {
    const removed = value[index];
    onChange(value.filter((_, i) => i !== index));
    toast(t("task-repositories.removed"), "info", { label: t("task-repositories.undo"), run: () => {
      if (!latest.current.pending && !latest.current.value.some((r) => r.repo_id === removed.repo_id)) {
        const rows = [...latest.current.value];
        rows.splice(Math.min(index, rows.length), 0, removed);
        onChange(rows);
      }
    } });
  };
  return <div className="space-y-3" data-task-repository-editor>
    {value.map((row, index) => {
      const saved = snapshots.find((r) => r.repo_id === row.repo_id);
      const entry = catalog.repositories.find((r) => r.id === row.repo_id);
      const identity = saved?.source_snapshot.identity ?? entry;
      const ref = row.requested_ref ?? { kind: "default" };
      const links = row.links ?? [];
      const aliasValid = validRepositoryAlias(row.directory_alias ?? "");
      const urls = links.map((link) => "url" in link ? link.url : saved?.links.find((l) => l.kind === link.kind && l.number === link.number)?.canonical_url
        ?? `${identity?.canonical_url}/${identity?.provider === "gitlab" ? link.kind === "issue" ? "-/issues" : "-/merge_requests" : link.kind === "issue" ? "issues" : "pull"}/${link.number}`);
      return <fieldset key={row.repo_id} disabled={pending} className="min-w-0 space-y-3 rounded-control border border-line p-3">
        <legend className="max-w-full break-words px-1 text-meta font-medium text-ink">{saved ? saved.source_snapshot.identity.path : entry?.name || identity?.path || row.repo_id}</legend>
        <p className="break-all text-label text-ink-2">{identity?.canonical_url}</p>
        {saved?.catalog_state && saved.catalog_state !== "current" && <p role="status" className="text-label text-ink-2">{t(saved.catalog_state === "removed" ? "task-repositories.catalog_removed" : "task-repositories.identity_changed")}</p>}
        <label className="block text-label text-ink-2">{t("task-repositories.links")}
          <textarea className={`${settingsInputClass} h-auto min-h-24 mt-1 resize-y py-2`} rows={4} value={urls.join("\n")} autoComplete="off" spellCheck={false}
            onChange={(e) => change(index, { links: e.target.value.split("\n").map((url) => ({ url })) })} />
        </label>
        <details className="text-label text-ink-2">
          <summary className="min-h-11 cursor-pointer py-3">{t("task-repositories.source_options")}</summary>
          <div className="space-y-3 pt-2">
            <label className="block">{t("task-repositories.alias")}
              <input className={`${settingsInputClass} min-h-11 mt-1`} maxLength={64} value={row.directory_alias ?? ""} aria-invalid={!aliasValid}
                onChange={(e) => change(index, { directory_alias: e.target.value })} />
            </label>
            {!aliasValid && <p role="alert">{t("task-repositories.alias_invalid")}</p>}
            <label className="block">{t("task-repositories.source")}
              <select className={`${settingsInputClass} min-h-11 mt-1`} value={ref.kind} onChange={(e) => change(index, { requested_ref: e.target.value === "default" ? { kind: "default" } : { kind: "branch", value: "" } })}>
                <option value="default">{t("task-repositories.default")}</option><option value="branch">{t("task-repositories.branch")}</option>
                {ref.kind !== "default" && ref.kind !== "branch" && <option value={ref.kind}>{t(`task-repositories.${ref.kind}`)}</option>}
              </select>
            </label>
            {ref.kind !== "default" && <label className="block">{t("task-repositories.ref_value")}
              <input className={`${settingsInputClass} min-h-11 mt-1`} value={ref.value} onChange={(e) => change(index, { requested_ref: { kind: ref.kind, value: e.target.value } as RequestedRef })} />
            </label>}
          </div>
        </details>
        <Button tone="quiet" onClick={() => remove(index)}>{t("task-repositories.remove")}<span className="sr-only"> {identity?.path}</span></Button>
      </fieldset>;
    })}
    {available.length > 0 && value.length < 20 && <label className="block text-label text-ink-2">{t("task-repositories.add")}
      <select className={`${settingsInputClass} min-h-11 mt-1`} disabled={pending} value="" onChange={(e) => {
        if (e.target.value) onChange([...value, { repo_id: e.target.value, requested_ref: { kind: "default" }, links: [] }]);
      }}><option value="">{t("task-repositories.choose")}</option>{available.map((r) => <option key={r.id} value={r.id}>{r.name || r.path}</option>)}</select>
    </label>}
    {!catalog.repositories.length && <p className="text-label text-ink-2">{t("task-repositories.empty")}</p>}
    <p className="text-label text-ink-3">{t("task-repositories.hint")}</p>
  </div>;
}

function errorMessage(error: unknown) {
  if (error instanceof ApiError && ["task_repositories_revision_conflict", "task_repositories_catalog_conflict"].includes(error.code ?? "")) return t("task-repositories.conflict");
  if (error instanceof ApiError && error.code?.startsWith("task_repositories_") && error.code !== "task_repositories_store_unavailable") return errorText(error);
  return t("task-repositories.failed");
}

type TodoRepositoryBindingsProps = { manager: string; owner: string; todoId: string };
export function TodoRepositoryBindings(props: TodoRepositoryBindingsProps) {
  return <TodoRepositoryBindingsSession key={`${props.owner}:${props.manager}:${props.todoId}`} {...props} />;
}
function TodoRepositoryBindingsSession({ manager, owner, todoId }: TodoRepositoryBindingsProps) {
  const context = useRequestContext(manager, owner);
  const [expanded, setExpanded] = useState(false);
  const [catalog, setCatalog] = useState<RepositoryCatalog>();
  const [saved, setSaved] = useState<TaskRepositorySet>();
  const [draft, setDraft] = useState<TaskRepositoryInput[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const lock = useRef(false);
  const load = async () => {
    if (lock.current || !context.isCurrent()) return;
    lock.current = true; setBusy(true); setError("");
    try {
      const [catalog, saved] = await Promise.all([loadRepositoryCatalog(context), loadTaskRepositories(context, todoId)]);
      if (context.isCurrent()) { setCatalog(catalog); setSaved(saved); setDraft(repositoryDraft(saved.repositories)); }
    } catch (error) { if (context.isCurrent()) setError(errorMessage(error)); }
    finally { lock.current = false; if (context.isCurrent()) setBusy(false); }
  };
  const save = async () => {
    if (lock.current || !saved || !catalog || !context.isCurrent()) return;
    lock.current = true; setBusy(true); setError("");
    try {
      // Empty text rows are editing affordances, not empty links sent to the server.
      const rows = cleanRepositoryDraft(draft);
      const result = await saveTaskRepositories(context, todoId, saved.revision, catalog.catalog_revision, rows);
      if (context.isCurrent()) { setSaved(result); setDraft(repositoryDraft(result.repositories)); toast(t("task-repositories.saved"), "ok"); }
    } catch (error) { if (context.isCurrent()) setError(errorMessage(error)); }
    finally { lock.current = false; if (context.isCurrent()) setBusy(false); }
  };
  return <section className="mt-4 space-y-3">
    <Button expanded={expanded} tone="quiet" disabled={busy} onClick={() => { setExpanded(!expanded); if (!expanded && !saved) void load(); }}>{t("task-repositories.title")}</Button>
    {expanded && <div className="space-y-3">
      {busy && <Loading label={t("common.loading")} />}
      {catalog && saved && <TaskRepositoryBindingsEditor catalog={catalog} value={draft} snapshots={saved.repositories} pending={busy} onChange={setDraft} />}
      {error && <p role="alert" className="text-label text-red">{error}</p>}
      <div className="flex flex-wrap gap-2">
        {saved && <Button disabled={busy || !draft.every((r) => validRepositoryAlias(r.directory_alias ?? ""))} busy={busy} onClick={() => void save()}>{t("common.save")}</Button>}
        <Button tone="quiet" disabled={busy} onClick={() => void load()}>{t(saved ? "task-repositories.reload" : "task-repositories.retry")}</Button>
      </div>
    </div>}
  </section>;
}

/** Creation remains one POST: the parent sends this optional draft with its Todo. */
export function NewTodoRepositoryBindings({ context, value, onChange, pending }: {
  context: RepositoryRequestContext; value?: CreateRepositoryBindings; onChange: (value: CreateRepositoryBindings | undefined) => void; pending: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const [catalog, setCatalog] = useState<RepositoryCatalog>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = async () => {
    if (busy || !context.isCurrent()) return;
    setBusy(true); setError("");
    try { const catalog = await loadRepositoryCatalog(context); if (context.isCurrent()) setCatalog(catalog); }
    catch { if (context.isCurrent()) setError(t("task-repositories.failed")); }
    finally { if (context.isCurrent()) setBusy(false); }
  };
  return <div className="space-y-3">
    <Button expanded={expanded} tone="quiet" disabled={pending} onClick={() => { setExpanded(!expanded); if (!expanded && !catalog) void load(); }}>{t("task-repositories.title")}</Button>
    {expanded && <>
      {busy && <Loading label={t("common.loading")} />}
      {catalog && <TaskRepositoryBindingsEditor catalog={catalog} value={value?.repositories ?? []} pending={pending} onChange={(rows) => onChange(rows.length ? { catalog_revision: catalog.catalog_revision, repositories: rows } : undefined)} />}
      {error && <><p role="alert" className="text-label text-red">{error}</p><Button tone="quiet" onClick={() => void load()}>{t("task-repositories.retry")}</Button></>}
    </>}
  </div>;
}

export { useRequestContext as useRepositoryRequestContext };
