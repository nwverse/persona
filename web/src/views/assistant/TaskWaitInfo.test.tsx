// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n } from "../../i18n";
import { reset } from "../../lib/store";
import type { Todo } from "../../lib/todos";
import { TaskWaitInfo } from "./TaskWaitInfo";

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW = new Date("2026-11-17T12:00:00Z").getTime();
const contact = { channel: "Email", url: "https://example.test/thread/7", sender: "Riley", sent_at: "2026-11-15T08:00:00Z", request: "Please confirm the revised date", status: "sent" };
const makeTodo = (evidence?: Record<string, unknown>, more: Record<string, unknown> = {}): Todo => ({
  id: "wait-1", title: "Example rollout", created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-17T11:00:00Z",
  supervision: { supervisor: "conversation-1", state: "parked", parked: { reason: "Waiting on the external team", until: "2026-11-16T12:00:00Z", waiting_on: "someone_else", count: 1, evidence } },
  ...more,
} as Todo);

let host: HTMLDivElement;
let root: Root;
let fetchMock: ReturnType<typeof vi.fn>;
const render = async (todo: Todo) => act(async () => { root.render(<TaskWaitInfo todo={todo} expanded />); });
const text = (selector: string) => host.querySelector<HTMLElement>(selector)?.textContent ?? "";

beforeEach(async () => {
  await initI18n("en");
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove(); reset(); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks();
  await initI18n("zh-Hans");
});

describe("task wait information", () => {
  it("shows the recorded person request, sender, age, wake and source without substituting the recorder", async () => {
    const todo = makeTodo({ kind: "person_reply", target: "Design team", wake: "They confirm a date", contact, recorded_by: "Supervisor", url: "https://example.test/evidence/7" });
    await render(todo);
    const info = host.querySelector<HTMLElement>('[data-task-wait-info="wait-1"]')!;
    expect(info.dataset.waitKind).toBe("person_reply");
    expect(text("[data-wait-target]")).toContain("Design team");
    expect(text("[data-wait-age]")).toContain("2 days");
    expect(text("[data-wait-contact]")).toContain("Riley");
    expect(text("[data-wait-request]")).toContain(contact.request);
    expect(text("[data-wait-wake]")).toContain("They confirm a date");
    expect([...host.querySelectorAll<HTMLAnchorElement>("[data-wait-source]")].map(a => a.href)).toEqual(["https://example.test/evidence/7"]);
    expect(info.textContent).not.toContain("Supervisor");
    expect(info.dataset.waitOverdue).toBe("true");
    expect(host.querySelector("[data-wait-due]")).not.toBeNull();
  });

  it("shows a ready review source, while unknown and unsafe URLs are never linked", async () => {
    const review = makeTodo({ kind: "merge_review", target: "Example PR", wake: "Review is approved", ready: true, url: "https://example.test/review/8", contact: { ...contact, url: "javascript:alert(1)" } });
    await render(review);
    expect(host.querySelector('[data-wait-kind="merge_review"]')).not.toBeNull();
    expect(host.querySelector('[data-wait-contact]')).toBeNull();
    expect(host.querySelector('[data-wait-contact-missing]')).not.toBeNull();
    expect(host.querySelectorAll("a[href^='javascript:']")).toHaveLength(0);
    expect(host.querySelectorAll("a[href='https://example.test/review/8']")).toHaveLength(1);
    expect(text("[data-wait-wake]")).toContain("Review is approved");
    await render(makeTodo({ kind: "date", target: "Release window", wake: "The date arrives", until: "2026-11-18T00:00:00Z", url: "https://example.test/calendar/3", recorded_by: "Supervisor" }));
    expect(host.querySelector('[data-wait-kind="date"]')).not.toBeNull();
    expect(host.querySelector('[data-wait-contact-missing]')).not.toBeNull();
    expect([...host.querySelectorAll<HTMLAnchorElement>("[data-wait-source]")].map(a => a.href)).toEqual(["https://example.test/calendar/3"]);
    await render(makeTodo({ kind: "external_event", target: "Deployment", wake: "Health check passes", url: "javascript:alert(1)" }));
    expect(host.querySelectorAll("[data-wait-source]")).toHaveLength(0);
  });

  it("keeps legacy missing evidence visibly unknown rather than inventing a contact, wake or source", async () => {
    await render(makeTodo(undefined));
    expect(host.querySelector('[data-wait-kind="legacy"]')).not.toBeNull();
    expect(host.querySelector('[data-wait-contact-missing]')).not.toBeNull();
    expect(text("[data-wait-age]")).toContain("Wait start not recorded");
    expect(text("[data-wait-wake]")).toContain("not recorded");
    expect(host.querySelectorAll("[data-wait-source]")).toHaveLength(0);
  });

  it("refreshes elapsed wait age from the current clock, not the task update time", async () => {
    let tick: (() => void) | undefined;
    vi.spyOn(globalThis, "setInterval").mockImplementation((callback) => { tick = callback as () => void; return {} as ReturnType<typeof setInterval>; });
    const todo = makeTodo({ kind: "person_reply", target: "Design team", wake: "They reply", contact });
    await render(todo);
    expect(text("[data-wait-age]")).toContain("2 days");
    vi.setSystemTime(NOW + 21 * 3600_000);
    await act(async () => tick?.());
    expect(text("[data-wait-age]")).toContain("3 days");
  });

  it("edits, copies, and cancels an overdue reminder draft without network or task-state writes", async () => {
    const todo = makeTodo({ kind: "person_reply", target: "Design team", wake: "They reply", contact });
    const clipboard = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: clipboard } });
    const snapshot = JSON.stringify(todo);
    await render(todo);
    expect(host.querySelector("[data-wait-overdue='true']")).not.toBeNull();
    await act(async () => host.querySelector<HTMLButtonElement>("[data-testid='wait-reminder']")!.click());
    const field = document.querySelector<HTMLTextAreaElement>('[role="dialog"] textarea')!;
    expect(field.value).toContain("Please confirm the revised date");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
      setter.call(field, "Edited local draft");
      field.dispatchEvent(new Event("input", { bubbles: true }));
      field.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(document.querySelector('[role="dialog"] [data-reminder-source]')?.getAttribute("href")).toBe(contact.url);
    await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(b => b.textContent === "Copy draft")!.click());
    expect(clipboard).toHaveBeenCalledWith("Edited local draft");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(JSON.stringify(todo)).toBe(snapshot);
    await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(b => b.textContent === "Cancel")!.click());
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(JSON.stringify(todo)).toBe(snapshot);
  });
});
