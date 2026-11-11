// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../../i18n";
import { setToken } from "../../lib/api";
import { dayTitle, minutesKeptUntil, recordingClock, type KBDetailResponse } from "../../lib/kb";
import { setRosterDefault } from "../../lib/manager-choice";
import { toast } from "../../ui/atoms";
import { EntryDetail } from "./KBDetail";

vi.mock("../../ui/atoms", async (original) => ({ ...await original<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
type Request = { url: string; init: RequestInit; resolve: (value: Response) => void };
const id = "dddddddd-dddd-dddd-dddd-dddddddddddd";
const meetingId = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee";
const detailPath = `/api/v1/kb/recordings/${id}`;
const minutesPath = `/api/v1/recordings/${id}/minutes`;
// 08:00 in Tokyo: the recap and transcript read the recording's own clock, whatever zone the test runs in.
const started = "2026-11-11T08:00:00+09:00";
let requests: Request[];
let host: HTMLDivElement;
let root: Root;
const saved = vi.fn();
const opened = vi.fn();
const scroll = vi.fn();
const clipboard = vi.fn().mockResolvedValue(undefined);
const fixture = (): KBDetailResponse => ({ detail: {
  kind: "recordings", id, path: `recordings/${id}`,
  meta: {
    title: "Day · Nov 11", kind: "day", day: "2026-11-11", tz: "Asia/Tokyo", part: 1, started_at: started, complete: false, device: "watch", revision: 9, base: 1, next: 5,
    recap_state: "ready", coverage: { to: 39600, gaps: [] },
  },
  summary: "Mostly the export release.",
  segments: [
    { start: 3600, end: 3610, text: "Let's start the design review." },
    { start: 3912, end: 3920, text: "The export path drops entries over 4 MB." },
    { start: 16200, end: 16210, text: "Bo will look at the flaky upload test." },
    { start: 16500, end: 16510, text: "" },
    { start: 37000, end: 37010, text: "Wrap up the day." },
  ],
  recap: {
    version: 3, revision: 9, generated_at: "2026-11-11T10:00:41Z", day: "2026-11-11", covers: { from: 0, to: 39600 }, final: false,
    summary: "Mostly the export release.",
    timeline: [
      { from: 16200, to: 16800, kind: "outside", title: "Lunch with the platform team", points: [{ text: "Bo will look at the flaky upload test.", at: 16200, who: "Bo" }] },
      { from: 3600, to: 6900, kind: "meeting", title: "Design review", meeting: { kind: "recordings", id: meetingId, name: "Export design review" }, points: [{ text: "The export path drops entries over 4 MB.", at: 3912, who: "Ada" }] },
      { from: 37000, to: 37600, kind: "meeting", title: "Evening sync", points: [{ text: "Wrap up the day.", at: 37000 }] },
    ],
    actions: [
      { id: "a1", who: "Bo", what: "Look at the flaky upload test.", by: "tomorrow", due_date: "2026-11-12", at: 16200 },
      { id: "a2", what: "Share the review notes.", at: 3912, todo_id: "todo-example" },
    ],
    gaps: [{ from: 8400, to: 9900, reason: "interrupted" }, { from: 12000, to: 12030, reason: "paused" }, { from: 13000, to: 14000, reason: "unheard-of" }],
    partial: false,
    markdown: "# 2026-11-11\n\n## Meetings\n\n- 09:00–09:55 Design review",
  },
} });
const render = (recording = id) => act(() => root.render(<EntryDetail kind="recordings" id={recording} reloadKey={0} onToggleSidebar={() => {}} onBack={() => {}} onOpen={opened} onSaved={saved} />));
const reply = async (request: Request, body: unknown, status = 200) => act(async () => {
  request.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
});
const load = async (body = fixture()) => { render(); await reply(requests.at(-1)!, body); };
const section = () => host.querySelector<HTMLElement>("[data-recording-recap]")!;
const button = (text: string) => [...(section()?.querySelectorAll("button") ?? [])].find((b) => b.textContent?.trim() === text)!;
const click = (text: string) => act(() => button(text).click());
const writes = () => requests.filter((r) => r.init.method === "POST");
const headings = (tag: string) => [...section().querySelectorAll(tag)].map((h) => h.textContent);

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  setToken("synthetic-owner-a");
  setRosterDefault("assistant");
  vi.clearAllMocks();
  requests = [];
  vi.stubGlobal("fetch", vi.fn((url: string, init: RequestInit) => new Promise<Response>((resolve) => requests.push({ url, init, resolve }))));
  vi.stubGlobal("navigator", { clipboard: { writeText: clipboard } });
  Object.defineProperty(HTMLElement.prototype, "scrollTo", { value: scroll, configurable: true });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  delete (HTMLElement.prototype as { scrollTo?: unknown }).scrollTo;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("an all-day recording's recap in Knowledge detail", () => {
  it("shows the summary, the timeline by part of the day in time order, and the suggested to-dos instead of minutes", async () => {
    await load();
    expect(host.querySelector("[data-recording-minutes]")).toBeNull();
    expect(host.textContent).not.toContain(t("knowledge-kbdetail.minutes_make"));
    expect(headings("h3")).toEqual(["Morning", "Afternoon", "Evening", "Suggested to-dos"]);
    expect(headings("h4")).toEqual(["Design review", "Lunch with the platform team", "Evening sync"]);
    for (const text of ["09:00–09:55", "12:30–12:40", "18:16–18:26", "Ada · The export path drops entries over 4 MB.", "Bo · Look at the flaky upload test. · tomorrow · Due 2026-11-12"]) expect(section().textContent).toContain(text);
    // The summary is the recap's: the profile does not repeat it.
    expect(host.textContent!.split("Mostly the export release.")).toHaveLength(2);
    const added = [...section().querySelectorAll("li")].find((li) => li.textContent?.includes("Share the review notes."))!;
    expect(added.textContent).toContain("Added");
    expect(added.textContent).not.toContain("Add to to-dos");
  });

  it("marks meetings, linking the meeting the knowledge base holds, and leaves the rest outside", async () => {
    await load();
    const entries = [...section().querySelectorAll<HTMLElement>("[data-recap-entry]")];
    expect(entries.map((e) => e.dataset.recapEntry)).toEqual(["meeting", "outside", "meeting"]);
    expect(entries.map((e) => e.textContent?.includes("Meeting"))).toEqual([true, false, true]);
    const link = entries[0].querySelector("a")!;
    expect(link.textContent).toBe("Export design review");
    expect(link.getAttribute("href")).toBe(`/kb/recordings/${meetingId}`);
    act(() => link.click());
    expect(opened).toHaveBeenCalledWith({ kind: "recordings", id: meetingId, name: "Export design review", relevance: 0 });
    expect(entries[2].querySelector("a")).toBeNull();
  });

  it("names the gaps with their reasons, leaving out the short ones", async () => {
    await load();
    expect(section().querySelector("[data-recap-gaps]")!.textContent).toBe("Not recorded: 10:20–10:45 (interrupted) and 11:36–11:53");
    const data = fixture();
    data.detail.recap!.gaps = Array.from({ length: 8 }, (_, i) => ({ from: 3600 * (i + 1), to: 3600 * (i + 1) + 600, reason: "paused" }));
    await act(async () => root.unmount());
    root = createRoot(host);
    await load(data);
    expect(section().querySelector("[data-recap-gaps]")!.textContent).toBe("Not recorded: 09:00–09:10 (paused), 10:00–10:10 (paused), 11:00–11:10 (paused), 12:00–12:10 (paused), 13:00–13:10 (paused), and 3 more");
  });

  it("reads every clock in the recording's zone, and in local time for a zone the browser doesn't know", async () => {
    await load();
    const time = section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 09:05:12"]')!;
    expect(time.getAttribute("aria-controls")).toBe("recording-line-3912");
    expect(document.getElementById("recording-line-3912")!.textContent).toContain("09:05:12");
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe("So far, up to 19:00");

    const data = fixture(); data.detail.meta!.tz = "Not/A_Zone";
    await act(async () => root.unmount());
    root = createRoot(host);
    await load(data);
    const local = recordingClock(started, 3912);
    expect(section().querySelector(`[aria-label="Go to transcript at ${local}"]`)).not.toBeNull();
    expect(document.getElementById("recording-line-3912")!.textContent).toContain(local);
  });

  it("jumps to the spoken transcript line like the minutes", async () => {
    await load();
    vi.useFakeTimers();
    act(() => section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 12:30:00"]')!.click());
    const target = document.getElementById("recording-line-16200")!;
    expect(scroll).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(target);
    expect(target.classList.contains("bg-accent-tint")).toBe(true);
    act(() => vi.advanceTimersByTime(2000));
    expect(target.classList.contains("bg-accent-tint")).toBe(false);
  });

  it("says it is an all-day recording, with its day and part", async () => {
    const data = fixture(); data.detail.meta!.part = 2;
    await load(data);
    expect(host.textContent).toContain(t("knowledge-kbdetail.recording_all_day"));
    expect(host.textContent).toContain(t("knowledge-kbdetail.recording_day_part", { date: dayTitle("2026-11-11"), part: 2 }));
  });

  it("copies the exact stored markdown", async () => {
    await load();
    await act(async () => button("Copy").click());
    expect(clipboard).toHaveBeenCalledWith(fixture().detail.recap!.markdown);
  });

  it("adds one suggested to-do with the recap version through the minutes route, then refreshes", async () => {
    await load();
    act(() => { button("Add to to-dos").click(); button("Add to to-dos").click(); });
    expect(writes()).toHaveLength(1);
    const write = writes()[0];
    expect(write.url).toBe(`${minutesPath}/todos`);
    expect(JSON.parse(write.init.body as string)).toEqual({ version: 3, actions: ["a1"] });
    expect(new Headers(write.init.headers).get("X-Persona-Manager")).toBe("assistant");
    expect(button("Update").disabled).toBe(true);
    const next = fixture(); next.detail.recap!.actions![0].todo_id = "todo-new";
    await reply(write, { todos: [{ id: "todo-new" }], recap: next.detail.recap });
    expect(requests.at(-1)!.url).toBe(detailPath);
    await reply(requests.at(-1)!, next);
    expect(button("Add to to-dos")).toBeUndefined();
    expect(button("Add all")).toBeUndefined();
    expect(saved).toHaveBeenCalledOnce();
  });

  it("adds all the remaining suggested to-dos in their order", async () => {
    const data = fixture(); data.detail.recap!.actions!.push({ id: "a3", what: "Book the retro.", at: 37000 });
    await load(data); click("Add all");
    expect(JSON.parse(writes()[0].init.body as string)).toEqual({ version: 3, actions: ["a1", "a3"] });
  });

  it("refetches a stale recap without resubmitting the chosen to-dos", async () => {
    await load(); click("Add to to-dos");
    await reply(writes()[0], { code: "minutes_stale", error: "Changed" }, 409);
    expect(toast).toHaveBeenCalledWith(t("knowledge-kbdetail.recap_stale"), "error");
    expect(requests.at(-1)!.url).toBe(detailPath);
    const next = fixture(); next.detail.recap!.version = 4; next.detail.recap!.actions![0].what = "Fix the flaky upload test.";
    await reply(requests.at(-1)!, next);
    expect(section().textContent).toContain("Fix the flaky upload test.");
    expect(writes()).toHaveLength(1);
    click("Add to to-dos");
    expect(JSON.parse(writes()[1].init.body as string)).toEqual({ version: 4, actions: ["a1"] });
  });

  it("writes the first recap with a spinner and polls only while it is pending", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    const data = fixture(); delete data.detail.recap; data.detail.meta!.recap_state = "pending";
    await load(data);
    await reply(requests.at(-1)!, data);
    expect(section().querySelector("[role=status]")!.textContent).toBe("Writing the recap…");
    expect(section().querySelector("[role=status] [aria-hidden]")).not.toBeNull();
    expect(button("Write now")).toBeUndefined();
    expect(button("Update")).toBeUndefined();
    const before = requests.length;
    act(() => vi.advanceTimersByTime(5000));
    expect(requests).toHaveLength(before + 1);
    await reply(requests.at(-1)!, fixture());
    expect(section().textContent).toContain("Design review");
    act(() => vi.advanceTimersByTime(10000));
    expect(requests).toHaveLength(before + 1);
  });

  it("keeps the older recap while a newer one is written", async () => {
    const data = fixture(); data.detail.meta!.recap_state = "pending";
    await load(data);
    expect(section().textContent).toContain("Updating…");
    expect(section().textContent).toContain("Design review");
    expect(button("Update").disabled).toBe(true);
  });

  it.each([false, true])("retries a failed recap (older one kept: %s) with a bodyless POST", async (retained) => {
    const data = fixture(); data.detail.meta!.recap_state = "failed"; if (!retained) delete data.detail.recap;
    await load(data);
    expect(section().textContent).toContain("Couldn’t write the recap.");
    expect(section().textContent?.includes("Design review")).toBe(retained);
    click("Retry");
    expect(writes()[0].url).toBe(minutesPath);
    expect(writes()[0].init.body).toBeUndefined();
    await reply(writes()[0], { state: "pending" }, 202);
    data.detail.meta!.recap_state = "pending";
    await reply(requests.at(-1)!, data);
    expect(section().textContent).toContain(retained ? "Updating…" : "Writing the recap…");
  });

  it("says when the recap comes before there is one, and can write it now", async () => {
    const data = fixture(); delete data.detail.recap; delete data.detail.meta!.recap_state; delete data.detail.summary;
    await load(data);
    expect(section().textContent).toContain("The recap is written in the evening.");
    click("Write now");
    expect(writes()[0].url).toBe(minutesPath);
    expect(writes()[0].init.body).toBeUndefined();
  });

  it("offers nothing to write without a transcript", async () => {
    const data = fixture(); delete data.detail.recap; delete data.detail.meta!.recap_state; data.detail.segments = [];
    await load(data);
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_no_text"));
    expect(section().querySelectorAll("button")).toHaveLength(0);
  });

  it.each([
    ["complete", "Final recap of the day"],
    ["cutoff", "Final recap. Some of the day may not have been transcribed."],
  ])("labels a final recap (%s) and offers to regenerate it", async (reason, label) => {
    const data = fixture(); data.detail.recap!.final = true; data.detail.recap!.final_reason = reason; data.detail.meta!.complete = reason === "complete";
    await load(data);
    expect(section().querySelector("[data-recap-covers]")!.textContent).toBe(label);
    expect(button("Regenerate")).toBeDefined();
    expect(button("Update")).toBeUndefined();
  });

  it("warns when the day was too long to read in full", async () => {
    const data = fixture(); data.detail.recap!.partial = true;
    await load(data);
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_partial"));
  });

  it("shows a recap with only its summary, and one whose summary was withdrawn", async () => {
    const data = fixture(); const recap = data.detail.recap!;
    delete recap.timeline; recap.actions = []; delete recap.gaps;
    await load(data);
    expect(headings("h3")).toEqual([]);
    expect(section().textContent).toContain("Mostly the export release.");
    expect(section().querySelector("[data-recap-gaps]")).toBeNull();

    const blank = fixture(); blank.detail.recap!.summary = ""; blank.detail.summary = "";
    await act(async () => root.unmount());
    root = createRoot(host);
    await load(blank);
    expect(section().textContent).not.toContain("Mostly the export release.");
    expect(headings("h4")).toEqual(["Design review", "Lunch with the platform team", "Evening sync"]);
  });

  // Past recording_days the recap stays (docs/assistant/day-recap.md "Retention and privacy"); it can't be written again.
  it("keeps a trimmed recording's recap and to-dos, says until when, and offers no rewrite", async () => {
    const data = fixture();
    data.detail.meta!.trimmed_at = "2026-11-25T02:00:00Z"; delete data.detail.segments; delete data.detail.summary;
    await load(data);
    const ask = requests.find((r) => r.url === "/api/v1/assistant")!;
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_transcript_expired"));
    await reply(ask, { manager: "assistant", recording_days: 14, minutes_days: 365 });
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_kept_until", { date: minutesKeptUntil(started, 365)! }));
    expect(section().textContent).toContain("Design review");
    expect(button("Update")).toBeUndefined();
    expect(button("Copy")).toBeDefined();
    expect(section().querySelector<HTMLButtonElement>('[aria-label="Go to transcript at 09:05:12"]')!.disabled).toBe(true);
    click("Add to to-dos");
    expect(writes().map((w) => w.url)).toEqual([`${minutesPath}/todos`]);
  });

  it("says the recap is kept until deleted when retention is forever", async () => {
    const data = fixture(); data.detail.meta!.trimmed_at = "2026-11-25T02:00:00Z"; delete data.detail.segments;
    await load(data);
    await reply(requests.find((r) => r.url === "/api/v1/assistant")!, { manager: "assistant", minutes_days: 0 });
    expect(section().textContent).toContain(t("knowledge-kbdetail.recap_kept_forever"));
  });

  it("ignores a late write after the route changed", async () => {
    await load(); click("Update"); const old = writes()[0];
    render("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
    const count = requests.length;
    await reply(old, { state: "pending" }, 202);
    expect(requests).toHaveLength(count);
    expect(saved).not.toHaveBeenCalled();
    expect(toast).not.toHaveBeenCalled();
  });
});
