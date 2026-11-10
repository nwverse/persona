// @vitest-environment jsdom
// Dragging a to-do on the calendar (#2045, views/TodoCalendar.tsx): a mouse drag on the axis shows where it would land
// and saves due_at there; a click without a move still opens it; Escape puts it back; it is drawn at the new time while
// saving, and back where it was when saving fails; the All day row and a day of the month take it as a whole; a touch
// that moves before it is held is the page's swipe. jsdom has no layout: the column and the block say where they are.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { DuePatch } from "../lib/todo-calendar";
import type { Todo } from "../lib/todos";
import { TodoCalendar, type CalendarSlot } from "./TodoCalendar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const HOUR = 48; // px an hour on the axis
const day = new Date(2026, 10, 12).getTime();
const at = (hour: number, minute = 0, d = 12) => new Date(2026, 10, d, hour, minute).getTime();
const todo = (id: string, extra: Partial<Todo>): Todo => ({ id, title: id, agents: [], ...extra }) as Todo;

let host: HTMLDivElement;
let root: Root;
let onMove: Mock<(id: string, patch: DuePatch) => Promise<unknown>>;
let onOpen: Mock<(id: string) => void>;
let onAdd: Mock<(slot: CalendarSlot) => void>;
/** What document.elementFromPoint answers: the drop target under the pointer. */
let under: Element | null = null;

function draw(todos: Todo[], mode: "day" | "month" = "day") {
  act(() =>
    root.render(
      <TodoCalendar
        todos={todos}
        lookup={() => undefined}
        mode={mode}
        anchor={day}
        showDone={false}
        onMode={() => {}}
        onAnchor={() => {}}
        onShowDone={() => {}}
        onOpen={onOpen}
        onAdd={onAdd}
        onShowList={() => {}}
        onMove={onMove}
      />,
    ),
  );
}

const entry = (id: string) => host.querySelector<HTMLElement>(`[data-testid="todo-calendar-entry"][data-todo-id="${id}"]`)!;
const ghost = () => host.querySelector<HTMLElement>('[data-testid="todo-calendar-drop"]');
const blockTop = (id: string) => parseFloat((entry(id).parentElement as HTMLElement).style.top);
function place(el: Element, top: number) {
  el.getBoundingClientRect = () => ({ top, bottom: top + 2000, left: 0, right: 400, width: 400, height: 2000, x: 0, y: top, toJSON() {} }) as DOMRect;
}
function pointer(target: EventTarget, type: string, clientY: number, extra: PointerEventInit = {}) {
  const e = new PointerEvent(type, { clientX: 100, clientY, pointerId: 1, button: 0, pointerType: "mouse", bubbles: true, cancelable: true, ...extra });
  act(() => void target.dispatchEvent(e));
}
const click = (el: Element) => act(() => void el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })));

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
  vi.setSystemTime(at(9));
  window.matchMedia ??= ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
  document.elementFromPoint = () => under;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  onMove = vi.fn(async () => {});
  onOpen = vi.fn<(id: string) => void>();
  onAdd = vi.fn<(slot: CalendarSlot) => void>();
});

afterEach(() => {
  act(() => void vi.runOnlyPendingTimers()); // the end of a drag stops listening for its click in a timer
  act(() => root.unmount());
  host.remove();
  under = null;
  vi.useRealTimers();
});

/** Picks the 10:00 block up 10 px below its top edge and moves the pointer to clientY (the column starts at 0). */
function dragTo(id: string, clientY: number) {
  const column = host.querySelector('[data-testid="todo-calendar-column"]')!;
  place(column, 0);
  place(entry(id), 10 * HOUR);
  under = entry(id);
  pointer(entry(id), "pointerdown", 10 * HOUR + 10);
  pointer(window, "pointermove", 10 * HOUR + 14); // under the slop: not a drag yet
  expect(ghost()).toBeNull();
  under = column;
  pointer(window, "pointermove", clientY);
}

