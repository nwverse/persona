// "Sort unsorted" (#13026, docs/assistant/todos.md "Sorting into projects"): the hub's suggestion for each open to-do
// in no project — a knowledge-base project node or Temporary — with how sure it is and why; the sure ones ticked, the
// unsure not. The owner unticks or changes a target, and the ticked ones are written as one batch, with Undo in the
// toast. Undo puts back what is still as the batch left it and says how many changed since.
import { useEffect, useMemo, useState } from "react";
import { errorText } from "../../lib/api";
import { refreshTodos } from "../../lib/store";
import {
  applySort,
  chosenMoves,
  initialPicks,
  loadSortSuggestions,
  sureness,
  TEMPORARY,
  undoSort,
  type SortPick,
  type SortSuggestion,
  type SortSuggestions,
} from "../../lib/todo-sort";
import { Button, cx, Dialog, Loading, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

const FIELD = "max-w-44 truncate rounded-control border border-line bg-canvas px-1.5 py-0.5 text-label text-ink outline-none focus:border-accent";
const TONE = { sure: "bg-green-tint text-green", fairly: "bg-field text-ink-2", unsure: "bg-field text-ink-3" } as const;

/** Undoes batch and says what it did: how many came back, and how many were left because they changed since. */
export async function undoSortBatch(batch: string) {
  try {
    const r = await undoSort(batch);
    void refreshTodos();
    toast(r.skipped ? t("todos-sort.undone_skipped", { count: r.undone, skipped: r.skipped }) : t("todos-sort.undone", { count: r.undone }), "ok");
  } catch (e) {
    toast(t("todos-sort.undo_failed", { error: errorText(e) }), "error");
  }
}

export default function SortTodos({ onClose }: { onClose: () => void }) {
  const [data, setData] = useState<SortSuggestions | null>(null);
  const [failed, setFailed] = useState("");
  const [picks, setPicks] = useState<Map<string, SortPick>>(new Map());
  const [rest, setRest] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const ask = new AbortController();
    loadSortSuggestions(ask.signal).then(
      (d) => {
        setData(d);
        setPicks(initialPicks(d.items));
      },
      (e) => {
        if (!ask.signal.aborted) setFailed(errorText(e));
      },
    );
    return () => ask.abort();
  }, []);

  const suggested = useMemo(() => (data?.items ?? []).filter((it) => it.project || it.temporary), [data]);
  const unplaced = useMemo(() => (data?.items ?? []).filter((it) => !it.project && !it.temporary), [data]);
  const moves = useMemo(() => chosenMoves(data?.items ?? [], picks), [data, picks]);
  const targets = data?.projects ?? [];

  const set = (it: SortSuggestion, patch: Partial<SortPick>) =>
    setPicks((m) => {
      const next = new Map(m);
      next.set(it.todo, { ...(m.get(it.todo) ?? { on: false, target: "" }), ...patch });
      return next;
    });

  const apply = async () => {
    setBusy(true);
    try {
      const r = await applySort(moves);
      void refreshTodos();
      const batch = r.batch;
      toast(t("todos-sort.done", { count: r.moved }), "ok", batch ? { label: t("todos-sort.undo"), run: () => void undoSortBatch(batch) } : undefined);
      onClose();
    } catch (e) {
      toast(t("todos-sort.failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open
      onClose={onClose}
      title={t("todos-sort.title")}
      width={720}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button tone="primary" icon="check" busy={busy} disabled={!moves.length} onClick={() => void apply()} testId="todos-sort-apply">
            {t("todos-sort.apply", { count: moves.length })}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4" data-testid="todos-sort">
        {failed && <p className="text-meta text-red">{t("todos-sort.load_failed", { error: failed })}</p>}
        {!data && !failed && <Loading label={t("todos-sort.loading")} />}
        {data && (
          <>
            <p className="text-meta leading-normal text-ink-2">
              {data.items.length ? t("todos-sort.summary", { count: suggested.length, total: data.unsorted }) : t("todos-sort.nothing")}
            </p>
            {(data.model === "unavailable" || data.model === "failed") && (
              <p className="flex items-center gap-1.5 text-label text-ink-3">
                <Icon name="info" size={12} />
                {t("todos-sort.no_model")}
              </p>
            )}
            {suggested.length > 0 && (
              <section>
                <h3 className="mb-1.5 text-label font-semibold text-ink-2">{t("todos-sort.suggestions")}</h3>
                <div className="-mx-1 flex flex-col">
                  {suggested.map((it) => (
                    <Row key={it.todo} it={it} pick={picks.get(it.todo)!} targets={targets} onChange={(p) => set(it, p)} />
                  ))}
                </div>
              </section>
            )}
            {unplaced.length > 0 && (
              <section>
                <button type="button" onClick={() => setRest(!rest)} className="flex items-center gap-1 text-label font-semibold text-ink-2" aria-expanded={rest}>
                  <Icon name={rest ? "chevron" : "chevronRight"} size={12} />
                  {t("todos-sort.unplaced", { count: unplaced.length })}
                </button>
                {rest && (
                  <div className="-mx-1 mt-1 flex flex-col">
                    {unplaced.map((it) => (
                      <Row key={it.todo} it={it} pick={picks.get(it.todo)!} targets={targets} onChange={(p) => set(it, p)} />
                    ))}
                  </div>
                )}
              </section>
            )}
          </>
        )}
      </div>
    </Dialog>
  );
}

/** One to-do: its tick and title, where it goes (changeable), how sure and why. */
function Row({ it, pick, targets, onChange }: { it: SortSuggestion; pick: SortPick; targets: { slug: string; name: string }[]; onChange: (p: Partial<SortPick>) => void }) {
  const suggestedTarget = it.temporary ? TEMPORARY : it.project;
  const sure = suggestedTarget && pick.target === suggestedTarget ? sureness(it.confidence) : null;
  const percent = Math.round(it.confidence * 100);
  return (
    <div className="flex gap-2 rounded-control px-1 py-1.5 hover:bg-hover" data-testid="todos-sort-row" data-todo={it.todo}>
      <input
        type="checkbox"
        checked={pick.on && !!pick.target}
        disabled={!pick.target}
        aria-label={it.title}
        onChange={(e) => onChange({ on: e.target.checked })}
        className="mt-0.5 size-4 shrink-0 accent-[var(--accent)]"
      />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 flex-1 truncate text-sm text-ink">{it.title}</span>
          <span className="flex shrink-0 items-center gap-1">
            <Icon name="cornerDownRight" size={11} className="text-ink-3" />
            <select
              aria-label={t("todos-sort.target")}
              value={pick.target}
              className={FIELD}
              onChange={(e) => onChange({ target: e.target.value, on: e.target.value !== "" })}
            >
              <option value="">{t("todos-sort.no_target")}</option>
              <option value={TEMPORARY}>{t("todos-sort.temporary")}</option>
              {targets.map((x) => (
                <option key={x.slug} value={x.slug}>
                  {x.name}
                </option>
              ))}
            </select>
            {sure && (
              <span
                className={cx("inline-flex h-5 items-center rounded-full px-1.5 text-label font-medium whitespace-nowrap tabular-nums", TONE[sure])}
                title={t("todos-sort.confidence", { percent })}
              >
                {t(`todos-sort.sure_${sure}`)} {percent}%
              </span>
            )}
          </span>
        </div>
        {sure && (it.reason || it.detail) && <p className="mt-0.5 truncate text-submeta text-ink-3">{[it.reason, it.detail].filter(Boolean).join(" · ")}</p>}
      </div>
    </div>
  );
}
