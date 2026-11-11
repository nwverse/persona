import { useEffect, useId, useRef, useState } from "react";
import { t } from "../../i18n";
import { api, ApiError, enc, errorText } from "../../lib/api";
import { minutesKeptUntil, recordingClock, transcriptTarget, type KBDetail, type MinutesAction, type MinutesPoint, type RecordingView } from "../../lib/kb";
import { documentPage, visiblePoll } from "../../lib/visible-poll";
import { Button, copyText, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Card, SectionTitle, TonePill } from "./KBParts";

export type RecordingAuth = { token: string; tokenOwner: string; headers: Record<string, string> };

type Props = {
  detail: KBDetail;
  rec: RecordingView;
  auth: RecordingAuth;
  current: () => boolean;
  refresh: () => Promise<boolean>;
  onSaved: () => void;
  onJump: (id: string) => void;
};

/** Minutes are retained during regeneration; suggested actions become to-dos only on explicit confirmation. A trimmed
 *  recording (its transcript expired, #3780) keeps its minutes and their to-dos, but they can't be made again. */
export function RecordingMinutes({ detail, rec, auth, current, refresh, onSaved, onJump }: Props) {
  const heading = useId();
  const minutes = detail.minutes;
  const state = detail.meta?.minutes_state;
  const { busy, needsRefresh, run } = useMinutesRequests({ id: detail.id, version: minutes?.version, pending: state === "pending", auth, current, refresh, onSaved });
  const minutesDays = useMinutesDays(rec.trimmedAt, auth, current);
  const keptUntil = rec.trimmedAt ? minutesKeptUntil(rec.startedAt, minutesDays) : undefined;

  if (detail.meta?.kind !== "meeting" && rec.partial) return null;
  const disabled = busy || needsRefresh;
  const canGenerate = !rec.trimmedAt && (!!minutes || state === "failed" || (!rec.partial && !state));
  const time = (at: number) => <TranscriptTime rec={rec} at={at} onJump={onJump} />;
  const points = (title: string, items?: MinutesPoint[]) => items?.length ? (
    <div className="px-4 py-3.5">
      <h3 className="mb-2 text-[13px] font-semibold text-ink">{title}</h3>
      <PointList items={items} time={time} />
    </div>
  ) : null;

  return (
    <section aria-labelledby={heading} data-recording-minutes>
      <SectionTitle title={<span id={heading}>{t("knowledge-kbdetail.minutes")}</span>} right={
        <div className="flex shrink-0 items-center gap-1.5">
          {minutes && <Button tone="quiet" disabled={busy} onClick={() => void copyText(minutes.markdown)}>{t("knowledge-kbdetail.minutes_copy")}</Button>}
          {canGenerate && <Button tone="secondary" disabled={disabled || rec.partial || state === "pending"} onClick={() => void run()}>
            {t(minutes || state === "failed" ? "knowledge-kbdetail.minutes_regenerate" : "knowledge-kbdetail.minutes_make")}
          </Button>}
        </div>
      } />
      {rec.trimmedAt && <p data-minutes-kept className="mb-2 px-1 text-[12.5px] leading-relaxed text-ink-3">
        {minutesDays === 0 ? t("knowledge-kbdetail.minutes_kept_forever") : keptUntil ? t("knowledge-kbdetail.minutes_kept_until", { date: keptUntil }) : t("knowledge-kbdetail.minutes_transcript_expired")}
      </p>}
      {!rec.trimmedAt && (state === "pending" || state === "failed") && <p role="status" className="mb-2 px-1 text-[12.5px] leading-relaxed text-ink-3">
        {t(state === "failed" ? "knowledge-kbdetail.minutes_failed" : minutes ? "knowledge-kbdetail.minutes_updating" : "knowledge-kbdetail.minutes_writing")}
      </p>}
      {!minutes && !state && rec.partial && <p className="px-1 text-[12.5px] text-ink-3">{t("knowledge-kbdetail.minutes_waiting")}</p>}
      {needsRefresh && !busy && <div className="mb-2 px-1">
        <Button tone="secondary" onClick={() => void run(undefined, true)}>{t("knowledge-kbdetail.minutes_retry_refresh")}</Button>
      </div>}
      {minutes && <Card>
        <div className="px-4 py-3.5">
          <h3 className="break-words text-[14px] font-semibold leading-relaxed text-ink">{minutes.title}</h3>
          <div className="mt-1.5 space-y-1 break-words text-[12.5px] leading-relaxed text-ink-2">
            {minutes.when && <p>{minutes.when}</p>}
            {minutes.where && <p>{t("knowledge-kbdetail.minutes_where", { where: minutes.where })}</p>}
            {!!minutes.attendees?.length && <p>{t("knowledge-kbdetail.minutes_attendees", { attendees: minutes.attendees.join(" · ") })}</p>}
          </div>
          {minutes.partial && <p className="mt-2 text-[12.5px] leading-relaxed text-ink-3">{t("knowledge-kbdetail.minutes_partial")}</p>}
        </div>
        {points(t("knowledge-kbdetail.minutes_points"), minutes.points)}
        {points(t("knowledge-kbdetail.minutes_decisions"), minutes.decisions)}
        <ActionItems title={t("knowledge-kbdetail.minutes_actions")} actions={minutes.actions} disabled={disabled} time={time} onAdd={(ids) => void run(ids)} />
        {points(t("knowledge-kbdetail.minutes_open"), minutes.open)}
      </Card>}
    </section>
  );
}

