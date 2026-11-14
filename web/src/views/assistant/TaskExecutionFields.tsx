// Task-local execution choices. Discovery uses the selected computer and the same launcher/model APIs as chat.
import { useEffect, useRef, useState } from "react";
import { errorText } from "../../lib/api";
import { modelPurpose, type ModelPurpose } from "../../lib/model-config";
import { defaultModel, effortChoices, machineLaunchers, providerModels, takesEffort, type MachineLaunchers } from "../../lib/launcher-models";
import { launcherTitle } from "../../lib/provider-names";
import { shownManager } from "../../lib/manager-choice";
import { taskExecutionSummary, type Task } from "../../lib/routines";
import { useStore } from "../../lib/store";
import { ModelPicker, type ModelList } from "../../ui/ModelPicker";
import { Button } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Field, inputClass } from "./parts";
import { t } from "../../i18n";

export function TaskExecutionFields({ draft, disabled, onChange }: { draft: Task; disabled: boolean; onChange: (patch: Partial<Task>) => void }) {
  const machines = useStore((s) => s.machines);
  const [expanded, setExpanded] = useState(false);
  const [main, setMain] = useState<ModelPurpose>();
  const [launchers, setLaunchers] = useState<MachineLaunchers>();
  const [models, setModels] = useState<ModelList>({ status: "idle" });
  const [problem, setProblem] = useState("");
  const [reload, setReload] = useState(0);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const search = useRef<HTMLInputElement>(null);
  useEffect(() => {
    let live = true;
    modelPurpose("assistant").then((r) => { if (live) { setMain(r.purpose); setProblem(""); } }, (e) => { if (live) setProblem(errorText(e)); });
    return () => { live = false; };
  }, [reload]);
  const machine = draft.execution_machine_id || main?.machine_id || draft.execution?.machine_id || "";
  const inheritedLauncher = main?.effective?.provider?.replace(/^launcher\./, "") || draft.execution?.launcher || "";
  const launcher = draft.launcher || inheritedLauncher;
  const inheritsAssistant = !draft.launcher || draft.launcher === inheritedLauncher;
  const inherited = inheritsAssistant ? main?.effective ?? {} : {};
  useEffect(() => {
    setLaunchers(undefined);
    if (!machine) return;
    const controller = new AbortController();
    machineLaunchers(machine, { purpose: "assistant", signal: controller.signal }).then((r) => { setLaunchers(r); setProblem(""); }, (e) => { if (!controller.signal.aborted) setProblem(errorText(e)); });
    return () => controller.abort();
  }, [machine, reload]);
  useEffect(() => {
    setOpen(false); setQuery("");
    if (!expanded) return;
    if (!machine || !launcher) { setModels({ status: "idle" }); return; }
    const controller = new AbortController();
    setModels({ status: "loading" });
    providerModels(`launcher.${launcher}`, { machine, signal: controller.signal, refresh: reload > 0 })
      .then((list) => setModels({ status: "ready", list }))
      .catch((e) => { if (!controller.signal.aborted) setModels({ status: "error", error: errorText(e) }); });
    return () => controller.abort();
  }, [expanded, machine, launcher, reload]);
  const list = models.list;
  const model = draft.model || inherited.model;
  const selectedModel = list?.models.find((m) => m.id === model) ?? (!model ? defaultModel(list?.models ?? []) : undefined);
  const efforts = list && (takesEffort(list.agent) || selectedModel?.efforts?.length) ? effortChoices(list, draft.model || "", inherited) : [];
  const known = launchers?.launchers ?? [];
  const knownLauncher = known.find((l) => l.name === launcher);
  const agent = knownLauncher?.agent_type || knownLauncher?.agent || list?.agent || (draft.execution?.launcher === launcher ? draft.execution.agent : "");
  const defaultLabel = t(inheritsAssistant ? "assistant-taskeditor.follow_assistant" : "assistant-taskeditor.follow_launcher");
  const defaultEffort = inherited.effort || selectedModel?.default_effort;
  const defaultModelID = selectedModel?.resolved || (selectedModel?.id !== "default" ? selectedModel?.id : "");
  const summary = taskExecutionSummary({ launcher, agent, model: model || defaultModelID, effort: draft.effort || defaultEffort }, known, defaultLabel);
  const incompatibleOrigin = !!draft.session_id && !!(draft.launcher || draft.model || draft.effort || draft.cwd || draft.execution_machine_id);
  return <details onToggle={(e) => setExpanded(e.currentTarget.open)} className="rounded-card bg-inset p-3 shadow-hairline">
    <summary className="flex cursor-pointer list-none items-center gap-2 text-[13px] font-medium text-ink [&::-webkit-details-marker]:hidden">
      <Icon name="chevron" size={13} className={expanded ? "shrink-0" : "shrink-0 -rotate-90"} />
      <span className="shrink-0">{t("assistant-taskeditor.execution_options")}</span>
      <span className="min-w-0 flex-1 truncate text-[12px] font-normal text-ink-2" title={summary}>{summary}</span>
    </summary>
    <div className="mt-3 flex flex-col gap-3">
      <p className="text-[11.5px] leading-relaxed text-ink-3">{t("assistant-taskeditor.execution_inheritance")}</p>
      <Field label={t("assistant-taskeditor.execution_computer")}>
        <select className={inputClass} value={draft.execution_machine_id ?? ""} disabled={disabled} onChange={(e) => onChange({ execution_machine_id: e.target.value })}>
          <option value="">{t("assistant-taskeditor.computer_default", { name: machines.find((m) => m.id === main?.machine_id)?.name || main?.machine_id || "…" })}</option>
          {draft.execution_machine_id && !machines.some((m) => m.id === draft.execution_machine_id) && <option value={draft.execution_machine_id}>{draft.execution_machine_id}</option>}
          {machines.filter((m) => !m.manager || m.manager === shownManager()).map((m) => <option key={m.id} value={m.id}>{m.name}{m.online ? "" : ` · ${t("assistant-taskeditor.computer_offline")}`}</option>)}
        </select>
      </Field>
      <Field label={t("assistant-taskeditor.execution_agent")}>
        <select className={inputClass} value={draft.launcher ?? ""} disabled={disabled || !launchers} onChange={(e) => onChange({ launcher: e.target.value, model: "", effort: "" })}>
          <option value="">{t("assistant-taskeditor.agent_default", { name: launcherTitle(inheritedLauncher, known) || "…" })}</option>
          {draft.launcher && !known.some((l) => l.name === draft.launcher) && <option value={draft.launcher}>{draft.launcher} · {t("assistant-taskeditor.execution_unavailable")}</option>}
          {known.map((l) => <option key={l.name} value={l.name}>{launcherTitle(l.name, known)}{l.agent ? ` · ${l.agent}` : ""}</option>)}
        </select>
      </Field>
      <div>
        <span className="mb-1 block text-[12px] font-medium text-ink-2">{t("settings-modelconfiguration.specific_model")}</span>
        <fieldset className="min-w-0" disabled={disabled}><ModelPicker models={models} model={draft.model || ""} followed={inherited} defaultLabel={defaultLabel} open={open} onToggle={() => setOpen((v) => !v)} query={query} onQuery={setQuery} search={search}
          onPick={(model) => { if (!disabled) { onChange({ model }); setOpen(false); } }} onReload={() => setReload((v) => v + 1)} custom failedNote={t("settings-modelconfiguration.list_failed")} /></fieldset>
        {models.status === "error" && <p role="alert" className="mt-1 break-words text-[12px] text-red">{models.error}</p>}
      </div>
      <Field label={t("settings-modelconfiguration.effort")}>
        <select className={inputClass} value={draft.effort ?? ""} disabled={disabled} onChange={(e) => onChange({ effort: e.target.value })}>
          <option value="">{[defaultLabel, defaultEffort].filter(Boolean).join(" · ")}</option>
          {draft.effort && !efforts.includes(draft.effort) && <option value={draft.effort}>{draft.effort}</option>}
          {efforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}
        </select>
      </Field>
      <Field label={t("assistant-taskeditor.execution_directory")} note={t("assistant-taskeditor.directory_hint")}>
        <input className={inputClass} value={draft.cwd ?? ""} disabled={disabled} placeholder={t("assistant-taskeditor.directory_follow")} spellCheck={false} autoComplete="off" onChange={(e) => onChange({ cwd: e.target.value })} />
      </Field>
      <p className="text-[11.5px] leading-relaxed text-ink-3">{t("assistant-taskeditor.execution_sessions")}</p>
      {incompatibleOrigin && <p role="alert" className="text-[12px] text-red">{t("assistant-taskeditor.execution_own_chat")}</p>}
      {(problem || draft.execution_error) && <div role="alert" className="text-[12px] text-red">{problem || draft.execution_error}<Button tone="quiet" onClick={() => setReload((v) => v + 1)}>{t("settings-modelconfiguration.retry")}</Button></div>}
    </div>
  </details>;
}
