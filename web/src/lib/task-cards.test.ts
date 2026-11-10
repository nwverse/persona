import { describe, expect, it } from "vitest";
import { taskCardSlots, taskMeter, upsertTaskCards, type TaskCardMetadata } from "./task-cards";
import type { TaskPlan, FeedItem } from "./feed";
import type { Todo } from "./todos";
import type { Session } from "./types";

const at = "2026-11-10T08:00:00Z";
const later = "2026-11-10T08:05:00Z";
const card: TaskCardMetadata = { id: "plan:p:0", plan_id: "p", index: 0, at, updated_at: at, state: "not_started" };
const plan: TaskPlan = { id: "p", conversation: "c", created_at: at, state: "open", tasks: [{ title: "Demo task", prompt: "Demo", machine: "demo", cwd: "/demo" }] };
const todo: Todo = { id: "t", title: "Demo task", origin: "c", plan_id: "p", created_at: later, updated_at: later };
const item = (kind: FeedItem["kind"], id = kind): FeedItem => ({ id, conversation: "c", at: later, kind, ref: "t" });

describe("stable assistant task slots", () => {
  it("upserts a slot in place and retains the plan time through binding and replay", () => {
    const bound = { ...card, todo_id: "t", state: "working", at: later, updated_at: later };
    const cards = upsertTaskCards([card], [bound]);
    expect(cards).toEqual([{ ...bound, at }]);
    expect(upsertTaskCards(cards, [card])).toEqual(cards);
    expect(upsertTaskCards(cards, [])).toBe(cards);
    expect(upsertTaskCards(cards, null)).toBe(cards);
    expect(upsertTaskCards(cards)).toBe(cards);
  });

  it("folds a legacy plan, dispatch, receipt and review into one slot", () => {
    const started = { ...plan, state: "started" as const, tasks: [{ ...plan.tasks[0], todo_id: "t" }] };
    const feed = [item("task"), item("receipt"), { ...item("review"), body: { verdict: "accepted" } }];
    const cards = taskCardSlots("c", [], feed, { p: started }, [todo]);
    expect(cards).toHaveLength(1);
    expect(cards[0]).toMatchObject({ id: card.id, todo_id: "t", at, receipt_id: "receipt", state: "accepted" });
  });

  it("uses a metadata slot on a page without the original plan", () => {
    const bound = { ...card, todo_id: "t", state: "working", updated_at: later };
    expect(taskCardSlots("c", [bound], [item("task"), item("report")], {}, [todo])).toHaveLength(1);
    expect(taskCardSlots("c", [bound], [], {}, [todo])[0]).toEqual(bound);
  });

  it("replaces task-only legacy fallbacks when a plan binding arrives", () => {
    const fallback = taskCardSlots("c", [], [item("task")], {}, [todo]);
    expect(fallback[0].id).toBe("task:t");
    expect(taskCardSlots("c", [{ ...card, todo_id: "t" }], [item("task")], {}, [todo]).map((c) => c.id)).toEqual([card.id]);
    expect(taskCardSlots("c", [{ ...card, id: "task:t", todo_id: "t", plan_id: undefined }], [item("task")], {}, [todo])).toHaveLength(1);
  });

  it("deduplicates reused to-dos in stale plan payloads and cached metadata", () => {
    const oldPlan = { ...plan, tasks: [{ ...plan.tasks[0], todo_id: "t" }] };
    const newPlan = { ...oldPlan, id: "next" };
    const old = { ...card, todo_id: "t", updated_at: later };
    const current = { ...card, id: "plan:next:0", plan_id: "next", todo_id: "t" };
    const reused = { ...todo, plan_id: "next" };
    const cards = taskCardSlots("c", [current, old], [item("task"), item("receipt")], { p: oldPlan, next: newPlan }, [reused]);
    expect(cards).toHaveLength(1);
    expect(cards[0]).toMatchObject({ id: current.id, plan_id: "next", todo_id: "t" });
  });

  it("keeps authoritative current state instead of a receipt's turn outcome", () => {
    const bound = { ...card, todo_id: "t", state: "follow_up", updated_at: later };
    const receipt = { ...item("receipt"), body: { state: "done" } };
    expect(taskCardSlots("c", [bound], [receipt], {}, [])[0].state).toBe("follow_up");
  });

  it("retains other slots when only one changes", () => {
    const second = { ...card, id: "plan:p:1", index: 1 };
    expect(upsertTaskCards([card, second], [{ ...card, state: "working", updated_at: later }])).toHaveLength(2);
  });
});

describe("task count meters", () => {
  it("uses actual reported counts, including zero, and otherwise the Agent plan", () => {
    const session = { plan: { done: 2, total: 5, current: "Demo check" } } as Session;
    expect(taskMeter(todo, session)).toEqual({ value: 2, total: 5 });
    expect(taskMeter({ ...todo, supervision: { supervisor: "c", progress: { kind: "progress", step: 0, steps: 4, text: "Starting" } } }, session)).toEqual({ value: 0, total: 4 });
  });

  it("never fabricates percentages from stage or unknown/out-of-range counts", () => {
    expect(taskMeter({ ...todo, stage: "review" })).toBeUndefined();
    for (const [done, total] of [[1, 0], [-1, 4], [5, 4], [NaN, 4], [1, Infinity]]) expect(taskMeter(todo, { plan: { done, total } } as Session)).toBeUndefined();
  });
});
