import { useId, useRef, useState } from "react";
import { t } from "../i18n";
import { api, ApiError } from "../lib/api";
import { useStore } from "../lib/store";
import { loadTaskRepositories, repositoryRequestOptions, type RepositoryLayout, type TaskRepository } from "../lib/task-repositories";
import { loadWorkspaceRules, previewWorkspaceNaming, RULE_FIELDS, rulesPatch, sameRules, saveWorkspaceRules, WORKSPACE_NAMING_VERSION,
  type NamingDiagnostic, type WorkspaceNamingPreview as Preview, type WorkspaceNamingSettings, type WorkspaceRules } from "../lib/workspace-naming";
import { settingsInputClass } from "../views/settings/SettingsParts";
import { Button, Loading } from "./atoms";
import { useRepositoryRequestContext } from "./TaskRepositoryBindingsEditor";

/** Controlled editor: clearing a field is invalid; Use inherited explicitly removes an override. */
export function WorkspaceRulesEditor({ value, inherited, disabled, diagnostics = [], onChange }: {
  value: WorkspaceRules; inherited: WorkspaceRules; disabled?: boolean; diagnostics?: NamingDiagnostic[]; onChange: (value: WorkspaceRules) => void;
}) {
  const id = useId();
  return <div className="space-y-2">
    {RULE_FIELDS.map((field) => {
      const error = diagnostics.find((d) => d.field === field && d.severity === "error");
      return <div key={field} className="grid min-w-0 grid-cols-1 items-center gap-x-3 gap-y-1 sm:grid-cols-3">
        <label htmlFor={`${id}-${field}`} className="text-label text-ink">{t(`workspace-naming.${field}`)}</label>
        <input id={`${id}-${field}`} className={`${settingsInputClass} min-w-0 font-mono text-label pointer-coarse:min-h-11`} spellCheck={false} disabled={disabled}
          value={value[field] ?? inherited[field] ?? ""} onChange={(e) => onChange({ ...value, [field]: e.target.value })}
          aria-invalid={value[field] === "" || !!error} aria-describedby={`${id}-${field}-status${error ? ` ${id}-${field}-error` : ""}`} />
        <div className="flex min-w-0 items-center justify-between gap-2 sm:justify-end">
          <span id={`${id}-${field}-status`} className="text-meta text-ink-3">{t(value[field] === undefined ? "workspace-naming.inherited" : "workspace-naming.overridden")}</span>
          <Button tone="quiet" size="sm" disabled={disabled || value[field] === undefined} onClick={() => { const next = { ...value }; delete next[field]; onChange(next); }}>{t("workspace-naming.inherit")}<span className="sr-only"> {t(`workspace-naming.${field}`)}</span></Button>
        </div>
        {error && <p id={`${id}-${field}-error`} role="alert" className="break-words text-meta text-red col-span-full">{t(`workspace-naming.${error.code}`, { field: t(`workspace-naming.${field}`) })}</p>}
      </div>;
    })}
    <details className="text-meta text-ink-3">
      <summary className="cursor-pointer py-2 pointer-coarse:min-h-11">{t("workspace-naming.template_help")}</summary>
      <dl className="space-y-2 pb-2">{RULE_FIELDS.map((field) => <div key={field}><dt className="font-medium text-ink-2">{t(`workspace-naming.${field}`)}</dt><dd className="break-words">{t(`workspace-naming.help_${field}`)}</dd></div>)}</dl>
    </details>
  </div>;
}

export function WorkspaceNamingPreview({ result, loading, error }: { result?: Preview; loading?: boolean; error?: string }) {
  return <div aria-live="polite" className="space-y-3 text-label text-ink-2">
    {loading && <Loading label={t("workspace-naming.previewing")} />}
    {error && <p role="alert" className="text-red">{error}</p>}
    {result && <>
      <p className="font-medium text-ink">{t(result.sample ? "workspace-naming.sample" : "workspace-naming.preview_title")}</p>
      {result.diagnostics.map((d, index) => <p key={index} className={d.severity === "error" ? "text-red" : "text-ink-2"}>
        {t(`workspace-naming.${d.code}`, { field: RULE_FIELDS.includes(d.field as typeof RULE_FIELDS[number]) ? t(`workspace-naming.${d.field}`) : t("workspace-naming.title") })}{d.repo_id ? ` (${d.repo_id})` : ""}
      </p>)}
      {result.plan && <>
        <p className="break-all rounded-control bg-inset p-3 font-mono text-ink" data-naming-root>{result.plan.root_path}</p>
        <p className="break-words text-meta"><time dateTime={result.plan.source.planned_at}>{result.plan.source.planned_at.slice(0, 16).replace("T", " ")}</time> UTC · {result.plan.source.timezone}</p>
        {result.plan.repositories.map((repo) => <dl key={repo.repo_id} className="space-y-1 border-t border-line pt-2">
          <dt className="font-medium text-ink">{repo.relative_path}</dt><dd className="break-all font-mono">{repo.absolute_path}</dd>
          <dt className="sr-only">{t("workspace-naming.branch_template")}</dt><dd className="break-all font-mono">{repo.task_branch}</dd>
        </dl>)}
      </>}
      <p className="text-meta text-ink-3">{t("workspace-naming.metadata_only")}</p>
    </>}
  </div>;
}

