// The offline sheet's "Start and reconnect" (docs/design/machine-cloud-ops.md "Start and reconnect", "The web"), loaded
// only for a computer that has a binding. It reads the computer's cloud host when the sheet opens; when the host offers
// start, the press is the first action, before Reconnect, and it is the confirmation for start: one press sends
// {action: start, confirm: true, reconnect: true}. The recovery's phase then shows under the actions (starting,
// reconnecting, done or failed, and a changed address), read again every few seconds until it ends, while the page is
// shown and for a bounded time. A failure says why and leaves Reconnect as it was.
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { canStartAndReconnect, cloudFailure, fetchMachineCloud, recoveryRunning, recoveryText, reloadCloud, runCloudAction, startAndReconnectRequest, type CloudFailure } from "../../lib/cloud-hosts";
import type { CloudMachine } from "../../lib/extension-wire";
import { documentPage } from "../../lib/visible-poll";
import { Button, cx, Spinner } from "../../ui/atoms";
import { FailureLine } from "./CloudParts";

export const RECOVERY_POLL_MS = 3_000;
export const RECOVERY_POLL_LIMIT_MS = 8 * 60_000;

/** `row(lead, quiet)`: the sheet's own row of actions, with `lead` before Reconnect and Reconnect then secondary. */
export function OfflineCloud({ mid, row }: { mid: string; row: (lead?: ReactNode, quiet?: boolean) => ReactNode }) {
  const [cm, setCm] = useState<CloudMachine | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<CloudFailure | null>(null);
  const [visible, setVisible] = useState(() => documentPage.visible());
  const live = useRef(true);
  const since = useRef(Date.now());

  const read = useCallback(async (signal?: AbortSignal) => {
    try {
      const next = await fetchMachineCloud(mid, { signal });
      if (live.current) setCm(next);
    } catch {
      // no host to read (an older hub, a provider that failed): the sheet stays as it is without a cloud host; a
      // poll that failed keeps the last phase on screen until the next one
    }
  }, [mid]);

  useEffect(() => {
    live.current = true;
    const ctl = new AbortController();
    void read(ctl.signal);
    return () => {
      live.current = false;
      ctl.abort();
    };
  }, [read]);

  useEffect(() => documentPage.subscribe(() => setVisible(documentPage.visible())), []);

  const running = recoveryRunning(cm?.recovery);
  // every poll, read or failed, counts a tick that schedules the next one (a failed read leaves `cm` as it was)
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!running || !visible || Date.now() - since.current > RECOVERY_POLL_LIMIT_MS) return;
    const timer = setTimeout(() => void read().finally(() => {
      if (live.current) setTick((n) => n + 1);
    }), RECOVERY_POLL_MS);
    return () => clearTimeout(timer);
  }, [running, visible, cm, read, tick]);

  const start = async () => {
    setBusy(true);
    setFailure(null);
    try {
      const out = await runCloudAction(mid, startAndReconnectRequest());
      if (!live.current) return;
      since.current = Date.now();
      setCm((c) => (c ? { ...c, host: out.host ?? c.host, recovery: out.recovery ?? { phase: "starting", started_at: new Date().toISOString() } } : c));
      void reloadCloud();
    } catch (e) {
      if (live.current) setFailure(cloudFailure(e));
    } finally {
      if (live.current) setBusy(false);
    }
  };

  const offer = canStartAndReconnect(cm) && !running;
  const recovery = cm?.recovery;
  if (!offer && !recovery) return <>{row()}</>;
  const lead = offer ? (
    <Button tone="primary" icon="power" busy={busy} onClick={() => void start()} testId="cloud-start-reconnect">
      {t("machines-cloud.start_reconnect")}
    </Button>
  ) : running ? (
    <Button tone="primary" icon="power" busy disabled testId="cloud-start-reconnect">
      {t("machines-cloud.start_reconnect")}
    </Button>
  ) : undefined;
  return (
    <div data-testid="cloud-offline">
      {row(lead, !!lead)}
      {recovery && (
        <p className={cx("mt-1.5 flex items-start gap-1.5 text-meta", recovery.phase === "failed" ? "text-red" : recovery.phase === "done" ? "text-green" : "text-ink-2")} role="status" data-testid="cloud-recovery" data-phase={recovery.phase}>
          {running && <span className="mt-1 shrink-0"><Spinner /></span>}
          <span className="min-w-0 break-words">
            {recoveryText(recovery)}
            {recovery.ip_changed && <span className="block text-submeta text-ink-3">{t("machines-cloud.ip_changed")}</span>}
          </span>
        </p>
      )}
      <FailureLine failure={failure} className="mt-1.5" />
    </div>
  );
}
