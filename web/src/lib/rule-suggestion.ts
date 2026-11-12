// A rule after the same answer three times (#4929, #5802, docs/assistant/unified-entry.md "A rule after the same answer
// three times"; docs/protocol/protocol.md `RuleSuggestion`): once the owner gave the same answer to a kind of card for
// the same scope three times, the hub attaches a suggestion to the next plan card, decision or passed-up question.
// Always makes the rule, Not now hides the line for that kind and scope for 30 days. The hub counts and words the line;
// this page says what Always stores and, afterwards, where the rule is listed and removed. The pure parts and the two
// requests; the view is views/assistant/RuleSuggestionLine.tsx.
import { t } from "../i18n";
import { api, ApiError, enc } from "./api";

/** What a suggested rule would cover; empty for `delegate`, whose setting is not per directory. */
export interface RuleScope {
  machine_id?: string;
  /** The computer's name, for display. */
  machine?: string;
  directory?: string;
  /** A standing authorization's action and the conditions it would be granted under. */
  action?: string;
  conditions?: string[];
  /** What the standing authorization would keep as the owner's words: their latest answer. */
  quote?: string;
}

export type RuleKind = "plan_start" | "standing_authorization" | "delegate";

/** The line a card carries. `text` is the hub's sentence, in the request's language. */
export interface RuleSuggestion {
  id: string;
  kind: RuleKind | (string & {});
  text: string;
  scope?: RuleScope;
  /** What Always stores: `tasks.trust`, `standing_authorization`, or "" when it stores nothing (`setting` names what to turn on). */
  creates?: string;
  setting?: string;
  count?: number;
}

/** A card that may carry one: an open plan (which names its conversation), a passed-up question (its manager), or a
 *  to-do's decision (which gives the `headers` its own answers go with). `suggestion` is as the hub sent it, checked
 *  by ruleSuggestion where it is drawn. */
export interface RuleCard {
  suggestion?: RuleSuggestion;
  conversation?: string;
  manager?: string;
  headers?: Record<string, string>;
}

const KINDS: readonly string[] = ["plan_start", "standing_authorization", "delegate"];

/** The suggestion when it is one this page can say everything about: an id to answer, the hub's sentence and a kind
 *  whose rule it knows. Anything else (no field, a kind from a newer hub) draws nothing, and the card is as before. */
export function ruleSuggestion(value: unknown): (RuleSuggestion & { kind: RuleKind }) | undefined {
  if (!value || typeof value !== "object") return undefined;
  const s = value as Partial<RuleSuggestion>;
  if (typeof s.id !== "string" || !s.id || typeof s.text !== "string" || !s.text.trim() || typeof s.kind !== "string" || !KINDS.includes(s.kind)) return undefined;
  return s as RuleSuggestion & { kind: RuleKind };
}

function actionLabel(action: string | undefined): string {
  return action === "merge" || action === "push" ? t(`settings-standing.action_${action}`) : action === "deploy-test" ? t("settings-standing.action_deploy_test") : action || "";
}

function conditionLabel(condition: string): string {
  return condition === "ci_green" || condition === "no_conflicts" ? t(`settings-standing.condition_${condition}`) : condition;
}

/** The computer as the line names it: its name, else its id. */
function scopeMachine(scope: RuleScope | undefined): string {
  return scope?.machine || scope?.machine_id || "";
}

/** Exactly what Always stores, in sentences: the remembered kind of task, the standing authorization with its
 *  conditions and the words kept as the owner's, or nothing but a pointer to the setting. */
