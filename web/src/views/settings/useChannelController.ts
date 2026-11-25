// Shared channel detail controller: pins manager scope, sequences reads, serializes writes, and fences channel switches.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import {
  channelDetail, forgetChannels, testChannel, unbindChannel, updateChannel,
  type ChannelDetail, type ChannelUpdateBody,
} from "../../lib/channels";
import { otherManager, pinManager } from "../../lib/manager-choice";
import { toast } from "../../ui/atoms";

export interface ChannelController {
  detail: ChannelDetail | null;
  error: string;
  loading: boolean;
  busy: string;
  reloadKey: number;
  testFeedback: { tone: "ok" | "error"; text: string } | null;
  load: () => Promise<void>;
  change: (what: string, body: ChannelUpdateBody) => Promise<void>;
  unbind: () => Promise<boolean>;
  runTest: () => Promise<void>;
  adopt: (next: ChannelDetail) => void;
}

/** Loads and mutates one channel with request sequencing, exclusive writes, and channel-switch fencing. */
export function useChannelController(channelId: string | null, manager?: string): ChannelController {
  useLayoutEffect(() => {
    if (!channelId) return undefined;
    return pinManager(`/channels/${encodeURIComponent(channelId)}`, otherManager(manager));
  }, [channelId, manager]);

  const [detail, setDetail] = useState<ChannelDetail | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [testFeedback, setTestFeedback] = useState<{ tone: "ok" | "error"; text: string } | null>(null);

  const mounted = useRef(true);
  const epochRef = useRef(0);
  const seq = useRef(0);
  const activeWrite = useRef(0);
  const detailRef = useRef<ChannelDetail | null>(null);
  detailRef.current = detail;

  const load = useCallback(async () => {
    const epoch = epochRef.current;
    const n = ++seq.current;
    if (!channelId) {
      setDetail(null);
      setError("");
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const next = await channelDetail(channelId);
      if (!mounted.current || epoch !== epochRef.current || n !== seq.current) return;
      setDetail(next);
      setError("");
      setReloadKey((k) => k + 1);
    } catch (e) {
      if (mounted.current && epoch === epochRef.current && n === seq.current) setError(errorText(e));
    } finally {
      if (mounted.current && epoch === epochRef.current && n === seq.current) setLoading(false);
    }
  }, [channelId]);

  useEffect(() => {
    mounted.current = true;
    epochRef.current++;
    activeWrite.current = 0;
    setDetail(null);
    setError("");
    setBusy("");
    setTestFeedback(null);
    void load();
    return () => {
      mounted.current = false;
      epochRef.current++;
      seq.current++;
      activeWrite.current = 0;
    };
  }, [load]);

  const adopt = useCallback((next: ChannelDetail) => {
    seq.current++;
    forgetChannels();
    setDetail(next);
    setError("");
  }, []);

  const exclusive = useCallback(async (what: string, save: () => Promise<ChannelDetail | void>): Promise<boolean> => {
    if (!channelId || activeWrite.current !== 0) return false;
    const epoch = epochRef.current;
    const n = ++seq.current;
    activeWrite.current = n;
    setBusy(what);
    let stale = false;
    let ok = false;
    try {
      const next = await save();
      if (next) {
        forgetChannels();
        if (mounted.current && epoch === epochRef.current) {
          if (n === seq.current) setDetail(next);
          else stale = true;
        }
      }
      ok = mounted.current && epoch === epochRef.current;
    } catch (e) {
      if (mounted.current && epoch === epochRef.current) toast(errorText(e), "error");
    } finally {
      if (activeWrite.current === n) activeWrite.current = 0;
      if (mounted.current && epoch === epochRef.current) setBusy("");
    }
    if (stale && mounted.current && epoch === epochRef.current) void load();
    return ok;
  }, [channelId, load]);

  const change = useCallback(async (what: string, body: ChannelUpdateBody) => {
    if (!channelId) return;
    await exclusive(what, () => updateChannel(channelId, body));
  }, [channelId, exclusive]);

  const unbind = useCallback(async () => {
    if (!channelId) return false;
    return exclusive("unbind", () => unbindChannel(channelId));
  }, [channelId, exclusive]);

  const runTest = useCallback(async () => {
    const current = detailRef.current;
    if (!channelId || !current) return;
    const epoch = epochRef.current;
    const replyOnly = !!current.capabilities.reply_only;
    await exclusive("test", async () => {
      if (mounted.current && epoch === epochRef.current) setTestFeedback(null);
      try {
        const res = await testChannel(channelId);
        const text = replyOnly || res?.reply_only
          ? (res?.note || t("settings-channels.connection_ok", { name: current.name }))
          : (res?.note || t("settings-channels.test_sent", { name: current.name }));
        if (mounted.current && epoch === epochRef.current) {
          setTestFeedback({ tone: "ok", text });
          toast(text, "ok");
        }
      } catch (e) {
        if (mounted.current && epoch === epochRef.current) {
          setTestFeedback({ tone: "error", text: errorText(e) });
        }
        throw e;
      }
    });
  }, [channelId, exclusive]);

  return { detail, error, loading, busy, reloadKey, testFeedback, load, change, unbind, runTest, adopt };
}
