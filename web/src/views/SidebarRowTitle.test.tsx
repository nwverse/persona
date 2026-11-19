// An agent row behind a prefix keeps room for its title: the prefix gives way first. Source and terminal details live
// in the structured hover card, including on a tab with several panes (endRoom).
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { Session } from "../lib/types";

// the sidebar's modules read the colour scheme and this browser's settings when they load
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const { SessionRow } = await import("./Sidebar");

// a herdr pane: its source is "Terminal"
const s: Session = { machine_id: "mac", session_id: "s1", title: "整理发布说明", cwd: "/code", status: "idle", state: "idle", pane_id: "p1" };
const props = { s, k: "s:mac/s1", reg: () => () => {}, hover: () => {}, active: false, to: { name: "session", mid: "mac", sid: "s1" } as const, go: () => () => {} };
const titleOf = (html: string) => html.match(/<span class="([^"]*)">整理发布说明<\/span>/)?.[1] ?? "";

describe("an agent row's title behind a prefix", () => {
  it("keeps its named minimum width, and the prefix may shrink", () => {
    const html = renderToStaticMarkup(<SessionRow {...props} prefix="release" prefixClass="min-w-0 shrink truncate" />);
    expect(titleOf(html)).toContain("min-w-(--row-title-min)");
    expect(titleOf(html)).not.toContain("min-w-0");
  });

  it("without a prefix it is what it was", () => {
    const html = renderToStaticMarkup(<SessionRow {...props} />);
    expect(titleOf(html)).toContain("min-w-0");
    expect(html).not.toContain("--row-title-min");
  });

  it("keeps source marks out of both ordinary and multi-pane agent rows", () => {
    expect(renderToStaticMarkup(<SessionRow {...props} prefix="release" />)).not.toContain("data-source=");
    expect(renderToStaticMarkup(<SessionRow {...props} prefix="release" endRoom />)).not.toContain("data-source=");
  });
});