/** Secondary advanced area. It changes no creation mode and never starts an Agent. */
export function WorkspaceNaming({ manager, owner, todoId }: { manager: string; owner: string; todoId?: string }) {
  return <WorkspaceNamingSession key={`${owner}:${manager}:${todoId ?? "settings"}`} manager={manager} owner={owner} todoId={todoId} />;
}
function WorkspaceNamingSession({ manager, owner, todoId }: { manager: string; owner: string; todoId?: string }) {
  const context = useRepositoryRequestContext(manager, owner);
  const machines = useStore((s) => s.machines);
  const [open, setOpen] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [supported, setSupported] = useState(false);
  const [draft, setDraft] = useState<WorkspaceRules>({});
  const [saved, setSaved] = useState<WorkspaceRules>({});
  const [inherited, setInherited] = useState<WorkspaceRules>({});
  const [revision, setRevision] = useState(0);
  const [repositories, setRepositories] = useState<TaskRepository[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [machine, setMachine] = useState("");
  const [layout, setLayout] = useState<RepositoryLayout>("container");
  const [busy, setBusy] = useState(false);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<Preview>();
  const sequence = useRef(0);
  const saving = useRef(false);
  const failure = (error: unknown, fallback: string) => {
    if (error instanceof ApiError && error.code?.startsWith("workspace_naming_")) {
      setResult({ status: "invalid", sample: !todoId, diagnostics: [{ code: error.code.slice("workspace_naming_".length), field: typeof error.body.field === "string" ? error.body.field : "workspace_rules", severity: "error" }] });
      setError(error.status === 400 ? t("workspace-naming.check_fields") : fallback);
      return;
    }
    setError(fallback);
  };
  const invalidate = () => { sequence.current++; setResult(undefined); setPreviewBusy(false); setError(""); };
  const load = async (keepDraft = false) => {
    if (!context.isCurrent() || saving.current) return;
    saving.current = true; setBusy(true); setError("");
    try {
      const info = await api<WorkspaceNamingSettings>("/assistant", repositoryRequestOptions(context));
      if (!context.isCurrent()) return;
      if (info.manager !== manager || info.workspace_naming_version !== WORKSPACE_NAMING_VERSION) { setLoaded(true); setSupported(false); return; }
      let rules = info.workspace_rules ?? {};
      if (todoId) {
        const [record, set] = await Promise.all([loadWorkspaceRules(context, todoId), loadTaskRepositories(context, todoId)]);
        if (!context.isCurrent()) return;
        rules = record.workspace_rules; setRevision(record.revision); setRepositories(set.repositories); if (!keepDraft) setSelected(set.repositories.map((r) => r.repo_id));
      }
      if (!keepDraft) setDraft(rules); setSaved(rules); setInherited(todoId ? info.effective_workspace_rules ?? {} : { base_directory: "{home}/workspace", task_directory_template: "task-{task-id}", branch_template: "persona/{task-id}" });
      if (!keepDraft) setMachine(info.machine_id ?? ""); setSupported(true); setLoaded(true);
    } catch { if (context.isCurrent()) setError(t("workspace-naming.failed")); }
    finally { if (context.isCurrent()) { saving.current = false; setBusy(false); } }
  };
  const save = async () => {
    if (!context.isCurrent() || saving.current) return;
    saving.current = true; setBusy(true); setError("");
    try {
      const response = await saveWorkspaceRules(context, todoId, revision, draft);
      if (!context.isCurrent()) return;
      setSaved({ ...draft }); if (todoId) setRevision(response.revision);
    } catch (error) { if (context.isCurrent()) failure(error, t("workspace-naming.save_failed")); }
    finally { if (context.isCurrent()) { saving.current = false; setBusy(false); } }
  };
  const preview = async () => {
    const generation = ++sequence.current; setPreviewBusy(true); setError(""); setResult(undefined);
    try {
      const response = await previewWorkspaceNaming(context, todoId, { machine_id: machine, layout, selected_repo_ids: selected, workspace_rules: rulesPatch(draft) });
      if (context.isCurrent() && generation === sequence.current) setResult(response);
    } catch (error) { if (context.isCurrent() && generation === sequence.current) failure(error, t("workspace-naming.preview_failed")); }
    finally { if (context.isCurrent() && generation === sequence.current) setPreviewBusy(false); }
  };
  return <div data-workspace-naming data-settings-group="workspace-naming" className="min-w-0">
    <Button tone="quiet" icon={open ? "chevronDown" : "chevronRight"} expanded={open}
      className="min-h-11 w-full justify-start rounded-none px-4 text-left font-normal text-ink"
      onClick={() => { setOpen(!open); if (!open && !loaded) void load(); }}>{t("workspace-naming.title")}</Button>
    {open && <div className="min-w-0 space-y-3 border-t border-line px-4 py-3">
      <p className="text-label text-ink-2">{t("workspace-naming.scope")}</p>
      {busy && !loaded && <Loading />}
      {loaded && !supported && <p className="text-label text-ink-2">{t("workspace-naming.unsupported")}</p>}
      {loaded && supported && <>
        <WorkspaceRulesEditor value={draft} inherited={inherited} disabled={busy} diagnostics={result?.diagnostics} onChange={(value) => { invalidate(); setDraft(value); }} />
        <div className="flex flex-wrap items-center gap-2">
          <Button tone="primary" busy={busy} disabled={sameRules(draft, saved) || RULE_FIELDS.some((field) => draft[field] === "")} onClick={() => void save()}>{t("common.save")}</Button>
          <Button tone="quiet" disabled={busy} onClick={() => { invalidate(); setDraft({}); }}>{t("workspace-naming.reset")}</Button>
          {!sameRules(draft, saved) && <span className="text-meta text-ink-3">{t("workspace-naming.unsaved")}</span>}
        </div>
        <details className="min-w-0 border-t border-line text-label text-ink-2">
          <summary className="min-h-11 cursor-pointer py-3">{t("workspace-naming.preview_options")}</summary>
          <div className="grid min-w-0 gap-3 sm:grid-cols-2">
            <label className="block min-w-0 text-label text-ink-2">{t("workspace-naming.computer")}
              <select aria-label={t("workspace-naming.computer")} className={`${settingsInputClass} mt-2 min-h-11`} value={machine} onChange={(e) => { invalidate(); setMachine(e.target.value); }}>
                <option value="">{t("workspace-naming.choose_computer")}</option>{machines.map((m) => <option key={m.id} value={m.id}>{m.name || m.id}</option>)}
              </select>
            </label>
            <label className="block text-label text-ink-2">{t("workspace-naming.layout")}
              <select aria-label={t("workspace-naming.layout")} className={`${settingsInputClass} mt-2 min-h-11`} value={layout} onChange={(e) => { invalidate(); setLayout(e.target.value as RepositoryLayout); }}>
                <option value="container">{t("workspace-naming.container")}</option><option value="root">{t("workspace-naming.root")}</option>
              </select>
            </label>
          </div>
          {todoId && repositories.map((repo) => <label key={repo.repo_id} className="flex min-h-11 items-center gap-2 text-label text-ink-2">
            <input type="checkbox" checked={selected.includes(repo.repo_id)} onChange={(e) => { invalidate(); setSelected(e.target.checked ? [...selected, repo.repo_id] : selected.filter((id) => id !== repo.repo_id)); }} />{repo.source_snapshot.identity.path}
          </label>)}
          <Button tone="secondary" busy={previewBusy} disabled={!machine} onClick={() => void preview()}>{t("workspace-naming.preview")}</Button>
          <div className="mt-3"><WorkspaceNamingPreview result={result} loading={previewBusy} /></div>
        </details>
      </>}
      {error && <><p role="alert" className="text-label text-red">{error}</p>{loaded && supported && <Button tone="quiet" disabled={busy} onClick={() => { invalidate(); void load(true); }}>{t("workspace-naming.reload")}</Button>}</>}
      {!loaded && !busy && <Button tone="quiet" onClick={() => void load()}>{t("task-repositories.retry")}</Button>}
    </div>}
  </div>;
}
