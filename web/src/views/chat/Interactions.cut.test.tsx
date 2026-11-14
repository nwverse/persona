// @vitest-environment jsdom
// A waiting card taller than the room left for it in the dock keeps its answers in view at the box's bottom edge, while
// what they answer is out of view above them (audit item 10): the answer row then says what it answers (the card's title
// and its command), a click on that brings the whole card into view, and the stack's first card says how many it holds.
// The browser check of the real layout is web/e2e/pending-cards-cut.mjs.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// The observer of each card's end mark, driven by the test: `cutOff(el)` says it is below the box, `inView(el)` in it.
const io = vi.hoisted(() => ({ watchers: [] as { el: Element; call: (entries: unknown[]) => void }[] }));
vi.hoisted(() => {
  (globalThis as { IntersectionObserver?: unknown }).IntersectionObserver = class {
    private call: (entries: unknown[]) => void;
    constructor(call: (entries: unknown[]) => void) { this.call = call; }
    observe(el: Element) { io.watchers.push({ el, call: this.call }); }
    unobserve() {}
    disconnect() { io.watchers = io.watchers.filter((w) => w.call !== this.call); }
    takeRecords() { return []; }
  };
});

import { PendingCards } from "./Interactions";
import { cutBelow } from "./card-cut";
import { getState, reset, upsertSession } from "../../lib/store";
import { t } from "../../i18n";
import type { Interaction, Session } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const question = { id: "q_1", machine_id: "m", session_id: "s", kind: "question", questions: [{ question: "Which test framework?", options: [{ label: "Swift Testing" }, { label: "XCTest" }] }] } as Interaction;
const permission = { id: "p_1", machine_id: "m", session_id: "s", kind: "permission", tool_name: "Bash", tool_input: { command: "swift test" } } as Interaction;

let host: HTMLDivElement;
let root: Root;
const card = (id: string) => host.querySelector(`[data-card="${id}"]`)!;
const watcher = (id: string) => io.watchers.find((w) => card(id).contains(w.el))!;
const say = (id: string, below: boolean) => act(() => watcher(id).call([{ isIntersecting: !below, boundingClientRect: { top: below ? 900 : 300 }, rootBounds: { bottom: 700 } }]));
const draw = (cards: Interaction[], narrow = false) => act(() => root.render(<PendingCards interactions={cards} answered={[]} narrow={narrow} keyboard={false} onDismissKeyboard={() => {}} />));

beforeEach(() => {
  io.watchers = [];
  upsertSession({ machine_id: "m", session_id: "s", title: "Demo", state: "needs_you" } as Session);
  Object.assign(getState(), { interactions: [question, permission], answered: {} });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
});

describe("a waiting card cut off below the dock's box", () => {
  it("is told apart from one in view or above it", () => {
    expect(cutBelow(false, { top: 720 }, 700)).toBe(true);
    expect(cutBelow(true, { top: 680 }, 700)).toBe(false);
    expect(cutBelow(false, { top: 40 }, 700)).toBe(false); // scrolled up past it: its row is not at the bottom edge
  });

  it("says in its answer row what the row answers, its title and command, and nothing while it is in view", () => {
    draw([question, permission]);
    expect(host.querySelector("[data-card-answers]")).toBeNull();
    say("p_1", true);
    const line = card("p_1").querySelector<HTMLButtonElement>("[data-card-answers]")!;
    expect(line.closest("[data-card-actions]")).not.toBeNull();
    expect(line.textContent).toBe(`${t("chat-interactions.permission_title", { tool: "Bash" })} · swift test`);
    // beside the answers it is about, which stay as they were
    const answers = [...card("p_1").querySelectorAll("[data-card-actions] button")].map((b) => b.textContent);
    expect(answers).toEqual(expect.arrayContaining([t("chat-interactions.deny"), t("chat-interactions.allow")]));
    say("p_1", false);
    expect(host.querySelector("[data-card-answers]")).toBeNull();
  });

  it("brings the whole card into view on a click on that line, with the focus on the card", () => {
    draw([question, permission]);
    say("p_1", true);
    const scrolled = vi.fn();
    const whole = card("p_1").querySelector<HTMLElement>("[data-pending-card]")!;
    whole.scrollIntoView = scrolled;
    act(() => card("p_1").querySelector<HTMLButtonElement>("[data-card-answers]")!.click());
    expect(scrolled).toHaveBeenCalledWith({ block: "nearest" });
    expect(document.activeElement).toBe(whole);
  });

  it("has the stack's first card say how many the stack holds while any is cut off, and only then", () => {
    draw([question, permission]);
    expect(host.querySelector("[data-chat-cards-count]")).toBeNull();
    say("p_1", true);
    const count = host.querySelector("[data-chat-cards-count]")!;
    expect(count.textContent).toBe(t("chat-interactions.pending", { count: 2 }));
    expect(card("q_1").contains(count)).toBe(true);
    say("p_1", false);
    expect(host.querySelector("[data-chat-cards-count]")).toBeNull();
    // a lone card has no stack to count
    draw([permission]);
    say("p_1", true);
    expect(host.querySelector("[data-chat-cards-count]")).toBeNull();
    expect(host.querySelector("[data-card-answers]")).not.toBeNull();
  });
});
