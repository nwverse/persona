import { afterEach, describe, expect, it } from "vitest";
import { initI18n } from "../i18n";
import { exactTime, relativeTime } from "./relative-time";

// The reply bar's time (#5314) in cindy's words (useRelativeTime.ts).
const now = new Date(2026, 10, 12, 15, 0, 0).getTime();
const before = (ms: number) => new Date(now - ms).toISOString();
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

afterEach(() => initI18n("zh-Hans"));

describe("relativeTime", () => {
  it.each([
    ["en", ["just now", "1 minute ago", "5 minutes ago", "1 hour ago", "23 hours ago", "1 day ago", "6 days ago"]],
    ["zh-Hans", ["刚刚", "1 分钟前", "5 分钟前", "1 小时前", "23 小时前", "1 天前", "6 天前"]],
    ["ja", ["たった今", "1分前", "5分前", "1時間前", "23時間前", "1日前", "6日前"]],
  ])("rounds down to whole minutes, hours and days in %s", async (lang, words) => {
    await initI18n(lang);
    const ages = [59_999, MIN, 5 * MIN + 59_000, HOUR, DAY - 1, DAY, 7 * DAY - 1];
    expect(ages.map((ms) => relativeTime(before(ms), now))).toEqual(words);
  });

  it("gives the date and time from a week on, with the year only when it is another one", () => {
    expect(relativeTime(new Date(2026, 10, 5, 9, 7).toISOString(), now)).toBe("11-05 09:07");
    expect(relativeTime(new Date(2025, 0, 2, 23, 59).toISOString(), now)).toBe("2025-01-02 23:59");
  });

  it("says just now for a time ahead of this computer's clock, and nothing for none", () => {
    expect(relativeTime(new Date(now + 5 * MIN).toISOString(), now)).toBe("刚刚");
    expect(relativeTime(undefined, now)).toBe("");
    expect(relativeTime("not a time", now)).toBe("");
  });
});

describe("exactTime", () => {
  it("is the local time to the second", () => {
    expect(exactTime(new Date(2026, 10, 12, 9, 5, 3).toISOString())).toBe("2026-11-12 09:05:03");
    expect(exactTime(undefined)).toBe("");
  });
});
