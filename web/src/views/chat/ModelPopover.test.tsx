import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ModelPopover, presetModelsFor, shortModelName } from "./ModelPopover";

describe("shortModelName", () => {
  it("strips provider prefix and trims whitespace", () => {
    expect(shortModelName("anthropic/claude-opus-5-5")).toBe("claude-opus-5-5");
    expect(shortModelName("  openai/gpt-6.2-astra  ")).toBe("gpt-6.2-astra");
    expect(shortModelName("claude-sonnet-5-5")).toBe("claude-sonnet-5-5");
    expect(shortModelName(undefined)).toBe("");
    expect(shortModelName("   ")).toBe("");
  });
});

describe("presetModelsFor", () => {
  it("returns Claude presets by default", () => {
    const list = presetModelsFor(undefined, undefined);
    expect(list.map((m) => m.id)).toEqual([
      "claude-opus-5-5",
      "claude-sonnet-5-5",
      "claude-haiku-5-5",
    ]);
    expect(list.every((m) => !m.current)).toBe(true);
  });

  it("places currentModel at top with current badge and deduplicates matching preset", () => {
    const list = presetModelsFor("claude", "anthropic/claude-sonnet-5-5");
    expect(list[0]).toEqual({
      id: "anthropic/claude-sonnet-5-5",
      label: "claude-sonnet-5-5",
      hint: "Sonnet",
      current: true,
    });
    expect(list.map((m) => m.label)).toEqual([
      "claude-sonnet-5-5",
      "claude-opus-5-5",
      "claude-haiku-5-5",
    ]);
  });

  it("returns Codex presets for codex launcher or gpt model", () => {
    const byLauncher = presetModelsFor("codex");
    expect(byLauncher.map((m) => m.id)).toEqual([
      "gpt-6.2-astra",
      "gpt-6-astra",
      "gpt-6.1-sol",
    ]);

    const byModel = presetModelsFor(undefined, "gpt-6.1-sol");
    expect(byModel[0].current).toBe(true);
    expect(byModel.map((m) => m.id)).toEqual([
      "gpt-6.1-sol",
      "gpt-6.2-astra",
      "gpt-6-astra",
    ]);
  });

  it("returns Gemini presets for gemini/agy launcher", () => {
    const list = presetModelsFor("agy", "gemini-4-argon-high");
    expect(list[0].current).toBe(true);
    expect(list.map((m) => m.id)).toEqual([
      "gemini-4-argon-high",
      "gemini-4-neon-high",
      "gemini-3.8-flash-medium",
    ]);
  });

  it("keeps a custom currentModel at top alongside launcher presets", () => {
    const list = presetModelsFor("claude", "custom/my-finetune");
    expect(list[0]).toEqual({
      id: "custom/my-finetune",
      label: "my-finetune",
      hint: undefined,
      current: true,
    });
    expect(list).toHaveLength(4);
  });
});

describe("ModelPopover", () => {
  it("renders compact trigger chip with short model name when closed", () => {
    const html = renderToStaticMarkup(
      <ModelPopover
        currentModel="anthropic/claude-opus-5-5"
        launcher="claude"
        onSelectModel={() => {}}
        onCustomModel={() => {}}
      />,
    );
    expect(html).toContain("claude-opus-5-5");
    expect(html).not.toContain('role="menu"');
    expect(html).not.toContain("输入自定义模型…");
  });

  it("renders floating frosted popover with presets, 当前 badge, and custom model action when open", () => {
    const html = renderToStaticMarkup(
      <ModelPopover
        currentModel="anthropic/claude-sonnet-5-5"
        launcher="claude"
        defaultOpen
        onSelectModel={() => {}}
        onCustomModel={() => {}}
      />,
    );
    expect(html).toContain('role="menu"');
    expect(html).toContain('data-chat-find-skip="true"');
    expect(html).toContain("当前");
    expect(html).toContain("claude-opus-5-5");
    expect(html).toContain("claude-sonnet-5-5");
    expect(html).toContain("claude-haiku-5-5");
    expect(html).toContain("输入自定义模型…");
  });

  it("renders fallback label when currentModel is omitted", () => {
    const html = renderToStaticMarkup(
      <ModelPopover onSelectModel={() => {}} onCustomModel={() => {}} />,
    );
    expect(html).toContain("模型");
  });
});
