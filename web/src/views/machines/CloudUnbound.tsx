// The computers' list's quiet line for cloud servers no computer is linked to (docs/design/machine-cloud-ops.md "The
// web"), loaded on its own and only once the providers list says a provider is enabled. Loading the list asks no
// program: the line has no count, and the instances are read only when it is opened. It opens the unbound ones, each
// with "Link…", which asks which computer runs on it (the computers not linked yet) and binds that one.
import { useCallback, useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { bindingOf, bindMachineCloud, bindRequest, cloudFailure, fetchCloudHosts, hostKey, hostName, patchBinding, reloadCloud, unboundHosts, useCloudStore, type CloudFailure } from "../../lib/cloud-hosts";
import type { CloudHost, CloudHosts } from "../../lib/extension-wire";
import { isAbort } from "../../lib/api";
import type { Machine } from "../../lib/types";
import { Button, Dialog, Menu, type MenuItem } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { FailureLine, HostList } from "./CloudParts";

export function CloudUnbound({ machines }: { machines: readonly Pick<Machine, "id" | "name">[] }) {
  const store = useCloudStore(false);
  const [hosts, setHosts] = useState<CloudHosts | null>(null);
  const [open, setOpen] = useState(false);
  const [picking, setPicking] = useState<{ host: CloudHost; anchor: HTMLElement } | null>(null);
  const [busy, setBusy] = useState("");
  const [failure, setFailure] = useState<CloudFailure | null>(null);
  const [readFailure, setReadFailure] = useState<CloudFailure | null>(null);
  const ctl = useRef<AbortController | null>(null);
  const load = useCallback(() => {
    ctl.current?.abort();
    const c = new AbortController();
    ctl.current = c;
    return fetchCloudHosts({ signal: c.signal }).then((h) => {
      if (c.signal.aborted) return;
      setHosts(h);
      setReadFailure(null);
    }, (e) => {
      if (isAbort(e) || c.signal.aborted) return;
      setReadFailure(cloudFailure(e));
      setHosts((h) => h ?? { hosts: [], problems: [] });
    });
  }, []);
  // a read still on its way when the list goes away is dropped
  useEffect(() => () => ctl.current?.abort(), []);
  const show = () => {
    setOpen(true);
    if (!hosts) void load();
  };
  const close = () => {
    setOpen(false);
    // the next opening reads the instances again: a server may have been made or linked meanwhile
    ctl.current?.abort();
    setHosts(null);
    setReadFailure(null);
    setFailure(null);
  };
  const unbound = hosts ? unboundHosts(hosts.hosts) : [];
  const free = machines.filter((m) => !bindingOf(store, m.id));
  const link = async (host: CloudHost, mid: string) => {
    setBusy(hostKey(host));
    setFailure(null);
    try {
      const cm = await bindMachineCloud(mid, bindRequest(host));
      patchBinding(mid, cm.binding);
      void reloadCloud();
      await load();
    } catch (e) {
      setFailure(cloudFailure(e));
    } finally {
      setBusy("");
    }
  };
  const items: MenuItem[] = picking
    ? free.length
      ? free.map((m) => ({ key: m.id, label: m.name || m.id, icon: "machine" as const, onSelect: () => void link(picking.host, m.id) }))
      : [{ key: "none", label: t("machines-cloud.no_free_computer"), disabled: true, onSelect: () => {} }]
    : [];
  return (
    <>
      <button type="button" onClick={show} className="col-span-full inline-flex min-h-11 w-fit items-center gap-1.5 rounded-control px-1 text-submeta text-ink-3 hover:text-ink" data-testid="cloud-unbound-line">
        <Icon name="cloud" size={12} className="shrink-0" />
        {t("machines-cloud.unbound_open")}
      </button>
      {open && (
        <Dialog open onClose={() => !busy && close()} width={560} title={t("machines-cloud.unbound_title")}>
          <p className="mb-2 text-meta leading-relaxed text-ink-2">{t("machines-cloud.unbound_body")}</p>
          <FailureLine failure={failure} className="mb-2" />
          <HostList
            hosts={unbound}
            problems={hosts?.problems ?? []}
            partial={hosts?.partial}
            loading={!hosts}
            failure={readFailure}
            empty={t("machines-cloud.link_empty")}
            action={(h) => <LinkTo host={h} busy={busy === hostKey(h)} disabled={!!busy} onPick={(anchor) => setPicking({ host: h, anchor })} />}
          />
        </Dialog>
      )}
      {picking && <Menu items={items} anchor={picking.anchor} header={t("machines-cloud.choose_computer")} onClose={() => setPicking(null)} align="right" width={232} />}
    </>
  );
}

function LinkTo({ host, busy, disabled, onPick }: { host: CloudHost; busy: boolean; disabled: boolean; onPick: (anchor: HTMLElement) => void }) {
  const ref = useRef<HTMLButtonElement>(null);
  return (
    <Button
      ref={ref}
      tone="secondary"
      busy={busy}
      disabled={disabled}
      aria-label={t("machines-cloud.link_to_label", { instance: hostName(host.instance) })}
      testId="cloud-unbound-link"
      onClick={() => ref.current && onPick(ref.current)}
    >
      {t("machines-cloud.link_to")}
    </Button>
  );
}
