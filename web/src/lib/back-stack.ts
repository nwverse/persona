// Views opened inside an overlay, one over another (a file in the Files panel, a diff in Git), each with its own history
// entry over the overlay's (lib/back-close.ts). Only the lazy panels use it, so it stays off the first screen.
import { useEffect, useRef } from "react";
import { openOverlay } from "./back-close";

/** `depth` views open: while `enabled` (a narrow screen) each has an entry over the overlay's, so Back steps out of the
 *  top one (`popTo(i)` keeps the first i) before it closes the overlay; closing the overlay takes them off with its own,
 *  in one Back. */
export function useBackStack(depth: number, popTo: (n: number) => void, enabled = true): void {
  const pop = useRef(popTo);
  pop.current = popTo;
  const levels = useRef<(() => void)[]>([]);
  const want = enabled ? depth : 0;
  useEffect(() => {
    // once this commit's effects have run: the overlay the views are in (its effect runs after its children's) has
    // pushed its own entry by then, so theirs go over it even when it mounts with one open (a panel restored on a file)
    let current = true;
    queueMicrotask(() => {
      if (!current) return;
      const open = levels.current;
      for (const close of open.splice(want)) close();
      while (open.length < want) {
        const i = open.length;
        open.push(
          openOverlay(() => {
            open.splice(i);
            pop.current(i);
          }),
        );
      }
    });
    return () => {
      current = false;
    };
  }, [want]);
  useEffect(
    () => () => {
      for (const close of levels.current.splice(0)) close();
    },
    [],
  );
}
