// @vitest-environment jsdom
// The suggested rule's line under a card (#5802): drawn under an open plan card, a decision and a passed-up question
// only when the hub sent the field; it says what was noticed and what Always stores, Always and Not now each make one
// request, Always then says what was made and where it is listed, and a suggestion gone on the hub clears the line.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const toast = vi.hoisted(() => vi.fn());
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast }));
import { initI18n } from "../../i18n";
import type { TaskPlan } from "../../lib/feed";
import { MANAGER_HEADER } from "../../lib/manager-choice";
import { resetRuleAnswered, type RuleSuggestion } from "../../lib/rule-suggestion";
import { reset, upsertSession } from "../../lib/store";
import type { Todo, TodoAsk } from "../../lib/todos";
import type { Interaction, Session } from "../../lib/types";
import { InteractionCard } from "../chat/Interactions";
import RuleSuggestionLine from "./RuleSuggestionLine";
import { TaskDecision } from "./TaskDecision";
import { PlanCard } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const at = "2026-11-10T08:00:00Z";
const planStart: RuleSuggestion = {
  id: "rs_plan", kind: "plan_start", count: 3, creates: "tasks.trust",
  text: "You started tasks like this 3 times. Always start tasks that change files in /code/example on Example node directly?",
  scope: { machine_id: "example-node", machine: "Example node", directory: "/code/example" },
};
const merge: RuleSuggestion = {
  id: "rs_merge", kind: "standing_authorization", count: 3, creates: "standing_authorization",
  text: "You approved this 3 times. Always allow merging in /code/example on Example node once checks pass and there are no conflicts?",
  scope: { machine_id: "example-node", machine: "Example node", directory: "/code/example", action: "merge", conditions: ["ci_green", "no_conflicts"], quote: "Merge the PR now? → Yes" },
};
const delegate: RuleSuggestion = {
  id: "rs_delegate", kind: "delegate", count: 3, creates: "", setting: "agents.delegate", scope: {},
  text: "You took the recommendation 3 times. Let Persona answer agents' own-call questions for you? You can turn this on in Settings.",
};

let host: HTMLDivElement;
let root: Root;
/** What the hub answers the next rule-suggestion request with. */
let answer: { status: number; body: unknown };
let posts: { path: string; manager: string | null; body: BodyInit | null | undefined }[];
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const render = async (el: React.ReactNode) => {
  await act(async () => root.render(el));
  await flush();
};
const line = () => host.querySelector<HTMLElement>("[data-rule-suggestion]");
const result = () => host.querySelector<HTMLElement>("[data-rule-result]");
const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>("[data-rule-suggestion] button")].find((b) => b.textContent?.trim() === label)!;
const press = async (label: string) => {
  await act(async () => button(label).click());
  await flush();
};

beforeAll(async () => void (await import("./TaskDecision")));
beforeEach(async () => {
  await initI18n("en");
  resetRuleAnswered();
  toast.mockClear();
  posts = [];
  answer = { status: 200, body: { ok: true } };
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const path = String(url);
    if (path.includes("/rule-suggestions/")) {
      posts.push({ path, manager: new Headers(init?.headers).get(MANAGER_HEADER), body: init?.body });
      return new Response(JSON.stringify(answer.body), { status: answer.status, headers: { "Content-Type": "application/json" } });
    }
    return new Response("{}", { headers: { "Content-Type": "application/json" } });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  reset();
  history.replaceState(null, "", "/");
  vi.unstubAllGlobals();
  await initI18n("zh-Hans");
});

