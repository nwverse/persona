// @vitest-environment jsdom
import { Blob as NodeBlob } from "node:buffer";
import { describe, expect, it } from "vitest";
import { inlineSiblings, type ReadFile } from "./html-inline";

// jsdom's Blob has no text(), which the inliner reads style sheets with: Node's Blob stands in for it.
const files = (entries: Record<string, string>): ReadFile => async (path) => (path in entries ? (new NodeBlob([entries[path]]) as unknown as Blob) : null);

describe("a previewed page's style sheets beside it", () => {
  it("inlines an imported style sheet exactly as it is", async () => {
    const imported = `.tier::after{content:"$$$"}\n.price::before{content:"$&"}\n.a::after{content:"$'"}\n.b::after{content:"$\`"}`;
    const read = files({ "/site/tier.css": imported });
    const { html } = await inlineSiblings(`<style>@import "tier.css";\np{margin:0}</style><p>x</p>`, "/site/index.html", read);
    const doc = new DOMParser().parseFromString(html, "text/html");
    expect(doc.querySelector("style")?.textContent).toBe(`${imported}\np{margin:0}`);
  });
});
