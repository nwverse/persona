// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { loadPages } from "../../lib/lazy-page";
import type { Todo } from "../../lib/todos";
import { CriteriaSummary, ReviewBadge } from "./Supervision";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

it("keeps the live count immediate but renders evidence only when its native disclosure opens", async () => {
  const todo: Todo = {
    id: "todo_criteria", title: "Release", created_at: "", updated_at: "",
    supervision: {
      supervisor: "main", done_when: ["Tests pass", "PR opened"],
      criteria: [
        { criterion: "Tests pass", state: "met", evidence: "go test: PASS", by: "agent:box/s1", at: "2026-11-11T01:00:00Z" },
        { criterion: "PR opened", state: "pending" },
      ],
    },
  };
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  let navigated = false;
  const draw = () => root.render(
    <div onClick={() => { navigated = true; }} onKeyDown={() => { navigated = true; }}>
      <ReviewBadge todo={todo} />
      <CriteriaSummary todo={todo} />
    </div>,
  );
  try {
    await act(async () => draw());
    expect(host.querySelector('[data-testid="review-badge"]')?.textContent).toContain("1/2");
    expect(host.querySelector("ul")).toBeNull();
    const details = host.querySelector("details")!;
    await act(async () => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
      await loadPages();
    });
    expect(host.textContent).toContain("go test: PASS");
    expect(host.textContent).toContain("Agent · box/s1");
    expect(host.querySelector("time")?.dateTime).toBe("2026-11-11T01:00:00Z");
    todo.supervision!.criteria![1] = { criterion: "PR opened", state: "met", evidence: "PR #7" };
    await act(async () => draw());
    expect(host.querySelector('[data-testid="review-badge"]')?.textContent).toContain("2/2");
    expect(host.textContent).toContain("PR #7");
    await act(async () => {
      host.querySelector("summary")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      host.querySelector("summary")!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      details.open = false;
      details.dispatchEvent(new Event("toggle"));
    });
    expect(navigated).toBe(false);
    expect(host.querySelector("ul")).toBeNull();
  } finally {
    await act(async () => root.unmount());
    host.remove();
  }
});