describe("the line under a card", () => {
  // `kept`: under a decision's row, where the line is always mounted (it names its to-do's manager in `headers`);
  // else under a plan card or a question, where it goes with the suggestion
  const card = (suggestion?: RuleSuggestion | unknown, kept = true) => (
    <>
      <div data-the-card>card</div>
      <RuleSuggestionLine of={{ suggestion: suggestion as RuleSuggestion, ...(kept ? { headers: { [MANAGER_HEADER]: "second" } } : { manager: "second" }) }} />
    </>
  );

  it("is not drawn without the field", async () => {
    await render(card());
    expect(line()).toBeNull();
    expect([...host.children].map((c) => c.hasAttribute("data-the-card"))).toEqual([true]);
  });

  it("is not drawn for a kind from a newer hub, nor for a field that is no suggestion", async () => {
    await render(card({ ...merge, kind: "something_newer" }));
    expect(line()).toBeNull();
    await render(card({ id: "rs_x" }));
    expect(line()).toBeNull();
  });

  it("says what was noticed, the scope and exactly what Always stores, with two single-line buttons", async () => {
    await render(card(merge));
    expect(line()!.dataset.ruleSuggestion).toBe("standing_authorization");
    expect(line()!.querySelector("[data-rule-text]")!.textContent).toBe(merge.text);
    expect(line()!.querySelector("[data-rule-creates]")!.textContent).toBe(
      "Always adds a standing authorization for /code/example on Example node: Merge changes. Only when: All checks pass, No merge conflicts. Kept as your words: “Merge the PR now? → Yes”.",
    );
    expect([...line()!.querySelectorAll("button")].map((b) => b.textContent)).toEqual(["Always", "Not now"]);
    for (const b of line()!.querySelectorAll("button")) expect(b.className).toContain("whitespace-nowrap");
    // right under the card
    expect(line()!.previousElementSibling!.hasAttribute("data-the-card")).toBe(true);
  });

  it("Always makes one request to the card's manager and then says what was made and where it is listed", async () => {
    answer = { status: 200, body: { ok: true, kind: "standing_authorization", creates: "standing_authorization", authorization: { directory: "/code/example" }, revision: 4 } };
    await render(card(merge));
    await press("Always");
    expect(posts).toEqual([{ path: "/api/v1/assistant/rule-suggestions/rs_merge/always", manager: "second", body: undefined }]);
    expect(line()).toBeNull();
    expect(result()!.textContent).toContain("Standing authorization added.");
    expect(result()!.textContent).toContain("Listed and removed in Settings › Assistant profile › Standing authorizations.");
    // the hub takes the suggestion off the card: what was made stays said
    await render(card());
    expect(result()!.textContent).toContain("Standing authorization added.");
    await act(async () => [...result()!.querySelectorAll("button")].find((b) => b.textContent === "Open settings")!.click());
    expect(location.pathname + location.search).toBe("/settings?tab=persona");
  });

  it("under a plan card or a question the line goes with the suggestion, so what Always made is said in a toast that opens the list", async () => {
    answer = { status: 200, body: { ok: true, kind: "plan_start", creates: "tasks.trust", key: "k" } };
    await render(card(planStart, false));
    await press("Always");
    expect(posts).toEqual([{ path: "/api/v1/assistant/rule-suggestions/rs_plan/always", manager: "second", body: undefined }]);
    expect(line()).toBeNull();
    expect(result()).toBeNull();
    const [text, tone, action] = toast.mock.calls[0];
    expect(text).toBe("Remembered: tasks like this now start without asking. Listed and removed in Settings › Assistant profile › Remembered auto-start rules.");
    expect(tone).toBe("ok");
    expect(action.label).toBe("Open settings");
    act(() => action.run());
    expect(location.pathname + location.search).toBe("/settings?tab=persona");
    // nothing stored for delegate: no list to open
    act(() => root.unmount());
    root = createRoot(host);
    toast.mockClear();
    answer = { status: 200, body: { ok: true, kind: "delegate", creates: "", setting: "agents.delegate" } };
    await render(card(delegate, false));
    await press("Always");
    expect(toast.mock.calls[0][0]).toContain("No rule was stored.");
    expect(toast.mock.calls[0][2]).toBeUndefined();
  });

  it("a remembered kind of task names its own list; a delegate suggestion stores nothing and has no page to open", async () => {
    answer = { status: 200, body: { ok: true, kind: "plan_start", creates: "tasks.trust", key: "k" } };
    await render(card(planStart));
    expect(line()!.querySelector("[data-rule-creates]")!.textContent).toContain("Always remembers this kind of task");
    await press("Always");
    expect(result()!.textContent).toContain("Listed and removed in Settings › Assistant profile › Remembered auto-start rules.");
    act(() => root.unmount());
    root = createRoot(host);
    answer = { status: 200, body: { ok: true, kind: "delegate", creates: "", setting: "agents.delegate" } };
    await render(card(delegate));
    expect(line()!.querySelector("[data-rule-creates]")!.textContent).toContain("Always stores no rule here");
    await press("Always");
    expect(result()!.textContent).toContain("No rule was stored.");
    expect(result()!.textContent).toContain("agents.delegate");
    expect(result()!.querySelector("button")).toBeNull();
  });

  it("Not now makes one request, says for how long, and the line goes", async () => {
    await render(card(merge));
    await press("Not now");
    expect(posts.map((p) => p.path)).toEqual(["/api/v1/assistant/rule-suggestions/rs_merge/not-now"]);
    expect(line()).toBeNull();
    expect(result()).toBeNull();
    expect(toast).toHaveBeenCalledWith("Not suggested again for 30 days.", "ok");
  });

  it("a 404 on a stale suggestion clears the line and says nothing was made", async () => {
    answer = { status: 404, body: { error: "This suggestion is no longer available." } };
    await render(card(merge));
    await press("Always");
    expect(line()).toBeNull();
    expect(result()).toBeNull();
    expect(toast).not.toHaveBeenCalled();
  });

  it("any other refusal says why and leaves the line to try again", async () => {
    answer = { status: 403, body: { error: "Full access is required to answer a suggested rule." } };
    await render(card(merge));
    await press("Always");
    expect(line()).not.toBeNull();
    expect(result()).toBeNull();
    expect(toast.mock.calls[0][0]).toContain("Full access is required");
    expect(toast.mock.calls[0][1]).toBe("error");
    expect(button("Always").disabled).toBe(false);
  });

  it("answered on one card, the same suggestion goes from every card that carries it", async () => {
    await render(<>{card(merge)}<RuleSuggestionLine of={{ suggestion: merge }} /></>);
    expect(host.querySelectorAll("[data-rule-suggestion]")).toHaveLength(2);
    await press("Not now");
    expect(host.querySelectorAll("[data-rule-suggestion]")).toHaveLength(0);
  });

  it("is in Simplified Chinese and Japanese", async () => {
    await initI18n("zh-Hans");
    await render(card(merge));
    expect([...line()!.querySelectorAll("button")].map((b) => b.textContent)).toEqual(["总是", "暂不"]);
    act(() => root.unmount());
    root = createRoot(host);
    await initI18n("ja");
    await render(card(merge));
    expect([...line()!.querySelectorAll("button")].map((b) => b.textContent)).toEqual(["常に", "今はしない"]);
  });
});

