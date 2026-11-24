import { createRef } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ChatFindBar, findTextRanges } from "./ChatFindBar";

interface FakeRange {
  startContainer: FakeNode;
  startOffset: number;
  endContainer: FakeNode;
  endOffset: number;
  setStart(node: FakeNode, offset: number): void;
  setEnd(node: FakeNode, offset: number): void;
}

interface FakeDocument {
  createRange(): FakeRange;
}

interface FakeNode {
  nodeType: number;
  nodeValue?: string;
  tagName?: string;
  attributes?: Record<string, string>;
  childNodes: FakeNode[];
  ownerDocument: FakeDocument;
  hasAttribute?(name: string): boolean;
}

function makeFakeDoc(): FakeDocument {
  return {
    createRange() {
      const r: FakeRange = {
        startContainer: null as unknown as FakeNode,
        startOffset: 0,
        endContainer: null as unknown as FakeNode,
        endOffset: 0,
        setStart(node, offset) {
          r.startContainer = node;
          r.startOffset = offset;
        },
        setEnd(node, offset) {
          r.endContainer = node;
          r.endOffset = offset;
        },
      };
      return r;
    },
  };
}

function el(
  doc: FakeDocument,
  tagName: string,
  children: FakeNode[] = [],
  attributes: Record<string, string> = {},
): FakeNode {
  return {
    nodeType: 1,
    tagName,
    attributes,
    childNodes: children,
    ownerDocument: doc,
    hasAttribute(name: string) {
      return Object.prototype.hasOwnProperty.call(attributes, name);
    },
  };
}

function txt(doc: FakeDocument, value: string): FakeNode {
  return {
    nodeType: 3,
    nodeValue: value,
    childNodes: [],
    ownerDocument: doc,
  };
}

describe("findTextRanges", () => {
  it("returns empty array for empty query", () => {
    const doc = makeFakeDoc();
    const root = el(doc, "DIV", [txt(doc, "hello world")]);
    expect(findTextRanges(root as unknown as Node, "")).toEqual([]);
  });

  it("finds multiple case-insensitive matches across text nodes with exact offsets", () => {
    const doc = makeFakeDoc();
    const t1 = txt(doc, "Hello persona, HELLO!");
    const t2 = txt(doc, "say hello again");
    const root = el(doc, "DIV", [el(doc, "P", [t1]), el(doc, "SPAN", [t2])]);

    const ranges = findTextRanges(root as unknown as Node, "hello") as unknown as FakeRange[];
    expect(ranges).toHaveLength(3);

    expect(ranges[0]).toMatchObject({ startContainer: t1, startOffset: 0, endContainer: t1, endOffset: 5 });
    expect(ranges[1]).toMatchObject({ startContainer: t1, startOffset: 15, endContainer: t1, endOffset: 20 });
    expect(ranges[2]).toMatchObject({ startContainer: t2, startOffset: 4, endContainer: t2, endOffset: 9 });
  });

  it("skips [data-chat-find-skip] subtrees and script/style/svg/textarea/input elements", () => {
    const doc = makeFakeDoc();
    const kept = txt(doc, "target visible");
    const root = el(doc, "DIV", [
      el(doc, "DIV", [txt(doc, "target skipped")], { "data-chat-find-skip": "true" }),
      el(doc, "SCRIPT", [txt(doc, "target script")]),
      el(doc, "STYLE", [txt(doc, "target style")]),
      el(doc, "SVG", [el(doc, "TEXT", [txt(doc, "target svg")])]),
      el(doc, "TEXTAREA", [txt(doc, "target textarea")]),
      el(doc, "INPUT", [txt(doc, "target input")]),
      el(doc, "P", [kept]),
    ]);

    const ranges = findTextRanges(root as unknown as Node, "target") as unknown as FakeRange[];
    expect(ranges).toHaveLength(1);
    expect(ranges[0].startContainer).toBe(kept);
    expect(ranges[0].startOffset).toBe(0);
    expect(ranges[0].endOffset).toBe(6);
  });

  it("finds non-overlapping repeated matches within a single text node", () => {
    const doc = makeFakeDoc();
    const t = txt(doc, "aaaa");
    const root = el(doc, "DIV", [t]);

    const ranges = findTextRanges(root as unknown as Node, "aa") as unknown as FakeRange[];
    expect(ranges).toHaveLength(2);
    expect(ranges[0]).toMatchObject({ startOffset: 0, endOffset: 2 });
    expect(ranges[1]).toMatchObject({ startOffset: 2, endOffset: 4 });
  });
});

describe("ChatFindBar rendering", () => {
  it("renders nothing when closed", () => {
    const ref = createRef<HTMLElement>();
    expect(renderToStaticMarkup(<ChatFindBar open={false} onClose={() => {}} containerRef={ref} />)).toBe("");
  });

  it("renders floating search bar with data-chat-find-skip and controls when open", () => {
    const ref = createRef<HTMLElement>();
    const html = renderToStaticMarkup(<ChatFindBar open={true} onClose={() => {}} containerRef={ref} />);
    expect(html).toContain('data-chat-find-skip="true"');
    expect(html).toContain('role="search"');
    expect(html).toContain('aria-label="上一个匹配"');
    expect(html).toContain('aria-label="下一个匹配"');
    expect(html).toContain('aria-label="关闭查找"');
  });
});
