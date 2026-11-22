// @vitest-environment jsdom
// Finished work held for the owner's OK in Needs you (#13191): its own words, not the permission card's "Waiting for
// your approval" (nothing waits to be approved on the row), and an explicit Open task.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { t } from "../../i18n";
import type { Todo } from "../../lib/todos";
import WaitingTaskRow from "./WaitingTaskRow";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const at = "2026-11-10T08:00:00Z";
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const held = (unblock: string): Todo => ({
  id: "todo_held", title: "Release the sample page", origin: "main", created_at: at, updated_at: at,
  delivery: { state: "pr", gate: "Approve the release", unblock, at },
} as unknown as Todo);

describe("a delivery held for the owner's OK in Needs you", () => {
  it("says what it is and offers Open task", () => {
    act(() => root.render(<WaitingTaskRow todo={held("user")} status="follow_up" countsOpenAttr={{}} />));
    expect(host.textContent).toContain(t("assistant-tasks.delivery_ok"));
    expect(host.textContent).not.toContain(t("todos.kicker_permission"));
    expect(host.textContent).toContain("Approve the release");
    const open = host.querySelector<HTMLButtonElement>("[data-open-task=todo_held]");
    expect(open?.textContent).toBe(t("assistant-tasks.open_task"));
    expect(open?.className).toContain("whitespace-nowrap");
  });

  it("offers nothing for a hold its supervisor has not read", () => {
    act(() => root.render(<WaitingTaskRow todo={held("supervisor")} status="follow_up" countsOpenAttr={{}} />));
    expect(host.querySelector("[data-open-task]")).toBeNull();
    expect(host.textContent).not.toContain(t("assistant-tasks.delivery_ok"));
  });
});
