// @vitest-environment jsdom
/// <reference types="node" />
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { compile } from "@tailwindcss/node";
import { Scanner } from "@tailwindcss/oxide";
import * as lightningcss from "lightningcss";
import postcss, { type Root as PostcssRoot } from "postcss";
import { textScale } from "../lib/text-scale";
import { closePanel, openPanel } from "../lib/panel";
import { SidePanel } from "../views/SidePanel";
import { Dialog } from "./atoms";
import { applyLook, setSurfaceStyle } from "./look";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface BackdropRule {
  selector: string;
  inLayer: boolean;
  media: string | null;
  order: number;
  backdropFilter?: string;
  webkitBackdropFilter?: string;
}

type Specificity = [number, number, number];

function splitTopLevel(input: string, delimiter = ","): string[] {
  const parts: string[] = [];
  let start = 0;
  let paren = 0;
  let bracket = 0;
  let quote: string | null = null;
  for (let i = 0; i < input.length; i++) {
    const ch = input[i]!;
    if (ch === "\\" && i + 1 < input.length) {
      i++;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") quote = ch;
    else if (ch === "(") paren++;
    else if (ch === ")" && paren > 0) paren--;
    else if (ch === "[") bracket++;
    else if (ch === "]" && bracket > 0) bracket--;
    else if (ch === delimiter && paren === 0 && bracket === 0) {
      const part = input.slice(start, i).trim();
      if (part) parts.push(part);
      start = i + 1;
    }
  }
  const tail = input.slice(start).trim();
  if (tail) parts.push(tail);
  return parts;
}

function compareSpecificity(a: Specificity, b: Specificity): number {
  return a[0] - b[0] || a[1] - b[1] || a[2] - b[2];
}

function maxSpecificity(selectorList: string): Specificity {
  let best: Specificity = [0, 0, 0];
  for (const part of splitTopLevel(selectorList)) {
    const spec = selectorSpecificity(part);
    if (compareSpecificity(spec, best) > 0) best = spec;
  }
  return best;
}

function readBalanced(input: string, openAt: number, openChar: string, closeChar: string): number {
  let depth = 0;
  let quote: string | null = null;
  for (let i = openAt; i < input.length; i++) {
    const ch = input[i]!;
    if (ch === "\\" && i + 1 < input.length) {
      i++;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") quote = ch;
    else if (ch === openChar) depth++;
    else if (ch === closeChar && --depth === 0) return i;
  }
  return input.length - 1;
}

function readIdent(input: string, from: number): number {
  let i = from;
  while (i < input.length) {
    const ch = input[i]!;
    if (ch === "\\" && i + 1 < input.length) {
      i += 2;
      continue;
    }
    if (/[\s>+~.#[:()*,]/.test(ch)) break;
    i++;
  }
  return i;
}

function selectorSpecificity(selector: string): Specificity {
  let a = 0;
  let b = 0;
  let c = 0;
  let i = 0;
  while (i < selector.length) {
    const ch = selector[i]!;
    if (/[\s>+~*]/.test(ch)) {
      i++;
      continue;
    }
    if (ch === "[") {
      b++;
      i = readBalanced(selector, i, "[", "]") + 1;
      continue;
    }
    if (ch === "#") {
      a++;
      i = readIdent(selector, i + 1);
      continue;
    }
    if (ch === ".") {
      b++;
      i = readIdent(selector, i + 1);
      continue;
    }
    if (ch === ":") {
      if (selector[i + 1] === ":") {
        c++;
        i = readIdent(selector, i + 2);
        if (selector[i] === "(") i = readBalanced(selector, i, "(", ")") + 1;
        continue;
      }
      const end = readIdent(selector, i + 1);
      const name = selector.slice(i + 1, end).toLowerCase();
      if (selector[end] === "(") {
        const close = readBalanced(selector, end, "(", ")");
        const inner = selector.slice(end + 1, close);
        if (name === "is" || name === "not" || name === "has") {
          const [ia, ib, ic] = maxSpecificity(inner);
          a += ia;
          b += ib;
          c += ic;
        } else if (name !== "where") {
          b++;
        }
        i = close + 1;
      } else {
        b++;
        i = end;
      }
      continue;
    }
    c++;
    i = readIdent(selector, i);
  }
  return [a, b, c];
}

/** Resolves CSS cascade winner for backdrop-filter properties on a real DOM node using compiled CSS AST + jsdom `matches()`. */
function resolveBackdropCascade(
  el: Element,
  rules: readonly BackdropRule[],
): { backdropFilter: string | null; webkitBackdropFilter: string | null } {
  const candidates: { layer: number; spec: Specificity; order: number; bf?: string; wbf?: string }[] = [];
  for (const rule of rules) {
    if (rule.media) continue;
    for (const part of splitTopLevel(rule.selector)) {
      if (!el.matches(part)) continue;
      candidates.push({
        layer: rule.inLayer ? 0 : 1,
        spec: selectorSpecificity(part),
        order: rule.order,
        bf: rule.backdropFilter,
        wbf: rule.webkitBackdropFilter,
      });
    }
  }
  candidates.sort((x, y) =>
    (x.layer - y.layer) || compareSpecificity(x.spec, y.spec) || (x.order - y.order));
  let backdropFilter: string | null = null;
  let webkitBackdropFilter: string | null = null;
  for (const hit of candidates) {
    if (hit.bf !== undefined) backdropFilter = hit.bf;
    if (hit.wbf !== undefined) webkitBackdropFilter = hit.wbf;
  }
  return { backdropFilter, webkitBackdropFilter };
}

let backdropRules: BackdropRule[] = [];
let host: HTMLDivElement;
let root: Root;

beforeAll(async () => {
  const stylesPath = resolve(import.meta.dirname, "../styles.css");
  const srcDir = dirname(stylesPath);
  const raw = readFileSync(stylesPath, "utf8");
  const compiled = await compile(raw, { base: srcDir, onDependency() {} });
  const candidates = new Scanner({
    sources: [{ base: srcDir, pattern: "**/*", negated: false }, ...compiled.sources],
  }).scan();
  const scaled = await postcss([textScale()]).process(compiled.build(candidates), { from: stylesPath });
  const compiledCss = Buffer.from(
    lightningcss.transform({
      filename: "styles.css",
      code: Buffer.from(scaled.css),
      minify: true,
      // Match Vite's default cssTarget (chrome87 / safari14) so -webkit-backdrop-filter is preserved as in dist builds.
      targets: { chrome: 87 << 16, safari: 14 << 16 },
    }).code,
  ).toString("utf8");
  expect(compiledCss.length).toBeGreaterThan(50_000);
  const parsed: PostcssRoot = postcss.parse(compiledCss);
  const collected: BackdropRule[] = [];
  let order = 0;
  parsed.walkRules((rule) => {
    let bf: string | undefined;
    let wbf: string | undefined;
    rule.walkDecls((decl) => {
      if (decl.prop === "backdrop-filter") bf = decl.value;
      if (decl.prop === "-webkit-backdrop-filter") wbf = decl.value;
    });
    if (bf === undefined && wbf === undefined) return;
    let inLayer = false;
    let media: string | null = null;
    for (let parent = rule.parent; parent && parent.type !== "root"; parent = parent.parent) {
      if (parent.type === "atrule" && parent.name === "layer") inLayer = true;
      if (parent.type === "atrule" && parent.name === "media") media = parent.params;
    }
    collected.push({ selector: rule.selector, inLayer, media, order: order++, backdropFilter: bf, webkitBackdropFilter: wbf });
  });
  backdropRules = collected;
}, 30_000);

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  applyLook();
});

afterEach(() => {
  act(() => closePanel());
  act(() => root.unmount());
  host.remove();
  delete (window as { personaDesktop?: unknown }).personaDesktop;
  localStorage.clear();
  applyLook();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

afterAll(() => {
  delete document.documentElement.dataset.surfaceStyle;
  delete document.documentElement.dataset.desktopGlass;
  document.documentElement.style.removeProperty("--glass-t");
});

function renderShellWithOverlays() {
  act(() => openPanel({ kind: "agent", mid: "m1", sid: "s1" }));
  act(() => {
    root.render(
      <>
        <div data-app-shell="">
          <main>
            <header data-probe="main-header" className="backdrop-blur-md" />
            <div data-probe="main-composer" className="backdrop-blur-md" />
            <div data-probe="main-drop-hint" className="backdrop-blur-[2px]" />
            <div data-probe="main-sticky" className="backdrop-blur-sm" />
            <div data-probe="main-diff" className="backdrop-blur" />
            <div data-probe="admin-devices" className="backdrop-blur-[2px]" />
          </main>
          <SidePanel
            deps={{
              narrow: false,
              maximized: false,
              canGoBack: false,
              width: 480,
              room: 1280,
              closeOnBack: false,
              renderPanel: () => (
                <>
                  <header data-probe="aside-header" className="backdrop-blur-md" />
                  <div data-probe="aside-composer" className="backdrop-blur-md" />
                  <div data-probe="aside-drop-hint" className="backdrop-blur-[2px]" />
                  <div data-probe="aside-diff" className="backdrop-blur" />
                </>
              ),
            }}
          />
        </div>
        <Dialog open onClose={() => {}} title="Dialog">
          <p>Modal content</p>
        </Dialog>
        <div data-probe="palette-backdrop" className="fixed inset-0 z-40 backdrop-blur-[2px]" />
        <div data-probe="image-viewer" className="fixed inset-0 z-50 backdrop-blur-sm" />
      </>,
    );
  });
}

describe("compiled CSS backdrop-filter suppression under macOS desktop Frosted Glass (#17761)", () => {
  it("disables backdrop-filter and -webkit-backdrop-filter across <main>, <aside>, portals, and arbitrary utilities when data-desktop-glass=1", () => {
    Object.assign(window, {
      personaDesktop: { platform: "darwin", setSurfaceStyle: vi.fn(), setThemeSource: vi.fn() },
    });
    renderShellWithOverlays();
    act(() => setSurfaceStyle("liquid-glass"));

    expect(document.documentElement.dataset.surfaceStyle).toBe("liquid-glass");
    expect(document.documentElement.dataset.desktopGlass).toBe("1");

    const shell = host.querySelector("[data-app-shell]")!;
    const main = host.querySelector("[data-app-shell] > main")!;
    const aside = host.querySelector("[data-app-shell] > aside[data-side-panel]")!;
    expect(aside).not.toBeNull();

    // Shell containers must NOT inherit Web-only glass blur on Desktop
    for (const container of [shell, main, aside]) {
      const resolved = resolveBackdropCascade(container, backdropRules);
      expect(resolved.backdropFilter).toBeNull();
      expect(resolved.webkitBackdropFilter).toBeNull();
    }

    // Real portaled Dialog wrapper (mounted under document.body outside [data-app-shell])
    const dialogBackdrop = document.body.querySelector<HTMLElement>('[role="dialog"]')?.parentElement;
    expect(dialogBackdrop).not.toBeNull();
    expect(dialogBackdrop!.closest("[data-app-shell]")).toBeNull();
    expect(dialogBackdrop!.className).toContain("backdrop-blur-[2px]");

    // Every backdrop-* node across <main>, <aside>, and document.body portals resolves to none
    const backdropNodes = [...document.querySelectorAll('[class*="backdrop-"]')];
    expect(backdropNodes.length).toBeGreaterThanOrEqual(13);
    for (const node of backdropNodes) {
      const resolved = resolveBackdropCascade(node, backdropRules);
      expect(resolved.backdropFilter, node.getAttribute("data-probe") ?? node.className).toBe("none");
      expect(resolved.webkitBackdropFilter, node.getAttribute("data-probe") ?? node.className).toBe("none");
    }
  });

  it("preserves Web-only Frosted Glass blur on [data-app-shell], > main, > aside, and overlays when data-desktop-glass is unset", () => {
    renderShellWithOverlays();
    act(() => setSurfaceStyle("liquid-glass"));

    expect(document.documentElement.dataset.surfaceStyle).toBe("liquid-glass");
    expect(document.documentElement.dataset.desktopGlass).toBeUndefined();

    const shell = host.querySelector("[data-app-shell]")!;
    const main = host.querySelector("[data-app-shell] > main")!;
    const aside = host.querySelector("[data-app-shell] > aside[data-side-panel]")!;

    for (const container of [shell, main, aside]) {
      const resolved = resolveBackdropCascade(container, backdropRules);
      expect(resolved.backdropFilter).toContain("blur(var(--glass-blur))");
      expect(resolved.backdropFilter).toContain("saturate(var(--glass-sat))");
      expect(resolved.webkitBackdropFilter).toContain("blur(var(--glass-blur))");
    }

    for (const node of document.querySelectorAll('[class*="backdrop-"]')) {
      const resolved = resolveBackdropCascade(node, backdropRules);
      expect(resolved.backdropFilter).toContain("var(--tw-backdrop-blur");
      expect(resolved.backdropFilter).not.toBe("none");
    }
  });

  it("keeps Default appearance without glass backdrop-filter", () => {
    renderShellWithOverlays();
    act(() => setSurfaceStyle("default"));

    expect(document.documentElement.dataset.surfaceStyle).toBeUndefined();
    expect(document.documentElement.dataset.desktopGlass).toBeUndefined();

    const shell = host.querySelector("[data-app-shell]")!;
    const main = host.querySelector("[data-app-shell] > main")!;
    const aside = host.querySelector("[data-app-shell] > aside[data-side-panel]")!;

    for (const container of [shell, main, aside]) {
      const resolved = resolveBackdropCascade(container, backdropRules);
      expect(resolved.backdropFilter).toBeNull();
      expect(resolved.webkitBackdropFilter).toBeNull();
    }

    for (const node of document.querySelectorAll('[class*="backdrop-"]')) {
      const resolved = resolveBackdropCascade(node, backdropRules);
      expect(resolved.backdropFilter).toContain("var(--tw-backdrop-blur");
      expect(resolved.webkitBackdropFilter).toContain("var(--tw-backdrop-blur");
    }
  });
});
