// The complete Agents list's long groups, drawn a part at a time (the sidebar and the phone's Agents page). With 400
// Agents in one group the list drew every row at once: about 8,000 elements, 170–230 ms of a mid-range phone's main
// thread each time the page opened, laid out again on every page switch. A short list (up to `windowHead + windowPart`
// rows) is drawn as it always was; a long one by WindowedParts (its own chunk, off the first screen: loaded once the
// browser is idle after start, or the first time a list is that long), and while that chunk is on its way, by its
// first `windowHead` rows and an empty box for the rest, so nothing below jumps when it comes in.
import { Suspense, type ComponentType, type ReactNode } from "react";
import { lazyPage } from "../lib/lazy-page";
import { SESSION_LIST_METRICS } from "../ui/list-metrics";

const { windowHead: HEAD, windowPart: PART, estimate } = SESSION_LIST_METRICS;

export type WindowedProps<T> = {
  items: readonly T[];
  render: (item: T) => ReactNode;
  /** The open row (its part and the two beside it are always drawn). */
  holds?: (item: T) => boolean;
  /** Each drawn part's own layout: the list's (its rows and the gap between them). */
  className?: string;
};

/** Whether a list of `n` rows is drawn a part at a time. */
export const windowed = (n: number) => n > HEAD + PART && typeof IntersectionObserver !== "undefined";

// at idle too: opening an Agent deep in a folded long group draws its part in the same frame, so the list can scroll to it
const Parts = lazyPage(() => import("./WindowedParts").then((m) => m.WindowedParts as ComponentType<WindowedProps<unknown>>), { idle: true });

export function WindowedRows<T>(p: WindowedProps<T>) {
  if (!windowed(p.items.length)) return <>{p.items.map((item) => p.render(item))}</>;
  return (
    <Suspense fallback={<>{p.items.slice(0, HEAD).map((item) => p.render(item))}<div aria-hidden style={{ height: (p.items.length - HEAD) * estimate }} /></>}>
      <Parts {...(p as WindowedProps<unknown>)} />
    </Suspense>
  );
}
