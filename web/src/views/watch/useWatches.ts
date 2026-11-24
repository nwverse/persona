// The watch cards as the hub has them, kept current: read on mount, again when the hub says the cards or the schedule
// changed (assistant_watches, assistant_schedule), and after a reconnect the hub could not resume.
import { useCallback, useEffect, useRef, useState } from "react";
import { isResumed } from "../../lib/reconnect";
import { onHubEvent } from "../../lib/store";
import { loadWatches, type Watch } from "../../lib/watches";

/** `null` while the first read is out; `error` set when the hub has no watch cards (an older manager) or failed. */
export function useWatches(project?: string) {
  const [watches, setWatches] = useState<Watch[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const seq = useRef(0);
  const load = useCallback(async () => {
    const mine = ++seq.current;
    try {
      const ws = await loadWatches(project);
      if (mine !== seq.current) return;
      setWatches(ws);
      setError(null);
    } catch (e) {
      if (mine !== seq.current) return;
      setError(e);
    }
  }, [project]);
  useEffect(() => {
    void load();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const changed = () => {
      clearTimeout(timer);
      timer = setTimeout(() => void load(), 300);
    };
    const offs = [onHubEvent("assistant_watches", changed), onHubEvent("assistant_schedule", changed), onHubEvent("hello", (msg) => !isResumed(msg) && changed())];
    return () => {
      offs.forEach((off) => off());
      clearTimeout(timer);
    };
  }, [load]);
  /** Show `next` at once (an optimistic change); a newer read replaces it. */
  const show = useCallback((next: Watch[]) => {
    seq.current++;
    setWatches(next);
  }, []);
  return { watches, error, reload: load, show };
}
