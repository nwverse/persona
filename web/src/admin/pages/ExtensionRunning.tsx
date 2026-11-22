// How an extension runs (docs/extensions/extensions.md "Runtime status", #1857): the error rate and p95 on its row, and in its details the
// calls and errors, the latency, why it exited, and its log (search, follow it live, copy). A hub from before #1857 has
// none of it (404): the rows and details show nothing of it then.
import { formatNumber, t } from "../../i18n";
import { useEffect, useMemo, useRef, useState } from "react";
import { ExtensionChart } from "../../views/extensions/ExtensionChart";
import { Button, cx, toast } from "../../ui/atoms";
import { AdminError, errorText, followExtensionLogs, getExtensionLogs, getExtensionMetrics } from "../api";
import { AdminIcon } from "../icons";
import { dateTime, logText, mergeLines, metricsCharts, msText, rateText, rateTone } from "../logic";
import type { ExtensionMetrics, ExtensionOverview, LogLine, MetricsWindow } from "../types";
import { Loader, Segmented, Tag } from "../parts";
import { latestRequest, visiblePoll } from "../../lib/visible-poll";

const WINDOWS: { value: MetricsWindow; label: string }[] = [
  { value: "1h", label: t("admin-pages-extensionrunning.window_1h") },
  { value: "24h", label: t("admin-pages-extensionrunning.window_24h") },
  { value: "7d", label: t("admin-pages-extensionrunning.window_7d") },
];

/** The row's two columns: the error rate and p95 over the last 24 hours. */
export function RunColumns({ o }: { o?: ExtensionOverview }) {
  if (!o) return null;
  const tone = rateTone(o.error_rate, o.calls);
  return (
    <div className="hidden shrink-0 items-center gap-4 sm:flex" title={t("admin-pages-extensionrunning.last_24h", { calls: o.calls, errors: o.errors })}>
      <Column label={t("admin-pages-extensionrunning.error_rate")} className={tone === "red" ? "text-red" : tone === "orange" ? "text-orange" : "text-ink-2"}>
        {rateText(o.error_rate, o.calls)}
      </Column>
      <Column label="p95" className="text-ink-2">
        {msText(o.p95_ms)}
      </Column>
    </div>
  );
}

function Column({ label, className, children }: { label: string; className?: string; children: React.ReactNode }) {
  return (
    <span className="flex w-[64px] flex-col items-end leading-tight">
      <span className="whitespace-nowrap text-[10.5px] text-ink-3">{label}</span>
      <span className={cx("whitespace-nowrap font-mono text-[12px] tabular-nums", className)}>{children}</span>
    </span>
  );
}

/** "Activity" and "Logs", in an extension's details. */
export function ExtensionRunning({ id }: { id: string }) {
  const [win, setWin] = useState<MetricsWindow>("24h");
  // Each answer is kept with the extension and window it is for, so a switch never shows the last one's figures as this one's.
  const [got, setGot] = useState<{ id: string; win: MetricsWindow; m: ExtensionMetrics } | null>(null);
  const [failed, setFailed] = useState<{ id: string; win: MetricsWindow; e: unknown } | null>(null);
  const [absent, setAbsent] = useState<string | null>(null); // the id a hub from before #1857 answered 404 for
  useEffect(() => {
    const request = latestRequest<ExtensionMetrics>(
      (m) => {
        setGot({ id, win, m });
        setFailed(null);
      },
      (e) => {
        if (e instanceof AdminError && e.status === 404) setAbsent(id);
        else setFailed({ id, win, e });
      },
    );
    const stop = visiblePoll(() => void request.run(() => getExtensionMetrics(id, win)), 30_000, {
      visible: () => document.visibilityState !== "hidden",
      subscribe: (fn) => {
        document.addEventListener("visibilitychange", fn);
        return () => document.removeEventListener("visibilitychange", fn);
      },
    });
    return () => {
      request.dispose();
      stop();
    };
  }, [id, win]);
  if (absent === id) return null; // a hub from before #1857
  const m = got?.id === id && got.win === win ? got.m : null;
  const error = failed?.id === id && failed.win === win ? failed.e : null;
  return (
    <>
      <section className="grid min-w-0 gap-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-[12px] font-medium text-ink-2">{t("admin-pages-extensionrunning.running")}</h4>
          <Segmented value={win} options={WINDOWS} onChange={setWin} label={t("admin-pages-extensionrunning.time_range")} />
        </div>
        {/* a failed refresh is said even while the last figures stay on screen */}
        {error != null && <p className="text-[12px] text-red">{errorText(error)}</p>}
        {m ? <Running m={m} /> : error == null && <Loader />}
      </section>
      <ExtensionLogs id={id} />
    </>
  );
}

