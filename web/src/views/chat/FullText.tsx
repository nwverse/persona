// "View full text (12 KB)" under a text the hub cut for the page (Item.full_size; Android FullTextButton, iOS ResultBlock's
// cut): a click fetches the whole item (GET …/items/{id}/full) and it takes the cut one's place; "Loading…" meanwhile,
// "Failed to load, tap to retry" when it fails. The hub's own note at the end of the cut text goes: the button says it.
import { useRef, useState, type ReactNode } from "react";
import { fullItem } from "../../lib/api";
import { bytes } from "../../lib/format";
import { withoutCutMark } from "../../lib/tool-detail";
import type { Item } from "../../lib/types";
import { cx, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

/** The visible text and full-text control. `getText` shares that control's pending load and cached complete text. */
export function useFullText(item: Item | undefined, mid: string | undefined, sid: string | undefined): { text: string; more: ReactNode; getText: () => Promise<string> } {
  const key = item?.full_size ? JSON.stringify([mid, sid, item.id, item.text, item.full_size]) : "";
  const current = useRef<{ key: string; promise?: Promise<string> }>({ key });
  if (current.current.key !== key) current.current = { key };
  const frame = current.current;
  const [value, setValue] = useState<{ key: string; text?: string; state?: "loading" | "failed" }>();
  const loaded = value?.key === key ? value : undefined;
  const raw = item?.text ?? "";
  const size = item?.full_size;
  const getText = (): Promise<string> => {
    if (loaded?.text !== undefined) return Promise.resolve(loaded.text);
    if (!size) return Promise.resolve(raw);
    if (!item || !mid || !sid) return Promise.reject(new Error(t("chat-fulltext.failed")));
    if (frame.promise) return frame.promise;
    setValue({ key, state: "loading" });
    const promise = fullItem(mid, sid, item.id).then((it) => {
      const text = it.text ?? "";
      if (current.current === frame) setValue({ key, text });
      return text;
    }, (error: unknown) => {
      if (current.current === frame) setValue({ key, state: "failed" });
      throw error;
    }).finally(() => {
      if (frame.promise === promise) frame.promise = undefined;
    });
    frame.promise = promise;
    return promise;
  };
  const paged = size && mid && sid;
  return {
    text: loaded?.text ?? (paged ? withoutCutMark(raw) : raw),
    more: loaded?.text === undefined && paged ? <FullTextButton size={size!} state={loaded?.state ?? "idle"} onClick={() => { void getText().catch(() => {}); }} /> : null,
    getText,
  };
}

function FullTextButton({ size, state, onClick }: { size: number; state: "idle" | "loading" | "failed"; onClick: () => void }) {
  const loading = state === "loading";
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        if (!loading) onClick();
      }}
      disabled={loading}
      aria-busy={loading || undefined}
      className={cx(
        "mt-1.5 flex h-7 w-fit items-center gap-1.5 rounded-control px-1 font-sans text-[12.5px] font-medium whitespace-nowrap transition-colors hover:bg-hover disabled:opacity-60",
        state === "failed" ? "text-red" : "text-accent-ink",
      )}
      data-full-text={state}
    >
      {loading ? <Spinner size={12} /> : <Icon name={state === "failed" ? "retry" : "external"} size={12} />}
      {loading ? t("chat-fulltext.loading") : state === "failed" ? t("chat-fulltext.failed") : t("chat-fulltext.show_all", { size: bytes(size) })}
    </button>
  );
}
