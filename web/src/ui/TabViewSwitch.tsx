import { t } from "../i18n";
import type { SessionView } from "../lib/router";
import { cx, IconButton } from "./atoms";
import { Icon } from "./icons";
import { SegmentedTrack } from "./Segmented";

export function TabViewSwitch({ live = true, view, onChange, chatDot, termDot }: {
  live?: boolean; view: SessionView; onChange: (view: SessionView) => void; chatDot?: boolean; termDot?: boolean;
}) {
  if (!live) {
    const next = view === "chat" ? "term" : "chat";
    return (
      <span className="relative flex shrink-0">
        <IconButton icon={next === "term" ? "terminal" : "chat"} label={t(next === "term" ? "chat-chatview.terminal" : "chat-chatview.chat")} onClick={() => onChange(next)} />
        {(next === "term" ? termDot : chatDot) && <span aria-hidden data-terminal-waits={next === "term" || undefined} className="pointer-events-none absolute top-0.5 right-0.5 size-1.5 rounded-full bg-attention" />}
      </span>
    );
  }
  return (
    <SegmentedTrack role="radiogroup" label={t("terminal-full.tab_view")} className="flex shrink-0 gap-0.5 rounded-control p-0.5" thumbClassName="rounded-[7px]">
      {(["chat", "term"] as const).map((v) => {
        const label = t(v === "chat" ? "terminal-full.conversation" : "chat-chatview.terminal");
        return (
          <button key={v} type="button" role="radio" aria-label={label} title={label} aria-checked={v === view} data-selected={v === view} tabIndex={v === view ? 0 : -1}
            onClick={() => onChange(v)}
            onKeyDown={(e) => {
              if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
              e.preventDefault();
              const next = e.currentTarget.parentElement?.querySelector<HTMLButtonElement>('button[data-selected="false"]');
              next?.focus();
              next?.click();
            }}
            className={cx("relative z-[1] flex h-7 min-w-7 [@media(pointer:coarse)]:h-11 [@media(pointer:coarse)]:min-w-11 items-center justify-center gap-1 rounded-[7px] px-1.5 text-label whitespace-nowrap @3xl:px-2", v === view ? "font-medium text-ink" : "text-ink-3 hover:text-ink-2")}>
            <Icon name={v === "chat" ? "chat" : "terminal"} size={14} />
            <span className="hidden @3xl:inline">{label}</span>
            {v !== view && (v === "chat" ? chatDot : termDot) && <span aria-hidden data-terminal-waits={v === "term" || undefined} className="pointer-events-none absolute top-0.5 right-0.5 size-1.5 rounded-full bg-attention" />}
          </button>
        );
      })}
    </SegmentedTrack>
  );
}
