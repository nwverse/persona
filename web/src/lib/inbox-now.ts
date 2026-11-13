// The manager's inbox state key in the app's words (docs/assistant/inbox.md "Following up"). The key, its
// translations and the shared examples live in spec/fixtures/inbox/now.json; an older hub's missing key, or a newer
// hub's unknown one, leaves each existing state line on its own words.
import { formatDate, t } from "../i18n";
import type { InboxItem } from "./inbox";
import { hm } from "./routines";

// State keys only: the catalogs also hold composition fragments (next, look_again…) and calendar words.
const NOW_KEYS = new Set([
  "confirm_reply", "confirm_agent", "decide", "queued", "looking", "agent_working", "waiting", "follow_up_ask", "check_due", "failed",
  "needs_you", "fyi", "folded", "done_replied", "done_manager", "done_user", "done_read", "done_dismissed", "done_muted", "done_superseded",
]);

/** A follow-up moment in the local zone: "09:00" today, "tomorrow 09:00", then a date and the 24-hour clock. */
function followTime(iso: string, now = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((day(d) - day(now)) / 86_400_000);
  if (days === 0) return hm(d);
  if (days === 1) return `${t("lib-inbox-now.tomorrow")} ${hm(d)}`;
  const opts: Intl.DateTimeFormatOptions = { month: "short", day: "numeric" };
  if (d.getFullYear() !== now.getFullYear()) opts.year = "numeric";
  return `${formatDate(d, opts)} ${hm(d)}`;
}

/** The item's current state in words, or undefined when this app does not know the manager's key. */
export function nowText(item: InboxItem): string | undefined {
  const key = item.now;
  if (!key || !NOW_KEYS.has(key)) return undefined;
  const follow = item.follow;
  if (key === "waiting") {
    const waiting = follow?.who ? t("lib-inbox-now.waiting", { who: follow.who }) : t("lib-inbox-now.waiting_anyone");
    const again = follow?.check_at ? t("lib-inbox-now.look_again", { time: followTime(follow.check_at) }) : "";
    const next = follow?.next ? t("lib-inbox-now.next", { next: follow.next }) : "";
    return `${waiting}${again}${next}`;
  }
  if (key === "check_due") {
    const next = follow?.next ? t("lib-inbox-now.next", { next: follow.next }) : "";
    return `${t("lib-inbox-now.check_due")}${next}`;
  }
  if (key === "failed") {
    return item.handling?.error ? t("lib-inbox-now.failed", { reason: item.handling.error }) : t("lib-inbox-now.failed_plain");
  }
  return t(`lib-inbox-now.${key}`);
}
