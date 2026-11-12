// @vitest-environment jsdom
// The composer's toolbar as cindy's (#5324): the + opens the + / @ panel with a search box that has the keys; a typed @
// opens the same panel above the box. "Add" first (files or images, Plan mode where the session has modes), then a few of
// each resource; picking a file types `@path `, a session becomes a context chip. One round button on the right: Stop
// while the agent works and nothing is typed, Send otherwise (Queue message while it works, with a second Stop).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { loadPages } from "../../lib/lazy-page";
import { getContexts } from "../../lib/links";
import { applyCompleteSessions } from "../../lib/store";
import type { Session } from "../../lib/types";
import { Composer } from "./Composer";
import type { MentionSource } from "./ComposerPanel";
import type { PlanMode } from "./PermissionPicker";
import { t } from "../../i18n";

const api = vi.fn(async (path: string, _init?: unknown): Promise<unknown> => {
  if (path.startsWith("/machines/m1/fs")) return { path: "/code/app", entries: [{ name: "src", dir: true }, { name: "README.md", dir: false }] };
  throw new Error(`unexpected ${path}`);
});
vi.mock("../../lib/api", async (orig) => ({ ...(await orig<typeof import("../../lib/api")>()), api: (path: string, init?: unknown) => api(path, init) }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const KEY = "m1/s1";
const source: MentionSource = { cwd: "/code/app", self: KEY, files: () => ["/code/app/web/Composer.tsx", "/code/app/lib/links.ts"] };
let host: HTMLDivElement;
let root: Root;

beforeAll(async () => {
  await loadPages();
  act(() => applyCompleteSessions([{ machine_id: "m2", session_id: "other", title: "Fix the login page", agent: "claude", cwd: "/code/site", updated_at: "2026-11-12T10:00:00Z" } as Session]));
});

beforeEach(() => {
  // a precise pointer: the search box takes the focus as the panel opens
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("fine"), media: q, addEventListener() {}, removeEventListener() {} }));
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  Element.prototype.scrollIntoView ??= () => {};
  api.mockClear();
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  getContexts(KEY).length = 0;
});

const render = (props: Partial<React.ComponentProps<typeof Composer>> = {}) =>
  act(() => root.render(<Composer draftKey={KEY} placeholder="" machineId="m1" mention={source} onSend={async () => true} {...props} />));
const box = () => host.querySelector("textarea")!;
const plus = () => host.querySelector<HTMLButtonElement>("button[data-composer-plus]")!;
const panel = () => host.querySelector<HTMLElement>("[data-composer-panel]");
const search = () => host.querySelector<HTMLInputElement>("[data-composer-panel-search]");
const options = () => [...host.querySelectorAll<HTMLElement>('[data-composer-panel] [role="option"]')];
const option = (text: string) => options().find((o) => o.textContent?.includes(text))!;
const settle = () => act(async () => void vi.advanceTimersByTime(200)); // the folder's listing, read after 120 ms
const setValue = (el: HTMLInputElement | HTMLTextAreaElement, text: string) =>
  act(() => {
    const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, text);
    if (el instanceof HTMLTextAreaElement) el.setSelectionRange(text.length, text.length);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
const press = (el: Element, key: string) => act(() => void el.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })));
const click = (el: Element) => act(() => el.dispatchEvent(new MouseEvent("click", { bubbles: true })));

