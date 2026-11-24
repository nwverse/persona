import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ContextRing, contextUsageRatio, formatTokenCount } from "./ContextRing";

describe("formatTokenCount", () => {
  it("formats raw, thousands, and millions cleanly", () => {
    expect(formatTokenCount(0)).toBe("0");
    expect(formatTokenCount(-5)).toBe("0");
    expect(formatTokenCount(850)).toBe("850");
    expect(formatTokenCount(12_400)).toBe("12.4k");
    expect(formatTokenCount(200_000)).toBe("200k");
    expect(formatTokenCount(1_200_000)).toBe("1.2M");
  });
});

describe("contextUsageRatio", () => {
  it("returns null when used tokens are missing or non-positive", () => {
    expect(contextUsageRatio(undefined)).toBeNull();
    expect(contextUsageRatio(0)).toBeNull();
    expect(contextUsageRatio(-100)).toBeNull();
    expect(contextUsageRatio(1000, 0)).toBeNull();
  });

  it("computes clamped ratio against default or custom window", () => {
    expect(contextUsageRatio(100_000)).toBe(0.5);
    expect(contextUsageRatio(250_000, 200_000)).toBe(1);
    expect(contextUsageRatio(50_000, 100_000)).toBe(0.5);
  });
});

describe("ContextRing", () => {
  it("returns null when neither usedTokens nor liveTokensDown is positive", () => {
    expect(renderToStaticMarkup(<ContextRing />)).toBe("");
    expect(renderToStaticMarkup(<ContextRing usedTokens={0} liveTokensDown={0} />)).toBe("");
  });

  it("uses accent (<65%), orange (65-85%), and red (>85%) ring tones", () => {
    const low = renderToStaticMarkup(<ContextRing usedTokens={80_000} windowTokens={200_000} />);
    expect(low).toContain("text-accent");
    expect(low).toContain('data-native-chat-context-usage="true"');

    const mid = renderToStaticMarkup(<ContextRing usedTokens={150_000} windowTokens={200_000} />);
    expect(mid).toContain("text-orange");

    const high = renderToStaticMarkup(<ContextRing usedTokens={180_000} windowTokens={200_000} />);
    expect(high).toContain("text-red");
  });

  it("renders pulsing accent ring when only liveTokensDown is provided", () => {
    const html = renderToStaticMarkup(<ContextRing liveTokensDown={4_200} working />);
    expect(html).toContain("text-accent");
    expect(html).toContain("animate-pulse");
  });

  it("renders frosted popover with usage details, live tokens, and /compact button when open", () => {
    const html = renderToStaticMarkup(
      <ContextRing
        usedTokens={150_000}
        windowTokens={200_000}
        liveTokensDown={12_400}
        onCompact={() => {}}
        defaultOpen
      />,
    );
    expect(html).toContain('data-chat-find-skip="true"');
    expect(html).toContain("上下文用量");
    expect(html).toContain("75%");
    expect(html).toContain("已用 / 窗口");
    expect(html).toContain("150k / 200k");
    expect(html).toContain("本轮输出：↓ 12.4k tokens");
    expect(html).toContain("执行 /compact 压缩");
  });

  it("omits /compact button when onCompact is not provided", () => {
    const html = renderToStaticMarkup(
      <ContextRing usedTokens={50_000} defaultOpen />,
    );
    expect(html).toContain("25%");
    expect(html).not.toContain("执行 /compact 压缩");
  });
});
