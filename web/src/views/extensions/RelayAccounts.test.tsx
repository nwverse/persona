// @vitest-environment jsdom
// quota-relay's sidebar page "Relay accounts" (nwverse/persona#4576) drawn by the real ExtensionUI from the views the
// extension exports (testdata/relay-accounts-view.json, made-up accounts; the extension repo's
// the extension's TestExportFixture writes it). The hub is faked: drawing answers the default
// view, or an account's page when the state names one; the type, the status and the search answer what the extension
// would. Checked: the search box and the status filter once at the top, the type segmented and the counts in the first
// section, a section per group with its rows, a row pushing the account's page (and back), no launcher anywhere.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type { UISection, UIView } from "../../lib/extension-ui";
import { loadPages } from "../../lib/lazy-page";
import fixture from "./testdata/relay-accounts-view.json";

type Sent = { action?: string; state?: string; values?: Record<string, string> };
const hub = vi.hoisted(() => ({ sent: [] as Sent[], answer: null as null | ((s: Sent) => unknown) }));

vi.mock("../../lib/extension-ui", async (original) => {
  const real = await original<typeof import("../../lib/extension-ui")>();
  return {
    ...real,
    renderUI: async (_ext: string, _ui: string, _ctx: unknown, state?: string) => {
      hub.sent.push({ state });
      return real.readView(hub.answer!({ state }));
    },
    actUI: async (_ext: string, _ui: string, action: string, opts: { state?: string; values?: Record<string, string> } = {}) => {
      const s = { action, state: opts.state, values: opts.values };
      hub.sent.push(s);
      return { view: real.readView(hub.answer!(s)) };
    },
  };
});
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
import { ExtensionUI } from "../settings/ExtensionView";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  g.matchMedia = (q: string) => ({ matches: q.includes("min-width"), addEventListener() {}, removeEventListener() {} });
});

const views = fixture as unknown as { default: UIView; kiro: UIView; detail: UIView };

/** The default view narrowed to the rows (and the progress under each) whose title has `q`, as the extension's search does. */
function searched(q: string): UIView {
  const sections: UISection[] = views.default.sections.flatMap((s, i) => {
    if (i === 0 || !s.title) return [s];
    const items = s.items.filter((it, j) => {
      const row = it.type === "row" ? it : s.items[j - 1];
      return row?.type === "row" && (row.title ?? "").toLowerCase().includes(q.toLowerCase());
    });
    return items.length ? [{ ...s, items }] : [];
  });
  return { ...views.default, search: { ...views.default.search!, value: q }, sections };
}

function answer(s: Sent): unknown {
  if (s.state?.includes('"a":')) return views.detail;
  if (s.action === "type" && s.values?.type === "kiro") return views.kiro;
  if (s.action === "search" && s.values?.q) return searched(s.values.q);
  return views.default;
}

