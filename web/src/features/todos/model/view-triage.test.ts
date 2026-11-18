import { describe, expect, it } from "vitest";
import type { TodoSummary } from "../../../lib/todo-view-wire";
import { TRIAGE_SECTIONS, triageTodos, type TriageResult } from "../../../lib/todo-triage";
import type { Todo } from "../../../lib/todos";
import { drawnFromView } from "./view-triage";

const NOW = Date.parse("2026-11-10T12:00:00Z");
const ago = (hours: number) => new Date(NOW - hours * 3_600_000).toISOString();
const todo = (id: string, more: Partial<Todo> = {}): Todo => ({ id, title: `Errand ${id}`, created_at: ago(5), updated_at: ago(5), ...more });
const view = (section: string, reason: string, more: Partial<TodoSummary> = {}): TodoSummary => ({ phase: "todo", section, reason, rank: 0, ...more });
const ids = (r: TriageResult) => Object.fromEntries(TRIAGE_SECTIONS.map((s) => [s, r.sections[s].map((g) => g.members.map((m) => m.id))]).filter(([, rows]) => rows.length));

describe("drawnFromView", () => {
  it("returns today's triage itself while no row has a view it can read", () => {
    const local = triageTodos([todo("todo_a"), todo("todo_b", { view: view("later", "rest") })], undefined, NOW);
    expect(drawnFromView(local)).toBe(local);
  });

  it("places each row with a view by it, and keeps today's place for a row without one", () => {
    const list = [
      todo("todo_a", { view: view("needs_you", "owner_decides", { phase: "needs_you" }) }),
      todo("todo_b"), // an older manager's row: no view
      todo("todo_c", { done_at: ago(1), view: view("just_finished", "closed_unseen", { phase: "done" }) }),
      todo("todo_d", { view: view("a_newer_section", "rest") }), // a view this web cannot read
    ];
    const r = drawnFromView(triageTodos(list, undefined, NOW));
    expect(ids(r)).toEqual({ waiting: [["todo_a"]], other: [["todo_b"], ["todo_d"]], just_done: [["todo_c"]] });
    expect(r.todoCounts.waiting).toBe(1);
    expect([r.open, r.shelved, r.finished]).toEqual([3, 0, 1]);
  });

  it("splits related rows: a viewed member stands alone, the rest stay together under their own lead", () => {
    // the same title makes the three one merged row in today's triage
    const same = { title: "Book the museum tickets" };
    const list = [todo("todo_a", same), todo("todo_b", { ...same, view: view("working", "agent_running", { phase: "working" }) }), todo("todo_c", same)];
    const local = triageTodos(list, undefined, NOW);
    expect(ids(local)).toEqual({ other: [["todo_a", "todo_b", "todo_c"]] });
    expect(ids(drawnFromView(local))).toEqual({ working: [["todo_b"]], other: [["todo_a", "todo_c"]] });
  });

  it("orders the rows with a view by the view's section, then rank, manager and id, ahead of the rest", () => {
    const list = [
      todo("todo_old"),
      todo("todo_y", { manager: "b", view: view("can_wait", "can_wait", { phase: "needs_you", rank: 0 }) }),
      todo("todo_x", { manager: "a", view: view("needs_you", "agent_asks", { phase: "needs_you", rank: 1 }) }),
      todo("todo_w", { manager: "b", view: view("needs_you", "agent_asks", { phase: "needs_you", rank: 0 }) }),
      todo("todo_v", { manager: "a", view: view("needs_you", "agent_asks", { phase: "needs_you", rank: 0 }) }),
      todo("todo_s", { view: view("someday", "deferred", { phase: "someday" }) }),
      todo("todo_t", { view: view("todo", "rest", { rank: 3 }) }),
    ];
    expect(ids(drawnFromView(triageTodos(list, undefined, NOW)))).toEqual({
      waiting: [["todo_v"], ["todo_w"], ["todo_x"], ["todo_y"]],
      other: [["todo_t"], ["todo_s"], ["todo_old"]],
    });
  });

  it("counts the rows where they are drawn: a failed start in Needs you is open, accepted work is not", () => {
    const list = [
      todo("todo_f", { done_at: ago(1), start_error: "The computer is offline", view: view("needs_you", "failed_start", { phase: "needs_you" }) }),
      todo("todo_g", { view: view("just_finished", "finished_unseen", { phase: "ready", line: { key: "accepted" } }) }),
      todo("todo_h", { view: view("todo", "rest", { inactive: ago(50) }) }),
    ];
    const r = drawnFromView(triageTodos(list, undefined, NOW));
    expect(ids(r)).toEqual({ waiting: [["todo_f"]], just_done: [["todo_g"]], shelved: [["todo_h"]] });
    expect(r.sections.waiting[0].triage.get("todo_f")?.rule).toBe("failed_start");
    expect([r.open, r.shelved, r.finished]).toEqual([1, 1, 1]);
  });
});
