// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { parseCommentBlock } from "../../lib/comments";
import { splitQuotes, setQuotes } from "../../lib/drafts";
import { Composer, type Outgoing } from "../chat/Composer";
import { FileViewerPanel } from "./FileViewer";

vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false, media: query, addEventListener() {}, removeEventListener() {},
  })) as typeof window.matchMedia;
});

const MID = "test-machine";
const SID = "file-comment-session";
const KEY = `${MID}/${SID}`;
const CWD = "/work";
const VISIBLE_TAIL = "endOfVisibleCode";
const DISPLAYED_LINE = "x".repeat(4000 - VISIBLE_TAIL.length) + VISIBLE_TAIL;
const HIDDEN_TAIL = "hiddenSourceBeyondTheDisplayedLine";
const FILES = {
  markdown: { name: "notes.md", mime: "text/markdown", text: "# Review notes\n\nCheck the example." },
  source: { name: "example.ts", mime: "text/plain", text: `const before = 0;\n  const first = 42;\n  const second = 7;\n${DISPLAYED_LINE}${HIDDEN_TAIL}` },
  html: { name: "page.html", mime: "text/html", text: "<!doctype html><h2>Rendered chart</h2>" },
};
type Kind = keyof typeof FILES;

vi.mock("../../lib/api", async (original) => ({
  ...(await original<typeof import("../../lib/api")>()),
  api: vi.fn(async (route: string) => {
    const path = new URL(route, "https://hub.invalid").searchParams.get("path");
    const file = Object.values(FILES).find(({ name }) => path === `${CWD}/${name}`);
    if (!file) throw new Error(`Unexpected request ${route}`);
    return { ...file, path, size: file.text.length, is_dir: false, truncated: false };
  }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
const sent = vi.fn(async (_message: Outgoing) => true);
const pill = () => document.querySelector<HTMLButtonElement>("[data-comment-pill]");
const frame = () => host.querySelector<HTMLIFrameElement>("iframe");
const rect = { left: 50, top: 80, right: 150, bottom: 100, width: 100, height: 20 } as DOMRect;

beforeEach(() => {
  sent.mockClear();
  localStorage.clear();
  setQuotes(KEY, []);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
    left: 0, top: 0, right: 600, bottom: 600, width: 600, height: 600,
  } as DOMRect);
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => window.setTimeout(() => callback(0), 0));
  vi.stubGlobal("cancelAnimationFrame", (id: number) => window.clearTimeout(id));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  document.getSelection()?.removeAllRanges();
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function show(kind: Kind) {
  const path = `${CWD}/${FILES[kind].name}`;
  await act(async () => root.render(
    <>
      <Composer draftKey={KEY} placeholder="" onSend={sent} />
      <FileViewerPanel key={path} path={path} cwd={CWD} mid={MID} sid={SID} onClose={() => {}} />
    </>,
  ));
}

function select(start: Node, from: number, end: Node, to: number) {
  const doc = start.ownerDocument!;
  const range = doc.createRange();
  range.setStart(start, from);
  range.setEnd(end, to);
  Object.defineProperty(range, "getClientRects", { value: () => [rect] });
  act(() => {
    doc.getSelection()!.removeAllRanges();
    doc.getSelection()!.addRange(range);
    doc.dispatchEvent(new doc.defaultView!.Event("selectionchange"));
  });
}

/** jsdom gives srcdoc an empty about:blank document. Chromium checks the real frame separately. */
function htmlText() {
  const iframe = frame()!;
  expect(iframe.getAttribute("sandbox")).toContain("allow-same-origin");
  expect(iframe.getAttribute("sandbox")).not.toContain("allow-scripts");
  expect(iframe.srcdoc).toContain("default-src 'none'");
  const doc = iframe.contentDocument!;
  Object.defineProperty(doc, "URL", { value: "about:srcdoc" });
  doc.body.innerHTML = "<h2>Rendered chart</h2>";
  act(() => iframe.dispatchEvent(new Event("load")));
  return doc.querySelector("h2")!.firstChild!;
}

function textAt(el: Element, offset: number) {
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const size = (node as Text).length;
    if (offset <= size) return { node, offset };
    offset -= size;
  }
  throw new Error("Missing text in source row");
}

const sourceText = (line: number) => host.querySelector(`[data-line="${line}"] [data-file-code-text]`)!;

async function waitForPill() {
  for (let i = 0; i < 8 && !pill(); i++) await act(async () => { await new Promise((done) => setTimeout(done, 5)); });
}

async function pick(kind: Kind) {
  await show(kind);
  if (kind === "source") {
    const a = textAt(sourceText(2), 2);
    const b = textAt(sourceText(3), 14);
    select(a.node, a.offset, b.node, b.offset);
  } else {
    const heading = kind === "html" ? htmlText() : host.querySelector("[data-testid=file-markdown] h1")!.firstChild!;
    select(heading, 0, heading, (heading as Text).length);
  }
  await waitForPill();
  expect(pill(), `Comment action for ${kind}`).not.toBeNull();
}

function writeNote(n: number, note: string) {
  const input = host.querySelector<HTMLTextAreaElement>(`[data-comment-card="${n}"] textarea`)!;
  expect(input).toBe(document.activeElement);
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, note);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  act(() => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
  expect(input.isConnected).toBe(false);
}

async function send() {
  expect(sent).not.toHaveBeenCalled();
  await act(async () => host.querySelector<HTMLButtonElement>("[data-testid=composer-send]")!.click());
  expect(sent).toHaveBeenCalledTimes(1);
  return sent.mock.calls[0][0].text;
}

const QUOTED_LINES: Record<Kind, string[]> = {
  markdown: ["> Review notes", "File: /work/notes.md"],
  source: ["> const first = 42;", ">   const second", "File: /work/example.ts:2-3"],
  html: ["> Rendered chart", "File: /work/page.html"],
};

function expected(kind: Kind, n: number, note: string) {
  return [`Comment ${n}`, "Selected text:", ...QUOTED_LINES[kind], "", note].map((line) => `> ${line}`).join("\n");
}

describe("selection comments from the Agent's file panel", () => {
  it.each(["markdown", "source", "html"] as const)("sends the selection and location from the %s preview", async (kind) => {
    await pick(kind);
    act(() => {
      if (kind === "html") {
        const doc = frame()!.contentDocument!;
        doc.body.dispatchEvent(new doc.defaultView!.KeyboardEvent("keydown", { key: "c", bubbles: true, cancelable: true }));
      } else {
        pill()!.click();
      }
    });
    expect(pill()).toBeNull();
    writeNote(1, "Clarify this passage.");
    expect(await send()).toBe(expected(kind, 1, "Clarify this passage."));
  });

  it("numbers comments across files in one message and restores their locations on readback", async () => {
    const kinds = ["markdown", "source", "html"] as const;
    for (const [i, kind] of kinds.entries()) {
      await pick(kind);
      act(() => pill()!.click());
      writeNote(i + 1, `Note ${i + 1}`);
    }
    const text = await send();
    expect(text).toBe(kinds.map((kind, i) => expected(kind, i + 1, `Note ${i + 1}`)).join("\n\n"));
    expect(splitQuotes(text)!.quotes.map((block) => parseCommentBlock(block)?.comment?.file)).toEqual([
      { location: "/work/notes.md" },
      { location: "/work/example.ts:2-3" },
      { location: "/work/page.html" },
    ]);
  });

  it("discards the selectable frame when scripts are enabled", async () => {
    await pick("html");
    const before = frame()!;
    const scripts = [...host.querySelectorAll("button")].find((b) => b.textContent === t("session-files-htmlpreview.scripts"))!;
    act(() => scripts.click());
    expect(pill()).toBeNull();
    expect(frame()).not.toBe(before);
    expect(frame()!.getAttribute("sandbox")).toContain("allow-scripts");
    expect(frame()!.getAttribute("sandbox")).not.toContain("allow-same-origin");
  });

  it.each([
    { description: "one line ending inside the marker", first: 4, endpoint: "marker" },
    { description: "one line ending after the marker", first: 4, endpoint: "text wrapper" },
    { description: "two lines ending inside the marker", first: 3, endpoint: "marker" },
    { description: "two lines ending at the boundary after the truncated row", first: 3, endpoint: "row" },
  ])("sends only visible source for $description", async ({ first, endpoint }) => {
    await show("source");
    const text = sourceText(4);
    const marker = text.lastElementChild!.firstChild!;
    const start = textAt(sourceText(first), first === 4 ? DISPLAYED_LINE.length - VISIBLE_TAIL.length : 2);
    const end = endpoint === "marker" ? marker : endpoint === "row" ? text.parentElement! : text;
    const endOffset = endpoint === "marker" ? 6 : end.childNodes.length;
    select(start.node, start.offset, end, endOffset);
    await waitForPill();
    expect(pill()).not.toBeNull();
    act(() => pill()!.click());
    writeNote(1, "Check this range.");
    const selected = first === 4 ? [VISIBLE_TAIL] : ["const second = 7;", DISPLAYED_LINE];
    const location = first === 4 ? "4" : "3-4";
    expect(await send()).toBe([
      "> Comment 1", "> Selected text:", ...selected.map((line) => `> > ${line}`),
      `> File: /work/example.ts:${location}`, "> ", "> Check this range.",
    ].join("\n"));
  });

  it("does not offer a source comment for the truncation marker alone", async () => {
    await show("source");
    const marker = sourceText(4).lastElementChild!.firstChild!;
    select(marker, 2, marker, 6);
    await waitForPill();
    expect(pill()).toBeNull();
  });
});
