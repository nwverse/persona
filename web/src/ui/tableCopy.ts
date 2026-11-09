// A rendered Markdown table, as a spreadsheet paste. text/plain is tab-separated rows (a spreadsheet splits
// them into cells); text/html is the table itself (a document keeps the table). A cell that contains a tab,
// newline or quote is quoted the way spreadsheets expect, so it stays one cell.

const SKIP_TAG = new Set(["script", "style", "annotation"]);

/** The words a cell shows: a link's label (not its address), inline code without its fences, an image's alt text.
 *  A <br> is a line break. KaTeX draws the formula twice; the spreadsheet gets the TeX source (the annotation),
 *  not MathML with the operators dropped. */
export function cellPlainText(cell: Node): string {
  const parts: string[] = [];
  const walk = (node: Node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      parts.push((node.textContent ?? "").replace(/\r\n?/g, "\n"));
      return;
    }
    if (!(node instanceof Element)) return;
    const tag = node.localName;
    // A nested table is not this cell's text (its rows are not copied as extra lines either).
    if (SKIP_TAG.has(tag) || tag === "table") return;
    if (node.classList.contains("katex")) {
      const tex = node.querySelector("annotation")?.textContent?.trim();
      if (tex) {
        parts.push(tex);
        return;
      }
      const visual = node.querySelector(".katex-html");
      if (visual) {
        visual.childNodes.forEach(walk);
        return;
      }
    }
    if (node.classList.contains("katex-html")) return;
    if (tag === "img") {
      parts.push(node.getAttribute("alt") ?? "");
      return;
    }
    if (tag === "br") {
      parts.push("\n");
      return;
    }
    node.childNodes.forEach(walk);
  };
  if (cell instanceof Element && (cell.tagName === "TD" || cell.tagName === "TH")) cell.childNodes.forEach(walk);
  else walk(cell);
  return parts.join("").replace(/[ \t]*\n[ \t]*/g, "\n").trim();
}

/** Quote a field that would otherwise split into another cell or row. A pipe is ordinary text.
 *  A cell that starts like a spreadsheet formula is prefixed so the paste stays text. */
export function quoteTsvField(value: string): string {
  const safe = /^[=+@]/.test(value) ? `'${value}` : value;
  return /["\t\n\r]/.test(safe) ? `"${safe.replaceAll('"', '""')}"` : safe;
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

/** The table's HTML for the clipboard: one drawing of each formula (the visual span, not MathML as well), and no
 *  device token. File links in a session chat carry `?token=` because an <img> can't send a header; the pasted
 *  markup must not. */
export function tableHtml(table: HTMLTableElement): string {
  const copy = table.cloneNode(true) as HTMLTableElement;
  copy.querySelectorAll(".katex-mathml").forEach((el) => el.remove());
  for (const el of copy.querySelectorAll("a[href], img[src], video[src], audio[src], source[src]")) {
    for (const attr of ["href", "src"] as const) {
      const value = el.getAttribute(attr);
      if (!value) continue;
      const next = withoutToken(value);
      if (next !== value) el.setAttribute(attr, next);
    }
  }
  return copy.outerHTML;
}

/** Drop a `token` query. Anything else (path, cwd, inline) stays, so the address still names the file. */
export function withoutToken(url: string): string {
  let parsed: URL;
  try {
    parsed = new URL(url, "http://clipboard.invalid");
  } catch {
    return url;
  }
  if (!parsed.searchParams.has("token")) return url;
  parsed.searchParams.delete("token");
  const tail = `${parsed.pathname}${parsed.search}${parsed.hash}`;
  if (/^https?:\/\//i.test(url)) return parsed.toString();
  if (url.startsWith("//")) return `//${parsed.host}${tail}`;
  return tail;
}

/** Puts both formats on the clipboard. The two-format write first; if this browser refuses it, the plain TSV
 *  (what a spreadsheet needs) still goes through writeText. A plain http page has neither, so the copy event
 *  carries both formats and the textarea selection is the TSV. */
export async function writeTableClipboard(text: string, html: string): Promise<boolean> {
  const clip = navigator.clipboard;
  if (clip && typeof clip.write === "function" && typeof ClipboardItem !== "undefined") {
    try {
      await clip.write([
        new ClipboardItem({
          "text/plain": new Blob([text], { type: "text/plain" }),
          "text/html": new Blob([html], { type: "text/html" }),
        }),
      ]);
      return true;
    } catch {
      // Not a secure context, or this browser refused a two-format write.
    }
  }
  if (clip && typeof clip.writeText === "function") {
    try {
      await clip.writeText(text);
      return true;
    } catch {
      // The plain write was refused too.
    }
  }
  return legacyCopy(text, html);
}

function legacyCopy(text: string, html: string): boolean {
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.setAttribute("aria-hidden", "true");
  ta.tabIndex = -1;
  ta.style.position = "fixed";
  ta.style.top = "0";
  ta.style.left = "0";
  ta.style.width = "1px";
  ta.style.height = "1px";
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
    // select() moves focus to the field. Put it back, without scrolling the chat to the bottom.
    if (previous?.isConnected) previous.focus({ preventScroll: true });
  }
}
