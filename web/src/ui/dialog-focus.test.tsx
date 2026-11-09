// @vitest-environment jsdom
// Dialog (atoms.tsx) and the keyboard focus: it moves in on open, Tab stays inside, and on close it goes back to the
// opener, unless the close moved it somewhere else on purpose (a footer action that focuses a field outside).
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Dialog } from "./atoms";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let opener: HTMLButtonElement;
let outside: HTMLInputElement;

beforeEach(() => {
  // focusables() keeps only shown controls; jsdom lays nothing out, so every element would count as hidden
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
  opener = document.createElement("button");
  outside = document.createElement("input");
  host = document.createElement("div");
  document.body.append(opener, outside, host);
  root = createRoot(host);
  opener.focus();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  opener.remove();
  outside.remove();
  delete (Element.prototype as { animate?: unknown }).animate;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function Box({ open, onClose = () => {} }: { open: boolean; onClose?: () => void }) {
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="T"
      footer={
        <button
          type="button"
          data-testid="move"
          onClick={() => {
            outside.focus();
            onClose();
          }}
        >
          move
        </button>
      }
    >
      <button type="button">body</button>
    </Dialog>
  );
}

// a body whose first field takes the focus itself (Rename and most other form dialogs)
function Fielded({ open }: { open: boolean }) {
  return (
    <Dialog open={open} onClose={() => {}} title="T">
      <input autoFocus data-testid="field" />
    </Dialog>
  );
}

const panel = () => document.querySelector<HTMLElement>("[role=dialog]")!;
const field = () => document.querySelector<HTMLInputElement>("[data-testid=field]");
function tab(shift = false): boolean {
  const e = new KeyboardEvent("keydown", { key: "Tab", shiftKey: shift, bubbles: true, cancelable: true });
  (document.activeElement ?? document.body).dispatchEvent(e);
  return e.defaultPrevented;
}

describe("Dialog focus", () => {
  it("moves the focus into the dialog and gives it back to the opener on close", () => {
    act(() => root.render(<Box open />));
    expect(document.activeElement).toBe(panel());
    act(() => root.render(<Box open={false} />));
    expect(document.activeElement).toBe(opener);
  });

  it("keeps the focus where a footer action moved it outside before closing", () => {
    let open = true;
    const close = () => {
      open = false;
      root.render(<Box open={open} onClose={close} />);
    };
    act(() => root.render(<Box open={open} onClose={close} />));
    act(() => document.querySelector<HTMLButtonElement>("[data-testid=move]")!.click());
    expect(document.querySelector("[role=dialog]")).toBeNull();
    expect(document.activeElement).toBe(outside);
  });

  it("gives the focus back while its exit plays (still mounted, inert)", () => {
    // an exit that never ends: the panel stays in the DOM, inert, while focus returns
    (Element.prototype as { animate?: unknown }).animate = () => ({ finished: new Promise(() => {}), cancel() {} }) as unknown as Animation;
    act(() => root.render(<Box open />));
    act(() => root.render(<Box open={false} />));
    expect(panel()).not.toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it("gives the focus back to the opener, not to its own autoFocus field", () => {
    // autoFocus runs while React commits, before the dialog's effect: the opener has to be noted before that
    act(() => root.render(<Fielded open={false} />));
    act(() => root.render(<Fielded open />));
    expect(document.activeElement).toBe(field());
    act(() => root.render(<Fielded open={false} />));
    expect(document.activeElement).toBe(opener);
  });

  it("gives the focus back to the opener while an exit plays after an autoFocus field had it", () => {
    (Element.prototype as { animate?: unknown }).animate = () => ({ finished: new Promise(() => {}), cancel() {} }) as unknown as Animation;
    // mounted already open, as a caller that renders {x && <Dialog open />} does
    act(() => root.render(<Fielded open />));
    expect(document.activeElement).toBe(field());
    act(() => root.render(<Fielded open={false} />));
    expect(panel()).not.toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it("keeps an autoFocus field focused through StrictMode's replay of a dialog mounted open", () => {
    // the app renders in StrictMode: on mount the cleanup gives the focus back to the opener and the effect runs again
    act(() => root.render(<StrictMode><Fielded open /></StrictMode>));
    expect(document.activeElement).toBe(field());
    act(() => root.render(<StrictMode>{null}</StrictMode>));
    expect(document.activeElement).toBe(opener);
  });

  it("wraps Tab and Shift+Tab among its controls", () => {
    act(() => root.render(<Box open />));
    const list = [...panel().querySelectorAll("button")];
    const first = list[0];
    const last = list[list.length - 1];
    expect(tab()).toBe(true);
    expect(document.activeElement).toBe(first);
    expect(tab(true)).toBe(true);
    expect(document.activeElement).toBe(last);
    expect(tab()).toBe(true);
    expect(document.activeElement).toBe(first);
  });
});
