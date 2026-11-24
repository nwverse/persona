import { useCallback, useEffect, useState } from "react";
import { t } from "../../i18n";
import { Icon } from "../../ui/icons";

export interface SelectionQuotePillProps {
  containerRef: React.RefObject<HTMLElement | null>;
  onQuote: (text: string) => void;
  disabled?: boolean;
}

interface PillState {
  text: string;
  x: number;
  y: number;
}

function isEditableElement(el: Element | null): boolean {
  if (!el) return false;
  const tag = el.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA") return true;
  if ((el as HTMLElement).isContentEditable) return true;
  return Boolean(el.closest("input, textarea, [contenteditable='true'], [contenteditable='']"));
}

function nodeAsElement(node: Node | null): Element | null {
  if (!node) return null;
  return node.nodeType === 1 ? (node as Element) : node.parentElement;
}

function isInsideQuoteSource(node: Node | null, container: HTMLElement): boolean {
  const el = nodeAsElement(node);
  if (!el || !container.contains(el)) return false;
  if (isEditableElement(el)) return false;
  const source = el.closest("[data-chat-quote-source]");
  return Boolean(source && container.contains(source));
}

export function SelectionQuotePill({ containerRef, onQuote, disabled }: SelectionQuotePillProps) {
  const [pill, setPill] = useState<PillState | null>(null);

  const computeSelection = useCallback(() => {
    if (disabled) {
      setPill(null);
      return;
    }
    const container = containerRef.current;
    if (!container || typeof window === "undefined") {
      setPill(null);
      return;
    }

    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
      setPill(null);
      return;
    }

    const text = sel.toString().trim();
    if (text.length < 2) {
      setPill(null);
      return;
    }

    if (
      !isInsideQuoteSource(sel.anchorNode, container) ||
      !isInsideQuoteSource(sel.focusNode, container)
    ) {
      setPill(null);
      return;
    }

    const rect = sel.getRangeAt(0).getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) {
      setPill(null);
      return;
    }

    const padX = 56;
    const x = Math.min(Math.max(rect.left + rect.width / 2, padX), Math.max(padX, window.innerWidth - padX));
    const preferredTop = rect.top - 40;
    const y =
      preferredTop >= 8
        ? preferredTop
        : Math.min(rect.bottom + 8, Math.max(8, window.innerHeight - 40));

    setPill({ text, x, y });
  }, [containerRef, disabled]);

  const triggerQuote = useCallback(
    (text: string) => {
      onQuote(text);
      window.getSelection()?.removeAllRanges();
      setPill(null);
    },
    [onQuote],
  );

  useEffect(() => {
    if (disabled) {
      setPill(null);
      return;
    }

    const onSelectionChange = () => computeSelection();
    const onPointerUp = () => computeSelection();
    const onKeyUp = () => computeSelection();
    const onScroll = () => computeSelection();

    document.addEventListener("selectionchange", onSelectionChange);
    document.addEventListener("mouseup", onPointerUp);
    document.addEventListener("keyup", onKeyUp);
    window.addEventListener("scroll", onScroll, { passive: true, capture: true });
    return () => {
      document.removeEventListener("selectionchange", onSelectionChange);
      document.removeEventListener("mouseup", onPointerUp);
      document.removeEventListener("keyup", onKeyUp);
      window.removeEventListener("scroll", onScroll, true);
    };
  }, [computeSelection, disabled]);

  useEffect(() => {
    if (!pill || disabled) return;

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key.toLowerCase() !== "c") return;
      if (isEditableElement(document.activeElement)) return;
      e.preventDefault();
      triggerQuote(pill.text);
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [pill, disabled, triggerQuote]);

  if (!pill || disabled) return null;

  return (
    <button
      type="button"
      data-chat-find-skip="true"
      aria-label={t("chat-selectionquote.aria")}
      onMouseDown={(e) => e.preventDefault()}
      onClick={() => triggerQuote(pill.text)}
      style={{ left: pill.x, top: pill.y }}
      className="fixed z-40 -translate-x-1/2 flex items-center gap-1.5 rounded-full border border-line-strong bg-surface/95 backdrop-blur-md px-2.5 py-1 text-[12px] font-medium text-ink shadow-overlay transition-[background-color,transform] duration-150 hover:bg-hover active:scale-[0.96]"
    >
      <Icon name="quote" size={13} className="text-ink-2" />
      <span>{t("chat-selectionquote.label")}</span>
      <kbd className="hidden sm:inline-flex h-4 min-w-4 items-center justify-center rounded-[4px] border border-line bg-inset px-1 font-sans text-[10px] font-medium text-ink-3">
        C
      </kbd>
    </button>
  );
}
