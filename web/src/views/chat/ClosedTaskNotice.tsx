import { t } from "../../i18n";
import { navigate } from "../../lib/router";
import type { Session } from "../../lib/types";

export default function ClosedTaskNotice({ todo, writable }: { todo: NonNullable<Session["completed_todo"]>; writable: boolean }) {
  const href = `/todos?id=${encodeURIComponent(todo.id)}`;
  return (
    <div role="status" data-completed-todo className="shrink-0 border-b border-line bg-inset px-3 py-2 text-xs text-ink-2">
      {t(todo.cancelled ? "chat-closed-task.cancelled" : "chat-closed-task.completed")}{" "}
      <a
        href={href}
        onClick={(event) => { event.preventDefault(); navigate(href); }}
        className="font-medium text-accent-ink underline underline-offset-2"
      >{todo.title}</a>
      {writable && <>{" "}{t("chat-closed-task.continue")}</>}
    </div>
  );
}