let host: HTMLDivElement;
let root: Root;
const settle = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
// An account's page opens with a header item, which the code views chunk draws (ViewBody.tsx). Loaded here, the page is
// drawn as soon as its view arrives; loaded on the push, the chunk could take longer than `settle` on a busy machine (#2874).
beforeAll(() => loadPages(), 60_000);
beforeEach(() => {
  hub.sent = [];
  hub.answer = answer;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

async function open() {
  // as the sidebar's nav page mounts it (ExtensionSlots.tsx ExtensionNavPage): the page's header has the title
  act(() => root.render(<ExtensionUI extension="quota-relay" ui="sidebar" title="中转账号" bare history={false} />));
  await settle();
  return host;
}
const texts = (el: Element, sel: string) => [...el.querySelectorAll(sel)].map((e) => e.textContent ?? "");
const sectionTitled = (el: Element, title: string) => [...el.querySelectorAll("section")].find((s) => s.querySelector("h3")?.textContent === title);
const rowTitles = (el: Element) => [...el.querySelectorAll("section button")].map((b) => b.querySelector(".truncate")?.textContent ?? "").filter(Boolean);
const click = async (el: Element | undefined | null) => {
  if (!el) throw new Error("not found");
  act(() => (el as HTMLElement).click());
  await settle();
};

describe("中转账号 (quota-relay's sidebar page)", () => {
  it("draws the default view: one search box and one status filter at the top, then every group", async () => {
    const page = await open();
    // the search box and the filter once, above every section, not inside one
    expect(page.querySelectorAll('input[type="search"]')).toHaveLength(1);
    expect(page.querySelectorAll("select")).toHaveLength(1);
    expect(page.querySelector('input[type="search"]')?.closest("section")).toBeNull();
    expect(page.querySelector("select")?.closest("section")).toBeNull();
    const filter = page.querySelector("select")!;
    expect(filter.value).toBe("all");
    expect(texts(filter, "option")).toEqual(views.default.filter!.choices.map((c) => c.title));
    // the type segmented in the first section, "All" chosen, every count in its title
    const first = page.querySelector("section")!;
    const types = [...first.querySelectorAll('[role="radio"]')];
    expect(types.map((b) => b.textContent)).toEqual(["全部 73", "Claude 4", "Kiro 61", "其他 8"]);
    expect(types[0].getAttribute("aria-checked")).toBe("true");
    for (const b of types) expect(b.className).toContain("whitespace-nowrap");
    // the counts and the caption
    expect(first.textContent).toContain("额度低或用尽");
    expect(first.textContent).toContain("网关账号 71 · 经上游 3 个席位");
    // a section per group, titled with its count, every account a row
    const titles = texts(page, "section > h3");
    expect(titles).toEqual(views.default.sections.filter((s) => s.title).map((s) => s.title));
    const kiro = sectionTitled(page, "Kiro · Free · 60")!;
    expect(kiro.querySelectorAll("button")).toHaveLength(60);
    expect(kiro.querySelectorAll('[role="progressbar"]').length).toBeGreaterThan(0);
    // each account's row starts with a hairline (but the first): an account's quota bar does not run into the next one
    const lined = [...kiro.querySelectorAll("button")].map((b) => b.parentElement!.classList.contains("border-t"));
    expect(lined[0]).toBe(false);
    expect(lined.slice(1).every(Boolean)).toBe(true);
    // a quota bar stays with the row above it
    expect([...kiro.querySelectorAll('[role="progressbar"]')].some((p) => p.closest(".border-t"))).toBe(false);
    expect(sectionTitled(page, "Claude · 经上游 · 3")!.textContent).toContain("经 relay-a");
    // no launcher, no key on the page
    expect(page.textContent).not.toMatch(/启动器|Key/);
  });

  it("draws Kiro alone when the type says so", async () => {
    const page = await open();
    const kiro = [...page.querySelectorAll('[role="radio"]')].find((b) => b.textContent === "Kiro 61");
    await click(kiro);
    const sent = hub.sent.at(-1)!;
    expect(sent.action).toBe("type");
    // a segmented sends its own value; the status and the search are in the state the view carries back
    expect(sent.values).toEqual({ type: "kiro" });
    expect(sent.state).toBe(views.default.state);
    expect(texts(page, "section > h3")).toEqual(["Kiro · Free · 60", "Kiro · 1"]);
    expect(page.querySelector('[role="radio"][aria-checked="true"]')?.textContent).toBe("Kiro 61");
    expect(page.querySelectorAll("select")).toHaveLength(1);
  });

  it("asks for what is typed in the search box, with the status filter's choice, and keeps the box as typed", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const page = await open();
    const box = page.querySelector<HTMLInputElement>('input[type="search"]')!;
    expect(box.placeholder).toBe("搜索账号");
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(box, "kiro-4");
      box.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(hub.sent.some((s) => s.action === "search")).toBe(false); // not on every key
    await act(async () => { vi.advanceTimersByTime(350); });
    await settle();
    const sent = hub.sent.at(-1)!;
    expect(sent).toMatchObject({ action: "search", values: { q: "kiro-4", status: "all" } });
    expect(page.querySelector<HTMLInputElement>('input[type="search"]')!.value).toBe("kiro-4");
    const rows = rowTitles(page).filter((t) => t.startsWith("kiro-"));
    expect(rows.length).toBeGreaterThan(0);
    for (const t of rows) expect(t).toContain("kiro-4");
  });

  it("sends the status filter's choice with the search typed", async () => {
    const page = await open();
    const filter = page.querySelector("select")!;
    act(() => {
      filter.value = "cooldown";
      filter.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await settle();
    expect(hub.sent.at(-1)).toMatchObject({ action: "status", values: { status: "cooldown", q: "" } });
  });

  it("pushes an account's page from its row, and comes back", async () => {
    const page = await open();
    const row = [...page.querySelectorAll("section button")].find((b) => b.textContent?.includes("kiro-48"));
    await click(row);
    // a push draws the page with the state it carries; no ui.action is sent
    const sent = hub.sent.at(-1)!;
    expect(sent.action).toBeUndefined();
    expect(sent.state).toBe(views.detail.state);
    // the page's header has its title; the account's name is said once, not again above it
    expect(texts(page, "h1, h2, h3, h4").filter((t) => t === "kiro-48")).toHaveLength(1);
    expect(page.textContent).toContain("Kiro · Free");
    expect(page.textContent).toContain("冷却中");
    expect(texts(page, "section > h3")).toEqual(["额度", "详情"]);
    expect(page.querySelectorAll('[role="progressbar"]')).toHaveLength(1);
    expect(page.querySelector('input[type="search"]')).toBeNull();
    expect(page.textContent).not.toMatch(/启动器|Key/);
    // back to the list
    const back = [...page.querySelectorAll("button")].find((b) => b.querySelector(".truncate")?.textContent === "中转账号");
    await click(back);
    expect(page.querySelector("h2")).toBeNull();
    expect(sectionTitled(page, "Kiro · Free · 60")).toBeTruthy();
  });
});