describe("dragging a to-do on the calendar (#2045)", () => {
  it("on the axis: where it would land, at the quarter hour, saved there when let go", async () => {
    draw([todo("a", { due_at: new Date(at(10)).toISOString() })]);
    dragTo("a", 14 * HOUR + 10 + 7); // its top edge 7 px (under 9 minutes) past 14:00
    expect(ghost()).not.toBeNull();
    expect(parseFloat(ghost()!.style.top)).toBe(14.25 * HOUR + 1);
    expect(host.querySelector('[data-testid="todo-calendar"]')!.hasAttribute("data-dragging")).toBe(true);
    pointer(window, "pointerup", 14 * HOUR + 17);
    click(entry("a")); // the click the drag ends with
    expect(onOpen).not.toHaveBeenCalled();
    expect(onAdd).not.toHaveBeenCalled();
    expect(onMove).toHaveBeenCalledWith("a", { due_at: new Date(at(14, 15)).toISOString() });
    expect(ghost()).toBeNull();
    await act(async () => {});
  });

  it("drawn at the new time while it saves, and back where it was if saving fails", async () => {
    let settle!: () => void;
    onMove.mockImplementation(() => new Promise<void>((r) => (settle = r)));
    draw([todo("a", { due_at: new Date(at(10)).toISOString() })]);
    dragTo("a", 16 * HOUR + 10);
    pointer(window, "pointerup", 16 * HOUR + 10);
    expect(blockTop("a")).toBe(16 * HOUR + 1);
    await act(async () => settle()); // the page said why; the hub still has 10:00
    expect(blockTop("a")).toBe(10 * HOUR + 1);
  });

  it("a click without a move opens it; dropped where it was, nothing is saved", () => {
    draw([todo("a", { due_at: new Date(at(10)).toISOString() })]);
    pointer(entry("a"), "pointerdown", 10 * HOUR + 10);
    pointer(window, "pointerup", 10 * HOUR + 10);
    click(entry("a"));
    expect(onOpen).toHaveBeenCalledWith("a");
    dragTo("a", 10 * HOUR + 10 + 3 * HOUR);
    pointer(window, "pointermove", 10 * HOUR + 10); // back to its own place
    pointer(window, "pointerup", 10 * HOUR + 10);
    expect(onMove).not.toHaveBeenCalled();
  });

  it("Escape puts it back: nothing saved, nothing opened", () => {
    draw([todo("a", { due_at: new Date(at(10)).toISOString() })]);
    dragTo("a", 15 * HOUR);
    act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    expect(ghost()).toBeNull();
    pointer(window, "pointerup", 15 * HOUR);
    click(entry("a"));
    expect(onMove).not.toHaveBeenCalled();
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("the All day row takes it as a whole: due that day", () => {
    draw([todo("a", { due_at: new Date(at(10)).toISOString() })]);
    dragTo("a", 15 * HOUR);
    under = host.querySelector('[data-testid="todo-calendar-all-day"]');
    pointer(window, "pointermove", 20);
    expect(under!.querySelector('[data-testid="todo-calendar-drop"]')).not.toBeNull();
    pointer(window, "pointerup", 20);
    expect(onMove).toHaveBeenCalledWith("a", { due_date: "2026-11-12" });
  });

  it("a day of the month takes it at the same time; an all-day one stays all day", () => {
    draw([todo("a", { due_at: new Date(at(18, 30)).toISOString() }), todo("b", { due_date: "2026-11-12" })], "month");
    const cell = (d: number) => [...host.querySelectorAll<HTMLElement>('[data-testid="todo-calendar-month-day"]')].find((c) => Number(c.dataset.day) === at(0, 0, d))!;
    for (const [id, patch] of [
      ["a", { due_at: new Date(at(18, 30, 19)).toISOString() }],
      ["b", { due_date: "2026-11-19" }],
    ] as const) {
      under = entry(id);
      pointer(entry(id), "pointerdown", 100);
      under = cell(19);
      pointer(window, "pointermove", 160);
      expect(cell(19).querySelector('[data-testid="todo-calendar-drop"]')).not.toBeNull();
      pointer(window, "pointerup", 160);
      expect(onMove).toHaveBeenLastCalledWith(id, patch);
    }
  });

  it("a touch drags once held still; one that moves first is the page's swipe", () => {
    draw([todo("a", { due_at: new Date(at(10)).toISOString() })]);
    const column = host.querySelector('[data-testid="todo-calendar-column"]')!;
    place(column, 0);
    place(entry("a"), 10 * HOUR);
    pointer(entry("a"), "pointerdown", 10 * HOUR + 10, { pointerType: "touch" });
    under = column;
    pointer(window, "pointermove", 12 * HOUR, { pointerType: "touch" });
    act(() => void vi.advanceTimersByTime(1000));
    expect(ghost()).toBeNull();
    pointer(window, "pointerup", 12 * HOUR, { pointerType: "touch" });
    expect(onMove).not.toHaveBeenCalled();

    pointer(entry("a"), "pointerdown", 10 * HOUR + 10, { pointerType: "touch" });
    act(() => void vi.advanceTimersByTime(400)); // held
    pointer(window, "pointermove", 12 * HOUR + 10, { pointerType: "touch" });
    expect(ghost()).not.toBeNull();
    pointer(window, "pointerup", 12 * HOUR + 10, { pointerType: "touch" });
    expect(onMove).toHaveBeenCalledWith("a", { due_at: new Date(at(12)).toISOString() });
  });
});
