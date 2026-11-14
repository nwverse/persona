// The island at the MacBook notch (#7669): what the web sends the desktop app. The counts are the Dock badge's; the
// rows are the agents that need you, then the working ones, newest first, each with its Agents-list line.
import { describe, expect, it } from "vitest";
import { ISLAND_ROWS, islandState } from "./desktop-island";
import type { Interaction, Session } from "./types";

const s = (id: string, o: Partial<Session>): Session => ({ machine_id: "mac", session_id: id, title: `示例 ${id}`, cwd: "/code", status: "idle", ...o });
const at = (minute: number) => `2030-01-01T09:${String(minute).padStart(2, "0")}:00Z`;

describe("islandState", () => {
  it("lists who needs you, then who works, newest first, each with what it asks or does", () => {
    const sessions = [
      s("w1", { state: "working", updated_at: at(1), activity: "在跑测试" }),
      s("n1", { state: "needs_you", updated_at: at(2) }),
      s("w2", { state: "working", updated_at: at(5) }),
      s("n2", { state: "needs_you", updated_at: at(3), attention_reason: "等你确认发布" }),
      s("u1", { state: "unread", updated_at: at(9) }),
      s("i1", { state: "idle", updated_at: at(9) }),
      s("a1", { state: "needs_you", updated_at: at(9), assistant: true }),
    ];
    const card: Interaction = { id: "ix1", machine_id: "mac", session_id: "n1", kind: "question", questions: [{ question: "用哪个框架？" } as NonNullable<Interaction["questions"]>[number]] };
    const state = islandState(sessions, [card], { needs: 2, working: 2, unread: 1 });
    expect(state.rows.map((r) => [r.title, r.state, r.label, r.line])).toEqual([
      ["示例 n2", "needs_you", "需要你", "等你确认发布"],
      ["示例 n1", "needs_you", "需要你", "在问你：用哪个框架？"],
      ["示例 w2", "working", "工作中", ""],
      ["示例 w1", "working", "工作中", "在跑测试"],
    ]);
    expect(state.rows.every((r) => r.route.startsWith("/") && !r.route.startsWith("//"))).toBe(true);
    expect(state.rows[0].route).toContain("n2");
    expect([state.needsYou, state.working, state.unread]).toEqual([2, 2, 1]);
    expect(state.summary).toBe("2 个需要你，2 个工作中，1 个完成未看");
  });

  it("lists at most five and says nothing when nothing is going on", () => {
    const many = Array.from({ length: 8 }, (_, i) => s(`w${i}`, { state: "working", updated_at: at(i) }));
    expect(islandState(many, [], { needs: 0, working: 8, unread: 0 }).rows).toHaveLength(ISLAND_ROWS);
    expect(islandState([], [], { needs: 0, working: 0, unread: 0 })).toEqual({ needsYou: 0, working: 0, unread: 0, rows: [], summary: "" });
  });
});
