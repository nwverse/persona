import { useId, useRef, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { withQuoteBlock } from "../../lib/drafts";
import { askPersonaOf } from "../../lib/outbox";
import { askOptions, taskReviewDecisionAsk, type Todo, type TodoAsk } from "../../lib/todos";
import { Button, Dialog } from "../../ui/atoms";

export function AskAboutDecision({ todo, ask, disabled, onOpenChat }: { todo: Todo; ask: TodoAsk; disabled: boolean; onOpenChat?: () => void }) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const sending = useRef(false);
  const hint = useId();
  const close = () => {
    if (sending.current) return;
    setOpen(false);
    setText("");
    setError("");
  };
  const send = async () => {
    const question = text.trim();
    if (!question || disabled || sending.current) return;
    sending.current = true;
    setBusy(true);
    setError("");
    const options = askOptions(ask);
    const quote = {
      task: { id: todo.id, title: todo.title.replace(/[\r\n]+/g, " ") },
      text: [
        ask.question || ask.verdict,
        "",
        ...options.map(({ label, leads_to }) => leads_to ? `- ${label}: ${leads_to}` : `- ${label}`),
        "",
        t("assistant-brief.recommend", { option: ask.recommend || options[0]?.label }),
        "",
        t("assistant-decisionrow.ask_discussion_context"),
      ].join("\n"),
    };
    try {
      // Use the ordinary outbox so a failed or uncertain send can be recovered in the destination chat.
      await askPersonaOf(todo.manager, withQuoteBlock(quote, question));
      sending.current = false;
      close();
      onOpenChat?.();
    } catch (e) {
      setError(errorText(e)); // The destination could not open. Keep the words in the dialog.
    } finally {
      sending.current = false;
      setBusy(false);
    }
  };

  if (!taskReviewDecisionAsk(todo)) return null;
  return <>
    <Button tone="quiet" className="min-h-11" disabled={disabled} onClick={() => setOpen(true)}>
      {t("assistant-decisionrow.ask_in_main_chat")}
    </Button>
    <Dialog open={open} onClose={close} title={t("assistant-decisionrow.ask_in_main_chat")} describedBy={hint} footer={<>
      <Button tone="quiet" disabled={busy} onClick={close}>{t("common.cancel")}</Button>
      <Button tone="accent" icon="send" busy={busy} disabled={disabled || !text.trim()} onClick={() => void send()}>
        {t("chat-composer.send")}
      </Button>
    </>}>
      <p className="mb-1 break-words text-label font-medium text-ink">{todo.title}</p>
      <p id={hint} className="mb-2 text-label text-ink-2">{t("assistant-decisionrow.ask_in_main_hint")}</p>
      <textarea
        aria-label={t("assistant-decisionrow.ask_in_main_input")}
        placeholder={t("assistant-decisionrow.ask_in_main_input")}
        rows={3}
        value={text}
        disabled={busy}
        onChange={(e) => setText(e.target.value)}
        className="block w-full resize-y rounded-control bg-field px-3 py-2 text-label text-ink shadow-inset-field outline-none placeholder:text-ink-3 focus:shadow-[inset_0_0_0_1px_var(--accent)]"
      />
      {error && <p role="alert" className="mt-2 text-label text-red">{error}</p>}
    </Dialog>
  </>;
}
