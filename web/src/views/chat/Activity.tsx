// Session activity in the chat header's overview: loops, workflows and background tasks.
import { useLayoutEffect, useRef, useState } from "react";
import { openPanel } from "../../lib/panel";
import { t } from "../../i18n";
import { backgroundTitle, elapsed, loopCadence, loopNext, loopTask, taskLabel, taskPill, type FinishedTask } from "../../lib/tasks";
import type { BackgroundTask, LoopInfo, Session } from "../../lib/types";
import { otherBackground, recentWorkflows } from "../../lib/workflows";
import { WorkflowCard } from "./WorkflowCard";
import { Button, copyText, cx, useNow } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export function SessionActivity({ session, finished, onStopLoop, onJumpToCall }: {
  session: Session;
  finished: readonly FinishedTask[];
  onStopLoop: () => void;
  onJumpToCall?: (call: string) => void;
}) {
  const loop = session.loop;
  const now = useNow(30_000, Boolean(session.workflows?.length));
  const runs = recentWorkflows(session, now);
  const agents = new Set(session.subagents?.map((a) => a.id));
  const bg = otherBackground(session).filter((b) => b.kind !== "agent" || !agents.has(b.id));
  const active = new Set(bg.map((b) => b.id));
  const ended = finished.filter((row) => !active.has(row.id));
  const [showFinished, setShowFinished] = useState(false);
  const finishedList = useRef<HTMLUListElement>(null);
  useLayoutEffect(() => {
    if (showFinished) finishedList.current?.firstElementChild?.scrollIntoView({ block: "nearest" });
  }, [showFinished]);
  const viewOutput = (path: string) => {
    openPanel({ kind: "file", mid: session.machine_id, path, cwd: session.cwd });
  };
  if (!loop && !bg.length && !runs.length && !ended.length) return null;
  return (
    <section className="flex flex-col gap-2 border-t border-line py-3">
      <h3 className="text-xs font-semibold text-ink-2">{loop && !bg.length && !runs.length && !ended.length ? t("chat-activity.title_loop") : t("chat-activity.title_background")}</h3>
      {bg.length > 0 && <BackgroundList tasks={bg} onOutput={(task) => viewOutput(task.output_file!)} />}
      {ended.length > 0 && (
        <>
          <button type="button" aria-expanded={showFinished} onClick={() => setShowFinished(!showFinished)}
            className="flex min-h-8 items-center gap-2 rounded-control px-2 text-left text-xs text-ink-2 hover:bg-hover">
            <Icon name="check" size={14} className="text-green" />
            <span className="flex-1">{t("chat-sessionoverview.finished_count", { count: ended.length })}</span>
            <Icon name="chevron" size={13} className={cx("text-ink-3", showFinished && "rotate-180")} />
          </button>
          {showFinished && (
            <ul ref={finishedList} className="divide-y divide-line rounded-control border border-line">
              {ended.map(({ id, task, title, call, at, startedAt }) => {
                const pill = taskPill(task, startedAt, at);
                return (
                  <li key={id} className="flex min-w-0 flex-col gap-1 px-2.5 py-2 text-xs">
                    <div className="flex items-start justify-between gap-2">
                      <span className="min-w-0 break-words text-ink">{title || taskLabel(task)}</span>
                      <span className={cx("shrink-0 whitespace-nowrap", pill?.tone === "red" ? "text-red" : "text-ink-3")}>{pill?.text}</span>
                    </div>
                    {(task.output_file || (onJumpToCall && call)) && (
                      <div className="flex flex-wrap gap-2 text-accent-ink">
                        {task.output_file && <button type="button" onClick={() => viewOutput(task.output_file!)} className="hover:underline">{t("chat-activity.view_output")}</button>}
                        {onJumpToCall && call && <button type="button" onClick={() => onJumpToCall(call)} className="hover:underline">{t("chat-sessionoverview.show_in_chat")}</button>}
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </>
      )}
      {runs.length > 0 && (
        <section aria-label={t("lib-workflows.title")} className="flex flex-col gap-2">
          {runs.some((w) => w.running) && <p className="text-xs leading-relaxed text-ink-3">{t("lib-workflows.waiting_note")}</p>}
          {runs.map((w) => (
            <WorkflowCard key={w.task_id} run={w} mid={session.machine_id} scope={`${session.machine_id}/${session.session_id}:`} onJump={onJumpToCall} />
          ))}
        </section>
      )}
      {loop && <LoopCard loop={loop} onStop={onStopLoop} />}
    </section>
  );
}

/** The /loop driving this agent: what it repeats, how often, when it wakes next — and a way to stop it. */
function LoopCard({ loop, onStop }: { loop: LoopInfo; onStop: () => void }) {
  const now = useNow(30_000);
  const [confirming, setConfirming] = useState(false);
  const cadence = loopCadence(loop);
  const next = loopNext(loop, now);
  return (
    <div className="flex flex-col gap-2 rounded-card bg-accent-tint/50 px-3.5 py-3 shadow-hairline">
      <div className="flex items-center gap-2.5">
        <Icon name="loop" size={15} stroke={2.2} className="shrink-0 text-accent" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-[13px] font-semibold text-ink">{cadence ? t("chat-activity.looping_every", { cadence }) : t("chat-activity.looping")}</span>
          {next && <span className="truncate text-[12px] text-ink-2">{next}</span>}
        </div>
        {!confirming && (
          <button
            type="button"
            onClick={() => setConfirming(true)}
            className="inline-flex h-8 shrink-0 items-center rounded-control bg-surface px-3 text-[12.5px] font-medium whitespace-nowrap text-red shadow-btn transition-colors hover:bg-hover"
          >
            {t("chat-activity.stop_loop")}
          </button>
        )}
      </div>
      {loopTask(loop) && <p className="text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2">{loopTask(loop)}</p>}
      {loop.reason && (
        <span className="flex items-start gap-1.5 text-[12px] text-ink-3">
          <Icon name="chat" size={12} className="mt-[2px] shrink-0" />
          {loop.reason}
        </span>
      )}
      {confirming && (
        <div className="flex flex-col gap-2 rounded-[8px] bg-surface px-3 py-2.5 shadow-hairline">
          <p className="text-[12.5px] leading-relaxed text-ink-2">{t("chat-activity.stop_loop_explain")}</p>
          <div className="flex justify-end gap-2">
            <Button tone="quiet" onClick={() => setConfirming(false)}>
              {t("common.cancel")}
            </Button>
            <Button tone="danger" onClick={() => { setConfirming(false); onStop(); }}>
              {t("chat-activity.stop_loop")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/** Every background task, how long it has run, and its output (in the side panel). */
function BackgroundList({ tasks, onOutput }: { tasks: BackgroundTask[]; onOutput: (t: BackgroundTask) => void }) {
  const now = useNow(1000);
  return (
    <div className="flex flex-col overflow-hidden rounded-card bg-surface shadow-hairline">
      {tasks.map((task, i) => {
        const cmd = task.command && task.command !== backgroundTitle(task) ? task.command : "";
        return (
          <div key={task.id} className={cx("flex items-start gap-2.5 px-3.5 py-2.5", i > 0 && "border-t border-line")}>
            <span className={cx("mt-[3px] flex size-4 shrink-0 items-center justify-center", task.kind === "workflow" ? "text-ai" : "text-orange")}>
              <Icon name={task.kind === "agent" ? "agent" : task.kind === "workflow" ? "workflow" : task.kind === "monitor" ? "monitor" : "run"} size={13} />
            </span>
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="truncate text-[13px] font-medium text-ink">{backgroundTitle(task)}</span>
              {cmd && (
                <button type="button" onClick={() => copyText(cmd)} title={t("chat-activity.copy_command")} className="line-clamp-2 text-left font-mono text-[11.5px] text-ink-3 hover:text-ink-2">
                  {cmd}
                </button>
              )}
            </div>
            <span className="shrink-0 text-[12px] whitespace-nowrap text-ink-3 tabular-nums">{elapsed(task.started_at, now)}</span>
            {task.output_file && (
              <button type="button" onClick={() => onOutput(task)} className="flex shrink-0 items-center gap-0.5 text-[12px] font-medium whitespace-nowrap text-accent-ink hover:underline">
                {t("chat-activity.view_output")}
                <Icon name="chevronRight" size={12} stroke={2.2} />
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