describe("on each kind of card", () => {
  const plan = (suggestion?: RuleSuggestion): TaskPlan => ({ id: "p1", conversation: "c1", state: "open", created_at: at, suggestion, tasks: [{ title: "Fix the example", prompt: "Fix", machine: "example-node", cwd: "/code/example" }] });

  it("an open plan card carries it under the card; one without the field is the card alone", async () => {
    await render(<PlanCard plan={plan(planStart)} />);
    expect(line()!.dataset.ruleSuggestion).toBe("plan_start");
    expect(line()!.previousElementSibling!.querySelector("[data-plan-actions]")).not.toBeNull();
    await press("Always");
    expect(posts.map((p) => p.path)).toEqual(["/api/v1/assistant/rule-suggestions/rs_plan/always"]);
    expect(line()).toBeNull();
    act(() => root.unmount());
    root = createRoot(host);
    resetRuleAnswered();
    await render(<PlanCard plan={plan()} />);
    expect(line()).toBeNull();
    // the card alone, as before
    expect(host.children).toHaveLength(1);
    expect(host.firstElementChild!.querySelector("[data-plan-actions]")).not.toBeNull();
  });

  it("a question a supervisor passed up carries it under the card", async () => {
    upsertSession({ machine_id: "m", session_id: "s", title: "Demo", state: "needs_you" } as Session);
    const ix = { id: "ix_up", machine_id: "m", session_id: "s", kind: "question", manager: "second", questions: [{ question: "Rerun the example checks?", options: [{ label: "Rerun" }, { label: "Skip" }] }], suggestion: delegate } as Interaction;
    await render(<InteractionCard ix={ix} />);
    expect(line()!.dataset.ruleSuggestion).toBe("delegate");
    expect(host.textContent).toContain("Rerun the example checks?");
    await press("Not now");
    expect(posts).toEqual([{ path: "/api/v1/assistant/rule-suggestions/rs_delegate/not-now", manager: "second", body: undefined }]);
    act(() => root.unmount());
    root = createRoot(host);
    resetRuleAnswered();
    await render(<InteractionCard ix={{ ...ix, suggestion: undefined }} />);
    expect(line()).toBeNull();
  });

  it("a decision carries it under its row, answered through the to-do's manager", async () => {
    const ask: TodoAsk = {
      outcome: "decide", verdict: "Needs your decision: merge the example change", recommend: "Merge", asked_at: at, question: "Merge the example change now?",
      why_owner: "priority", options: [{ label: "Merge", leads_to: "it ships today" }, { label: "Wait", leads_to: "it ships tomorrow" }], suggestion: merge,
    };
    const todo: Todo = { id: "t1", title: "Example change", manager: "second", created_at: at, updated_at: at, supervision: { supervisor: "c1", state: "needs_user", reviews: [{ at, verdict: "needs_user" }] }, decision_review: { review_at: at }, ask } as Todo;
    await render(<TaskDecision todo={todo} rowKey="task:t1" />);
    expect(line()!.dataset.ruleSuggestion).toBe("standing_authorization");
    expect(line()!.previousElementSibling!.getAttribute("data-needs-row")).toBe("task:t1");
    answer = { status: 200, body: { ok: true, kind: "standing_authorization", creates: "standing_authorization" } };
    await press("Always");
    expect(posts).toEqual([{ path: "/api/v1/assistant/rule-suggestions/rs_merge/always", manager: "second", body: undefined }]);
    expect(result()!.textContent).toContain("Standing authorization added.");
    act(() => root.unmount());
    root = createRoot(host);
    resetRuleAnswered();
    await render(<TaskDecision todo={{ ...todo, ask: { ...ask, suggestion: undefined } }} rowKey="task:t1" />);
    expect(line()).toBeNull();
    expect(host.querySelector('[data-needs-row="task:t1"]')).not.toBeNull();
  });
});
