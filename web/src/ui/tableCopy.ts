// A rendered Markdown table, as a spreadsheet paste. text/plain is tab-separated rows (a spreadsheet splits
// them into cells); text/html is the table itself (a document keeps the table). A cell that contains a tab,
// newline or quote is quoted the way spreadsheets expect, so it stays one cell.

const SKIP_TAG = new Set(["script", "style", "annotation"]);

/** The words a cell shows: a link's label (not its address), inline code without its fences. A <br> is a
 *  line break. KaTeX draws the same formula twice (MathML and a visual span); only the MathML is kept. */
export function cellPlainText(cell: Node): string {
  const parts: string[] = [];
  const walk = (node: Node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      parts.push((node.textContent ?? "").replace(/\r\n?/g, "\n"));
      return;
    }
    if (!(node instanceof Element)) return;
    const tag = node.localName;
    // A table nested in a cell is not part of this cell's text; its own rows are not copied either.
    // MathML keeps the lowercase local name (annotation), HTML the same local name.
    if (SKIP_TAG.has(tag) || tag === "table" || node.classList.contains("katex-html")) return;
    if (tag === "br") {
      parts.push("\n");
      return;
    }
    node.childNodes.forEach(walk);
  };
  if (cell instanceof Element && (cell.tagName === "TD" || cell.tagName === "TH")) cell.childNodes.forEach(walk);
  else walk(cell);
  return parts.join("").replace(/[ \t]*\n[ \t]*/g, "\n").replace(/\n{2,}/g, "\n").trim();
}

/** Quote a field that would otherwise split into another cell or row. A pipe is ordinary text. */
export function quoteTsvField(value: string): string {
  return /["\t\n\r]/.test(value) ? `"${value.replaceAll('"', '""')}"` : value;
}

/** Header row included, one line per row. Undefined when there is no row to copy. */
export function tableToTsv(root: ParentNode): string | undefined {
  const scope = root instanceof Element && root.tagName === "TABLE" ? root : null;
  const rows = [...root.querySelectorAll("tr")].filter((tr) => !scope || tr.closest("table") === scope);
  if (rows.length === 0) return undefined;
  return rows
    .map((row) =>
      [...row.children]
        .filter((el) => el.tagName === "TH" || el.tagName === "TD")
        .map((cell) => quoteTsvField(cellPlainText(cell)))
        .join("\t"),
    )
    .join("\n");
}

/** Puts both formats on the clipboard. The async API first; on a plain http page (no secure context) the
 *  copy event carries the same two formats, and the textarea selection is what older browsers copy. */
export async function writeTableClipboard(text: string, html: string): Promise<boolean> {
  try {
    const clip = navigator.clipboard;
    if (clip && typeof clip.write === "function" && typeof ClipboardItem !== "undefined") {
      await clip.write([
        new ClipboardItem({
          "text/plain": new Blob([text], { type: "text/plain" }),
          "text/html": new Blob([html], { type: "text/html" }),
        }),
      ]);
      return true;
    }
  } catch {
    // Not a secure context, or this browser refused a two-format write.
  }
  return legacyCopy(text, html);
}

function legacyCopy(text: string, html: string): boolean {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.setAttribute("aria-hidden", "true");
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  const onCopy = (e: ClipboardEvent) => {
    const data = e.clipboardData;
    if (!data) return;
    e.preventDefault();
    data.setData("text/plain", text);
    data.setData("text/html", html);
  };
  document.addEventListener("copy", onCopy);
  try {
    ta.select();
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    document.removeEventListener("copy", onCopy);
    ta.remove();
  }
}
