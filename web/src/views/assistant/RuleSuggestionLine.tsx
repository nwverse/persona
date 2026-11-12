// The line under a card that carries a suggested rule (#4929, #5802, docs/assistant/unified-entry.md "A rule after
// the same answer three times"): what the hub noticed and for which computer and directory (its sentence), exactly what
// Always stores (lib/rule-suggestion ruleCreates), and the two answers. After Always it says what was made and where
// it is listed and removed. A suggestion that is gone on the hub (404) only clears the line. Loaded with the first card
// that carries one, not with the first screen (views/assistant/NeedsYouPart ruleLine). Under a decision's row it is
// always mounted (TaskDecision), so what Always made stays said there once the hub took the suggestion off; under a
// plan card or a question it goes with the suggestion, and what Always made is said in a toast.
import { useState, useSyncExternalStore } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { conversationHeaders } from "../../lib/feed";
import { itemHeaders } from "../../lib/manager-choice";
import { navigate } from "../../lib/router";
import { answerRuleSuggestion, markRuleAnswered, ruleAnswered, ruleCreates, ruleJoin, ruleResult, ruleSuggestion, subscribeRuleAnswered, type RuleCard } from "../../lib/rule-suggestion";
import { Button, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

type Result = ReturnType<typeof ruleResult>;

/** `of`: the card the line is under. */
export default function RuleSuggestionLine({ of }: { of: RuleCard }) {
  const suggestion = ruleSuggestion(of.suggestion);
  const [busy, setBusy] = useState<"always" | "not-now" | null>(null);
  const [result, setResult] = useState<(Result & { id: string }) | null>(null);
  const gone = useSyncExternalStore(subscribeRuleAnswered, () => !!suggestion && ruleAnswered(suggestion.id));
  // what Always made, until another suggestion comes to this card
  if (result && (!suggestion || gone || suggestion.id === result.id)) {
    const page = result.page;
    return (
      <div data-rule-result role="status" className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 px-1 text-xs text-ink-2">
        <p className="flex min-w-0 flex-1 basis-48 items-start gap-1.5 [overflow-wrap:anywhere]">
          <Icon name="check" size={13} stroke={2.2} className="mt-0.5 shrink-0 text-green" />
          <span>
            {ruleJoin([result.text, result.where])}
          </span>
        </p>
        {page && (
          <Button size="sm" tone="quiet" onClick={() => navigate(page)}>
            {t("assistant-rulesuggestion.open_settings")}
          </Button>
        )}
      </div>
    );
  }
  if (!suggestion || gone) return null;
  const answer = (which: "always" | "not-now") => {
    if (busy) return;
    setBusy(which);
    const sent = of.headers ?? (of.conversation ? conversationHeaders(of.conversation) : itemHeaders(of.manager));
    answerRuleSuggestion(suggestion.id, which, sent).then(
      (res) => {
        setBusy(null);
        if (res.state === "made") {
          const made = ruleResult(suggestion, res.made);
          const page = made.page;
          // a decision's line stays under its row and says it there; a plan's or a question's goes with the suggestion
          // (the hub sends the card again without it), so there it is said in a toast, with the same way to the list
          if (of.headers) setResult({ ...made, id: suggestion.id });
          else toast(ruleJoin([made.text, made.where]), "ok", page ? { label: t("assistant-rulesuggestion.open_settings"), run: () => navigate(page) } : undefined);
        } else if (res.state === "hidden") toast(t("assistant-rulesuggestion.hidden"), "ok");
        markRuleAnswered(suggestion.id);
      },
      (e) => {
        setBusy(null);
        toast(t("assistant-rulesuggestion.failed", { error: errorText(e) }), "error");
      },
    );
  };
  return (
    <div data-rule-suggestion={suggestion.kind} role="group" aria-label={t("assistant-rulesuggestion.label")} className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 px-1 text-xs text-ink-2">
      <p className="min-w-0 flex-1 basis-48 [overflow-wrap:anywhere]">
        <span data-rule-text>{suggestion.text}</span>
        {ruleJoin(["", ""]) /* what this language puts between two sentences */}
        <span data-rule-creates className="text-ink-3">
          {ruleJoin(ruleCreates(suggestion))}
        </span>
      </p>
      <span className="flex shrink-0 items-center gap-2">
        <Button size="sm" busy={busy === "always"} disabled={!!busy} title={t("assistant-rulesuggestion.always_title")} onClick={() => answer("always")}>
          {t("assistant-rulesuggestion.always")}
        </Button>
        <Button size="sm" tone="quiet" busy={busy === "not-now"} disabled={!!busy} title={t("assistant-rulesuggestion.not_now_title")} onClick={() => answer("not-now")}>
          {t("assistant-rulesuggestion.not_now")}
        </Button>
      </span>
    </div>
  );
}
