import { useCallback, useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { scrollBehavior } from "../../ui/stream/motion";

export interface ChatFindBarProps {
  open: boolean;
  onClose: () => void;
  containerRef: React.RefObject<HTMLElement | null>;
}

const SKIP_TAGS = new Set(["SCRIPT", "STYLE", "SVG", "TEXTAREA", "INPUT"]);
const MAX_MATCHES = 5000;
const HIGHLIGHT_MATCH = "chat-find-match";
const HIGHLIGHT_ACTIVE = "chat-find-active";
const STYLE_ID = "persona-chat-find-highlight-style";

interface HighlightRegistryLike {
  set(name: string, highlight: unknown): void;
  delete(name: string): void;
}

interface HighlightGlobal {
  CSS?: { highlights?: HighlightRegistryLike };
  Highlight?: new (...ranges: AbstractRange[]) => unknown;
}

function getHighlightApi(): { registry: HighlightRegistryLike; Ctor: new (...ranges: AbstractRange[]) => unknown } | null {
  const g = globalThis as unknown as HighlightGlobal;
  if (g.CSS?.highlights && typeof g.Highlight === "function") {
    return { registry: g.CSS.highlights, Ctor: g.Highlight };
  }
  return null;
}

function ensureHighlightStyles(): void {
  if (typeof document === "undefined" || document.getElementById(STYLE_ID)) return;
  const style = document.createElement("style");
  style.id = STYLE_ID;
  style.textContent = [
    `::highlight(${HIGHLIGHT_MATCH}) { background-color: color-mix(in srgb, var(--accent, #2563eb) 22%, transparent); color: inherit; }`,
    `::highlight(${HIGHLIGHT_ACTIVE}) { background-color: color-mix(in srgb, var(--accent, #2563eb) 52%, transparent); color: inherit; }`,
  ].join("\n");
  document.head.appendChild(style);
}

function clearHighlights(): void {
  const api = getHighlightApi();
  if (!api) return;
  api.registry.delete(HIGHLIGHT_MATCH);
  api.registry.delete(HIGHLIGHT_ACTIVE);
}

function applyHighlights(ranges: Range[], activeIndex: number): void {
  const api = getHighlightApi();
  if (!api) return;
  if (ranges.length === 0) {
    api.registry.delete(HIGHLIGHT_MATCH);
    api.registry.delete(HIGHLIGHT_ACTIVE);
    return;
  }
  ensureHighlightStyles();
  api.registry.set(HIGHLIGHT_MATCH, new api.Ctor(...ranges));
  if (activeIndex >= 0 && activeIndex < ranges.length) {
    api.registry.set(HIGHLIGHT_ACTIVE, new api.Ctor(ranges[activeIndex]));
  } else {
    api.registry.delete(HIGHLIGHT_ACTIVE);
  }
}

function scrollRangeIntoView(range: Range, container: HTMLElement | null): void {
  const behavior = scrollBehavior();

  const rect = typeof range.getBoundingClientRect === "function" ? range.getBoundingClientRect() : null;
  if (
    container &&
    rect &&
    (rect.height > 0 || rect.width > 0) &&
    container.scrollHeight > container.clientHeight &&
    typeof container.scrollTo === "function"
  ) {
    const cRect = container.getBoundingClientRect();
    const top = container.scrollTop + (rect.top - cRect.top) - container.clientHeight / 2 + rect.height / 2;
    container.scrollTo({ top: Math.max(0, top), behavior });
    return;
  }

  const startNode = range.startContainer;
  const el = startNode.nodeType === 1 ? (startNode as Element) : startNode.parentElement;
  el?.scrollIntoView?.({ block: "center", behavior });
}

/**
 * Finds all case-insensitive text ranges matching `query` within `root`,
 * skipping subtrees with `[data-chat-find-skip]` and `<script>`, `<style>`,
 * `<svg>`, `<textarea>`, `<input>`.
 */
export function findTextRanges(root: Node, query: string): Range[] {
  if (!query) return [];
  const needle = query.toLowerCase();
  const doc = root.ownerDocument ?? (typeof document !== "undefined" ? document : null);
  if (!doc || typeof doc.createRange !== "function") return [];

  const ranges: Range[] = [];

  const walk = (node: Node) => {
    if (ranges.length >= MAX_MATCHES) return;

    if (node.nodeType === 1) {
      const el = node as Element;
      const tag = el.tagName?.toUpperCase() ?? "";
      if (SKIP_TAGS.has(tag)) return;
      if (typeof el.hasAttribute === "function" && el.hasAttribute("data-chat-find-skip")) return;
      const children = node.childNodes;
      for (let i = 0; i < children.length; i++) {
        walk(children[i]);
        if (ranges.length >= MAX_MATCHES) return;
      }
      return;
    }

    if (node.nodeType === 3) {
      const text = node.nodeValue;
      if (!text) return;
      const lower = text.toLowerCase();
      let pos = 0;
      while (ranges.length < MAX_MATCHES) {
        const idx = lower.indexOf(needle, pos);
        if (idx === -1) break;
        const range = doc.createRange();
        range.setStart(node, idx);
        range.setEnd(node, idx + needle.length);
        ranges.push(range);
        pos = idx + needle.length;
      }
      return;
    }

    // Document / DocumentFragment
    const children = node.childNodes;
    if (children) {
      for (let i = 0; i < children.length; i++) {
        walk(children[i]);
        if (ranges.length >= MAX_MATCHES) return;
      }
    }
  };

  walk(root);
  return ranges;
}

export function ChatFindBar({ open, onClose, containerRef }: ChatFindBarProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const rangesRef = useRef<Range[]>([]);
  const [query, setQuery] = useState("");
  const [matchCount, setMatchCount] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);

  // Auto-focus and select existing text when opened; clear highlights when closed.
  useEffect(() => {
    if (!open) {
      rangesRef.current = [];
      clearHighlights();
      return;
    }
    const input = inputRef.current;
    if (input) {
      input.focus();
      input.select();
    }
    return () => {
      clearHighlights();
    };
  }, [open]);

  // Re-run search when opened or query changes.
  useEffect(() => {
    if (!open) return;
    const container = containerRef.current;
    if (!container || !query) {
      rangesRef.current = [];
      setMatchCount(0);
      setActiveIndex(0);
      clearHighlights();
      return;
    }

    const found = findTextRanges(container, query);
    rangesRef.current = found;
    setMatchCount(found.length);
    const nextIdx = found.length > 0 ? 0 : -1;
    setActiveIndex(nextIdx >= 0 ? nextIdx : 0);
    applyHighlights(found, nextIdx);
    if (found.length > 0) {
      scrollRangeIntoView(found[0], container);
    }
  }, [open, query, containerRef]);

  const step = useCallback(
    (delta: number) => {
      const ranges = rangesRef.current;
      if (ranges.length === 0) return;
      setActiveIndex((prev) => {
        const next = (prev + delta + ranges.length) % ranges.length;
        applyHighlights(ranges, next);
        scrollRangeIntoView(ranges[next], containerRef.current);
        return next;
      });
    },
    [containerRef],
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Escape") {
        e.preventDefault();
        clearHighlights();
        onClose();
      } else if (e.key === "Enter") {
        e.preventDefault();
        step(e.shiftKey ? -1 : 1);
      }
    },
    [onClose, step],
  );

  if (!open) return null;

  const counterText = !query ? "" : matchCount > 0 ? `${activeIndex + 1} / ${matchCount}` : t("chat-findbar.no_matches");

  return (
    <div
      role="search"
      aria-label={t("chat-findbar.region_aria")}
      data-chat-find-skip="true"
      className="absolute top-2.5 right-4 z-30 flex items-center gap-1.5 rounded-[10px] border border-line-strong bg-surface/95 backdrop-blur-md px-2.5 py-1.5 shadow-overlay"
    >
      <Icon name="search" size={14} className="shrink-0 text-ink-3" />
      <input
        ref={inputRef}
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={t("chat-findbar.placeholder")}
        aria-label={t("chat-findbar.input_aria")}
        className="w-40 sm:w-52 bg-transparent text-[12.5px] text-ink placeholder:text-ink-3 outline-none"
      />
      {counterText && (
        <span
          aria-live="polite"
          className="shrink-0 select-none px-1 text-[11px] tabular-nums text-ink-3"
        >
          {counterText}
        </span>
      )}
      <div className="flex items-center gap-0.5 border-l border-line pl-1">
        <IconButton
          icon="up"
          label={t("chat-findbar.prev_match")}
          size={24}
          iconSize={13}
          disabled={matchCount === 0}
          onClick={() => step(-1)}
        />
        <IconButton
          icon="arrowDown"
          label={t("chat-findbar.next_match")}
          size={24}
          iconSize={13}
          disabled={matchCount === 0}
          onClick={() => step(1)}
        />
        <IconButton
          icon="x"
          label={t("chat-findbar.close")}
          size={24}
          iconSize={13}
          onClick={() => {
            clearHighlights();
            onClose();
          }}
        />
      </div>
    </div>
  );
}
