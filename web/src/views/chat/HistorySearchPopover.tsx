import { useEffect, useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import { cx, EASE } from "../../ui/atoms";

export interface HistoryEntry {
  text: string;
  fromCurrent: boolean;
}

const HISTORY_PREFIX = "chat-history:";

function readStoredList(key: string): string[] {
  if (typeof sessionStorage === "undefined") return [];
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === "string" && x.trim().length > 0) : [];
  } catch {
    return [];
  }
}

/**
 * Reads all `chat-history:*` arrays from `sessionStorage`, orders newest first,
 * puts current session entries first, and deduplicates keeping the newest occurrence.
 */
export function collectSessionHistory(currentDraftKey: string): HistoryEntry[] {
  if (typeof sessionStorage === "undefined") return [];
  const currentKey = `${HISTORY_PREFIX}${currentDraftKey}`;
  const seen = new Set<string>();
  const out: HistoryEntry[] = [];

  const pushNewestFirst = (items: string[], fromCurrent: boolean) => {
    for (let i = items.length - 1; i >= 0; i--) {
      const text = items[i];
      if (seen.has(text)) continue;
      seen.add(text);
      out.push({ text, fromCurrent });
    }
  };

  pushNewestFirst(readStoredList(currentKey), true);

  try {
    const otherKeys: string[] = [];
    for (let i = 0; i < sessionStorage.length; i++) {
      const k = sessionStorage.key(i);
      if (k && k.startsWith(HISTORY_PREFIX) && k !== currentKey) otherKeys.push(k);
    }
    for (const k of otherKeys) {
      pushNewestFirst(readStoredList(k), false);
    }
  } catch {
    // ignore storage iteration errors
  }

  return out;
}

/** Multi-token case-insensitive substring match. */
export function filterHistory(entries: readonly HistoryEntry[], query: string): HistoryEntry[] {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return [...entries];
  return entries.filter((entry) => {
    const lower = entry.text.toLowerCase();
    return tokens.every((tok) => lower.includes(tok));
  });
}

export interface HistorySearchPopoverProps {
  open: boolean;
  currentDraftKey: string;
  onSelect: (text: string) => void;
  onClose: () => void;
}

export function HistorySearchPopover({ open, currentDraftKey, onSelect, onClose }: HistorySearchPopoverProps) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const entries = useMemo(() => (open ? collectSessionHistory(currentDraftKey) : []), [open, currentDraftKey]);
  const matches = useMemo(() => filterHistory(entries, query), [entries, query]);
  const safeActive = matches.length > 0 ? Math.min(active, matches.length - 1) : 0;

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setActive(0);
    const id = requestAnimationFrame(() => inputRef.current?.focus());
    return () => cancelAnimationFrame(id);
  }, [open, currentDraftKey]);

  useEffect(() => {
    setActive(0);
  }, [query]);

  useEffect(() => {
    if (!open) return;
    itemRefs.current[safeActive]?.scrollIntoView?.({ block: "nearest" });
  }, [open, safeActive, matches.length]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) onClose();
    };
    document.addEventListener("mousedown", onPointerDown);
    return () => document.removeEventListener("mousedown", onPointerDown);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      ref={rootRef}
      role="dialog"
      aria-label={t("chat-historysearch.dialog_aria")}
      data-chat-find-skip="true"
      className="absolute inset-x-0 bottom-full z-20 mb-2 overflow-hidden rounded-[12px] border border-line-strong bg-surface/95 shadow-overlay backdrop-blur-md"
      style={{ animation: `pop-in 160ms ${EASE} both`, transformOrigin: "bottom center" }}
    >
      <div className="flex items-center gap-2 border-b border-line px-2.5 py-2">
        <span className="shrink-0 font-mono text-[11.5px] font-medium text-ink-2">{t("chat-historysearch.header")}</span>
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing || e.keyCode === 229) return;
            const lower = e.key.toLowerCase();
            const isCtrl = e.ctrlKey && !e.metaKey && !e.altKey;
            if (e.key === "ArrowUp" || (isCtrl && lower === "p")) {
              e.preventDefault();
              if (matches.length > 0) setActive((i) => (i - 1 + matches.length) % matches.length);
              return;
            }
            if (e.key === "ArrowDown" || (isCtrl && (lower === "n" || lower === "r"))) {
              e.preventDefault();
              if (matches.length > 0) setActive((i) => (i + 1) % matches.length);
              return;
            }
            if (e.key === "Enter" || e.key === "Tab") {
              e.preventDefault();
              const picked = matches[safeActive];
              if (picked) onSelect(picked.text);
              return;
            }
            if (e.key === "Escape" || (isCtrl && lower === "g")) {
              e.preventDefault();
              e.stopPropagation();
              onClose();
            }
          }}
          placeholder={t("chat-historysearch.placeholder")}
          aria-label={t("chat-historysearch.input_aria")}
          className="min-w-0 flex-1 bg-transparent text-[13px] text-ink outline-none placeholder:text-ink-3"
        />
        <span className="shrink-0 text-[11px] text-ink-3 tabular-nums">{t("chat-historysearch.count", { count: matches.length })}</span>
      </div>

      <div className="max-h-56 overflow-y-auto p-1">
        {entries.length === 0 ? (
          <div className="px-2.5 py-5 text-center text-[12px] text-ink-3">{t("chat-historysearch.empty")}</div>
        ) : matches.length === 0 ? (
          <div className="px-2.5 py-5 text-center text-[12px] text-ink-3">{t("chat-historysearch.no_matches")}</div>
        ) : (
          matches.map((entry, idx) => {
            const isActive = idx === safeActive;
            return (
              <button
                key={`${entry.fromCurrent ? "c" : "o"}:${idx}:${entry.text.slice(0, 32)}`}
                ref={(el) => {
                  itemRefs.current[idx] = el;
                }}
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onMouseEnter={() => setActive(idx)}
                onClick={() => onSelect(entry.text)}
                className={cx(
                  "flex w-full items-start justify-between gap-2 rounded-[7px] px-2.5 py-1.5 text-left transition-colors",
                  isActive ? "bg-hover text-ink" : "text-ink-2 hover:bg-hover/60",
                )}
              >
                <span className="line-clamp-2 min-w-0 flex-1 text-[12.5px] leading-[1.45] whitespace-pre-wrap [overflow-wrap:anywhere]">
                  {entry.text}
                </span>
                {entry.fromCurrent && (
                  <span className="mt-0.5 shrink-0 rounded-[4px] bg-inset px-1.5 py-0.5 text-[10.5px] text-ink-3">
                    {t("chat-historysearch.current_session")}
                  </span>
                )}
              </button>
            );
          })
        )}
      </div>

      <div className="border-t border-line px-2.5 py-1.5 text-[11px] text-ink-3">
        {t("chat-historysearch.footer_hint")}
      </div>
    </div>
  );
}
