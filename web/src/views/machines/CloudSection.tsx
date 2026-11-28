// A computer's "Cloud server" section (docs/design/machine-cloud-ops.md "The web"), loaded on its own by
// MachineDetail. Nothing at all without a binding or an enabled provider. Unbound: one line and "Link a cloud
// server…", which lists the instances no computer is bound to. Bound: the provider and vendor, the state as a plain
// word, the address, place and size, the lifecycle notes, Refresh, and the instance's own actions: Start, Shut down and
// Restart in the row, the rest and Unlink in the menu. Each action passes its gate as interface ("Actions and safety
// levels"): read runs at once, confirm asks in a dialog that names the computer, destructive asks in the danger tone
// and sends the instance's id back. The row shows the press's progress and outcome, and the section reads the host
// again after a write, every few seconds while the instance is on its way or a recovery runs (not while the page is
// hidden, and not for ever). A provider that is off or failing leaves the binding, its words and only Unlink.
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { formatDate, t } from "../../i18n";
import {
  actionKey,
  actionLabel,
  actionRequest,
  bindingOf,
  bindMachineCloud,
  bindRequest,
  cloudFailure,
  consoleUrl,
  fetchCloudHosts,
  fetchCloudSnapshots,
  fetchMachineCloud,
  gateOf,
  hasAction,
  hostKey,
  hostName,
  outcomeText,
  patchBinding,
  recoveryRunning,
  recoveryText,
  reloadCloud,
  runCloudAction,
  shouldPoll,
  snapshotStateWord,
  splitActions,
  transitional,
  unbindMachineCloud,
  unboundHosts,
  useCloudStore,
  vendorLabel,
  type CloudFailure,
} from "../../lib/cloud-hosts";
import type { CloudBinding, CloudHost, CloudHostAction, CloudHosts, CloudMachine, CloudSnapshot } from "../../lib/extension-wire";
import { isAbort } from "../../lib/api";
import { documentPage } from "../../lib/visible-poll";
import { Button, cx, Dialog, IconButton, Loading, Menu, Spinner, useMenu } from "../../ui/atoms";
import type { MenuItem } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { ActionConfirm, FailureLine, HostList, InstanceFacts, StateChip, VendorBadge } from "./CloudParts";

/** How often a host on its way is read again, and for how long at most after the last press or change. */
export const CLOUD_POLL_MS = 4_000;
export const CLOUD_POLL_LIMIT_MS = 8 * 60_000;

type Heading = (title: string, right?: ReactNode) => ReactNode;

export function CloudSection({ mid, machineName, heading }: { mid: string; machineName: string; heading: Heading }) {
  const store = useCloudStore();
  const binding = bindingOf(store, mid);
  if (store.providers === null || store.bindings === null) return null;
  if (!binding && store.providers.length === 0) return null;
  return (
    <section data-testid="cloud-section" data-bound={binding ? "yes" : "no"}>
      {binding ? <Bound key={binding.instance} mid={mid} machineName={machineName} binding={binding} heading={heading} /> : <Unbound mid={mid} machineName={machineName} heading={heading} />}
    </section>
  );
}

// ── unbound ──

function Unbound({ mid, machineName, heading }: { mid: string; machineName: string; heading: Heading }) {
  const [picking, setPicking] = useState(false);
  return (
    <>
      {heading(t("machines-cloud.title"))}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-card bg-surface px-4 py-3 shadow-card">
        <Icon name="cloud" size={15} className="shrink-0 text-ink-3" />
        <p className="min-w-0 flex-1 basis-48 text-meta text-ink-3">{t("machines-cloud.unbound_line", { machine: machineName })}</p>
        <Button tone="secondary" icon="link" onClick={() => setPicking(true)} testId="cloud-link-open">{t("machines-cloud.link_open")}</Button>
      </div>
      {picking && <LinkDialog mid={mid} machineName={machineName} onClose={() => setPicking(false)} />}
    </>
  );
}

