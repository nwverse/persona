// @vitest-environment jsdom
// What a dispatch draws in the conversation (#3700, #5096: cindy's hand-off card): a card for each Agent it handed work
// to, which opens that Agent's own page, never a side panel and never the whole In progress tray.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  globalThis.IntersectionObserver ??= class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } } as unknown as typeof IntersectionObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

import { t } from "../../i18n";
import { getFeedState, type FeedItem, type PlanTask, type TaskPlan } from "../../lib/feed";
import { handoffFrom } from "../../lib/handoff";
import { setAgentOpenMode } from "../../lib/agent-open";
import { closePanel, getPanel } from "../../lib/panel";
import { applyCompleteSessions, refreshTodos, reset } from "../../lib/store";
import type { Todo, TodoAgent } from "../../lib/todos";
import type { Session } from "../../lib/types";
import { handoffAgents, lastActive, shortSessionId, TASK_LINE_SLACK_MS } from "./Handoff";
import { AssistantActionBar, FeedLineView, PlanCard } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const at = "2026-11-10T08:00:00Z";
const conversation = "dispatch-demo";
const agent: Session = { machine_id: "example-computer", session_id: "0f3c2a91-5d7e-4b8a-9c11-2e4f6a8b7ed0", title: "Example Agent", cwd: "/example", state: "working", status: "working", updated_at: at } as Session;
const linked = (id: string, title: string, sessions: Session[], extra: Partial<Todo> = {}): Todo => ({
  id, title, origin: conversation, created_at: at, updated_at: at,
  agents: sessions.map((s) => ({ machine_id: s.machine_id, session_id: s.session_id, title: s.title, linked_at: at })), ...extra,
});
let served: Todo[];
let host: HTMLDivElement;
let root: Root;
const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const opened = () => location.pathname + location.search;
const tray = () => host.querySelector<HTMLButtonElement>('[data-tray-entry="running"]')!;
const cards = () => [...host.querySelectorAll<HTMLElement>("[data-handoff-card]")];

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => new Response(JSON.stringify(String(url).includes("/todos") ? { todos: served } : String(url).includes("/items") ? { items: [], to_seq: 0 } : {}), { headers: { "Content-Type": "application/json" } })));
  history.replaceState(null, "", "/persona");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setAgentOpenMode("panel");
  closePanel();
  reset();
  history.replaceState(null, "", "/");
  for (const id of Object.keys(getFeedState().plans)) delete getFeedState().plans[id];
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function show(item: FeedItem, todos: Todo[], sessions: Session[]) {
  served = todos;
  await act(async () => {
    applyCompleteSessions(sessions);
    await refreshTodos();
  });
  await act(async () => root.render(<StrictMode><AssistantActionBar conversation={conversation} /><div data-chat-scroll><FeedLineView item={item} simplified /></div></StrictMode>));
  await settle();
  await act(async () => { await vi.dynamicImportSettled(); });
}