describe("the + (#5324)", () => {
  it("is a bare round button that says what it does, with no @ or / button beside it", () => {
    render();
    expect(plus().getAttribute("aria-label")).toBe(t("chat-composer.add_more"));
    expect(plus().title).toBe(t("chat-composer.more_actions"));
    expect(plus().className).toContain("rounded-full");
    expect(plus().className).toContain("border-transparent");
    const labels = [...host.querySelectorAll("button")].map((b) => b.textContent);
    expect(labels).not.toContain("/");
    expect(host.querySelectorAll("button").length).toBe(2); // the + and Send
  });

  it("opens the panel over the box with its search box focused: Add, then a few of each resource", async () => {
    const plan: PlanMode = { on: false, toggle: vi.fn() };
    render({ planMode: plan });
    click(plus());
    await settle();
    expect(panel()?.dataset.composerPanel).toBe("plus");
    expect(plus().getAttribute("aria-expanded")).toBe("true");
    expect(document.activeElement).toBe(search());
    expect(panel()!.textContent).toContain(t("chat-composerpanel.add"));
    const texts = options().map((o) => o.textContent);
    expect(texts[0]).toBe(t("chat-composerpanel.add_files"));
    expect(texts[1]).toBe(t("lib-session-actions.mode_plan"));
    expect(texts.slice(2).join("|")).toContain("Composer.tsx");
    expect(texts.join("|")).toContain("src/");
    expect(texts.join("|")).toContain("Fix the login page");
    // cindy's 44 px resource rows
    expect(option("Composer.tsx").className).toContain("h-[44px]");
  });

  it("filters as the search is typed, and Backspace in an empty search or Esc closes it, the caret back in the box", async () => {
    render();
    click(plus());
    await settle();
    setValue(search()!, "links");
    expect(options().map((o) => o.textContent?.replace(/lib$/, ""))).toEqual(["links.ts"]);
    setValue(search()!, "");
    press(search()!, "Backspace");
    expect(panel()).toBeNull();
    expect(document.activeElement).toBe(box());
    click(plus());
    press(search()!, "Escape");
    expect(panel()).toBeNull();
    expect(document.activeElement).toBe(box());
  });

  it("types a picked file's `@path ` at the caret", async () => {
    render();
    setValue(box(), "look at");
    click(plus());
    await settle();
    setValue(search()!, "Composer");
    press(search()!, "Enter");
    expect(panel()).toBeNull();
    expect(box().value).toBe("look at @web/Composer.tsx ");
  });

  it("browses a folder in its search box, and adds a session as a context chip", async () => {
    render();
    click(plus());
    await settle();
    click(option("src/"));
    expect(search()!.value).toBe("src/");
    click(plus()); // the + toggles it shut
    expect(panel()).toBeNull();
    click(plus());
    await settle();
    click(option("Fix the login page"));
    expect(panel()).toBeNull();
    expect(box().value).toBe("");
    expect(getContexts(KEY).map((c) => c.title)).toEqual(["Fix the login page"]);
  });

  it("checks Plan mode while the session plans, and its row switches it", async () => {
    const plan: PlanMode = { on: true, toggle: vi.fn() };
    render({ planMode: plan });
    click(plus());
    const row = option(t("lib-session-actions.mode_plan"));
    expect(row.getAttribute("aria-checked")).toBe("true");
    expect(row.querySelectorAll("svg")).toHaveLength(2); // its symbol and the check
    click(row);
    expect(plan.toggle).toHaveBeenCalledOnce();
    expect(panel()).toBeNull();
  });

  it("opens the file picker from its Add row, images only where there is no computer", async () => {
    const picker = vi.spyOn(HTMLInputElement.prototype, "click").mockImplementation(() => {});
    render({ machineId: undefined });
    click(plus());
    click(option(t("chat-composerpanel.add_images")));
    expect(picker).toHaveBeenCalledOnce();
    picker.mockRestore();
  });
});

describe("a typed @ (#5324)", () => {
  it("opens the same panel above the box, without a search box, and the keys stay in the box", async () => {
    const plan: PlanMode = { on: false, toggle: vi.fn() };
    render({ planMode: plan });
    act(() => box().focus());
    setValue(box(), "@");
    await settle();
    expect(panel()?.dataset.composerPanel).toBe("at");
    expect(search()).toBeNull();
    expect(options()[0].textContent).toBe(t("chat-composerpanel.add_files"));
    expect(panel()!.textContent).toContain(t("chat-composerpanel.search")); // the hint under the rows
    // Plan mode from the keyboard: the @ goes, the row runs
    press(box(), "ArrowDown");
    press(box(), "Enter");
    expect(plan.toggle).toHaveBeenCalledOnce();
    expect(box().value).toBe("");
  });

  it("filters by the text after @ and picks a file in its place", async () => {
    render();
    act(() => box().focus());
    setValue(box(), "see @links");
    await settle();
    expect(options().map((o) => o.textContent?.replace(/lib$/, ""))).toEqual(["links.ts"]);
    press(box(), "Enter");
    expect(box().value).toBe("see @lib/links.ts ");
    expect(panel()).toBeNull();
  });
});

describe("the round button (#5324)", () => {
  const send = () => host.querySelector<HTMLButtonElement>('[data-testid="composer-send"]');
  const stops = () => host.querySelectorAll<HTMLButtonElement>('[data-testid="composer-stop"]');

  it("is Send when something is typed and the agent is idle", () => {
    localStorage.setItem(`draft:${KEY}`, "hello");
    render({ onStop: () => {} });
    expect(send()!.getAttribute("aria-label")).toBe(t("chat-composer.send"));
    expect(send()!.className).toContain("rounded-full");
    expect(stops()).toHaveLength(0);
  });

  it("is Stop while the agent works and nothing is typed: the same round shell, not red", () => {
    const onStop = vi.fn();
    render({ working: true, onStop });
    expect(send()).toBeNull();
    expect(stops()).toHaveLength(1);
    expect(stops()[0].getAttribute("aria-label")).toBe(t("chat-composer.interrupt"));
    expect(stops()[0].className).toContain("bg-ink");
    expect(stops()[0].className).not.toContain("danger");
    click(stops()[0]);
    expect(onStop).toHaveBeenCalledOnce();
  });

  it("queues what is typed while the agent works, a second Stop left of voice", () => {
    localStorage.setItem(`draft:${KEY}`, "and also this");
    render({ working: true, onStop: () => {}, voice: { mode: "start", label: "voice", onClick: () => {} } });
    expect(send()!.getAttribute("aria-label")).toBe(t("chat-composer.queue"));
    expect(send()!.title).toBe(t("chat-composer.queue_hint"));
    expect(stops()).toHaveLength(1);
    const row = send()!.parentElement!;
    const order = [...row.children].map((c) => c.getAttribute("data-testid")).filter(Boolean);
    expect(order).toEqual(["composer-stop", "composer-voice", "composer-send"]);
  });
});
