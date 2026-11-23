// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { formatUSD, resetTodoUsageForTest, type TodoUsageDetail } from "../lib/todo-usage";
import { TodoUsageLine, TodoUsageSection } from "./TodoUsage";

vi.hoisted(() => { Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); });
const tk = (input: number, write: number, read: number, out: number) => ({ in: input, cache_write: write, cache_read: read, out, total: input + write + read + out });
const detail: TodoUsageDetail = {
  todo_id: "t1", estimate: true, sessions: 1, found: 1, usd: 124.4, tokens: tk(1e6, 8e6, 298e6, 1e6),
  parts: [{ machine_id: "box", session_id: "s1", title: "Project board", found: true, tokens: tk(1e6, 8e6, 298e6, 1e6), usd: 124.4,
    subagents: [{ subagent: "a1", kind: "Explore", title: "Find the card", found: true, tokens: tk(0, 0, 2e6, 1e5), usd: 2.4 }] }],
  models: [{ model: "claude-opus-5-5", tokens: tk(1e6, 8e6, 298e6, 1e6), usd: 124.4, price: { input: 4, cache_write: 5, cache_read: 0.2, output: 20 }, price_from: "catalog" }],
};
const calls = vi.hoisted(() => [] as string[]);
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  api: vi.fn(async (path: string) => {
    calls.push(path);
    if (path.startsWith("/todos/usage?")) return { todos: { t1: { tokens: detail.tokens, usd: detail.usd, sessions: 1, found: 1 } } };
    if (path === "/todos/t1/usage") return detail;
    throw new Error("unexpected " + path);
  }),
}));

let host: HTMLDivElement, root: Root;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  calls.length = 0;
  resetTodoUsageForTest();
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});
const settle = async () => { await act(async () => { await new Promise((r) => setTimeout(r, 5)); }); };

describe("a to-do's usage (#12936)", () => {
  it("cards ask together and show tokens with the estimate", async () => {
    await act(async () => root.render(<>
      <TodoUsageLine todoId="t1" hasAgents />
      <TodoUsageLine todoId="t2" hasAgents />
      <TodoUsageLine todoId="t3" hasAgents={false} />
    </>));
    await settle();
    expect(calls).toEqual(["/todos/usage?ids=t1,t2"]);
    const lines = host.querySelectorAll('[data-testid="todo-usage-line"]');
    expect(lines.length).toBe(1);
    expect(lines[0].textContent).toContain("308.0M");
    expect(lines[0].textContent).toContain("$124");
  });

  it("the detail breaks it down by session and subagent", async () => {
    await act(async () => root.render(<TodoUsageSection todoId="t1" hasAgents />));
    await settle();
    expect(host.querySelector('[data-testid="todo-usage-total"]')?.textContent).toContain("308.0M");
    expect(host.querySelector('[data-testid="todo-usage-usd"]')?.textContent).toContain("$124");
    expect(host.querySelectorAll('[data-testid="todo-usage-session"]').length).toBe(1);
    expect(host.querySelector('[data-testid="todo-usage-subagent"]')?.textContent).toContain("Explore · Find the card");
  });

  it("formats dollars as the reply bar does", () => {
    expect(formatUSD(0)).toBe("$0.00");
    expect(formatUSD(0.004)).toBe("<$0.01");
    expect(formatUSD(1234.4)).toBe("$1234.40");
  });
});
