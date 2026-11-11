// An all-day recording's recap (#4741, docs/assistant/day-recap.md): the day's summary, its timeline by part of the day
// on the recording's own clock (meetings marked, each point a jump into the transcript), the time nothing was recorded
// and why, and suggested to-dos that become to-dos only when confirmed. It goes through the minutes' routes and their
// write flow (RecordingMinutes); a day recording never offers minutes.
import { useId } from "react";
import { formatList, t } from "../../i18n";
import { tRich } from "../../i18n/rich";
import { dayClock, isKind, minutesKeptUntil, recapGaps, recapGroups, type DayPart, type DayRecapEntry, type DayRecapGap, type KBDetail, type KBEntry, type RecordingView } from "../../lib/kb";
import { href } from "../../lib/router";
import { Button, copyText, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Card, KBIcon, KIND_ICON, onPlainClick, SectionTitle, TonePill } from "./KBParts";
import { ActionItems, PointList, TranscriptTime, useMinutesDays, useMinutesRequests, type RecordingAuth } from "./RecordingMinutes";

type Props = {
  detail: KBDetail;
  rec: RecordingView;
  auth: RecordingAuth;
  current: () => boolean;
  refresh: () => Promise<boolean>;
  onSaved: () => void;
  onJump: (id: string) => void;
  onOpen: (e: KBEntry) => void;
};

/** Gaps named past this many say how many more there are. */
const GAPS_SHOWN = 5;

const PART_TITLE: Record<DayPart, string> = {
  morning: "knowledge-kbdetail.recap_morning",
  afternoon: "knowledge-kbdetail.recap_afternoon",
  evening: "knowledge-kbdetail.recap_evening",
};

// The reasons the contract names; any other is shown without one.
const GAP_REASON: Record<string, string> = {
  paused: "knowledge-kbdetail.recap_gap_paused",
  interrupted: "knowledge-kbdetail.recap_gap_interrupted",
  stopped: "knowledge-kbdetail.recap_gap_stopped",
  pending: "knowledge-kbdetail.recap_gap_pending",
  lost: "knowledge-kbdetail.recap_gap_lost",
  erased: "knowledge-kbdetail.recap_gap_erased",
};

/** The recap is kept while a newer one is written or failed, and after the transcript expired (then it can't be
 *  written again). */
export function RecordingDayRecap({ detail, rec, auth, current, refresh, onSaved, onJump, onOpen }: Props) {
  const heading = useId();
  const recap = detail.recap;
  const state = detail.meta?.recap_state;
  const { busy, needsRefresh, run } = useMinutesRequests({ id: detail.id, version: recap?.version, pending: state === "pending", recap: true, auth, current, refresh, onSaved });
  const minutesDays = useMinutesDays(rec.trimmedAt, auth, current);
  const keptUntil = rec.trimmedAt ? minutesKeptUntil(rec.startedAt, minutesDays) : undefined;

  const disabled = busy || needsRefresh;
  const spoken = rec.lines.some((line) => !line.bookmark);
  const clock = (at: number) => dayClock(rec.startedAt, at, rec.tz);
  const range = (from: number, to: number) => clock(from) === clock(to) ? clock(from) : t("knowledge-kbdetail.recap_range", { from: clock(from), to: clock(to) });
  const time = (at: number) => <TranscriptTime rec={rec} at={at} onJump={onJump} />;
  // What asking now does: retry a failed one, bring one up to what has arrived, or write the first before the evening.
  const ask = rec.trimmedAt || (!recap && state === "pending") || (!spoken && state !== "failed") ? null
    : state === "failed" ? "knowledge-kbdetail.recap_retry"
      : recap ? (recap.final ? "knowledge-kbdetail.recap_regenerate" : "knowledge-kbdetail.recap_update")
        : "knowledge-kbdetail.recap_write_now";

  return (
    <section aria-labelledby={heading} data-recording-recap>
      <SectionTitle title={<span id={heading}>{t("knowledge-kbdetail.recap")}</span>} right={
        <div className="flex shrink-0 items-center gap-1.5">
          {recap && <Button tone="quiet" disabled={busy} onClick={() => void copyText(recap.markdown)}>{t("knowledge-kbdetail.minutes_copy")}</Button>}
          {ask && <Button tone="secondary" disabled={disabled || state === "pending"} onClick={() => void run()}>{t(ask)}</Button>}
        </div>
      } />
      {rec.trimmedAt && <p data-recap-kept className="mb-2 px-1 text-[12.5px] leading-relaxed text-ink-3">
        {minutesDays === 0 ? t("knowledge-kbdetail.recap_kept_forever") : keptUntil ? t("knowledge-kbdetail.recap_kept_until", { date: keptUntil }) : t("knowledge-kbdetail.recap_transcript_expired")}
      </p>}
      {!rec.trimmedAt && state === "pending" && <p role="status" className="mb-2 flex items-center gap-2 px-1 text-[12.5px] leading-relaxed text-ink-3">
        <Spinner />{t(recap ? "knowledge-kbdetail.recap_updating" : "knowledge-kbdetail.recap_writing")}
      </p>}
      {!rec.trimmedAt && state === "failed" && <p role="status" className="mb-2 px-1 text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.recap_failed")}</p>}
      {!recap && !state && !rec.trimmedAt && <p className="px-1 text-[12.5px] leading-relaxed text-ink-3">
        {t(spoken ? "knowledge-kbdetail.recap_absent" : "knowledge-kbdetail.recap_no_text")}
      </p>}
      {needsRefresh && !busy && <div className="mb-2 px-1">
        <Button tone="secondary" onClick={() => void run(undefined, true)}>{t("knowledge-kbdetail.minutes_retry_refresh")}</Button>
      </div>}
      {recap && <Card>
        <div className="px-4 py-3.5">
          {recap.summary && <p className="break-words text-[14px] leading-relaxed text-ink">{recap.summary}</p>}
          <div className={recap.summary ? "mt-1.5 space-y-1 text-[12.5px] leading-relaxed" : "space-y-1 text-[12.5px] leading-relaxed"}>
            <p data-recap-covers className="text-ink-2">
              {!recap.final ? t("knowledge-kbdetail.recap_so_far", { time: clock(recap.covers?.to ?? 0) })
                : recap.final_reason === "cutoff" ? t("knowledge-kbdetail.recap_final_cutoff") : t("knowledge-kbdetail.recap_final")}
            </p>
            <Gaps gaps={recap.gaps} rec={rec} />
            {recap.partial && <p className="text-ink-3">{t("knowledge-kbdetail.recap_partial")}</p>}
          </div>
        </div>
        {recapGroups(recap.timeline, rec.startedAt, rec.tz).map((group, i) => (
          <div key={i} className="px-4 py-3.5">
            {/* A quiet label: the part of the day divides the entries, which are the content. */}
            <h3 className="mb-1.5 text-[12px] font-medium text-ink-3">{t(group.part ? PART_TITLE[group.part] : "knowledge-kbdetail.recap_timeline")}</h3>
            <ol className="divide-y divide-line">
              {group.entries.map((entry, j) => <Entry key={j} entry={entry} range={range(entry.from, entry.to)} time={time} onOpen={onOpen} />)}
            </ol>
          </div>
        ))}
        <ActionItems title={t("knowledge-kbdetail.recap_actions")} actions={recap.actions} disabled={disabled} time={time} onAdd={(ids) => void run(ids)} />
      </Card>}
    </section>
  );
}

