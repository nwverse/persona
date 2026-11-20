import { useId, useState, useSyncExternalStore } from "react";
import { t } from "../../i18n";
import {
  TODO_VIEW_LIMIT, TODO_VIEW_NAME_LIMIT, todoViewProblem, todoViewSort,
  type SavedTodoCriteria, type SavedTodoView, type SavedTodoViewsModel, type TodoViewMutation,
} from "../../lib/saved-todo-views";
import { Button, Dialog, Loading } from "../../ui/atoms";

type SaveDraft = {
  mode: "save";
  id?: string;
  revision: string;
  baseline: Pick<SavedTodoView, "name" | "criteria">;
  name?: string;
  /** Set only when the owner chooses Use current filters. */
  criteria?: SavedTodoCriteria;
};
type Draft = SaveDraft | { mode: "delete"; id: string; name: string; revision: string };

const touchButton = "[@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:min-w-11";

const draftValues = (draft: SaveDraft) => ({
  name: draft.name ?? draft.baseline.name,
  criteria: draft.criteria ?? draft.baseline.criteria,
});

function criteriaText(criteria: SavedTodoCriteria): string {
  return [criteria.text, t(`todos.filter_${criteria.due_window}`), t(`todos.sort_${todoViewSort(criteria)}`)].filter(Boolean).join(" · ");
}

