// Files and Git keep their own view arrays. Register the matching phone history entries after their enclosing panel.
import { useEffect, useRef } from "react";
import { openOverlay } from "./back-close";

export function useBackStack(depth: number, popTo: (depth: number) => void, narrow: boolean) {
  const entries = useRef<(() => void)[]>([]);
  const pop = useRef(popTo);
  pop.current = popTo;
  useEffect(() => {
    let current = true;
    // A child's effect runs before its parent's when a panel opens directly on a file.
    queueMicrotask(() => {
      if (!current) return;
      const stack = entries.current;
      const target = narrow ? depth : 0;
      while (stack.length > target) stack.pop()!();
      while (stack.length < target) {
        const level = stack.length;
        stack.push(openOverlay((navigating) => {
          // The session's normal route change saves the current file before unmounting the panel.
          if (navigating) return;
          if (level >= stack.length) return;
          stack.length = level;
          pop.current(level);
        }));
      }
    });
    return () => { current = false; };
  }, [depth, narrow]);
  useEffect(() => () => { for (const close of entries.current.splice(0)) close(); }, []);
}