/** One stretch of the day: its title and clock range, the meeting it was in, then what was said. */
function Entry({ entry, range, time, onOpen }: { entry: DayRecapEntry; range: string; time: (at: number) => React.ReactNode; onOpen: (e: KBEntry) => void }) {
  const meeting = entry.kind === "meeting";
  // A meeting the knowledge base holds opens there: one of the user's meeting recordings (its minutes), or the item.
  const ref = meeting && entry.meeting?.id ? entry.meeting : null;
  const name = entry.meeting?.name?.trim() || entry.title;
  return (
    <li data-recap-entry={meeting ? "meeting" : "outside"} className="py-2.5 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          <h4 className="min-w-0 break-words text-[13px] font-semibold leading-relaxed text-ink">{entry.title}</h4>
          {meeting && <TonePill tone="accent">{t("knowledge-kbdetail.recap_meeting")}</TonePill>}
        </div>
        <span className="shrink-0 whitespace-nowrap text-[11.5px] text-ink-3 tabular-nums">{range}</span>
      </div>
      {ref && isKind(ref.kind) && (
        <a
          href={href({ name: "kb", kind: ref.kind, id: ref.id })}
          onClick={(ev) => onPlainClick(ev, () => onOpen({ kind: ref.kind, id: ref.id, name, relevance: 0 }))}
          className="mt-0.5 inline-flex max-w-full items-center gap-1 rounded-control text-[12px] font-medium text-accent-ink hover:underline"
        >
          <KBIcon name={KIND_ICON[ref.kind]} size={12} stroke={2} className="shrink-0" />
          <span className="truncate">{name}</span>
          <Icon name="chevronRight" size={11} stroke={2.2} className="shrink-0 text-ink-3" />
        </a>
      )}
      {!!entry.points?.length && <div className="mt-1.5"><PointList items={entry.points} time={time} /></div>}
    </li>
  );
}

/** "Not recorded: 10:20–10:45 (interrupted), …": the gaps a minute or longer inside what the recap read. */
function Gaps({ gaps, rec }: { gaps?: DayRecapGap[] | null; rec: RecordingView }) {
  const shown = recapGaps(gaps, rec.startedAt, rec.tz);
  if (!shown.length) return null;
  const items = shown.slice(0, GAPS_SHOWN).map((g) => {
    const range = g.from === g.to ? g.from : t("knowledge-kbdetail.recap_range", { from: g.from, to: g.to });
    return Object.hasOwn(GAP_REASON, g.reason) ? t("knowledge-kbdetail.recap_gap", { range, reason: t(GAP_REASON[g.reason]) }) : range;
  });
  if (shown.length > GAPS_SHOWN) items.push(t("knowledge-kbdetail.recap_gaps_more", { count: shown.length - GAPS_SHOWN }));
  // Joined the language's way, each item kept on one line ("13:40–14:30" never splits at the dash).
  const list = formatList(items.map((_, i) => `{${i}}`)).split(/(\{\d+\})/g).map((piece, i) => {
    const m = /^\{(\d+)\}$/.exec(piece);
    return m ? <span key={i} className="whitespace-nowrap">{items[Number(m[1])]}</span> : piece;
  });
  return <p data-recap-gaps className="break-words text-ink-3">{tRich("knowledge-kbdetail.recap_gaps", { gaps: list })}</p>;
}
