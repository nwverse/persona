import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { collectSessionHistory, filterHistory, HistorySearchPopover, type HistoryEntry } from "./HistorySearchPopover";

function stubSessionStorage(entries: Record<string, string>) {
  const keys = Object.keys(entries);
  vi.stubGlobal("sessionStorage", {
    get length() {
      return keys.length;
    },
    key(i: number) {
      return keys[i] ?? null;
    },
    getItem(k: string) {
      return Object.prototype.hasOwnProperty.call(entries, k) ? entries[k] : null;
    },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe("filterHistory", () => {
  const sample: HistoryEntry[] = [
    { text: "Fix TypeScript error in Composer.tsx", fromCurrent: true },
    { text: "Run vitest for RunningStatusBar", fromCurrent: true },
    { text: "Deploy web bundle to staging", fromCurrent: false },
  ];

  it("returns all entries when query is empty or whitespace", () => {
    expect(filterHistory(sample, "")).toEqual(sample);
    expect(filterHistory(sample, "   \t  ")).toEqual(sample);
  });

  it("matches case-insensitively with multiple tokens in any order", () => {
    expect(filterHistory(sample, "composer ts")).toEqual([sample[0]]);
    expect(filterHistory(sample, "status vitest")).toEqual([sample[1]]);
    expect(filterHistory(sample, "STAGING web")).toEqual([sample[2]]);
  });

  it("returns empty array when no entry matches all tokens", () => {
    expect(filterHistory(sample, "composer staging")).toEqual([]);
  });
});

describe("collectSessionHistory", () => {
  it("puts current session entries first (newest first) and deduplicates across sessions", () => {
    stubSessionStorage({
      "unrelated-key": JSON.stringify(["ignored"]),
      "chat-history:other": JSON.stringify(["older other", "shared prompt", "newest other"]),
      "chat-history:cur": JSON.stringify(["first cur", "shared prompt", "latest cur"]),
      "chat-history:broken": "{not valid json",
    });

    expect(collectSessionHistory("cur")).toEqual([
      { text: "latest cur", fromCurrent: true },
      { text: "shared prompt", fromCurrent: true },
      { text: "first cur", fromCurrent: true },
      { text: "newest other", fromCurrent: false },
      { text: "older other", fromCurrent: false },
    ]);
  });

  it("deduplicates repeated entries within the same session keeping newest", () => {
    stubSessionStorage({
      "chat-history:cur": JSON.stringify(["dup", "middle", "dup"]),
    });

    expect(collectSessionHistory("cur")).toEqual([
      { text: "dup", fromCurrent: true },
      { text: "middle", fromCurrent: true },
    ]);
  });

  it("returns empty array when sessionStorage is unavailable", () => {
    vi.stubGlobal("sessionStorage", undefined);
    expect(collectSessionHistory("cur")).toEqual([]);
  });
});

describe("HistorySearchPopover", () => {
  it("renders nothing when closed", () => {
    expect(renderToStaticMarkup(<HistorySearchPopover open={false} currentDraftKey="cur" onSelect={() => {}} onClose={() => {}} />)).toBe("");
  });

  it("renders header, entries with 当前会话 tag, and footer when open", () => {
    stubSessionStorage({
      "chat-history:cur": JSON.stringify(["检查单测"]),
      "chat-history:other": JSON.stringify(["构建发布"]),
    });
    const html = renderToStaticMarkup(<HistorySearchPopover open currentDraftKey="cur" onSelect={() => {}} onClose={() => {}} />);
    expect(html).toContain('data-chat-find-skip="true"');
    expect(html).toContain("^R 历史搜索");
    expect(html).toContain("检查单测");
    expect(html).toContain("当前会话");
    expect(html).toContain("构建发布");
    expect(html).toContain("↑↓ 选择 · ⏎ 填入 · Esc 取消");
  });
});
