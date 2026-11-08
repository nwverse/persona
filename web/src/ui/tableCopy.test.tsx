// @vitest-environment jsdom
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Markdown } from "./Markdown";
import { cellPlainText, quoteTsvField, tableToTsv, writeTableClipboard } from "./tableCopy";

function table(html: string): HTMLTableElement {
  const doc = new DOMParser().parseFromString(`<table>${html}</table>`, "text/html");
  return doc.querySelector("table")!;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("tableToTsv", () => {
  it("keeps the header row and a link's label, not its address", () => {
    const tsv = tableToTsv(
      table(`<tr><th>Name</th><th>Link</th></tr>
        <tr><td><a href="https://example.com/docs">Docs</a></td><td>plain</td></tr>`),
    );
    expect(tsv).toBe("Name\tLink\nDocs\tplain");
    expect(tsv).not.toContain("https://");
  });

  it("copies inline code as its text, pipes included", () => {
    expect(tableToTsv(table(`<tr><td>see <code>a|b</code></td><td>x|y</td></tr>`))).toBe("see a|b\tx|y");
    expect(quoteTsvField("a|b")).toBe("a|b");
  });

  it("keeps a line break inside one quoted cell", () => {
    expect(tableToTsv(table(`<tr><td>line1<br>line2</td><td>next</td></tr>`))).toBe('"line1\nline2"\tnext');
  });

  it("quotes a cell that contains a tab so it does not become another column", () => {
    const cell = document.createElement("td");
    cell.textContent = "left\tright";
    const root = table("<tr><td></td><td>ok</td></tr>");
    root.rows[0].cells[0].replaceWith(cell);
    expect(tableToTsv(root)).toBe('"left\tright"\tok');
  });

  it("quotes a quote and keeps an empty cell empty", () => {
    expect(tableToTsv(table(`<tr><th></th><th>Say</th></tr><tr><td></td><td>say "hi"</td></tr>`))).toBe('\tSay\n\t"say ""hi"""');
  });

  it("does not count a nested table's rows, and skips KaTeX's visual duplicate", () => {
    const root = table(`<tr><td>
      <span class="katex"><span class="katex-mathml"><math><semantics><mi>x</mi><annotation>x^2</annotation></semantics></math></span>
      <span class="katex-html" aria-hidden="true">x</span></span>
      <table><tr><td>nested</td></tr></table>
    </td></tr>`);
    expect(cellPlainText(root.rows[0].cells[0])).toBe("x");
    expect(tableToTsv(root)).toBe("x");
  });

  it("is undefined when there is no row", () => {
    expect(tableToTsv(table(""))).toBeUndefined();
  });
});

describe("Markdown table copy button", () => {
  const md = ["| Name | Code | Empty |", "| --- | --- | --- |", "| [Docs](https://example.com/docs) | `ab` | |", "| p\\|q |  | r |"].join("\n");

  it("renders the button outside the table, hidden until hover or focus", () => {
    const html = renderToStaticMarkup(createElement(Markdown, { text: md }));
    const doc = new DOMParser().parseFromString(html, "text/html");
    const button = doc.querySelector("button");
    expect(button?.getAttribute("aria-label")).toBe("拷贝表格");
    expect(button?.className).toContain("opacity-0");
    expect(button?.className).toContain("group-hover/table:opacity-100");
    expect(button?.className).toContain("focus-visible:opacity-100");
    expect(doc.querySelector("table button")).toBeNull();
  });

  it("converts the rendered table: header, link label, inline code, pipes, empty cells", () => {
    const html = renderToStaticMarkup(createElement(Markdown, { text: md }));
    const rendered = new DOMParser().parseFromString(html, "text/html").querySelector("table")!;
    expect(tableToTsv(rendered)).toBe("Name\tCode\tEmpty\nDocs\tab\t\np|q\t\tr");
    expect(rendered.querySelector("a")?.getAttribute("href")).toBe("https://example.com/docs");
    expect(rendered.querySelector("code")?.textContent).toBe("ab");
  });
});

describe("writeTableClipboard", () => {
  function install(write: (items: ClipboardItem[]) => Promise<void>) {
    class Item {
      constructor(readonly parts: Record<string, Blob>) {}
    }
    vi.stubGlobal("ClipboardItem", Item);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { write } });
  }

  async function textOf(blob: Blob): Promise<string> {
    const reader = new FileReader();
    return new Promise((resolve, reject) => {
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsText(blob);
    });
  }

  it("writes tab-separated text and the table html", async () => {
    let parts: Record<string, Blob> | undefined;
    install(async (items) => {
      parts = (items[0] as unknown as { parts: Record<string, Blob> }).parts;
    });
    const html = "<table><tr><td>a</td></tr></table>";
    expect(await writeTableClipboard("a", html)).toBe(true);
    expect(parts).toBeTruthy();
    expect(await textOf(parts!["text/plain"])).toBe("a");
    expect(await textOf(parts!["text/html"])).toBe(html);
  });

  it("falls back to the copy command when the async write is refused", async () => {
    install(async () => {
      throw new Error("denied");
    });
    let plain = "";
    let rich = "";
    const previous = document.execCommand;
    document.execCommand = ((cmd: string) => {
      if (cmd !== "copy") return false;
      try {
        const store = new Map<string, string>();
        const data = {
          setData: (type: string, value: string) => store.set(type, value),
          getData: (type: string) => store.get(type) ?? "",
        };
        const ev = new Event("copy", { bubbles: true, cancelable: true });
        Object.defineProperty(ev, "clipboardData", { value: data });
        document.dispatchEvent(ev);
        plain = data.getData("text/plain");
        rich = data.getData("text/html");
      } catch (e) {
        plain = e instanceof Error ? e.message : "threw";
      }
      return true;
    }) as typeof document.execCommand;
    try {
      expect(await writeTableClipboard("a\tb", "<table></table>")).toBe(true);
      expect(plain).toBe("a\tb");
      expect(rich).toBe("<table></table>");
    } finally {
      document.execCommand = previous;
    }
  });
});
