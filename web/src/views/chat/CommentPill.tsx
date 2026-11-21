import { createPortal } from "react-dom";
import { t } from "../../i18n";
import { composing } from "../../lib/send-history";
import { focusKind } from "../../lib/shortcuts";
import { EASE } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export function isCommentShortcut(e: KeyboardEvent): boolean {
  return !e.defaultPrevented && e.key.toLowerCase() === "c" && !e.metaKey && !e.ctrlKey &&
    !e.altKey && !e.shiftKey && !e.repeat && !composing(e) && focusKind(e.target) === "page";
}

export function CommentPill({ at, onComment }: { at: { x: number; y: number }; onComment: () => void }) {
  return createPortal(
    <button
      type="button"
      data-comment-pill
      aria-label={t("chat-comments.pill_label")}
      onPointerDown={(e) => e.preventDefault()}
      onMouseDown={(e) => e.preventDefault()}
      onClick={onComment}
      className="fixed z-40 flex h-8 items-center gap-1.5 rounded-full bg-tooltip-bg pr-2 pl-3 text-[12.5px] font-medium whitespace-nowrap text-tooltip-fg shadow-overlay"
      style={{ left: at.x, top: at.y, animation: `pop-in 140ms ${EASE} both` }}
    >
      <Icon name="chat" size={13} />
      {t("chat-comments.pill")}
      <kbd className="rounded-[4px] border border-tooltip-border px-1 font-sans text-[10.5px] leading-[16px] text-tooltip-muted">C</kbd>
    </button>,
    document.body,
  );
}