function Running({ m }: { m: ExtensionMetrics }) {
  const charts = useMemo(() => metricsCharts(m), [m]);
  const total = m.total;
  if (total.calls === 0 && m.starts === 0 && m.exits === 0) {
    return <p className="text-[12px] text-ink-3">{t("admin-pages-extensionrunning.no_activity")}</p>;
  }
  const tone = rateTone(total.error_rate, total.calls);
  const reasons = Object.entries(m.exit_reasons ?? {}).sort((a, b) => b[1] - a[1]);
  return (
    <div className="grid min-w-0 gap-3">
      <dl className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <Figure label={t("admin-pages-extensionrunning.calls")}>{formatNumber(total.calls)}</Figure>
        <Figure label={t("admin-pages-extensionrunning.error_rate")} className={tone === "red" ? "text-red" : tone === "orange" ? "text-orange" : undefined}>
          {rateText(total.error_rate, total.calls)}
          {total.errors > 0 && <span className="ml-1 text-[11px] text-ink-3">{t("admin-pages-extensionrunning.error_count", { count: total.errors })}</span>}
        </Figure>
        <Figure label={t("admin-pages-extensionrunning.latency_p95")}>
          {msText(total.p95_ms)}
          <span className="mt-0.5 block truncate text-[11px] text-ink-3">
            p50 {msText(total.p50_ms)} · p99 {msText(total.p99_ms)}
          </span>
        </Figure>
        <Figure label={t("admin-pages-extensionrunning.starts_exits")} className={m.exits > 0 ? "text-orange" : undefined}>
          {m.starts} / {m.exits}
        </Figure>
      </dl>
      <div className="grid min-w-0 gap-3 lg:grid-cols-2">
        <div className="min-w-0 rounded-control bg-inset p-3 shadow-hairline">
          <p className="mb-1 text-[11.5px] text-ink-3">{t("admin-pages-extensionrunning.calls_errors")}</p>
          <ExtensionChart item={charts.calls} />
        </div>
        {charts.latency && (
          <div className="min-w-0 rounded-control bg-inset p-3 shadow-hairline">
            <p className="mb-1 text-[11.5px] text-ink-3">{t("admin-pages-extensionrunning.latency_chart")}</p>
            <ExtensionChart item={charts.latency} />
          </div>
        )}
      </div>
      {m.methods.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[460px] text-[12px]">
            <thead>
              <tr className="text-left text-[11px] text-ink-3">
                <th className="py-1 pr-3 font-normal">{t("admin-pages-extensionrunning.method")}</th>
                <th className="py-1 pr-3 text-right font-normal">{t("admin-pages-extensionrunning.calls")}</th>
                <th className="py-1 pr-3 text-right font-normal">{t("admin-pages-extensionrunning.error_rate")}</th>
                <th className="py-1 pr-3 text-right font-normal">p95</th>
                <th className="py-1 font-normal">{t("admin-pages-extensionrunning.error_codes")}</th>
              </tr>
            </thead>
            <tbody>
              {m.methods.slice(0, 12).map((x) => (
                <tr key={x.method} className="border-t border-line">
                  <td className="max-w-[240px] truncate py-1 pr-3 font-mono text-ink-2">{x.method}</td>
                  <td className="py-1 pr-3 text-right font-mono tabular-nums text-ink-2">{x.calls}</td>
                  <td className={cx("py-1 pr-3 text-right font-mono tabular-nums", rateTone(x.error_rate, x.calls) === "gray" ? "text-ink-2" : "text-orange")}>
                    {rateText(x.error_rate, x.calls)}
                  </td>
                  <td className="py-1 pr-3 text-right font-mono tabular-nums text-ink-2">{msText(x.p95_ms)}</td>
                  <td className="py-1 font-mono text-[11px] text-ink-3">
                    {Object.entries(x.codes ?? {})
                      .map(([c, n]) => `${c} ×${n}`)
                      .join("  ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {reasons.length > 0 && (
        <div className="grid gap-1">
          <p className="text-[11.5px] text-ink-3">{t("admin-pages-extensionrunning.exit_reasons")}</p>
          {reasons.slice(0, 5).map(([r, n]) => (
            <p key={r} className="flex items-start gap-1.5 text-[12px] leading-relaxed text-orange">
              <AdminIcon name="alert" size={13} className="mt-0.5 shrink-0" />
              <span className="min-w-0 break-words">{r}</span>
              <span className="ml-auto shrink-0 font-mono text-ink-3">×{n}</span>
            </p>
          ))}
          {m.recent_exits.length > 0 && <p className="text-[11px] text-ink-3">{t("admin-pages-extensionrunning.last_exit", { time: dateTime(m.recent_exits[m.recent_exits.length - 1].at) })}</p>}
        </div>
      )}
    </div>
  );
}

function Figure({ label, className, children }: { label: string; className?: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 rounded-control bg-inset px-3 py-2 shadow-hairline">
      <dt className="whitespace-nowrap text-[11px] text-ink-3">{label}</dt>
      <dd className={cx("mt-0.5 font-mono text-[15px] tabular-nums text-ink", className)}>{children}</dd>
    </div>
  );
}

/** The extension's log: the kept lines, searched on the hub; following adds each new line as it comes. */
function ExtensionLogs({ id }: { id: string }) {
  const [q, setQ] = useState("");
  const query = q.trim();
  // Each answer is kept with the extension and search it is for, so a new search never shows the last one's lines as its own.
  const [got, setGot] = useState<{ id: string; q: string; lines: LogLine[]; next: number } | null>(null);
  const [failed, setFailed] = useState<{ id: string; q: string; e: unknown } | null>(null);
  const [following, setFollowing] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const atEnd = useRef(true);

  // The kept lines, again whenever the search changes (0.3 s after the last key).
  useEffect(() => {
    let live = true;
    const timer = setTimeout(() => {
      getExtensionLogs(id, query).then(
        (r) => live && (setGot({ id, q: query, lines: r.lines, next: r.next }), setFailed(null)),
        (e) => live && setFailed({ id, q: query, e }),
      );
    }, 300);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [id, query]);
  const answer = got?.id === id && got.q === query ? got : null;
  const lines = answer?.lines ?? null;
  const error = failed?.id === id && failed.q === query ? failed.e : null;

  // Following: the new lines from the stream, filtered here as the hub filters the kept ones. It starts from the end of the
  // current search's answer, so a new search stops it until its own answer is in.
  const answered = answer !== null;
  useEffect(() => {
    if (!following || !answer) return;
    const needle = query.toLowerCase();
    return followExtensionLogs(
      id,
      answer.next - 1,
      (l) => {
        if (needle && !l.text.toLowerCase().includes(needle) && !l.source.includes(needle)) return;
        setGot((have) => (have && have.id === id && have.q === query ? { ...have, lines: mergeLines(have.lines, [l]) } : have));
      },
      () => {
        setFollowing(false);
        toast(t("admin-pages-extensionrunning.follow_broke"), "error");
      },
    );
    // the answer's next only matters when following starts
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [following, id, query, answered]);

  // Stay at the end while the reader is there.
  useEffect(() => {
    const el = box.current;
    if (el && atEnd.current) el.scrollTop = el.scrollHeight;
  }, [lines]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(logText(lines ?? []));
      toast(t("admin-pages-extensionrunning.copied_lines", { count: lines?.length ?? 0 }), "ok");
    } catch {
      toast(t("admin-pages-extensionrunning.copy_failed"), "error");
    }
  };

  return (
    <section className="grid min-w-0 gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <h4 className="mr-auto text-[12px] font-medium text-ink-2">{t("admin-pages-extensionrunning.logs")}</h4>
        <label className="flex h-7 min-w-[160px] items-center gap-1.5 rounded-control bg-inset px-2 shadow-hairline">
          <AdminIcon name="search" size={12} className="shrink-0 text-ink-3" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("admin-pages-extensionrunning.search_logs")}
            aria-label={t("admin-pages-extensionrunning.search_logs")}
            className="min-w-0 flex-1 bg-transparent text-[12px] text-ink outline-none placeholder:text-ink-3"
          />
        </label>
        <Button tone={following ? "primary" : "quiet"} className="h-7 px-2" onClick={() => setFollowing((f) => !f)} aria-pressed={following}>
          <AdminIcon name={following ? "x" : "pulse"} size={12} />
          {following ? t("admin-pages-extensionrunning.stop_following") : t("admin-pages-extensionrunning.follow")}
        </Button>
        <Button tone="quiet" className="h-7 px-2" onClick={() => void copy()} disabled={!lines?.length}>
          <AdminIcon name="copy" size={12} />
          {t("admin-parts.copy")}
        </Button>
      </div>
      {/* a failed search is said even while lines stay on screen */}
      {error != null && <p className="text-[12px] text-red">{errorText(error)}</p>}
      {!lines ? (
        error == null && <Loader />
      ) : (
        <div
          ref={box}
          onScroll={(e) => {
            const el = e.currentTarget;
            atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
          }}
          className="max-h-[320px] overflow-auto rounded-control bg-inset px-3 py-2 font-mono text-[11.5px] leading-[1.6] shadow-hairline"
        >
          {lines.length === 0 ? (
            <p className="py-4 text-center font-sans text-ink-3">{q.trim() ? t("admin-pages-extensionrunning.no_match") : t("admin-pages-extensionrunning.no_logs")}</p>
          ) : (
            lines.map((l) => (
              <div key={l.seq} className="flex gap-2 whitespace-pre-wrap break-all">
                <span className="shrink-0 select-none text-ink-3">{timeOf(l.at)}</span>
                {l.source === "hub" && (
                  <span className="shrink-0 select-none">
                    <Tag tone="accent">hub</Tag>
                  </span>
                )}
                <span className={cx("min-w-0", l.source === "hub" ? "text-ink-2" : "text-ink")}>{l.text}</span>
              </div>
            ))
          )}
        </div>
      )}
      <p className="text-[11px] text-ink-3">{t("admin-pages-extensionrunning.logs_footer")}</p>
    </section>
  );
}

function timeOf(iso: string): string {
  const d = new Date(iso);
  const two = (n: number) => String(n).padStart(2, "0");
  return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;
}