describe("hand-off cards", () => {
  it("one dispatched task: its Agent's card, New, opening the Agent in the side panel, or its page with the way back set to Full page, not the In progress tray", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task" }, [linked("example-task", "Example task", [agent])], [agent]);
    const [card] = cards();
    expect(cards()).toHaveLength(1);
    expect(card!.tagName).toBe("BUTTON");
    expect(card!.textContent).toContain("Example Agent");
    expect(card!.querySelector('[data-handoff-wake="created"]')?.textContent).toBe(t("assistant-tasks.handoff_wake_created"));
    expect(card!.querySelector('[data-handoff-state="working"]')?.textContent).toContain(t("lib-state-labels.working"));
    // the id is only in the tooltip, cindy's short form
    expect(card!.textContent).not.toContain("0f3c2a91");
    expect(card!.getAttribute("title")).toBe("Example Agent · 0f3c2a91…7ed0");
    // the default (#5424): beside the chat, which stays where it is
    act(() => card!.click());
    expect(getPanel()).toEqual({ kind: "agent", mid: "example-computer", sid: agent.session_id });
    expect(opened()).toBe("/persona");
    act(() => closePanel());
    // set to Full page (#5096): the Agent's page, which says where it came from
    setAgentOpenMode("page");
    act(() => card!.click());
    expect(opened()).toBe(`/s/example-computer/${agent.session_id}`);
    expect(handoffFrom()).toEqual({ path: "/persona" });
    expect(getPanel()).toBeNull();
    expect(tray().getAttribute("aria-expanded")).toBe("false");
  });

  it("a task made of an Agent that was running already says Already active", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task", body: { wake: "active" } }, [linked("example-task", "Example task", [agent])], [agent]);
    expect(cards()[0]!.querySelector('[data-handoff-wake="active"]')?.textContent).toBe(t("assistant-tasks.handoff_wake_active"));
  });

  it("a closed Agent the hand-off resumed says Resumed", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task", body: { wake: "resumed" } }, [linked("example-task", "Example task", [agent])], [agent]);
    expect(cards()[0]!.querySelector('[data-handoff-wake="resumed"]')?.textContent).toBe(t("assistant-tasks.handoff_wake_resumed"));
  });

  // Anchored at its line's time (#11854): the first Agents linked from then on. Before #11854 this held because the
  // card kept the to-do's earliest Agents whatever line it was under; now it holds because the line is the earlier one.
  it("draws the Agents the hand-off went to, not one a later message started (that one has its own card)", async () => {
    const next: Session = { ...agent, session_id: "next-agent", title: "Next Agent" };
    const todo = linked("example-task", "Example task", [agent, next]);
    todo.agents![1]!.linked_at = "2026-11-10T09:00:00Z";
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task", body: { wake: "active" } }, [todo], [agent, next]);
    expect(cards().map((c) => c.getAttribute("data-handoff-card"))).toEqual([`example-computer/${agent.session_id}`]);
  });

  it("before its Agent is up the card is the task's: starting, and it opens the to-do", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task" }, [linked("example-task", "Example task", [])], []);
    const [card] = cards();
    expect(card!.textContent).toContain("Example task");
    expect(card!.textContent).toContain(t("assistant-tasks.starting_agent"));
    act(() => card!.click());
    expect(getPanel()).toEqual({ kind: "task", todo: "example-task" });
    act(() => closePanel());
    setAgentOpenMode("page");
    act(() => card!.click());
    expect(opened()).toBe("/todos?id=example-task");
  });

  it("a task that did not start says why; one removed since says so and opens nothing", async () => {
    const plan: TaskPlan = { id: "plan-two", conversation, state: "started", created_at: at, tasks: [
      { title: "Broken task", prompt: "a", cwd: "/example", machine: "example-computer", todo_id: "broken-task" },
      { title: "Removed task", prompt: "b", cwd: "/example", machine: "example-computer", todo_id: "removed-task" },
    ] };
    getFeedState().plans[plan.id] = plan;
    await show({ id: "plan-event", conversation, at, kind: "plan", ref: plan.id }, [linked("broken-task", "Broken task", [], { start_error: "launcher missing", done_at: at })], []);
    const [broken, removed] = cards();
    act(() => broken!.click());
    expect(getPanel()).toEqual({ kind: "task", todo: "broken-task" });
    // a raw error that names no known cause says to open the task, where it is (#8237)
    expect(broken!.textContent).toContain(t("assistant-tasks.start_failed_details"));
    expect(broken!.textContent).not.toContain("launcher missing");
    expect(removed!.tagName).toBe("DIV");
    expect(removed!.textContent).toContain("Removed task");
    expect(removed!.textContent).toContain(t("assistant-tasks.handoff_gone"));
  });

  it("shows a failed continuation without making its previous to-do the destination", async () => {
    const plan: TaskPlan = { id: "plan-failed", conversation, state: "started", created_at: at, tasks: [
      { title: "Carry on the task", prompt: "a", cwd: "/example", machine: "example-computer", continues: "previous-task", error: "continues: cannot stop agent example-computer/session on to-do previous-task: exit status 1" },
    ] };
    getFeedState().plans[plan.id] = plan;
    await show({ id: "plan-event", conversation, at, kind: "plan", ref: plan.id }, [linked("previous-task", "Previous task", [agent])], [agent]);
    const [card] = cards();
    expect(card!.tagName).toBe("DIV");
    expect(card!.querySelector('[data-handoff-wake="continuation"]')).not.toBeNull();
    expect(card!.querySelector("[data-handoff-failure]")?.textContent).toBe(t("assistant-tasks.continuation_failed_attempt"));
    act(() => card!.click());
    expect(getPanel()).toBeNull();
    await act(async () => { await vi.dynamicImportSettled(); });
    const details = host.querySelector<HTMLDetailsElement>("[data-handoff-history]")!;
    expect(details.open).toBe(false);
    expect(details.textContent).toContain(plan.tasks[0]!.error);
    act(() => details.querySelector("summary")!.click());
    expect(details.open).toBe(true);
    expect(getPanel()).toBeNull();
    act(() => host.querySelector<HTMLButtonElement>("[data-handoff-current-task]")!.click());
    expect(getPanel()).toEqual({ kind: "task", todo: "previous-task" });
  });

  describe("a failed continuation sent to its original session", () => {
    const reason = "continues: cannot stop agent example-computer/session on to-do previous-task: this session belongs to another tool";
    const recovery: NonNullable<PlanTask["continuation_recovery"]> = { todo_id: "previous-task", machine_id: agent.machine_id, session_id: agent.session_id, scope_revision: "scope-1", state: "sent", input_id: "continuation-input", sent_at: at };
    const makePlan = (r: PlanTask["continuation_recovery"]): TaskPlan => ({ id: "recovery-plan", conversation, state: "started", created_at: at, tasks: [
      { title: "Carry on the task", prompt: "Finish the missing cases", cwd: "/example", machine: agent.machine_id, continues: "previous-task", reuse_todo: "previous-task", error: reason, continuation_recovery: r },
    ] });
    const original = (extra: Partial<TodoAgent> = {}): Todo => {
      const todo = linked("previous-task", "Original task", [agent]);
      todo.agents![0] = { ...todo.agents![0]!, linked_at: "2026-11-10T07:00:00Z", ...extra };
      return todo;
    };
    const showPlan = async (plan: TaskPlan, todos = [original()], sessions = [agent]) => {
      getFeedState().plans[plan.id] = plan;
      await show({ id: "recovery-event", conversation, at, kind: "plan", ref: plan.id }, todos, sessions);
    };

    it("uses the current task row when an older open-chat snapshot is still cached", async () => {
      const idle: Session = { ...agent, state: "idle", status: "idle", rev: 1 };
      await showPlan(makePlan(recovery), [original()], [idle]);
      const working: Session = { ...agent, rev: 2 };
      await act(async () => applyCompleteSessions([idle], [], { sessionSummaries: [working] }));
      expect(cards()[0]!.querySelector('[data-handoff-state="working"]')).not.toBeNull();
      expect(cards()[0]!.querySelector('[data-handoff-state="idle"]')).toBeNull();
      expect(host.querySelector('[data-handoff-history]')?.textContent).toContain(reason);
      expect(cards()).toHaveLength(1);
    });

    it("keeps delivery, the original session's live state and the failed attempt distinct; duplicate updates create no extra card or send", async () => {
      await showPlan(makePlan(undefined));
      expect(cards()[0]!.querySelector("[data-handoff-failure]")).not.toBeNull();
      const idle: Session = { ...agent, state: "idle", status: "idle" };
      await showPlan(makePlan(recovery), [original()], [idle]);
      expect(cards()).toHaveLength(1);
      const card = cards()[0]!;
      expect(card.textContent).toContain("Carry on the task");
      expect(card.textContent).not.toContain("Original task");
      expect(card.querySelector('[data-handoff-recovery="sent"]')?.textContent).toBe(t("assistant-tasks.continuation_sent"));
      expect(card.textContent).toContain(t("assistant-tasks.continuation_current_session"));
      expect(card.querySelector('[data-handoff-state="idle"]')).not.toBeNull();
      expect(card.querySelector('[data-handoff-state="working"]')).toBeNull();
      expect(card.querySelector("[data-handoff-failure]")).toBeNull();
      const details = host.querySelector<HTMLDetailsElement>("[data-handoff-history]")!;
      expect(details.open).toBe(false);
      act(() => details.querySelector("summary")!.click());
      expect(details.open).toBe(true);
      expect(details.textContent).toContain(reason);
      expect(getPanel()).toBeNull();
      act(() => card.click());
      expect(getPanel()).toEqual({ kind: "agent", mid: agent.machine_id, sid: agent.session_id });
      act(() => closePanel());

      await act(async () => applyCompleteSessions([{ ...agent, updated_at: "2026-11-10T08:01:00Z" }]));
      expect(cards()[0]!.querySelector('[data-handoff-state="working"]')).not.toBeNull();
      await showPlan(makePlan({ ...recovery }));
      await showPlan(makePlan({ ...recovery }));
      expect(cards()).toHaveLength(1);
      expect(host.querySelectorAll("[data-handoff-history]")).toHaveLength(1);
      expect(getFeedState().plans["recovery-plan"]!.tasks[0]!.error).toBe(reason);
      expect(vi.mocked(fetch).mock.calls.every(([, init]) => !init?.method || init.method === "GET")).toBe(true);
      setAgentOpenMode("page");
      act(() => cards()[0]!.click());
      expect(opened()).toBe(`/s/${agent.machine_id}/${agent.session_id}`);
    });

    it("says queued without claiming the request is executing or requiring a loaded session row", async () => {
      await showPlan(makePlan({ ...recovery, state: "queued" }), [original()], []);
      expect(cards()[0]!.querySelector('[data-handoff-recovery="queued"]')?.textContent).toBe(t("assistant-tasks.continuation_queued"));
      expect(cards()[0]!.querySelector("[data-handoff-state]")).toBeNull();
      expect(host.querySelector("[data-handoff-history]")!.textContent).toContain(reason);
    });

    it("shows the same verified send and folded history on a non-simplified plan", async () => {
      const plan = makePlan(recovery);
      await showPlan(plan, [original()], []);
      act(() => root.render(<PlanCard plan={plan} />));
      expect(cards()[0]!.querySelector('[data-handoff-recovery="sent"]')?.textContent).toBe(t("assistant-tasks.continuation_sent"));
      expect(cards()[0]!.getAttribute("data-handoff-card")).toBe(`${agent.machine_id}/${agent.session_id}`);
      const details = host.querySelector<HTMLDetailsElement>("[data-handoff-history]")!;
      expect(details.open).toBe(false);
      expect(details.textContent).toContain(reason);
      expect(cards()[0]!.querySelector('[data-handoff-state="working"]')).toBeNull();
    });

    it.each(["available", "sending", "unknown"] as const)("does not turn %s into a successful continuation even when the old session is working", async (state) => {
      await showPlan(makePlan({ ...recovery, state }));
      expect(cards()[0]!.tagName).toBe("DIV");
      expect(cards()[0]!.querySelector("[data-handoff-recovery]")).toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-failure]")).not.toBeNull();
      if (state !== "available") expect(cards()[0]!.querySelector(`[data-handoff-delivery="${state}"]`)?.textContent).toBe(t(`assistant-tasks.continuation_${state}`));
    });

    it("keeps an unknown send, its error and current-task navigation on the full plan", async () => {
      const plan = makePlan({ ...recovery, state: "unknown", send_error: "Delivery acknowledgement was lost" });
      await showPlan(plan);
      act(() => root.render(<PlanCard plan={plan} />));
      expect(cards()[0]!.querySelector('[data-handoff-delivery="unknown"]')?.textContent).toBe(t("assistant-tasks.continuation_unknown"));
      expect(host.querySelector("[data-handoff-delivery-error]")?.textContent).toBe("Delivery acknowledgement was lost");
      act(() => host.querySelector<HTMLButtonElement>("[data-handoff-current-task]")!.click());
      expect(getPanel()).toEqual({ kind: "task", todo: "previous-task" });
    });

    it.each([
      ["another task", { todo_id: "other-task" }],
      ["no verified scope", { scope_revision: "" }],
      ["no input correlation", { input_id: "" }],
      ["no confirmed receipt", { sent_at: "" }],
    ])("does not associate %s with the failure", async (_name, patch) => {
      await showPlan(makePlan({ ...recovery, ...patch as Partial<typeof recovery> }));
      expect(cards()[0]!.querySelector("[data-handoff-recovery]")).toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-failure]")).not.toBeNull();
    });

    it.each([
      ["released", { released_at: at }],
      ["dismissed", { dismissed_at: at }],
    ])("keeps delivery history after the original Agent is %s, without presenting its later work as this task", async (_name, extra) => {
      await showPlan(makePlan(recovery), [original(extra as Partial<TodoAgent>)]);
      expect(cards()[0]!.querySelector('[data-handoff-recovery="sent"]')).not.toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-failure]")).toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-state]")).toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-historical-session]")).not.toBeNull();
      expect(host.querySelector("[data-handoff-history]")?.textContent).toContain(reason);
    });

    it("retains confirmed delivery after workspace unlink removes membership", async () => {
      const todo = original();
      todo.agents = [];
      await showPlan(makePlan(recovery), [todo]);
      expect(cards()[0]!.querySelector('[data-handoff-recovery="sent"]')).not.toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-state]")).toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-historical-session]")).not.toBeNull();
      expect(host.querySelector("[data-handoff-current-task]")).not.toBeNull();
    });

    it("retains confirmed delivery while current tasks have not loaded", async () => {
      await showPlan(makePlan(recovery), [], []);
      expect(cards()[0]!.querySelector('[data-handoff-recovery="sent"]')).not.toBeNull();
      expect(cards()[0]!.querySelector("[data-handoff-state]")).toBeNull();
    });
  });

  it("wraps the recovery instructions and filenames on an interactive failed launch", async () => {
    const failure = 'The previous workspace has uncommitted changes. Commit or stash them, then retry (task_retry). Changed files include "timeout.ts".';
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task" }, [linked("example-task", "Example task", [], { start_error: failure, done_at: at })], []);
    const [card] = cards();
    const reason = card!.querySelector("[data-handoff-failure]");
    expect(card!.tagName).toBe("BUTTON");
    expect(reason?.textContent).toContain(failure);
    expect(reason?.classList.contains("break-words")).toBe(true);
    expect(reason?.classList.contains("truncate")).toBe(false);
  });

  describe("a to-do continued by later plans (#11854)", () => {
    // three plans on one to-do: the first made it, the next two continued it; each continuation released the Agent
    // before and cleared the older plan's todo_id, as the hub does (tasks_start.go)
    const hour = (h: number, m = 0) => `2026-11-10T${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:00Z`;
    const session = (id: string, title: string, state: "working" | "idle"): Session => ({ ...agent, session_id: id, title, state, status: state } as Session);
    const first = session("first-agent", "First Agent", "idle");
    const second = session("second-agent", "Second Agent", "idle");
    const third = session("third-agent", "Third Agent", "working");
    const row = (s: Session, linkedAt: string, releasedAt?: string): TodoAgent => ({ machine_id: s.machine_id, session_id: s.session_id, title: s.title, linked_at: linkedAt, released_at: releasedAt });
    const todoWith = (agents: TodoAgent[]): Todo => ({ id: "continued-task", title: "Continued task", origin: conversation, plan_id: "plan-a", created_at: hour(8), updated_at: hour(12), agents });
    const plan = (id: string, at: string, task: Partial<TaskPlan["tasks"][number]>): FeedItem => {
      getFeedState().plans[id] = { id, conversation, state: "started", created_at: at, tasks: [{ title: "Continued task", prompt: "p", cwd: "/example", machine: "example-computer", ...task }] };
      return { id: `${id}-event`, conversation, at, kind: "plan", ref: id };
    };
    const plans = () => ({
      a: plan("plan-a", hour(8), {}),
      b: plan("plan-b", hour(9), { continues: "continued-task" }),
      c: plan("plan-c", hour(12), { continues: "continued-task", todo_id: "continued-task" }),
    });
    const ids = () => cards().map((c) => c.getAttribute("data-handoff-card"));

    it("the newest plan's card is the running Agent it started, and opens it, not the first stopped one", async () => {
      const { c } = plans();
      const todo = todoWith([row(third, hour(12, 1)), row(second, hour(9, 1), hour(12)), row(first, hour(8, 1), hour(9))]);
      await show(c, [todo], [first, second, third]);
      expect(ids()).toEqual([`example-computer/third-agent`]);
      const [card] = cards();
      expect(card!.textContent).toContain("Third Agent");
      expect(card!.querySelector('[data-handoff-wake="continuation"]')).not.toBeNull();
      expect(card!.querySelector('[data-handoff-state="working"]')).not.toBeNull();
      act(() => card!.click());
      expect(getPanel()).toEqual({ kind: "agent", mid: "example-computer", sid: "third-agent" });
    });

    it("cards under the earlier plans keep the Agents those plans started, the first one included (its todo_id was cleared)", async () => {
      const { a, b } = plans();
      const todo = todoWith([row(third, hour(12, 1)), row(second, hour(9, 1), hour(12)), row(first, hour(8, 1), hour(9))]);
      await show(b, [todo], [first, second, third]);
      expect(ids()).toEqual([`example-computer/second-agent`]);
      act(() => root.render(<StrictMode><div data-chat-scroll><FeedLineView item={a} simplified /></div></StrictMode>));
      await settle();
      expect(ids()).toEqual([`example-computer/first-agent`]);
      expect(cards()[0]!.querySelector('[data-handoff-wake="created"]')).not.toBeNull();
    });

    it("before the newest plan's Agent is linked its card says starting, never an older Agent", async () => {
      const { c } = plans();
      await show(c, [todoWith([row(second, hour(9, 1), hour(12)), row(first, hour(8, 1), hour(9))])], [first, second]);
      expect(ids()).toEqual(["todo:continued-task"]);
      expect(cards()[0]!.textContent).toContain(t("assistant-tasks.starting_agent"));
      expect(cards()[0]!.textContent).not.toContain("Second Agent");
    });

    it("with every Agent stopped, the newest plan's card is the Agent it started, the latest", async () => {
      const { c } = plans();
      const todo = todoWith([row(third, hour(12, 1), hour(13)), row(second, hour(9, 1), hour(12)), row(first, hour(8, 1), hour(9))]);
      await show(c, [todo], [first, second, { ...third, state: "idle", status: "idle" } as Session]);
      expect(ids()).toEqual([`example-computer/third-agent`]);
    });

    it("several Agents the newest plan started together each get a card; the released older ones none", async () => {
      const { c } = plans();
      const fourth = session("fourth-agent", "Fourth Agent", "working");
      const todo = todoWith([row(fourth, hour(12, 2)), row(third, hour(12, 1)), row(second, hour(9, 1), hour(12)), row(first, hour(8, 1), hour(9))]);
      await show(c, [todo], [first, second, third, fourth]);
      // in the hub's order (formatTodo: the newest linked first)
      expect(ids()).toEqual([`example-computer/fourth-agent`, `example-computer/third-agent`]);
    });

    // continued in quick succession: the previous Agent was linked 10 seconds before the newest plan's line, within a
    // task line's look-behind (TASK_LINE_SLACK_MS); a plan line takes none, so it is never the plan's own
    describe("when the previous Agent was linked just before the newest plan's line", () => {
      const justBefore = (releasedAt?: string) => row(second, "2026-11-10T11:59:50Z", releasedAt);

      it("its card says starting until the plan's Agent is linked, not the previous one", async () => {
        const { c } = plans();
        await show(c, [todoWith([justBefore("2026-11-10T12:00:20Z"), row(first, hour(8, 1), hour(9))])], [first, second]);
        expect(ids()).toEqual(["todo:continued-task"]);
        expect(cards()[0]!.textContent).toContain(t("assistant-tasks.starting_agent"));
      });

      it("then it is only the plan's Agent, linked soon after", async () => {
        const { c } = plans();
        await show(c, [todoWith([row(third, hour(12, 1)), justBefore("2026-11-10T12:00:20Z"), row(first, hour(8, 1), hour(9))])], [first, second, third]);
        expect(ids()).toEqual([`example-computer/third-agent`]);
      });

      it("or linked more than two minutes later", async () => {
        const { c } = plans();
        await show(c, [todoWith([row(third, hour(12, 5)), justBefore("2026-11-10T12:00:20Z"), row(first, hour(8, 1), hour(9))])], [first, second, third]);
        expect(ids()).toEqual([`example-computer/third-agent`]);
      });
    });
  });

  it("a message that started a new Agent for its task has that Agent's card under it", async () => {
    const next: Session = { ...agent, session_id: "next-agent", title: "Next Agent" };
    await show({ id: "routed-event", conversation, at, kind: "routed", ref: "example-task", body: { text: "Go on", new_agent: "next-agent", new_agent_machine: "example-computer" } },
      [linked("example-task", "Example task", [agent, next])], [agent, next]);
    const [card] = cards();
    expect(cards()).toHaveLength(1);
    expect(card!.textContent).toContain("Next Agent");
    act(() => card!.click());
    expect(getPanel()).toEqual({ kind: "agent", mid: "example-computer", sid: "next-agent" });
    act(() => closePanel());
    setAgentOpenMode("page");
    act(() => card!.click());
    expect(opened()).toBe("/s/example-computer/next-agent");
  });
});

