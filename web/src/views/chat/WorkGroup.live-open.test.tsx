// @vitest-environment jsdom
// #1614: in a Persona conversation the running exchange's work line is open, each tool call its own row as it comes;
// once the exchange ends it folds back to its summary, and a tap opens it again with every call. A fold the reader
// made during the run holds for the run and is dropped when it ends. A coding agent's chat keeps its folded default.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { emptyTaskIndex } from "../../lib/tasks";
import type { Item } from "../../lib/types";
import type { Segment, Step } from "../../lib/turns";
import type { WorkLineStyle } from "../../lib/work-groups";
import { ChatContext, type ChatContextValue } from "./chat-context";
import { forgetExpanded } from "./expanded";
import { SegmentView } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-04T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
const tool = (n: number, done = true): Step => ({
  kind: "tool",
  id: `t${n}#0`,
  use: { id: `t${n}#0`, seq: n, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: `c-${n}`, tool_input: { command: `echo ${n}` }, ts: at(n) },
  result: done ? { id: `t${n}-r#0`, seq: n, role: "user", kind: "tool_result", tool_use_id: `c-${n}`, text: "ok", ts: at(n + 1) } : undefined,
  images: [],
});
const words = (n: number, text: string): Step => {
  const item = { id: `a${n}#0`, seq: n, role: "assistant", kind: "text", text, ts: at(n) } as Item;
  return { kind: "text", id: item.id, item };
};
const seg = (steps: Step[]): Segment => ({ id: "seg", steps, tools: steps.length, thoughts: 0, replies: 0, folds: [], start: at(0) });
const expandFold = async () => {};

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  forgetExpanded();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

function draw(style: WorkLineStyle, s: Segment, live: boolean) {
  const ctx: ChatContextValue = { tasks: emptyTaskIndex(), subagents: [], scope: "x:", lineStyle: style };
  act(() =>
    root.render(
      <ChatContext.Provider value={ctx}>
        <SegmentView seg={s} live={live} mid="m" sid="s" expandFold={expandFold} />
      </ChatContext.Provider>,
    ),
  );
}

const header = () => host.querySelector<HTMLButtonElement>("[data-work-group] > button")!;
const detail = () => host.querySelector("[data-work-detail]");
const detailText = () => detail()?.textContent ?? "";
const tap = () => act(() => header().click());

// seven calls (more than the five a live group peeks), the last still running
const running = [1, 2, 3, 4, 5, 6].map((n) => tool(n)).concat(tool(7, false));
const finished = [1, 2, 3, 4, 5, 6, 7].map((n) => tool(n)).concat(words(9, "Done."));

describe("assistant work line (#1614)", () => {
  it("is open while the exchange runs, every call its own row, the running one too", () => {
    draw("assistant", seg(running), true);
    expect(header().getAttribute("aria-expanded")).toBe("true");
    for (let n = 1; n <= 7; n++) expect(detailText()).toContain(`echo ${n}`);
    expect(host.querySelector("[data-live-preview]")).toBeNull();
  });

  it("lists a call as it arrives, and opens even with one call", () => {
    draw("assistant", seg([tool(1, false)]), true);
    expect(header().disabled).toBe(false);
    expect(detailText()).toContain("echo 1");
    draw("assistant", seg([tool(1), tool(2, false)]), true);
    expect(detailText()).toContain("echo 2");
  });

  it("folds to its summary when the exchange ends, and a tap opens it with every call", () => {
    draw("assistant", seg(running), true);
    draw("assistant", seg(finished), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    tap();
    expect(header().getAttribute("aria-expanded")).toBe("true");
    for (let n = 1; n <= 7; n++) expect(detailText()).toContain(`echo ${n}`);
    tap();
    expect(header().getAttribute("aria-expanded")).toBe("false");
  });

  it("a fold during the run holds while it runs, and is dropped when it ends", () => {
    draw("assistant", seg(running), true);
    tap();
    expect(header().getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-live-preview]")).toBeNull(); // just the summary: no steps peek out
    draw("assistant", seg([...running.slice(0, 6), tool(7), tool(8, false)]), true);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    // opened again during the run: that is dropped too, the ended line folds
    tap();
    expect(header().getAttribute("aria-expanded")).toBe("true");
    draw("assistant", seg(finished), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
  });

  it("opening the summary of an exchange with words between its calls lists every call (inner lines open)", () => {
    const steps = [tool(1), tool(2), words(3, "Now the tests."), tool(4), tool(5), words(9, "Done.")];
    draw("assistant", seg(steps), false);
    tap();
    for (const n of [1, 2, 4, 5]) expect(detailText()).toContain(`echo ${n}`);
  });

  it("a reloaded finished exchange starts folded and opens with every call", () => {
    draw("assistant", seg(finished), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    tap();
    for (let n = 1; n <= 7; n++) expect(detailText()).toContain(`echo ${n}`);
  });
});

describe("agents work line (unchanged)", () => {
  it("is folded while running: the newest five calls peek out", () => {
    draw("agents", seg(running), true);
    expect(detail()).toBeNull();
    const peek = host.querySelector("[data-live-preview]")?.textContent ?? "";
    expect(peek).not.toContain("echo 2");
    expect(peek).toContain("echo 7");
  });

  it("a short live line does not open; a finished one stays folded; inner lines stay folded", () => {
    draw("agents", seg([tool(1, false)]), true);
    expect(header().disabled).toBe(true);
    draw("agents", seg(finished), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    const steps = [tool(1), tool(2), words(3, "Now the tests."), tool(4), tool(5), words(9, "Done.")];
    draw("agents", seg(steps), false);
    tap();
    expect(detailText()).not.toContain("echo 1");
  });
});
