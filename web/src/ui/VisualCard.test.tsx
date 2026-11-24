// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { buildVisualSrcDoc, extractVisualTitle, VisualCard, VISUAL_LANGUAGES } from "./VisualCard";
import { Markdown } from "./Markdown";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("buildVisualSrcDoc", () => {
  it("wraps HTML fragments with strict CSP meta including connect-src 'none'", () => {
    const doc = buildVisualSrcDoc("<div id='app'>Hello</div>", { dark: false, nonce: "nonce-1" });
    expect(doc).toContain('http-equiv="Content-Security-Policy"');
    expect(doc).toContain("default-src 'none'");
    expect(doc).toContain("script-src 'unsafe-inline'");
    expect(doc).toContain("style-src 'unsafe-inline'");
    expect(doc).toContain("img-src data: blob:");
    expect(doc).toContain("font-src data:");
    expect(doc).toContain("connect-src 'none'");
    expect(doc).toContain("frame-ancestors 'none'");
    expect(doc).toContain("form-action 'none'");
    expect(doc).toContain("<div id='app'>Hello</div>");
  });

  it("injects light and dark Persona theme CSS tokens and color-scheme", () => {
    const light = buildVisualSrcDoc("<p>light</p>", { dark: false, nonce: "n-light" });
    expect(light).toContain("color-scheme: light;");
    expect(light).toContain("--bg: #fafafb;");
    expect(light).toContain("--surface: #ffffff;");
    expect(light).toContain("--fg: #1f2124;");
    expect(light).toContain("--accent: #0285ff;");
    expect(light).toContain("--chart-1: #0285ff;");
    expect(light).toContain("--chart-4: #e87ba4;");

    const dark = buildVisualSrcDoc("<p>dark</p>", { dark: true, nonce: "n-dark" });
    expect(dark).toContain("color-scheme: dark;");
    expect(dark).toContain("--bg: #17181a;");
    expect(dark).toContain("--surface: #232427;");
    expect(dark).toContain("--fg: #f2f3f4;");
    expect(dark).toContain("--accent: #3d9aff;");
    expect(dark).toContain("--chart-1: #3987e5;");
    expect(dark).toContain("--chart-4: #d55181;");
  });

  it("embeds a ResizeObserver postMessage bridge keyed by nonce", () => {
    const doc = buildVisualSrcDoc("<main>Chart</main>", { dark: false, nonce: "v-nonce-42" });
    expect(doc).toContain("ResizeObserver");
    expect(doc).toContain("persona-visual-height");
    expect(doc).toContain('"v-nonce-42"');
    expect(doc).toContain("window.parent.postMessage");
  });

  it("injects CSP, theme, and resize bridge into full HTML documents", () => {
    const full = "<!doctype html><html><head><title>Metrics</title></head><body><h1>OK</h1></body></html>";
    const doc = buildVisualSrcDoc(full, { dark: true, nonce: "full-1" });
    expect(doc).toContain("<title>Metrics</title>");
    expect(doc).toContain("connect-src 'none'");
    expect(doc).toContain("color-scheme: dark;");
    expect(doc).toContain('"full-1"');
    expect(doc.indexOf("persona-visual-height")).toBeLessThan(doc.indexOf("</body>"));
  });
});

describe("extractVisualTitle", () => {
  it("extracts trimmed text from <title> tag or returns undefined", () => {
    expect(extractVisualTitle("<html><head><title>  Q4 营收趋势  </title></head></html>")).toBe("Q4 营收趋势");
    expect(extractVisualTitle("<div>No title here</div>")).toBeUndefined();
    expect(extractVisualTitle("<title>   </title>")).toBeUndefined();
  });
});

describe("VisualCard", () => {
  it("renders streaming placeholder with collapsible source preview and no iframe", () => {
    const html = renderToStaticMarkup(
      <VisualCard code="<title>实时仪表盘</title><div>partial" streaming />,
    );
    expect(html).toContain('data-visual-card="streaming"');
    expect(html).toContain("正在生成可视化内容…");
    expect(html).toContain("实时仪表盘");
    expect(html).toContain("<details");
    expect(html).not.toContain("<iframe");
  });

  it("renders settled sandboxed iframe with title from <title> and header controls", () => {
    const html = renderToStaticMarkup(
      <VisualCard code="<html><head><title>系统架构图</title></head><body><svg></svg></body></html>" />,
    );
    expect(html).toContain('data-visual-card="ready"');
    expect(html).toContain("系统架构图");
    expect(html).toContain("<iframe");
    expect(html).toContain('sandbox="allow-scripts"');
    expect(html).toContain("源码");
    expect(html).toContain("拷贝");
    expect(html).toContain("全屏");
  });

  it("falls back to default title 交互视图 when <title> is absent", () => {
    const html = renderToStaticMarkup(<VisualCard code="<div>Hello</div>" />);
    expect(html).toContain("交互视图");
    expect(html).toContain('title="交互视图"');
  });
});

describe("Markdown visual fence integration", () => {
  it.each(VISUAL_LANGUAGES)("routes ```%s blocks to VisualCard", async (lang) => {
    const md = `\`\`\`${lang}\n<title>Demo ${lang}</title><div>chart</div>\n\`\`\``;
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    try {
      await act(async () => {
        root.render(<Markdown text={md} />);
      });
      expect(host.innerHTML).toContain('data-visual-card="ready"');
      expect(host.innerHTML).toContain(`Demo ${lang}`);
      expect(host.innerHTML).toContain('sandbox="allow-scripts"');

      await act(async () => {
        root.render(<Markdown text={md} streaming />);
      });
      expect(host.innerHTML).toContain('data-visual-card="streaming"');
      expect(host.innerHTML).toContain("正在生成可视化内容…");
      expect(host.innerHTML).not.toContain("<iframe");
    } finally {
      await act(async () => {
        root.unmount();
      });
      host.remove();
    }
  });
});
