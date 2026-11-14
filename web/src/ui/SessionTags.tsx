import { cx } from "./atoms";
import { Icon } from "./icons";

/** One bounded metadata line for a session's presentation tags, shared by grouped and chat-list rows. */
export function SessionTags({ tags, className }: { tags: readonly string[] | undefined; className?: string }) {
  if (!tags?.length) return null;
  const text = tags.join(" · ");
  return (
    <span data-session-tags title={text} aria-label={text} className={cx("flex h-4 min-w-0 items-center gap-1 text-xs leading-4 text-ink-3", className)}>
      <Icon name="tag" size={10} stroke={2} className="shrink-0" />
      <span className="truncate">{text}</span>
    </span>
  );
}