export function ruleCreates(s: RuleSuggestion & { kind: RuleKind }): string[] {
  const scope = s.scope ?? {};
  const where = { directory: scope.directory ?? "", machine: scopeMachine(scope) };
  switch (s.kind) {
    case "plan_start":
      return [t("assistant-rulesuggestion.creates_plan_start", where)];
    case "standing_authorization": {
      const conditions = (scope.conditions ?? []).filter((c) => typeof c === "string" && c).map(conditionLabel);
      return [
        t("assistant-rulesuggestion.creates_standing", { ...where, action: actionLabel(scope.action) }),
        conditions.length ? t("assistant-rulesuggestion.creates_conditions", { conditions: conditions.join(t("assistant-rulesuggestion.list_separator")) }) : "",
        scope.quote ? t("assistant-rulesuggestion.creates_quote", { quote: scope.quote }) : "",
      ].filter(Boolean);
    }
    case "delegate":
      return [t("assistant-rulesuggestion.creates_delegate", { setting: s.setting || "agents.delegate" })];
  }
}

/** Sentences one after another, as this language sets them (a space between in English, nothing in Chinese and Japanese). */
export function ruleJoin(sentences: string[]): string {
  return sentences.reduce((first, second) => t("assistant-rulesuggestion.joined", { first, second }));
}

/** What the hub answers Always with: the kind, what it stored and, by kind, the entry it made. */
export interface RuleMade {
  ok?: boolean;
  kind?: string;
  creates?: string;
  /** `plan_start`: the `tasks.trust` entry's key. */
  key?: string;
  /** `standing_authorization`: the grant as stored. */
  authorization?: { machine_id?: string; directory?: string; actions?: string[]; conditions?: string[]; quote?: string };
  /** `delegate`: the setting to turn on; nothing was stored. */
  setting?: string;
}

/** What is said once Always went through: what was made, and where it is listed and removed. `page` is where this
 *  app lists it; a `delegate` rule is no setting of this app (the assistant's manager.json), so it has none. */
export function ruleResult(s: RuleSuggestion & { kind: RuleKind }, made: RuleMade): { text: string; where: string; page?: string } {
  const kind = KINDS.includes(made.kind ?? "") ? (made.kind as RuleKind) : s.kind;
  // both lists are on the assistant's own settings page, each under its own heading
  const where = (list: string) => t("assistant-rulesuggestion.where", { tab: t("settings-nav.tab_profile"), list });
  switch (kind) {
    case "plan_start":
      return { text: t("assistant-rulesuggestion.made_plan_start"), where: where(t("settings-assistantsettings.trust_title")), page: "/settings?tab=persona" };
    case "standing_authorization":
      return { text: t("assistant-rulesuggestion.made_standing"), where: where(t("settings-standing.title")), page: "/settings?tab=persona" };
    case "delegate":
      return { text: t("assistant-rulesuggestion.made_delegate"), where: t("assistant-rulesuggestion.where_delegate", { setting: made.setting || s.setting || "agents.delegate" }) };
  }
}

/** How answering went: the hub took it (`made` for Always), or the suggestion is gone (404: answered on another device,
 *  or no longer earned), which only clears the line. Any other failure throws, and the line stays to try again. */
export type RuleAnswer = { state: "made"; made: RuleMade } | { state: "hidden" } | { state: "gone" };

export async function answerRuleSuggestion(id: string, answer: "always" | "not-now", headers?: Record<string, string>): Promise<RuleAnswer> {
  try {
    const made = await api<RuleMade>(`/assistant/rule-suggestions/${enc(id)}/${answer}`, { method: "POST", headers });
    return answer === "always" ? { state: "made", made: made ?? {} } : { state: "hidden" };
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return { state: "gone" };
    throw e;
  }
}

// The suggestions answered on this page: the hub takes an answered one off every card and sends the cards again, but a
// plan and a to-do's decision are only worded when read, so the line goes at once here whatever arrives.
const answered = new Set<string>();
const listeners = new Set<() => void>();

export function ruleAnswered(id: string): boolean {
  return answered.has(id);
}

export function markRuleAnswered(id: string) {
  if (answered.has(id)) return;
  answered.add(id);
  listeners.forEach((l) => l());
}

export function subscribeRuleAnswered(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** For tests. */
export function resetRuleAnswered() {
  answered.clear();
}
