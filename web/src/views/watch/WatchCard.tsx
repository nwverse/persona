// One watch card (docs/assistant/watch-cards.md, docs/design/DESIGN_SYSTEM.md "Watch cards"): its name, its latest
// answer drawn as its display kind (a number, a status light, a small table or a number with its trend), and a quiet
// line saying when it refreshes and where it belongs. A tripped alert rings the card in red; a draft is a dashed outline
// with Confirm and Discard. The same card is drawn on Overview's wall and in a project node's panel (ProjectWatchCards).
import { ago } from "../../lib/format";
import { alertText, sparkPoints, trendValues, watchRule, watchTone, type Watch, type WatchStatus } from "../../lib/watches";
import { formatNumber, t } from "../../i18n";
import { Button, cx, IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

const LIGHT: Record<WatchStatus, string> = { ok: "bg-green", warn: "bg-orange", bad: "bg-red" };

export function statusWord(s?: WatchStatus): string {
  return s === "bad" ? t("watch.status_bad") : s === "warn" ? t("watch.status_warn") : s === "ok" ? t("watch.status_ok") : t("watch.status_none");
}

function value(n: number | undefined): string {
  return typeof n === "number" ? formatNumber(n, { maximumFractionDigits: 2 }) : "—";
}

/** The trend: the history's numbers as a line, the last point marked. */
export function Spark({ values, alert }: { values: number[]; alert?: boolean }) {
  const w = 96, h = 32;
  if (values.length < 2) return null;
  const pts = sparkPoints(values, w, h);
  const last = pts.split(" ").at(-1)!.split(",");
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className={cx("shrink-0", alert ? "text-red" : "text-accent")} aria-hidden data-watch-spark>
      <polyline points={pts} fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={last[0]} cy={last[1]} r={2.4} fill="currentColor" />
    </svg>
  );
}

