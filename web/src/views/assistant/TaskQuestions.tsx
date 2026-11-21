import { memo, useRef } from "react";
import { t } from "../../i18n";
import { useStore, type State } from "../../lib/store";
import { byTime } from "../../lib/times";
import type { Todo } from "../../lib/todos";
import type { Interaction } from "../../lib/types";
import { Icon } from "../../ui/icons";
import { AnsweredSummary, InteractionCard } from "../chat/Interactions";

function sameRow(a: object, b: object): boolean {
  const ka = Object.keys(a), kb = Object.keys(b);
  return ka.length === kb.length && ka.every((k) => Object.is((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
}

function useStoreRows<T extends object>(select: (s: State) => T[]): T[] {
  const last = useRef<T[] | null>(null);
  return useStore((s) => {
    const rows = select(s);
    const prev = last.current;
    if (prev && prev.length === rows.length && rows.every((r, i) => sameRow(r, prev[i]))) return prev;
    last.current = rows;
    return rows;
  });
}

/** The oldest question the conversation's tasks' agents ask, under its task's name ("N more"). */
function questionsIn(s: State, conversation: string) {
  const out: { todo: Todo; ix: Interaction }[] = [];
  const keys = new Set<string>();
  for (const todo of s.todos) {
    if (todo.origin !== conversation || todo.done_at) continue;
    const mine = (todo.agents ?? []).filter((a) => !a.released_at).map((a) => `${a.machine_id}/${a.session_id}`);
    mine.forEach((k) => keys.add(k));
    for (const ix of s.interactions) if (mine.includes(`${ix.machine_id}/${ix.session_id}`)) out.push({ todo, ix });
  }
  const done = Object.values(s.answered).filter((a) => keys.has(`${a.ix.machine_id}/${a.ix.session_id}`));
  return { asks: out.sort((a, b) => byTime(a.ix.created_at, b.ix.created_at)), answered: done };
}

/** Live voice-call task questions strip; loaded lazily when a voice call enters live state. */
export const TaskQuestions = /* @__PURE__ */ memo(function TaskQuestions({ conversation }: { conversation: string }) {
  const asks = useStoreRows((s) => questionsIn(s, conversation).asks);
  const answered = useStoreRows((s) => questionsIn(s, conversation).answered);
  const first = asks[0];
  if (!first && !answered.length) return null;
  return (
    <div className="flex flex-col gap-1">
      {answered.length > 0 && <AnsweredSummary answered={answered} />}
      {first && (
        <>
          <div className="flex items-center gap-1.5 px-1 text-label font-semibold text-attention">
            <Icon name="hand" size={13} />
            <span className="truncate">{first.todo.title}</span>
            <span className="flex-1" />
            {asks.length > 1 && <span className="shrink-0">{t("assistant-tasks.more_asks", { count: asks.length - 1 })}</span>}
          </div>
          <InteractionCard ix={first.ix} />
        </>
      )}
    </div>
  );
});
