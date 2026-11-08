// @vitest-environment jsdom
// A running Claude Code session's model popover with a typed model ID (#1644): "Other model…" (also when the list is
// empty or couldn't be read) says why an ID can't be used as it is typed, Apply sends it through POST …/modes like a
// listed one, the field keeps its own keys, and an ID typed before can be forgotten (back when the hub says no).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getState } from "../../lib/store";
import type { Session } from "../../lib/types";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  Element.prototype.scrollIntoView ??= () => {}; // jsdom has none
});

const { ModelEffortPopover } = await import("./ModelEffortPopover");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const flush = () => act(async () => { for (let i = 0; i < 6; i++) await Promise.resolve(); });
let host: HTMLDivElement;
let root: Root;
let listAnswer: () => Response;
let forgetAnswer: () => Response;
const calls: { method: string; url: string }[] = [];

const session = (mid: string): Session => ({ machine_id: mid, session_id: "s1", cwd: "/code", status: "idle", state: "idle", agent: "claude", effort: "high", model: "claude-opus-5-5", pane_id: "p1" }) as Session;
const listed = () => json({ agent: "claude", models: [
  { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: ["low", "high"] },
  { id: "vendor/model-x", name: "vendor/model-x", description: "之前用过的模型 ID", custom: true },
] });

function type(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  act(() => {
    setter.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
const key = (el: HTMLElement, k: string) => act(() => { el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })); });
const button = (text: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.includes(text));

beforeEach(() => {
  Object.assign(getState(), { features: [], sessions: {} });
  calls.length = 0;
  listAnswer = listed;
  forgetAnswer = () => json({ ok: true });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ method: init?.method ?? "GET", url });
    if (url.includes("/models/custom")) return forgetAnswer();
    if (url.includes("/models")) return listAnswer();
    return json({ ok: true });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

async function open(mid: string, apply = vi.fn(async () => true)) {
  const anchor = document.createElement("button");
  document.body.append(anchor);
  act(() => root.render(<ModelEffortPopover session={session(mid)} anchor={anchor} onClose={() => {}} apply={apply} />));
  await flush();
  return apply;
}

describe("the session's model popover: a typed model ID (#1644)", () => {
  it("Other model… says why, keeps its keys, and Apply sends the typed ID", async () => {
    const apply = await open("mac-a");
    act(() => button("其他模型…")!.click());
    const input = document.body.querySelector<HTMLInputElement>("[data-model-id-field] input")!;
    type(input, "a;b");
    expect(document.body.querySelector("[data-model-id-problem]")!.textContent).toContain("「;」");
    // ← in the field moves the caret, not the effort tab
    const tab = () => document.body.querySelector('[role="tab"][aria-selected="true"]')?.textContent;
    const before = tab();
    key(input, "ArrowLeft");
    expect(tab()).toBe(before);
    type(input, "~vendor/model-latest");
    key(input, "Enter");
    expect(button("~vendor/model-latest")!.getAttribute("aria-current")).toBe("true");
    await act(async () => button("应用")!.click());
    expect(apply).toHaveBeenCalledWith({ model: "~vendor/model-latest" });
  });

  it("offers Enter model ID… when the list couldn't be read", async () => {
    listAnswer = () => json({ error: "claude 没有登录" }, 502);
    await open("mac-b");
    expect(button("输入模型 ID…")).toBeTruthy();
  });

  it("forgets an ID typed before, and puts it back with why when the hub says no", async () => {
    forgetAnswer = () => json({ error: "只读设备不能改" }, 403);
    await open("mac-c");
    const forget = () => document.body.querySelector<HTMLButtonElement>('button[aria-label="忘记「vendor/model-x」"]');
    act(() => forget()!.click());
    expect(forget()).toBeNull();
    await flush();
    expect(calls.some((c) => c.method === "DELETE" && c.url.includes("/machines/mac-c/launchers/claude/models/custom?model=vendor%2Fmodel-x"))).toBe(true);
    expect(forget()).not.toBeNull();
    expect(document.body.querySelector('[role="alert"]')!.textContent).toMatch(/没能忘记「vendor\/model-x」：.*只读设备不能改/);
  });
});