export function SavedTodoViewsDialog({ model, mode, criteria, onApply, onClose }: {
  model: SavedTodoViewsModel;
  mode: "manage" | "new";
  criteria: SavedTodoCriteria;
  onApply: (criteria: SavedTodoCriteria) => void;
  onClose: () => void;
}) {
  const state = useSyncExternalStore(model.subscribe, model.snapshot, model.snapshot);
  const document = state.document;
  const [draft, setDraft] = useState<Draft | undefined>(() => mode === "new" && document
    ? { mode: "save", baseline: { name: "", criteria }, revision: document.revision } : undefined);
  const [backToList, setBackToList] = useState(mode === "manage");
  const fieldID = useId();
  const ready = Boolean(document?.can_write) && !state.busy && !state.loading;
  const stale = state.conflict || Boolean(draft && document && draft.revision !== document.revision);
  const latest = draft?.id ? document?.views.find((row) => row.id === draft.id) : undefined;
  const missing = Boolean(draft?.id && document && !latest);
  const values = draft?.mode === "save" ? draftValues(draft) : undefined;
  const problem = values ? todoViewProblem(values.name, values.criteria) : "";
  const atLimit = Boolean(draft?.mode === "save" && !draft.id && document && document.views.length >= TODO_VIEW_LIMIT);
  const canSubmit = ready && !stale && !missing && !problem && !atLimit;
  const title = t(!draft ? "saved-todo-views.title" : draft.mode === "delete" ? "saved-todo-views.delete_title"
    : draft.id ? "saved-todo-views.edit_title" : "saved-todo-views.save_current");

  const edit = (view?: SavedTodoView) => {
    if (!document || !ready) return;
    setBackToList(true);
    setDraft({
      mode: "save", id: view?.id, baseline: { name: view?.name ?? "", criteria: view?.criteria ?? criteria },
      revision: document.revision,
    });
  };
  const remove = (view: SavedTodoView) => {
    if (!document || !ready) return;
    setBackToList(true);
    setDraft({ mode: "delete", id: view.id, name: view.name, revision: document.revision });
  };
  const close = () => { if (!state.busy) { if (draft && backToList) setDraft(undefined); else onClose(); } };
  const reload = async () => {
    const fresh = await model.refresh();
    if (!fresh) return;
    setDraft((current) => {
      if (!current) return;
      const next = { ...current, revision: fresh.revision };
      if (next.mode !== "save" || !next.id) return next;
      const latest = fresh.views.find((row) => row.id === next.id);
      if (!latest) return next; // the missing id stays on the disabled draft
      // Carry the owner's edits onto the latest saved values, not the older full object.
      return {
        ...next,
        name: next.name?.trim() === next.baseline.name ? undefined : next.name,
        baseline: { name: latest.name, criteria: latest.criteria },
      };
    });
  };
  const submit = async () => {
    if (!draft || !canSubmit) return;
    let mutation: TodoViewMutation;
    if (draft.mode === "delete") mutation = { op: "delete", id: draft.id };
    else {
      const { name, criteria } = draftValues(draft);
      const values = { name: name.trim(), criteria: { ...criteria, text: criteria.text.trim() } };
      mutation = draft.id ? { op: "update", id: draft.id, ...values } : { op: "add", ...values };
    }
    if (await model.write(mutation, draft.revision)) setDraft(undefined);
  };

  return <Dialog open onClose={close} title={title} footer={draft ? <>
    <Button className={touchButton} tone="quiet" disabled={state.busy} onClick={close}>{t("common.cancel")}</Button>
    <Button className={touchButton} tone={draft.mode === "delete" ? "danger" : "primary"} disabled={!canSubmit}
      busy={state.busy} onClick={() => void submit()}>{t(draft.mode === "delete" ? "saved-todo-views.delete" : "saved-todo-views.save")}</Button>
  </> : <Button className={touchButton} tone="quiet" disabled={state.busy} onClick={onClose}>{t("saved-todo-views.done")}</Button>}>
    <div className="min-w-0 space-y-3">
      {state.unsupported && <p role="status" className="text-sm text-ink-3">{t("saved-todo-views.unsupported")}</p>}
      {!document && state.loading && <Loading />}
      {stale || state.error ? <div className="space-y-2" role="alert">
        <p className="break-words text-sm text-red">{stale ? t("saved-todo-views.conflict") : state.error}</p>
        <Button className={touchButton} busy={state.loading} disabled={state.busy} onClick={() => void reload()}>{t("saved-todo-views.reload")}</Button>
      </div> : null}

      {draft?.mode === "save" && values && <form className="min-w-0 space-y-3" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
        {latest && latest.name !== values.name && stale && <p className="text-sm text-ink-3">
          {t("saved-todo-views.current_name", { name: latest.name })}
        </p>}
        <div>
          <label htmlFor={fieldID} className="mb-1 block text-sm font-medium text-ink">{t("saved-todo-views.name")}</label>
          <input id={fieldID} maxLength={TODO_VIEW_NAME_LIMIT * 2} value={values.name} disabled={!ready}
            className="w-full rounded-control bg-field px-3 py-2 text-sm text-ink outline-none focus:ring-2 focus:ring-accent [@media(pointer:coarse)]:min-h-11"
            onChange={(event) => setDraft({ ...draft, name: event.target.value })} />
        </div>
        <p className="break-words text-sm text-ink-2">{criteriaText(values.criteria)}</p>
        {draft.id && <Button className={touchButton} tone="quiet" disabled={!ready} onClick={() => setDraft({ ...draft, criteria })}>{t("saved-todo-views.use_current")}</Button>}
        {values.name.trim() && problem && <p role="alert" className="text-sm text-red">{problem}</p>}
        {atLimit && <p role="alert" className="text-sm text-ink-2">{t("saved-todo-views.limit", { count: TODO_VIEW_LIMIT })}</p>}
      </form>}
      {draft?.mode === "delete" && <p className="break-words text-sm text-ink-2">{t("saved-todo-views.delete_detail", { name: latest?.name ?? draft.name })}</p>}
      {missing && <p role="alert" className="text-sm text-ink-2">{t("saved-todo-views.missing")}</p>}

      {!draft && document && <>
        {!document.views.length && <p className="text-sm text-ink-3">{t("saved-todo-views.empty")}</p>}
        <ol className="min-w-0 divide-y divide-line">
          {document.views.map((view) => <li key={view.id} data-saved-todo-view={view.id} className="space-y-1 py-2">
            <button type="button" className="block min-h-11 w-full min-w-0 rounded-control px-2 py-1 text-left hover:bg-hover"
              aria-label={t("saved-todo-views.apply_named", { name: view.name })} disabled={state.busy} onClick={() => onApply(view.criteria)}>
              <span className="block break-words text-sm font-medium text-ink">{view.name}</span>
              <span className="block break-words text-label text-ink-3">{criteriaText(view.criteria)}</span>
            </button>
            {document.can_write && <div className="flex flex-wrap gap-2">
              <Button className={touchButton} tone="quiet" disabled={!ready} aria-label={t("saved-todo-views.edit_named", { name: view.name })} onClick={() => edit(view)}>
                {t("saved-todo-views.edit")}
              </Button>
              <Button className={touchButton} tone="quiet" disabled={!ready} aria-label={t("saved-todo-views.delete_named", { name: view.name })} onClick={() => remove(view)}>
                {t("saved-todo-views.delete")}
              </Button>
            </div>}
          </li>)}
        </ol>
        {document.can_write && <Button className={touchButton} icon="plus" disabled={!ready || document.views.length >= TODO_VIEW_LIMIT} onClick={() => edit()}>{t("saved-todo-views.save_current")}</Button>}
      </>}
    </div>
  </Dialog>;
}
