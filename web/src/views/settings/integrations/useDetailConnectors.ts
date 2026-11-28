// One manager-scoped read serves all connector panels, including their startup polling.
import { useCallback, useEffect, useRef, useState } from "react";
import { errorText } from "../../../lib/api";
import { isStarting, listConnectors, type HubConnector } from "../../../lib/connectors";
import { sameManager } from "../../../lib/manager-choice";

const EMPTY: HubConnector[] = [];

export function useDetailConnectors(ids: readonly string[], manager?: string) {
  const [connectors, setConnectors] = useState<HubConnector[] | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const sequence = useRef(0);
  const pending = useRef(false);
  const key = JSON.stringify(ids);

  const load = useCallback(async () => {
    const wanted: string[] = JSON.parse(key);
    if (!wanted.length) return;
    const read = ++sequence.current;
    pending.current = true;
    setLoading(true);
    try {
      const list = await listConnectors();
      if (sequence.current !== read) return;
      const found = wanted.flatMap((id) => list.find((c) => c.id === id && sameManager(c.manager, manager)) ?? []);
      setConnectors(found);
      setError("");
    } catch (e) {
      if (sequence.current === read) setError(errorText(e));
    } finally {
      if (sequence.current === read) {
        pending.current = false;
        setLoading(false);
      }
    }
  }, [key, manager]);

  useEffect(() => {
    setConnectors(null);
    setLoading(false);
    setError("");
    void load();
    return () => {
      sequence.current++;
      pending.current = false;
    };
  }, [load]);

  const starting = connectors?.some(isStarting);
  useEffect(() => {
    if (!starting) return;
    let ticks = 0;
    const timer = setInterval(() => {
      if (++ticks === 15) clearInterval(timer);
      if (!pending.current) void load();
    }, 1000);
    return () => clearInterval(timer);
  }, [starting, load]);

  function onSaved(next: HubConnector) {
    setConnectors((cur) => cur?.map((c) => c.id === next.id && sameManager(c.manager, next.manager) ? next : c) ?? null);
  }
  return { connectors: connectors ?? EMPTY, loaded: connectors !== null, load, loading, error, onSaved };
}
