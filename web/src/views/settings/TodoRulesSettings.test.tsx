// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Pending = { path: string; method: string; json?: unknown; resolve: (value: unknown) => void; reject: (error: unknown) => void };
const pending: Pending[] = [];
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string, init?: { method?: string; json?: unknown }) => new Promise((resolve, reject) => pending.push({ path, method: init?.method ?? "GET", json: init?.json, resolve, reject })),
}));
const toast = vi.fn();
vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast: (...args: unknown[]) => toast(...args) }));

const { TodoRulesSettings } = await import("./TodoRulesSettings");

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  pending.length = 0;
  toast.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("Settings › To-dos", () => {
  it("loads rules, adds a rule, and saves the form", async () => {
    await act(async () => root.render(<TodoRulesSettings />));
    const at = (path: string) => pending.find((p) => p.path === path)!;
    expect(pending.map((p) => p.path).sort()).toEqual(["/connectors?all=1", "/todo-rules", "/todo-rules/decisions"]);
    await act(async () => at("/todo-rules").resolve({ rules: [], dry_run: false }));
    await act(async () => at("/todo-rules/decisions").resolve({ decisions: [{ at: new Date().toISOString(), item_id: "i1", verdict: "not_todo", title: "Build finished" }] }));
    await act(async () => at("/connectors?all=1").resolve({ connectors: [{ id: "larkcli", name: "Lark via lark-cli" }, { id: "github", name: "GitHub" }] }));
    // The decision time follows the app's own clock format (HH:MM today), not the browser's default locale string.
    expect(host.querySelector("time")?.textContent).toMatch(/^\d{2}:\d{2}$/);
    const add = [...host.querySelectorAll("button")].find((button) => button.textContent?.includes("添加规则"));
    expect(add).toBeTruthy();
    await act(async () => add!.click());
    const inputs = [...host.querySelectorAll<HTMLInputElement>("input")];
    expect(inputs.length).toBeGreaterThanOrEqual(2);
    // Sources are picked from the hub's connectors, not typed as ids.
    const chip = [...host.querySelectorAll<HTMLButtonElement>("button[aria-pressed]")].find((button) => button.textContent === "Lark via lark-cli");
    expect(chip?.getAttribute("aria-pressed")).toBe("false");
    await act(async () => chip!.click());
    expect(chip?.getAttribute("aria-pressed")).toBe("true");
    inputs[0]!.value = "Review requests";
    inputs[0]!.dispatchEvent(new Event("input", { bubbles: true }));
    const prompt = host.querySelector("textarea") as HTMLTextAreaElement;
    prompt.value = "Someone asks me to review a change";
    prompt.dispatchEvent(new Event("input", { bubbles: true }));
    const save = [...host.querySelectorAll("button")].find((button) => button.textContent?.includes("保存规则"));
    expect(save).toBeTruthy();
    await act(async () => save!.click());
    const write = pending.find((p) => p.method === "PUT");
    expect(write?.path).toBe("/todo-rules");
    expect(write?.json).toEqual(expect.objectContaining({ dry_run: false, rules: [expect.objectContaining({ enabled: true, sources: ["larkcli"] })] }));
  });
});
