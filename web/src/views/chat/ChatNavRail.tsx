import { lazy, Suspense, useCallback, useMemo, useRef, useState } from "react";
import { indexedNavTicks, navRailTicks } from "../../lib/nav-rail";
import type { QuestionMark } from "../../lib/question-states";
import type { ChatLine } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { USER_INPUT_PAGE_SIZE, type UserInputIndex } from "../../lib/user-input-index";
import { NavRail } from "./NavRail";

const QuestionMarksHost = lazy(() => import("./QuestionMarksHost"));

/** Build previews with the rail. A chat without a rail has no need to project its history into ticks. */
export function ChatNavRail({ mid, sid, lines, items, inputIndex, revealedSeq, scroller, onJump, questionsOn, loading, busy }: {
  mid: string; sid: string; lines: ChatLine[]; items: Item[]; inputIndex: UserInputIndex | null; revealedSeq?: number;
  scroller: HTMLDivElement | null; onJump: (id: string) => Promise<void>; questionsOn: boolean; loading: boolean; busy?: string;
}) {
  const ticks = useMemo(() => {
    if (inputIndex) return indexedNavTicks(inputIndex, lines, items);
    const loaded = navRailTicks(lines);
    return revealedSeq === undefined ? loaded.slice(-USER_INPUT_PAGE_SIZE) : loaded;
  }, [inputIndex, lines, items, revealedSeq]);
  const [marks, setMarks] = useState<ReadonlyMap<string, QuestionMark>>();
  const questionOpened = useRef<((id: string) => void) | undefined>(undefined);
  const setOpened = useCallback((callback: ((id: string) => void) | undefined) => { questionOpened.current = callback; }, []);
  const openQuestion = useCallback((id: string) => {
    questionOpened.current?.(id);
    return onJump(id);
  }, [onJump]);
  return (
    <>
      {questionsOn && (
        <Suspense fallback={null}>
          <QuestionMarksHost mid={mid} sid={sid} on={!loading} lines={lines} busy={busy} ticks={ticks} scroller={scroller} onMarks={setMarks} onOpened={setOpened} />
        </Suspense>
      )}
      {ticks.length > 0 && <NavRail ticks={ticks} total={inputIndex?.total} scroller={scroller} onJump={marks ? openQuestion : onJump} reset={`${mid}/${sid}`} marks={marks} />}
    </>
  );
}