function LinkDialog({ mid, machineName, onClose }: { mid: string; machineName: string; onClose: () => void }) {
  const [hosts, setHosts] = useState<CloudHosts | null>(null);
  const [failure, setFailure] = useState<CloudFailure | null>(null);
  const [busy, setBusy] = useState("");
  const [linkFailure, setLinkFailure] = useState<CloudFailure | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    fetchCloudHosts({ signal: ctl.signal }).then(setHosts, (e) => !isAbort(e) && setFailure(cloudFailure(e)));
    return () => ctl.abort();
  }, []);
  const link = async (h: CloudHost) => {
    setBusy(hostKey(h));
    setLinkFailure(null);
    try {
      const cm = await bindMachineCloud(mid, bindRequest(h));
      patchBinding(mid, cm.binding);
      void reloadCloud();
      onClose();
    } catch (e) {
      setLinkFailure(cloudFailure(e));
      setBusy("");
    }
  };
  return (
    <Dialog open onClose={() => !busy && onClose()} width={520} title={t("machines-cloud.link_title", { machine: machineName })}>
      <p className="mb-2 text-meta leading-relaxed text-ink-2">{t("machines-cloud.link_body", { machine: machineName })}</p>
      <FailureLine failure={linkFailure} className="mb-2" />
      <HostList
        hosts={hosts ? unboundHosts(hosts.hosts) : []}
        problems={hosts?.problems ?? []}
        partial={hosts?.partial}
        loading={!hosts && !failure}
        failure={failure}
        empty={t("machines-cloud.link_empty")}
        action={(h) => (
          <Button tone="secondary" busy={busy === hostKey(h)} disabled={!!busy} onClick={() => void link(h)} testId="cloud-link">{t("machines-cloud.link")}</Button>
        )}
      />
    </Dialog>
  );
}

// ── bound ──

type Outcome = { key: string; tone: "ok" | "info" | "error"; text: string; failure?: CloudFailure; url?: string };

