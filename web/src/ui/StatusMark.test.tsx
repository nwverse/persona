import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { STATE_LABEL, stateName } from "../lib/state-labels";
import { foldedCounts } from "../lib/folded-counts";
import type { AgentState, Session } from "../lib/types";
import { markOf, StateTally, StatusMark, type MarkShape } from "./StatusMark";

const ALL = Object.keys(STATE_LABEL) as AgentState[];

describe("state → mark", () => {
  it("draws each state with the shape and color it is known by", () => {
    const want: Record<AgentState, [MarkShape, string]> = {
      working: ["arc", "var(--status-working)"],
      needs_you: ["alert", "var(--status-needs)"],
      unread: ["glow", "var(--status-unread)"],
      idle: ["dot", "var(--status-idle)"],
      unknown: ["dot", "var(--status-idle)"],
      offline: ["ring", "var(--status-offline)"],
    };
    for (const st of ALL) expect([markOf(st).shape, markOf(st).color]).toEqual(want[st]);
  });

  it("keeps the states apart by shape, not only by color", () => {
    const shapes = (["working", "needs_you", "unread", "idle", "offline"] as const).map((s) => markOf(s).shape);
    expect(new Set(shapes).size).toBe(shapes.length);
  });

  it("draws only the working mark as a turning arc, with Moshi's dash (28 of 100)", () => {
    const arc = renderToStaticMarkup(<StatusMark state="working" />);
    expect(arc).toContain("<svg");
    expect(arc).toContain('stroke-dasharray="28 72"');
    expect(arc).toContain('pathLength="100"');
    for (const st of ALL.filter((s) => s !== "working")) expect(renderToStaticMarkup(<StatusMark state={st} />)).not.toContain("<svg");
  });

  it("says the state to a screen reader, or stays out of the way when a label sits beside it", () => {
    for (const st of ALL) expect(renderToStaticMarkup(<StatusMark state={st} />)).toContain(`role="img" aria-label="${STATE_LABEL[st]}"`);
    const quiet = renderToStaticMarkup(<StatusMark state="working" label={false} />);
    expect(quiet).toContain('aria-hidden="true"');
    expect(quiet).not.toContain("aria-label");
  });

  it("takes its size from tokens: sm for dense rows, md otherwise", () => {
    expect(renderToStaticMarkup(<StatusMark state="idle" size="sm" />)).toContain('data-size="sm"');
    expect(renderToStaticMarkup(<StatusMark state="idle" />)).toContain('data-size="md"');
    expect(renderToStaticMarkup(<StatusMark state="idle" />)).not.toMatch(/width:|height:/);
  });
});

describe("a finished turn nobody has looked at (#9751, #14447)", () => {
  it("draws Just finished and Finished, not opened as the plain dot in the unread tint, each under its own word", () => {
    const unopened = renderToStaticMarkup(<StatusMark state="idle" unopened />);
    expect(unopened).toContain('data-shape="dot"');
    expect(unopened).toContain('data-state="unopened"');
    expect(unopened).toContain("--mark-color:var(--status-unread)");
    expect(unopened).toContain(`aria-label="${stateName("unopened")}"`);
    // Just finished's word wins while it lasts, Awaiting Reply's over both
    const finished = renderToStaticMarkup(<StatusMark state="idle" finished unopened />);
    expect(finished).toContain('data-state="just_finished"');
    expect(finished).toContain(`aria-label="${stateName("just_finished")}"`);
    expect(renderToStaticMarkup(<StatusMark state="idle" awaiting unopened />)).toContain('data-state="awaits_reply"');
  });

  it("leaves every other state as it is", () => {
    expect(renderToStaticMarkup(<StatusMark state="unread" unopened />)).toContain('data-shape="glow"');
    expect(renderToStaticMarkup(<StatusMark state="offline" unopened />)).toContain('data-shape="ring"');
    expect(renderToStaticMarkup(<StatusMark state="working" unopened />)).toContain("<svg");
    expect(renderToStaticMarkup(<StatusMark state="idle" />)).toContain("--mark-color:var(--status-idle)");
  });
});

const sess = (state: AgentState, i: number): Session => ({ machine_id: "m", session_id: `s${i}`, state }) as Session;

describe("folded counts", () => {
  it("counts what wants a look and leaves idle and offline out", () => {
    const list = [sess("needs_you", 1), sess("working", 2), sess("working", 3), sess("unread", 4), sess("idle", 5), sess("offline", 6), sess("unknown", 7)];
    expect(foldedCounts(list)).toEqual({ needs_you: 1, working: 2, bg_waiting: 0, unread: 1 });
    expect(foldedCounts([])).toEqual({ needs_you: 0, working: 0, bg_waiting: 0, unread: 0 });
  });

  it("counts waiting in the background apart from working, with the still mark (#6045)", () => {
    const list = [sess("working", 1), { ...sess("working", 2), background_waiting: true }, { ...sess("idle", 3), background_waiting: true }];
    const counts = foldedCounts(list);
    expect(counts).toEqual({ needs_you: 0, working: 1, bg_waiting: 1, unread: 0 });
    const html = renderToStaticMarkup(<StateTally counts={counts} />);
    expect([...html.matchAll(/<span aria-hidden="true" data-state="(\w+)">/g)].map((m) => m[1])).toEqual(["working", "bg_waiting"]);
    expect(html).toContain('data-shape="wait"');
    expect(html).toContain('d="M12 7v5l3 2"'); // the clock, as the chat's running bar has it
    const mark = renderToStaticMarkup(<StatusMark state="working" waiting />);
    expect(mark).toContain('aria-label="后台等待中"');
    expect(mark).not.toContain("busy");
    expect(renderToStaticMarkup(<StatusMark state="idle" waiting />)).toContain('data-shape="dot"'); // only a working state waits
  });

  it("shows needs you first, then working, then done, a monospace count beside each mark", () => {
    const html = renderToStaticMarkup(<StateTally counts={foldedCounts([sess("unread", 1), sess("working", 2), sess("needs_you", 3), sess("needs_you", 4)])} />);
    const order = [...html.matchAll(/<span aria-hidden="true" data-state="(\w+)">/g)].map((m) => m[1]);
    expect(order).toEqual(["needs_you", "working", "unread"]);
    expect(html).toMatch(/data-state="needs_you"[^>]*>.*?<\/span>2</);
    expect(html).toContain('class="state-tally"');
  });

  it("renders nothing for a group with nothing to tally", () => {
    expect(renderToStaticMarkup(<StateTally counts={foldedCounts([sess("idle", 1)])} />)).toBe("");
  });
});
