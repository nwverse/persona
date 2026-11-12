// @vitest-environment jsdom
// A model error's text-derived fix ("Fix it", Persona's model settings) belongs to Persona's own conversations only: an
// agent session's Claude Code error keeps the plain card (and "Retry" when it can help).
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { parseAPIError } from "../../lib/api-errors";
import type { ChatLine } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { ChatLineView } from "./Lines";

function line(text: string): ChatLine {
  const item = { id: "e1", seq: 1, role: "assistant", kind: "text", text } as Item;
  return { kind: "apiError", id: "err-e1", item, info: parseAPIError(text)!, count: 1 };
}
const render = (text: string, assistant: boolean) =>
  renderToStaticMarkup(<ChatLineView line={line(text)} mid="m" sid="s" expandFold={async () => {}} onRetry={() => {}} assistant={assistant} />);

describe("a model error's fix", () => {
  it("offers 去修 in a Persona conversation", () => {
    const html = render("Invalid API key · Fix external API key", true);
    expect(html).toContain("去修");
  });

  it("does not point an agent session at Persona's settings", () => {
    const html = render("Invalid API key · Fix external API key", false);
    expect(html).not.toContain("去修");
  });

  it("keeps 重试 on an agent session's passing error", () => {
    const html = render("API Error: 529 overloaded", false);
    expect(html).toContain("重试");
    expect(html).not.toContain("去修");
  });

  it("recognizes safeguards as requiring action and offers editing only on a supported agent chat", () => {
    const text = "API Error: Opus 5.5's safeguards flagged this message. Details: [frontier_llm]";
    const info = parseAPIError(text)!;
    expect(info.code).toBe("safeguards");
    expect(info.transient).toBe(false);
    const html = renderToStaticMarkup(<ChatLineView line={line(text)} mid="m" sid="s" expandFold={async () => {}} onRetry={() => {}} onEditLastMessage={() => {}} />);
    expect(html).toContain("编辑上一条消息");
    expect(html).toContain("模型的安全检查拦截了这条消息");
    expect(html).not.toContain(">重试</button>");
    expect(render(text, false)).not.toContain("编辑上一条消息");
    const main = renderToStaticMarkup(<ChatLineView line={line(text)} mid="m" sid="s" expandFold={async () => {}} assistant onEditLastMessage={() => {}} />);
    expect(main).not.toContain("编辑上一条消息");
  });
});

describe("a model error is cindy's error notice (#5157)", () => {
  const gpu = "API Error: 502 upstream error: Req 202610302115A1B2C3D4E5F6 Session 1ebd0000:7e470000 no available GPU pod";

  it("is a neutral card: the red mark only, what happened, what helps, then Retry and Error details", () => {
    const html = render(gpu, false);
    expect(html).toContain("data-error-notice");
    expect(html).toContain("border border-line bg-surface");
    expect(html).not.toMatch(/bg-red-tint|var\(--red\)_22%/);
    expect(html).toContain('class="mt-0.5 inline-flex shrink-0 text-red"');
    expect(html).toContain("推理服务暂时没有可用的 GPU");
    expect(html).toContain("临时故障，过一会儿多半会好");
    expect(html).toContain("重试");
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain("错误详情");
    // the head stays quiet: the status code, the ids and the original wait under Error details
    expect(html).not.toContain(">502<");
    expect(html).not.toContain("1ebd0000:7e470000");
  });

  it("holds the ids, the status code, how many times and the original under Error details", async () => {
    const { createRoot } = await import("react-dom/client");
    const { act } = await import("react");
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const l = { ...line(gpu), count: 3 };
    act(() => root.render(<ChatLineView line={l} mid="m" sid="s" expandFold={async () => {}} />));
    const toggle = [...host.querySelectorAll("button")].find((b) => b.textContent === "错误详情")!;
    act(() => toggle.click());
    const details = host.querySelector("[data-error-details]")!;
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(toggle.textContent).toBe("收起详情");
    expect(details.textContent).toContain("202610302115A1B2C3D4E5F6");
    expect(details.textContent).toContain("1ebd0000:7e470000");
    expect(details.textContent).toContain("502");
    expect(details.textContent).toContain("连续 3 次");
    expect(details.querySelector("pre")!.textContent).toBe(gpu);
    expect(details.textContent).toContain("复制原文");
    act(() => root.unmount());
    host.remove();
  });

  it("draws a usage limit as the same notice, saying when it resets", () => {
    const html = renderToStaticMarkup(<ChatLineView line={line(gpu)} mid="m" sid="s" expandFold={async () => {}} limit="用量上限 · 15:00 重置" />);
    expect(html).toContain("data-error-notice");
    expect(html).toContain('role="status"');
    expect(html).toContain("用量上限 · 15:00 重置");
    expect(html).not.toContain("错误详情");
    expect(html).not.toContain("重试");
  });
});