describe("the card's words", () => {
  it("shortens an id as cindy does", () => {
    expect(shortSessionId("0f3c2a91-5d7e-4b8a-9c11-2e4f6a8b7ed0")).toBe("0f3c2a91…7ed0");
    expect(shortSessionId("short-id")).toBe("short-id");
  });

  it("says when the Agent was last active", () => {
    const now = Date.parse(at);
    expect(lastActive({ activity_at: new Date(now - 10_000).toISOString() }, now)).toBe(t("assistant-tasks.handoff_active_now"));
    expect(lastActive({ updated_at: new Date(now - 5 * 60_000).toISOString() }, now)).toMatch(/5/);
    expect(lastActive({}, now)).toBeUndefined();
  });
});

describe("which Agents a card stands for (#11854)", () => {
  const row = (id: string, linkedAt: string, extra: Partial<TodoAgent> = {}): TodoAgent => ({ machine_id: "example-computer", session_id: id, linked_at: linkedAt, ...extra });
  const names = (agents: TodoAgent[]) => agents.map((a) => a.session_id);

  it("without an anchor: the newest batch still linked, not the first", () => {
    const agents = [row("old", "2026-11-10T08:00:00Z", { released_at: "2026-11-10T09:00:00Z" }), row("new", "2026-11-10T12:00:00Z"), row("new-too", "2026-11-10T12:01:00Z")];
    expect(names(handoffAgents(agents))).toEqual(["new", "new-too"]);
  });

  it("without an anchor and with every Agent released: the one released last", () => {
    const agents = [row("first", "2026-11-10T08:00:00Z", { released_at: "2026-11-10T09:00:00Z" }), row("last", "2026-11-10T09:00:00Z", { released_at: "2026-11-10T12:00:00Z" })];
    expect(names(handoffAgents(agents))).toEqual(["last"]);
  });

  it("anchored: the first Agents linked since, skipping ones that failed to start; none yet unless it may fall back", () => {
    const agents = [row("old", "2026-11-10T08:00:00Z", { released_at: "2026-11-10T09:00:00Z" }), row("failed", "2026-11-10T09:00:30Z", { start_error: "x" }), row("retry", "2026-11-10T09:05:00Z")];
    expect(names(handoffAgents(agents, "2026-11-10T09:00:00Z"))).toEqual(["retry"]);
    expect(names(handoffAgents(agents.slice(0, 1), "2026-11-10T09:00:00Z", false))).toEqual([]);
    expect(names(handoffAgents(agents.slice(0, 1), "2026-11-10T09:00:00Z"))).toEqual(["old"]);
  });

  it("looks behind its anchor only as far as asked: a task line's 30 seconds, a plan line's none", () => {
    const agents = [row("previous", "2026-11-10T11:59:50Z"), row("planned", "2026-11-10T12:03:00Z")];
    expect(names(handoffAgents(agents, "2026-11-10T12:00:00Z", false, TASK_LINE_SLACK_MS))).toEqual(["previous"]);
    expect(names(handoffAgents(agents, "2026-11-10T12:00:00Z", false))).toEqual(["planned"]);
    expect(names(handoffAgents(agents.slice(0, 1), "2026-11-10T12:00:00Z", false))).toEqual([]);
  });
});
