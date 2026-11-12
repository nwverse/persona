// When a reply was written, in the words of the bar under it (#5314), ported with changes from cindy's useRelativeTime.ts:
// "just now" under a minute, then whole minutes, hours and days (rounded down), and from a week on the date and time,
// with the year only when it is not this one. The exact time, to the second, is its tooltip.
import { t } from "../i18n";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const WEEK = 7 * DAY;

const pad = (n: number) => String(n).padStart(2, "0");

/** "5 minutes ago", "3 days ago", "11-05 14:30", "2025-11-05 14:30"; "" for a missing or unreadable time. A time ahead of
 *  `now` (another computer's clock) is "just now". */
export function relativeTime(iso: string | undefined, now = Date.now()): string {
  const at = iso ? Date.parse(iso) : NaN;
  if (!Number.isFinite(at)) return "";
  const delta = now - at;
  if (delta < MINUTE) return t("lib-relative-time.just_now");
  if (delta < HOUR) return t("lib-relative-time.minutes_ago", { count: Math.floor(delta / MINUTE) });
  if (delta < DAY) return t("lib-relative-time.hours_ago", { count: Math.floor(delta / HOUR) });
  if (delta < WEEK) return t("lib-relative-time.days_ago", { count: Math.floor(delta / DAY) });
  const d = new Date(at);
  const day = `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  return d.getFullYear() === new Date(now).getFullYear() ? day : `${d.getFullYear()}-${day}`;
}

/** "2026-11-12 14:30:05", local time; "" for a missing or unreadable time. */
export function exactTime(iso: string | undefined): string {
  const at = iso ? Date.parse(iso) : NaN;
  if (!Number.isFinite(at)) return "";
  const d = new Date(at);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