/** Asking for minutes and confirming their actions through `POST /recordings/{id}/minutes[/todos]`; an all-day
 *  recording's recap (#4741) goes through the same routes (`recap` picks its messages). One request at a time; a
 *  successful write, or a stale-version rejection, is read back before another mutation is allowed; answers that arrive
 *  after the route, credential or assistant changed are dropped; a pending one is read again every 5 s while the page
 *  shows. */
export function useMinutesRequests({ id, version, pending, recap = false, auth, current, refresh, onSaved }: {
  id: string;
  /** The shown minutes' (or recap's) version, sent with confirmed actions. */
  version?: number;
  pending: boolean;
  recap?: boolean;
  auth: RecordingAuth;
  current: () => boolean;
  refresh: () => Promise<boolean>;
  onSaved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const inFlight = useRef(false);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const valid = () => alive.current && current();

  useEffect(() => {
    if (!pending) return;
    let polling = false;
    return visiblePoll(() => {
      if (polling || inFlight.current || !current()) return;
      polling = true;
      void refresh().catch(() => {}).finally(() => { polling = false; });
    }, 5000, documentPage);
  }, [pending, current, refresh]);

  // A successful write (or stale-version rejection) must be read back before another mutation is allowed.
  const readback = async () => {
    setNeedsRefresh(true);
    try {
      if (await refresh() && valid()) {
        setNeedsRefresh(false);
        onSaved();
      }
    } catch (err) {
      if (valid()) toast(t(recap ? "knowledge-kbdetail.recap_refresh_failed" : "knowledge-kbdetail.minutes_refresh_failed", { error: errorText(err) }), "error");
    }
  };

  const run = async (actions?: string[], refreshOnly = false) => {
    if (inFlight.current || !valid() || (needsRefresh && !refreshOnly)) return;
    inFlight.current = true;
    setBusy(true);
    try {
      if (!refreshOnly) {
        try {
          await api(`/recordings/${enc(id)}/minutes${actions ? "/todos" : ""}`, {
            method: "POST", ...auth,
            ...(actions ? { json: { version: version!, actions } } : {}),
          });
        } catch (err) {
          if (!valid()) return;
          if (!(err instanceof ApiError && err.status === 409 && err.code === "minutes_stale")) throw err;
          toast(t(recap ? "knowledge-kbdetail.recap_stale" : "knowledge-kbdetail.minutes_stale"), "error");
        }
      }
      if (valid()) await readback();
    } catch (err) {
      if (valid()) toast(t(recap ? "knowledge-kbdetail.recap_request_failed" : "knowledge-kbdetail.minutes_request_failed", { error: errorText(err) }), "error");
    } finally {
      if (valid()) { inFlight.current = false; setBusy(false); }
    }
  };

  return { busy, needsRefresh, run };
}

/** Until when a trimmed recording's minutes (or recap) stay: the assistant's minutes_days in effect, when its hub says;
 *  nothing is asked of a whole recording. */
export function useMinutesDays(trimmedAt: string | undefined, auth: RecordingAuth, current: () => boolean): unknown {
  const [minutesDays, setMinutesDays] = useState<unknown>();
  useEffect(() => {
    if (!trimmedAt) return;
    let live = true;
    api<{ minutes_days?: unknown }>("/assistant", auth).then((a) => { if (live && current()) setMinutesDays(a.minutes_days); }, () => {});
    return () => { live = false; };
  }, [trimmedAt, auth, current]);
  return minutesDays;
}

/** The clock time an item cites, jumping to the transcript line said then; disabled when there is no such line (the
 *  transcript expired, or none yet). */
export function TranscriptTime({ rec, at, onJump }: { rec: RecordingView; at: number; onJump: (id: string) => void }) {
  const clock = recordingClock(rec.startedAt, at, rec.tz);
  const target = transcriptTarget(rec.lines, at);
  return (
    <button
      type="button"
      aria-label={t("knowledge-kbdetail.minutes_jump", { time: clock })}
      aria-controls={target}
      disabled={!target}
      onClick={() => { if (target) onJump(target); }}
      className="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-control text-[11.5px] text-accent-ink tabular-nums hover:bg-hover disabled:text-ink-3"
    >
      <Icon name="clock" size={12} />{clock}
    </button>
  );
}

/** Points with who said them and their time. */
export function PointList({ items, time }: { items: MinutesPoint[]; time: (at: number) => React.ReactNode }) {
  return (
    <ul className="space-y-2">
      {items.map((item, i) => (
        <li key={i} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-[13px] leading-relaxed">
          <p className="min-w-0 flex-1 break-words text-ink">{[item.who, item.text].filter(Boolean).join(" · ")}</p>
          {time(item.at)}
        </li>
      ))}
    </ul>
  );
}

/** Suggested actions, each added to the to-dos only when the user confirms it (or all that are left). */
export function ActionItems({ title, actions, disabled, time, onAdd }: {
  title: string;
  actions?: MinutesAction[] | null;
  disabled: boolean;
  time: (at: number) => React.ReactNode;
  onAdd: (ids: string[]) => void;
}) {
  if (!actions?.length) return null;
  const remaining = actions.filter((action) => !action.todo_id);
  return (
    <div className="px-4 py-3.5">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-[13px] font-semibold text-ink">{title}</h3>
        {remaining.length > 0 && <Button tone="quiet" disabled={disabled} onClick={() => onAdd(remaining.map((action) => action.id))}>{t("knowledge-kbdetail.minutes_add_all")}</Button>}
      </div>
      <ul className="divide-y divide-line">
        {actions.map((action) => <li key={action.id} className="py-2 first:pt-0 last:pb-0">
          <p className="break-words text-[13px] leading-relaxed text-ink">{[action.who, action.what, action.by, action.due_date && t("knowledge-kbdetail.minutes_due", { date: action.due_date })].filter(Boolean).join(" · ")}</p>
          <div className="mt-1.5 flex flex-wrap items-center justify-between gap-2">
            {time(action.at)}
            {action.todo_id ? <TonePill>{t("knowledge-kbdetail.minutes_added")}</TonePill> :
              <Button tone="secondary" disabled={disabled} onClick={() => onAdd([action.id])}>{t("knowledge-kbdetail.minutes_add_todo")}</Button>}
          </div>
        </li>)}
      </ul>
    </div>
  );
}