function Bound({ mid, machineName, binding, heading }: { mid: string; machineName: string; binding: CloudBinding; heading: Heading }) {
  const [cm, setCm] = useState<CloudMachine | null>(null);
  const [failure, setFailure] = useState<CloudFailure | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [busy, setBusy] = useState("");
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [asking, setAsking] = useState<CloudHostAction | null>(null);
  const [askFailure, setAskFailure] = useState<CloudFailure | null>(null);
  const [snapshots, setSnapshots] = useState(false);
  const [visible, setVisible] = useState(() => documentPage.visible());
  const menu = useMenu();
  const generation = useRef(0);
  const live = useRef(true);
  // when the section last had a reason to follow the host (a press, a change it saw): polling stops this long after
  const since = useRef(Date.now());

  const read = useCallback(async (refresh = false) => {
    const n = ++generation.current;
    if (refresh) setRefreshing(true);
    try {
      const next = await fetchMachineCloud(mid, { refresh });
      if (!live.current || n !== generation.current) return;
      setCm(next);
      setFailure(null);
      patchBinding(mid, next.bound ? next.binding : undefined);
    } catch (e) {
      if (!live.current || n !== generation.current || isAbort(e)) return;
      setFailure(cloudFailure(e));
    } finally {
      if (live.current && n === generation.current) setRefreshing(false);
    }
  }, [mid]);

  useEffect(() => {
    live.current = true;
    void read();
    return () => {
      live.current = false;
      generation.current++;
    };
  }, [read]);

  useEffect(() => documentPage.subscribe(() => setVisible(documentPage.visible())), []);

  // follow the host while it is on its way or a recovery runs; hidden, unmounted or past the limit, it stops. A failed
  // poll sets a new `failure` and so schedules the next one too, as a read sets a new `cm`
  useEffect(() => {
    if (!shouldPoll(cm) || !visible) return;
    if (Date.now() - since.current > CLOUD_POLL_LIMIT_MS) return;
    const timer = setTimeout(() => void read(transitional(cm?.host?.instance.state)), CLOUD_POLL_MS);
    return () => clearTimeout(timer);
  }, [cm, visible, read, failure]);

  const host = cm?.host;
  const instance = host?.instance;
  const problem = cm?.problem;

  const unlink = async () => {
    setBusy("unlink");
    try {
      await unbindMachineCloud(mid);
      if (!live.current) return;
      patchBinding(mid, undefined);
      void reloadCloud();
    } catch (e) {
      if (!live.current) return;
      setOutcome({ key: "unlink", tone: "error", text: "", failure: cloudFailure(e) });
      setBusy("");
    }
  };

  const run = async (a: CloudHostAction, fromDialog: boolean) => {
    if (!instance) return;
    const key = actionKey(a);
    setBusy(key);
    setOutcome(null);
    if (fromDialog) setAskFailure(null);
    try {
      const out = await runCloudAction(mid, actionRequest(a, instance.id));
      if (!live.current) return;
      let url: string | undefined;
      if (a.action === "console") {
        url = consoleUrl(out, instance) || undefined;
        if (url) window.open(url, "_blank", "noopener,noreferrer");
      }
      setOutcome({ key, tone: out.status === "accepted" ? "info" : "ok", text: outcomeText(a, out), url });
      if (out.host) setCm((c) => (c ? { ...c, host: out.host, recovery: out.recovery ?? c.recovery } : c));
      setAsking(null);
      since.current = Date.now();
      if (gateOf(a) !== "read") {
        void reloadCloud();
        void read();
      }
    } catch (e) {
      if (!live.current) return;
      const f = cloudFailure(e);
      if (fromDialog) setAskFailure(f);
      else setOutcome({ key, tone: "error", text: "", failure: f });
    } finally {
      if (live.current) setBusy("");
    }
  };

  const press = (a: CloudHostAction) => {
    if (a.action === "snapshot_list") return setSnapshots(true);
    if (a.action === "console" && !hasAction(host, "console")) {
      // only the instance's own page: a link, no program call
      const url = consoleUrl(undefined, instance);
      if (url) window.open(url, "_blank", "noopener,noreferrer");
      return;
    }
    if (gateOf(a) === "read") return void run(a, false);
    setAskFailure(null);
    setAsking(a);
  };

  const split = host ? splitActions(host) : { row: [], menu: [] };
  const items: MenuItem[] = [
    ...(problem ? [] : split.menu).map((a) => ({
      key: actionKey(a),
      label: a.action === "snapshot_list" ? t("machines-cloud.action_snapshots") : actionLabel(a),
      icon: a.action === "console" ? ("external" as const) : a.action === "snapshot_list" ? ("archive" as const) : gateOf(a) === "destructive" ? ("power" as const) : undefined,
      danger: gateOf(a) === "destructive",
      disabled: !!busy,
      onSelect: () => press(a),
    })),
    {
      key: "unlink",
      label: t("machines-cloud.unlink"),
      icon: "x" as const,
      divider: !problem && split.menu.length > 0,
      disabled: !!busy,
      confirm: t("machines-cloud.unlink_confirm"),
      onSelect: () => unlink(),
    },
  ];

  const right = (
    <IconButton icon="retry" label={t("machines-cloud.refresh")} disabled={refreshing || !!busy} onClick={() => void read(true)} />
  );
  const title = instance ? hostName(instance) : binding.display_name?.trim() || binding.instance;
  const state = instance?.state ?? binding.state;
  return (
    <>
      {heading(t("machines-cloud.title"), right)}
      <div className="rounded-card bg-surface shadow-card" data-testid="cloud-bound" data-state={state}>
        <div className="flex items-start gap-3 px-4 pt-3 pb-2">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-[9px] bg-field text-ink-2"><Icon name="cloud" size={16} /></span>
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <span className="truncate text-card-title font-medium text-ink" data-testid="cloud-name">{title}</span>
              {!problem && <StateChip state={state} />}
              {refreshing && <span role="status" aria-label={t("machines-cloud.refreshing")}><Spinner /></span>}
            </div>
            <p className="flex min-w-0 items-center gap-1.5 text-submeta text-ink-3">
              <VendorBadge vendor={vendorLabel(binding)} />
              <span className="truncate">{[host?.provider_name || binding.provider_name, binding.account].filter(Boolean).join(" · ")}</span>
            </p>
          </div>
          <IconButton icon="more" label={t("machines-cloud.more")} size={32} onClick={menu.open} />
        </div>
        <div className="px-4 pb-3">
          {problem ? (
            <p className="flex items-start gap-1.5 text-label text-orange" data-cloud-problem={problem.code} data-testid="cloud-problem">
              <Icon name="warning" size={13} className="mt-0.5 shrink-0" />
              <span className="min-w-0 break-words">{t("machines-cloud.problem_line", { provider: problem.provider_name || binding.provider_name || binding.provider, error: problem.error })}</span>
            </p>
          ) : instance ? (
            <InstanceFacts instance={instance} />
          ) : failure ? null : (
            <Loading />
          )}
          <FailureLine failure={failure} className="mt-1" />
          {cm?.recovery && (
            <p className={cx("mt-1 text-label", cm.recovery.phase === "failed" ? "text-red" : cm.recovery.phase === "done" ? "text-green" : "text-ink-2")} role="status" data-testid="cloud-recovery" data-phase={cm.recovery.phase}>
              {recoveryRunning(cm.recovery) && <span className="mr-1.5 inline-block align-middle"><Spinner /></span>}
              {recoveryText(cm.recovery)}
              {cm.recovery.ip_changed && <span className="block text-submeta text-ink-3">{t("machines-cloud.ip_changed")}</span>}
            </p>
          )}
          {!problem && split.row.length > 0 && (
            <div className="mt-2.5 flex flex-wrap gap-2" data-testid="cloud-row-actions">
              {split.row.map((a) => (
                <Button key={actionKey(a)} tone="secondary" busy={busy === actionKey(a)} disabled={!!busy && busy !== actionKey(a)} onClick={() => press(a)} testId={`cloud-action-${a.action}`}>
                  {actionLabel(a)}
                </Button>
              ))}
            </div>
          )}
          {outcome && (
            <div className="mt-2" role="status" data-testid="cloud-outcome" data-tone={outcome.tone} data-key={outcome.key}>
              {outcome.failure ? (
                <FailureLine failure={outcome.failure} />
              ) : (
                <p className={cx("flex flex-wrap items-center gap-x-2 text-label", outcome.tone === "ok" ? "text-green" : "text-ink-2")}>
                  <span className="min-w-0 break-words">{outcome.text}</span>
                  {outcome.url && <a href={outcome.url} target="_blank" rel="noopener noreferrer" className="text-accent-ink hover:underline" data-testid="cloud-console-link">{t("machines-cloud.open_console_again")}</a>}
                </p>
              )}
            </div>
          )}
        </div>
      </div>
      {menu.anchor && <Menu items={items} anchor={menu.anchor} onClose={menu.close} align="right" width={232} />}
      {asking && instance && (
        <ActionConfirm
          action={asking}
          machine={machineName}
          instance={instance}
          busy={busy === actionKey(asking)}
          failure={askFailure}
          onCancel={() => setAsking(null)}
          onConfirm={() => void run(asking, true)}
        />
      )}
      {snapshots && host && (
        <SnapshotsDialog
          mid={mid}
          machineName={machineName}
          host={host}
          onClose={() => setSnapshots(false)}
          onTaken={() => {
            since.current = Date.now();
            void read();
          }}
        />
      )}
    </>
  );
}

