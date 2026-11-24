import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Segment, Step } from "../../lib/turns";
import type { Item } from "../../lib/types";
import type { ToolStep } from "../../lib/work-groups";
import { ActionRow } from "./ActionRow";
import { formatTurnDuration, TurnFooter, turnSummaryMetrics } from "./TurnFooter";

const T0 = Date.parse("2026-11-24T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();

function makeSeg(overrides: Partial<Segment> = {}): Segment {
  return {
    id: "seg:1",
    user: { id: "u1#0", text: "hello", images: [], contexts: [], ts: at(0) },
    steps: [],
    tools: 0,
    thoughts: 0,
    replies: 0,
    folds: [],
    start: at(0),
    ...overrides,
  };
}

function makeToolStep(id: string, name: string, input: Record<string, unknown>, ts = 1): ToolStep {
  const use: Item = {
    id: `${id}#0`,
    seq: 1,
    role: "assistant",
    kind: "tool_use",
    tool_name: name,
    tool_use_id: `call-${id}`,
    tool_input: input,
    ts: at(ts),
  };
  const result: Item = {
    id: `${id}-r#0`,
    seq: 2,
    role: "user",
    kind: "tool_result",
    tool_use_id: `call-${id}`,
    text: "ok",
    ts: at(ts + 1),
  };
  return { kind: "tool", id: use.id, use, result, images: [] };
}

describe("formatTurnDuration", () => {
  it("formats sub-minute durations as Xs and >= 60s as Xm Ys", () => {
    expect(formatTurnDuration(1)).toBe("1s");
    expect(formatTurnDuration(59)).toBe("59s");
    expect(formatTurnDuration(60)).toBe("1m 0s");
    expect(formatTurnDuration(75)).toBe("1m 15s");
    expect(formatTurnDuration(125)).toBe("2m 5s");
  });
});

describe("turnSummaryMetrics", () => {
  it("computes duration, tool/thought counts, and joins multi-reply text", () => {
    const steps: Step[] = [
      { kind: "thinking", id: "th1#0", item: { id: "th1#0", seq: 1, role: "assistant", kind: "thinking", text: "先分析问题", ts: at(1) } },
      makeToolStep("t1", "Read", { file_path: "/a.ts" }, 2),
      { kind: "text", id: "txt1#0", item: { id: "txt1#0", seq: 3, role: "assistant", kind: "text", text: "第一步完成了。", ts: at(5) } },
      makeToolStep("t2", "Edit", { file_path: "/a.ts", old_string: "a", new_string: "b" }, 6),
    ];
    const seg = makeSeg({
      steps,
      answer: { id: "ans#0", seq: 5, role: "assistant", kind: "text", text: "全部完成！", ts: at(72) },
      end: at(72),
      tools: 2,
      thoughts: 1,
      replies: 1,
    });

    const m = turnSummaryMetrics(seg);
    expect(m).toEqual({
      durationSec: 72,
      toolCount: 2,
      thoughtCount: 1,
      fullReplyText: "第一步完成了。\n\n全部完成！",
    });
  });

  it("falls back to answer timestamp when seg.end is omitted and preserves folded counts", () => {
    const seg = makeSeg({
      tools: 5,
      thoughts: 2,
      answer: { id: "ans#0", seq: 10, role: "assistant", kind: "text", text: "done", ts: at(14) },
    });

    const m = turnSummaryMetrics(seg);
    expect(m.durationSec).toBe(14);
    expect(m.toolCount).toBe(5);
    expect(m.thoughtCount).toBe(2);
    expect(m.fullReplyText).toBe("done");
  });

  it("returns null duration when timestamps are missing", () => {
    const seg = makeSeg({ user: { id: "u1#0", text: "hi", images: [], contexts: [] }, start: undefined, end: undefined });
    expect(turnSummaryMetrics(seg).durationSec).toBeNull();
  });
});

describe("TurnFooter", () => {
  it("returns null for trivial turns (no tools, no thoughts, duration < 2s)", () => {
    const trivial = makeSeg({
      answer: { id: "a#0", seq: 2, role: "assistant", kind: "text", text: "你好", ts: at(1) },
      end: at(1),
    });
    expect(renderToStaticMarkup(<TurnFooter seg={trivial} />)).toBe("");

    const instant = makeSeg({
      answer: { id: "a#0", seq: 2, role: "assistant", kind: "text", text: "你好", ts: at(0) },
      end: at(0),
    });
    expect(renderToStaticMarkup(<TurnFooter seg={instant} />)).toBe("");
  });

  it("renders duration, tool count, thought count, and copy button for a work turn", () => {
    const seg = makeSeg({
      steps: [
        { kind: "thinking", id: "th#0", item: { id: "th#0", seq: 1, role: "assistant", kind: "thinking", text: "想一下", ts: at(1) } },
        makeToolStep("t1", "Bash", { command: "npm test" }, 2),
      ],
      answer: { id: "a#0", seq: 4, role: "assistant", kind: "text", text: "测试通过。", ts: at(65) },
      end: at(65),
      tools: 1,
      thoughts: 1,
    });

    const html = renderToStaticMarkup(<TurnFooter seg={seg} />);
    expect(html).toContain('data-chat-find-skip="true"');
    expect(html).toContain("data-turn-footer");
    expect(html).toContain("耗时 1m 5s");
    expect(html).toContain("1 次工具");
    expect(html).toContain("1 段推理");
    expect(html).not.toContain("思考");
    expect(html).toContain("复制本轮回复");
  });

  it("omits copy button for single-reply turn with no tools, but shows it for multi-reply turns", () => {
    const slowSingle = makeSeg({
      answer: { id: "a#0", seq: 2, role: "assistant", kind: "text", text: "长回复", ts: at(5) },
      end: at(5),
    });
    const singleHtml = renderToStaticMarkup(<TurnFooter seg={slowSingle} />);
    expect(singleHtml).toContain("耗时 5s");
    expect(singleHtml).not.toContain("复制本轮回复");

    const multiReply = makeSeg({
      steps: [{ kind: "text", id: "t1#0", item: { id: "t1#0", seq: 1, role: "assistant", kind: "text", text: "第一段", ts: at(2) } }],
      answer: { id: "a#0", seq: 2, role: "assistant", kind: "text", text: "第二段", ts: at(4) },
      end: at(4),
      replies: 1,
    });
    const multiHtml = renderToStaticMarkup(<TurnFooter seg={multiReply} />);
    expect(multiHtml).toContain("耗时 4s");
    expect(multiHtml).toContain("复制本轮回复");
  });
});

describe("ActionRow edit rows", () => {
  it("opens edit rows in the changes side panel rather than expanding inline", () => {
    const edit = makeToolStep("e1", "Edit", { file_path: "/src/app.ts", old_string: "const a = 1;", new_string: "const a = 2;" });
    const html = renderToStaticMarkup(<ActionRow step={edit} mid="m" sid="s" />);
    expect(html).not.toContain("aria-expanded=");
    expect(html).not.toContain("data-inline-diff");
  });
});