function Answer({ w }: { w: Watch }) {
  const r = w.latest;
  const tone = watchTone(w);
  if (!r) {
    return <p className="mt-2 text-meta text-ink-3" data-watch-empty>{w.state === "draft" ? t("watch.draft_empty") : w.paused ? t("watch.paused_empty") : t("watch.empty")}</p>;
  }
  const red = tone === "alert" || tone === "bad";
  if (w.display === "status") {
    return (
      <div className="mt-2 flex items-start gap-2">
        <span className={cx("mt-1 size-2.5 shrink-0 rounded-full", r.status ? LIGHT[r.status] : "bg-line-strong")} />
        <div className="min-w-0">
          <div className={cx("text-card-title font-semibold", red ? "text-red" : "text-ink")}>{statusWord(r.status)}</div>
          {r.text && <p className="line-clamp-2 text-meta text-ink-2">{r.text}</p>}
        </div>
      </div>
    );
  }
  if (w.display === "table" && r.table && r.table.columns.length) {
    const rows = r.table.rows.slice(0, 5);
    return (
      <div className="mt-2 min-w-0">
        {r.text && <p className="mb-1.5 truncate text-meta text-ink-2">{r.text}</p>}
        <div className="overflow-x-auto rounded-control bg-inset">
          <table className="w-full text-left text-label">
            <thead>
              <tr>{r.table.columns.map((c, i) => <th key={i} scope="col" className="px-2 py-1 font-medium whitespace-nowrap text-ink-3">{c}</th>)}</tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i} className="border-t border-line">
                  {row.map((c, j) => <td key={j} className="px-2 py-1 whitespace-nowrap text-ink">{c}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {r.table.rows.length > rows.length && <p className="mt-1 text-submeta text-ink-3">{t("watch.more_rows", { count: r.table.rows.length - rows.length })}</p>}
      </div>
    );
  }
  const values = w.display === "trend" ? trendValues(w) : [];
  return (
    <div className="mt-2 flex items-end justify-between gap-2">
      <div className="min-w-0">
        <div className="flex items-baseline gap-1">
          <span className={cx("text-2xl leading-tight font-semibold tabular-nums", red ? "text-red" : "text-ink")}>{value(r.number)}</span>
          {w.unit && <span className="text-meta text-ink-3">{w.unit}</span>}
        </div>
        {r.text && <p className="line-clamp-2 text-meta text-ink-2">{r.text}</p>}
      </div>
      {w.display === "trend" && <Spark values={values} alert={tone === "alert"} />}
    </div>
  );
}

export function WatchCard({ w, now, onConfirm, onDiscard, onMenu, drag, where = true }: {
  w: Watch;
  now: number;
  onConfirm?: () => void;
  onDiscard?: () => void;
  /** Opens the card's actions (refresh, pause, edit, move, delete) at the ⋯ button. */
  onMenu?: (e: React.MouseEvent<HTMLElement>) => void;
  /** Drag to reorder on the wall: the grip and the drop target. */
  drag?: { onDragStart: () => void; onDrop: () => void; over: boolean; setOver: (on: boolean) => void };
  /** Show the project the card belongs to (off inside that project's own panel). */
  where?: boolean;
}) {
  const tone = watchTone(w);
  const draft = w.state === "draft";
  const updated = w.latest?.at ? ago(w.latest.at, now) : "";
  const meta = [w.paused ? t("watch.paused") : w.no_task ? t("watch.no_task") : watchRule(w), updated && t("watch.updated", { when: updated }), where && w.project ? w.project_name || w.project : ""].filter(Boolean);
  return (
    <article
      data-watch={w.id}
      data-watch-tone={tone}
      draggable={!!drag}
      onDragStart={drag ? (e) => { e.dataTransfer.effectAllowed = "move"; drag.onDragStart(); } : undefined}
      onDragOver={drag ? (e) => { e.preventDefault(); drag.setOver(true); } : undefined}
      onDragLeave={drag ? () => drag.setOver(false) : undefined}
      onDrop={drag ? (e) => { e.preventDefault(); drag.setOver(false); drag.onDrop(); } : undefined}
      aria-label={`${w.title}${tone === "alert" ? `, ${t("watch.alert_tag")}` : ""}`}
      className={cx(
        "flex min-w-0 flex-col rounded-card p-3.5",
        draft ? "border border-dashed border-line-strong bg-canvas" : "bg-surface shadow-card",
        tone === "alert" && !draft && "ring-2 ring-red/50",
        w.paused && "opacity-70",
        drag?.over && "ring-2 ring-inset ring-accent",
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        {drag && <Icon name="grip" size={12} className="shrink-0 cursor-grab text-ink-3" />}
        <h3 className="min-w-0 flex-1 truncate text-meta font-semibold text-ink" title={w.question}>{w.title}</h3>
        {tone === "alert" && <span className="shrink-0 rounded-full bg-red-tint px-1.5 text-submeta font-semibold whitespace-nowrap text-red">{t("watch.alert_tag")}</span>}
        {w.alert && !draft && <span className="flex shrink-0 text-ink-3" title={alertText(w.alert, w.unit)} role="img" aria-label={alertText(w.alert, w.unit)}><Icon name="bell" size={12} /></span>}
        {onMenu && !draft && <IconButton icon="more" label={t("watch.actions", { title: w.title })} size={28} onClick={onMenu} />}
      </div>
      {draft ? (
        <>
          <p className="mt-1 text-submeta font-medium text-accent-ink">{t("watch.draft_tag")}</p>
          <p className="mt-1 line-clamp-3 text-meta text-ink-2">{w.question}</p>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-label text-ink-3">
            <dt>{t("watch.field_refresh")}</dt><dd className="text-ink-2">{watchRule(w)}</dd>
            <dt>{t("watch.field_display")}</dt><dd className="text-ink-2">{t(`watch.display_${w.display}`)}{w.unit ? ` · ${w.unit}` : ""}</dd>
            <dt>{t("watch.field_alert")}</dt><dd className="text-ink-2">{alertText(w.alert, w.unit)}</dd>
            {w.project && <><dt>{t("watch.field_project")}</dt><dd className="text-ink-2">{w.project_name || w.project}</dd></>}
          </dl>
          {(onConfirm || onDiscard) && (
            <div className="mt-3 flex flex-wrap gap-2">
              {onConfirm && <Button tone="primary" size="sm" icon="check" onClick={onConfirm}>{t("watch.confirm")}</Button>}
              {onDiscard && <Button size="sm" onClick={onDiscard}>{t("watch.discard")}</Button>}
            </div>
          )}
        </>
      ) : (
        <>
          <Answer w={w} />
          <p className="mt-2 truncate text-submeta text-ink-3" data-watch-meta>{meta.join(" · ")}</p>
        </>
      )}
    </article>
  );
}
