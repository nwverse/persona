// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const fake = vi.hoisted(() => ({ load: vi.fn(), apply: vi.fn(), undo: vi.fn(), toast: vi.fn(), refresh: vi.fn() }));
vi.mock("../../lib/todo-sort", async (orig) => ({
  ...(await orig<typeof import("../../lib/todo-sort")>()),
  loadSortSuggestions: fake.load,
  applySort: fake.apply,
  undoSort: fake.undo,
}));
vi.mock("../../lib/store", () => ({ refreshTodos: fake.refresh }));
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast: fake.toast }));
import SortTodos from "./SortTodos";
import { t } from "../../i18n";
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root, host: HTMLDivElement;
beforeEach(() => {
  for (const f of Object.values(fake)) f.mockReset();
  fake.load.mockResolvedValue({
    model: "used",
    unsorted: 3,
    sorted: 1,
    projects: [{ slug: "garden-app", name: "Garden app" }, { slug: "docs-site", name: "Docs site" }],
    items: [
      { todo: "t1", title: "Water the seedlings", project: "garden-app", project_name: "Garden app", confidence: 0.9, reason: "Its title names Garden app", preselect: true },
      { todo: "t2", title: "Buy milk", project: "", temporary: true, confidence: 0.85, reason: "A one-off errand", preselect: true },
      { todo: "t3", title: "Think about it", project: "", confidence: 0, preselect: false },
    ],
  });
  fake.apply.mockResolvedValue({ batch: "tsb_1", moved: 2, unchanged: 0 });
  fake.undo.mockResolvedValue({ batch: "tsb_1", undone: 1, skipped: 1, outcomes: [] });
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });

it("shows each suggestion with how sure and why, writes the ticked ones as one batch and offers Undo", async () => {
  const onClose = vi.fn();
  act(() => root.render(<SortTodos onClose={onClose} />));
  await flush();
  const rows = [...document.querySelectorAll<HTMLElement>("[data-testid=todos-sort-row]")];
  expect(rows.map((r) => r.dataset.todo)).toEqual(["t1", "t2"]); // the unplaced one is folded
  expect(rows[0].textContent).toContain("Its title names Garden app");
  expect(rows[0].textContent).toContain("90%");
  const apply = document.querySelector<HTMLButtonElement>("[data-testid=todos-sort-apply]")!;
  expect(apply.textContent).toBe(t("todos-sort.apply", { count: 2 }));

  // the owner re-files the first under Docs site
  const select = rows[0].querySelector("select")!;
  act(() => {
    select.value = "docs-site";
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await act(async () => apply.click());
  await flush();
  expect(fake.apply).toHaveBeenCalledWith([{ id: "t1", project: "docs-site" }, { id: "t2", temporary: true }]);
  expect(onClose).toHaveBeenCalled();
  const [text, tone, action] = fake.toast.mock.calls[0];
  expect([text, tone]).toEqual([t("todos-sort.done", { count: 2 }), "ok"]);
  await act(async () => action.run());
  await flush();
  expect(fake.undo).toHaveBeenCalledWith("tsb_1");
  expect(fake.toast.mock.calls[1][0]).toBe(t("todos-sort.undone_skipped", { count: 1, skipped: 1 }));
});

it("says when only the rules could suggest", async () => {
  fake.load.mockResolvedValue({ model: "unavailable", unsorted: 0, sorted: 0, projects: [], items: [] });
  act(() => root.render(<SortTodos onClose={() => {}} />));
  await flush();
  expect(document.body.textContent).toContain(t("todos-sort.no_model"));
  expect(document.body.textContent).toContain(t("todos-sort.nothing"));
  expect(document.querySelector<HTMLButtonElement>("[data-testid=todos-sort-apply]")!.disabled).toBe(true);
});
