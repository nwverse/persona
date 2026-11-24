// Historical details load only for a failed continuation; its title and original failure stay immediate.
import { t } from "../../i18n";
import { openTask } from "../../lib/handoff";

export default function ContinuationHistory({ error, deliveryError, todo }: { error: string; deliveryError?: string; todo?: string }) {
  return <>
    <details data-handoff-history className="text-label text-ink-2">
      <summary className="cursor-pointer pointer-coarse:min-h-11">{t("assistant-tasks.continuation_dispatch_history")}</summary>
      <p className="whitespace-pre-wrap break-words select-text">{error.trim() || t("assistant-tasks.unknown_reason")}</p>
    </details>
    {deliveryError && <p data-handoff-delivery-error className="text-label text-ink-2 whitespace-pre-wrap break-words">{deliveryError}</p>}
    {todo && <button type="button" data-handoff-current-task className="self-start text-label text-ink-2 hover:text-ink hover:underline pointer-coarse:min-h-11" onClick={() => openTask(todo)}>{t("assistant-tasks.continuation_view_task")}</button>}
  </>;
}