// ── snapshots ──

function SnapshotsDialog({ mid, machineName, host, onClose, onTaken }: { mid: string; machineName: string; host: CloudHost; onClose: () => void; onTaken: () => void }) {
  const canList = hasAction(host, "snapshot_list");
  const create = host.actions.find((a) => a.action === "snapshot_create");
  const [list, setList] = useState<CloudSnapshot[] | null>(canList ? null : []);
  const [failure, setFailure] = useState<CloudFailure | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [createFailure, setCreateFailure] = useState<CloudFailure | null>(null);
  const [taken, setTaken] = useState("");
  const load = useCallback((signal?: AbortSignal) => {
    if (!canList) return;
    fetchCloudSnapshots(mid, signal).then(
      (s) => {
        setList(s);
        setFailure(null);
      },
      (e) => !isAbort(e) && setFailure(cloudFailure(e)),
    );
  }, [mid, canList]);
  useEffect(() => {
    const ctl = new AbortController();
    load(ctl.signal);
    return () => ctl.abort();
  }, [load]);
  const take = async () => {
    if (!create) return;
    setBusy(true);
    setCreateFailure(null);
    try {
      const out = await runCloudAction(mid, actionRequest(create, host.instance.id, { name }));
      setTaken(outcomeText(create, out));
      setName("");
      onTaken();
      load();
    } catch (e) {
      setCreateFailure(cloudFailure(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open onClose={() => !busy && onClose()} width={520} title={t("machines-cloud.snapshots_title", { machine: machineName })}>
      <div data-testid="cloud-snapshots">
        {canList && (list === null && !failure ? (
          <div className="py-3"><Loading /></div>
        ) : (
          <>
            <FailureLine failure={failure} />
            {list && list.length === 0 && <p className="text-meta text-ink-3">{t("machines-cloud.snapshots_empty")}</p>}
            {list && list.length > 0 && (
              <ul className="divide-y divide-line" data-testid="cloud-snapshot-list">
                {list.map((s) => (
                  <li key={s.id} className="flex items-baseline gap-3 py-1.5 text-meta">
                    <span className="min-w-0 flex-1 truncate text-ink">{s.name?.trim() || s.id}</span>
                    <span className="shrink-0 text-submeta text-ink-3">{[snapshotStateWord(s.state), s.created_at && formatDate(s.created_at, { dateStyle: "medium", timeStyle: "short" }), s.size_gb ? t("machines-cloud.spec_disk", { size: s.size_gb }) : ""].filter(Boolean).join(" · ")}</span>
                  </li>
                ))}
              </ul>
            )}
          </>
        ))}
        {create && (
          <form
            className={cx(canList && "mt-3 border-t border-line pt-3")}
            onSubmit={(e) => {
              e.preventDefault();
              void take();
            }}
          >
            <p className="mb-2 text-meta leading-relaxed text-ink-2">{t("machines-cloud.confirm_snapshot_create", { machine: machineName, instance: hostName(host.instance) })}</p>
            <div className="flex flex-wrap items-center gap-2">
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
                placeholder={t("machines-cloud.snapshot_name_placeholder")}
                aria-label={t("machines-cloud.snapshot_name")}
                className="h-8 min-w-0 flex-1 basis-48 rounded-control bg-inset px-2.5 text-meta text-ink shadow-hairline outline-none placeholder:text-ink-3"
                data-testid="cloud-snapshot-name"
              />
              <Button tone="primary" type="submit" busy={busy} testId="cloud-snapshot-take">{t("machines-cloud.action_snapshot_create")}</Button>
            </div>
            <FailureLine failure={createFailure} className="mt-2" />
            {taken && <p role="status" className="mt-2 text-label text-green">{taken}</p>}
          </form>
        )}
      </div>
    </Dialog>
  );
}
