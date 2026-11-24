// The line over the To-dos list while some open to-dos are in no project (#13026): how many, and the way to the sort
// sheet (SortTodos.tsx, loaded only when it opens).
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

/** The line over the list while some open to-dos are in no project: how many, and the way to sort them. */
export function SortUnsortedLine({ count, onOpen }: { count: number; onOpen: () => void }) {
  return (
    <div data-testid="todos-unsorted" className="mx-1 mb-1 flex items-center gap-2 rounded-control bg-field px-2.5 py-2 text-label text-ink-2">
      <Icon name="layers" size={12} className="shrink-0 text-ink-3" />
      <span className="min-w-0 flex-1">{t("todos-sort.unsorted_line", { count })}</span>
      <button type="button" onClick={onOpen} className="shrink-0 font-medium whitespace-nowrap text-accent-ink hover:underline" data-testid="todos-sort-open">
        {t("todos-sort.open", { count })}
      </button>
    </div>
  );
}
