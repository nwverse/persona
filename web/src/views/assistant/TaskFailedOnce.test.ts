// A started plan's task that did not start says why on the plan (#4145, #4809).
import { describe, expect, it } from "vitest";

describe("a started PlanCard", () => {
  it("says why the task did not start", async () => {
    const { renderToStaticMarkup } = await import("react-dom/server");
    const { createElement } = await import("react");
    const { PlanCard } = await import("./Tasks");
    const plan = { id: "p", conversation: "c", state: "started" as const, tasks: [{ title: "示例任务", prompt: "", machine: "m", cwd: "/", todo_id: "t1", error: "rpc: connection closed" }] };
    expect(renderToStaticMarkup(createElement(PlanCard, { plan }))).toContain("「示例任务」没开起来：rpc: connection closed");
  });
});

describe("PlanCard with a blank reason (#4809)", () => {
  it("says the reason is unknown instead of leaving it empty", async () => {
    const { renderToStaticMarkup } = await import("react-dom/server");
    const { createElement } = await import("react");
    const { PlanCard } = await import("./Tasks");
    const plan = { id: "p", conversation: "c", state: "started" as const, tasks: [{ title: "示例任务", prompt: "", machine: "m", cwd: "/", todo_id: "t1", error: "  \n " }] };
    expect(renderToStaticMarkup(createElement(PlanCard, { plan }))).toContain("「示例任务」没开起来：原因不明");
  });
});
