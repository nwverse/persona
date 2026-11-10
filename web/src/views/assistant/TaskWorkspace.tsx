import { useEffect, useId, useState } from "react";
import { t } from "../../i18n";
import { api, enc, errorText } from "../../lib/api";
import { conversationHeaders, type PlanTask, type TaskPlan, type TaskWorkspace } from "../../lib/feed";
import type { WorkspaceList } from "../../lib/workspace-board";
import { fieldClass } from "./parts";

export function workspaceMode(w?: TaskWorkspace): "new" | "existing" | "none" {
  return typeof w === "object" ? ("existing" in w ? "existing" : "new") : w ?? "none";
}

export function TaskWorkspaceLine({ task }: { task: PlanTask }) {
  if (!task.workspace) return null;
  return <div className="min-w-0 text-[12px] break-all text-ink-2" data-task-workspace>
    {t("assistant-tasks.workspace")} · {t(`assistant-tasks.workspace_${workspaceMode(task.workspace)}`)} · {task.workspace_resolved?.path ?? task.cwd}
  </div>;
}

/** No paths are synthesized in the browser: machine/cwd/branch changes ask the
 * engine-backed preview, and Start sends only the editable policy. */
export function TaskWorkspaceEditor({ plan, index, disabled, onChange }: {
  plan: TaskPlan; index: number; disabled?: boolean; onChange: (workspace: TaskWorkspace) => void;
}) {
  const task = plan.tasks[index];
  const mode = workspaceMode(task.workspace);
  const id = useId();
  const [list, setList] = useState<WorkspaceList>({ workspaces: [] });
  const [preview, setPreview] = useState({ key: "", path: "", error: "" });
  const key = JSON.stringify([task.machine, task.cwd, task.workspace]);
  const headers = conversationHeaders(plan.conversation);
  useEffect(() => {
    if (mode !== "existing") return;
    let live = true;
    void api<WorkspaceList>("/workspaces", { headers: conversationHeaders(plan.conversation) }).then((r) => {
      if (live) setList(r);
    }).catch(() => { if (live) setList({ workspaces: [] }); });
    return () => { live = false; };
  }, [mode, task.machine, plan.conversation]);
  useEffect(() => {
    if (!task.workspace) return;
    let live = true;
    const timer = window.setTimeout(() => {
      void api<{ plan: TaskPlan }>(`/task-plans/${enc(plan.id)}/workspace-preview`, {
        method: "POST", headers,
        json: { tasks: plan.tasks.map((t, i) => i === index ? { machine: t.machine, cwd: t.cwd, workspace: t.workspace } : {}) },
      }).then((r) => {
        if (live) setPreview({ key, path: r.plan.tasks[index].workspace_resolved?.path ?? "", error: "" });
      }).catch((e) => { if (live) setPreview({ key, path: "", error: errorText(e) }); });
    }, 200);
    return () => { live = false; window.clearTimeout(timer); };
    // The key is the complete editable resolution input; other task changes are irrelevant.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, plan.id, plan.conversation, index]);
  if (!task.workspace) return null; // Older hubs retain their original card.
  const current = preview.key === key ? preview : null;
  const original = task.workspace_resolved?.machine === task.machine && task.workspace_resolved?.path;
  const pathText = current?.path || (!preview.key && original) || "";
  const existing = typeof task.workspace === "object" && "existing" in task.workspace ? task.workspace.existing : task.cwd;
  const branch = typeof task.workspace === "object" && "new" in task.workspace ? task.workspace.new.branch ?? "" : "";
  const field = `${fieldClass} min-w-0 max-w-full [@media(pointer:coarse)]:min-h-11`;
  return <fieldset disabled={disabled} className="flex min-w-0 flex-col gap-1.5" data-task-workspace-editor>
    <div className="flex flex-wrap items-center gap-2">
      <label htmlFor={`${id}-policy`} className="text-[12px] text-ink-2">{t("assistant-tasks.workspace")}</label>
      <select id={`${id}-policy`} value={mode} className={field} onChange={(e) => onChange(e.target.value === "existing" ? { existing: task.cwd } : e.target.value as "new" | "none")}>
        <option value="new">{t("assistant-tasks.workspace_new")}</option>
        <option value="existing">{t("assistant-tasks.workspace_existing")}</option>
        <option value="none">{t("assistant-tasks.workspace_none")}</option>
      </select>
      {mode === "existing" && <select aria-label={t("assistant-tasks.workspace_select")} value={list.workspaces.some((w) => w.id === existing) ? existing : ""} className={`${field} flex-1`}
        onChange={(e) => { if (e.target.value) onChange({ existing: e.target.value }); }}>
        <option value="">{t("assistant-tasks.workspace_manual")}</option>
        {list.workspaces.filter((w) => w.machine_id === task.machine && w.available).map((w) => <option key={w.id} value={w.id}>{[w.title, w.branch, w.cwd].filter(Boolean).join(" · ")}</option>)}
      </select>}
    </div>
    {mode === "existing" && <input aria-label={t("assistant-tasks.workspace_path")} className={`${field} w-full font-mono`} value={existing}
      spellCheck={false} autoCapitalize="off" autoCorrect="off" onChange={(e) => onChange({ existing: e.target.value })} />}
    {mode === "new" && <input aria-label={t("assistant-tasks.workspace_branch")} className={`${field} w-full font-mono`} value={branch}
      placeholder={task.workspace_resolved?.branch || t("assistant-tasks.workspace_branch_default")}
      spellCheck={false} autoCapitalize="off" autoCorrect="off" onChange={(e) => onChange({ new: { branch: e.target.value } })} />}
    <div className="text-[12px] break-all text-ink-2" aria-live="polite" data-workspace-path>
      {current?.error ? <span role="alert" className="text-red">{current.error}</span> : pathText || t("assistant-tasks.workspace_resolving")}
    </div>
    {mode === "new" && <div className="text-[11px] text-ink-3">{t("assistant-tasks.workspace_engine_path")}</div>}
  </fieldset>;
}
