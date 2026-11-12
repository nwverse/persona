// The bar under a finished turn's last reply (#5314), after cindy's MessageActionBar as it sits under an assistant
// message, at the owner's request (2026-11-12): Copy message (a ✓ once copied, the copy icon again on the next hover),
// Start a new session where the session can branch, ⋯ More message actions (Quote, Forward, Use as context), then when
// the reply was written ("5 minutes ago", the exact time as its tooltip). The pointer over the reply or keyboard focus in
// the bar shows it; it stays REPLY_BAR_LINGER_MS after the pointer leaves, then fades (at once with reduced motion), and
// an open ⋯ menu keeps it. Narration inside a work summary and a reply still streaming have no bar: their right-click /
// long-press menu (MessageActions) has every action, and touch screens have MessageActions' copy + ⋯ row instead.
import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { exactTime, relativeTime } from "../../lib/relative-time";
import { copyText, cx, Menu } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { REPLY_BAR_FADE_MS, REPLY_BAR_LINGER_MS } from "../../ui/motion";
import type { Action } from "./MessageActions";

/** How often the time is worked out again while the pointer is over the reply (cindy's 30 s): only then, so a long chat
 *  holds no timer per reply. */
const TIME_TICK_MS = 30_000;

const BUTTON = "flex size-6 shrink-0 items-center justify-center rounded-full text-ink-3 transition-colors hover:bg-hover-2";

/** `hovered`: the pointer is over the reply (MessageActions). `fork`: "Start a new session", when the session can.
 *  `more`: the ⋯ menu's actions; no ⋯ without any. */
export function ReplyBar({ text, ts, hovered, fork, more }: { text: string; ts?: string; hovered: boolean; fork?: Action; more: Action[] }) {
  const [visible, setVisible] = useState(hovered);
  const [copied, setCopied] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [menu, setMenu] = useState<HTMLElement | null>(null);

  useEffect(() => {
    if (!hovered) {
      // a quick way back in cancels this, so the bar does not flicker
      const linger = window.setTimeout(() => setVisible(false), REPLY_BAR_LINGER_MS);
      return () => clearTimeout(linger);
    }
    setVisible(true);
    // the ✓ goes on the way back in, not as the bar fades (it would snap back mid-fade)
    setCopied(false);
    setNow(Date.now());
    const tick = window.setInterval(() => setNow(Date.now()), TIME_TICK_MS);
    return () => clearInterval(tick);
  }, [hovered]);

  const time = relativeTime(ts, now);
  const copyLabel = t("chat-messageactions.copy_message");
  const moreLabel = t("chat-messageactions.more_actions");
  return (
    <div
      data-reply-bar
      // hidden on touch screens, which have the copy + ⋯ row (MessageActions); the keyboard shows it (:focus-visible,
      // so a click does not keep it after the pointer leaves)
      className={cx(
        "mt-1.5 hidden h-6 items-center gap-0.5 transition-opacity [@media(hover:hover)]:flex",
        visible || menu ? "opacity-100" : "pointer-events-none opacity-0 has-[:focus-visible]:pointer-events-auto has-[:focus-visible]:opacity-100",
      )}
      style={{ transitionDuration: `${REPLY_BAR_FADE_MS}ms` }}
      onFocus={() => setNow(Date.now())}
    >
      <button
        type="button"
        aria-label={copyLabel}
        title={copyLabel}
        onClick={async () => {
          if (await copyText(text, { quiet: true })) setCopied(true);
        }}
        className={cx(BUTTON, !copied && "hover:text-ink")}
      >
        <Icon name={copied ? "check" : "copy"} size={14} />
      </button>
      {fork && (
        <button type="button" aria-label={fork.label} title={fork.label} onClick={() => void fork.onSelect()} className={cx(BUTTON, "hover:text-ink")}>
          <Icon name={fork.icon} size={14} />
        </button>
      )}
      {more.length > 0 && (
        <button
          type="button"
          aria-label={moreLabel}
          title={moreLabel}
          aria-haspopup="menu"
          aria-expanded={Boolean(menu)}
          onClick={(e) => setMenu(menu ? null : e.currentTarget)}
          className={cx(BUTTON, "hover:text-ink", menu && "bg-hover-2 text-ink")}
        >
          <Icon name="more" size={14} />
        </button>
      )}
      {time && (
        <time dateTime={ts} title={exactTime(ts)} className="flex h-6 cursor-default items-center text-[12px] whitespace-nowrap text-ink-3">
          {time}
        </time>
      )}
      {menu && <Menu anchor={menu} onClose={() => setMenu(null)} items={more} width={more.some((a) => a.hint) ? 200 : 184} />}
    </div>
  );
}
