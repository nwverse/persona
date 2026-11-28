// What the cloud-host views share (docs/design/machine-cloud-ops.md "The web"): the state as a plain word in its tone,
// an instance's summary lines, the confirmation an action's gate turns into (a plain one for confirm, a danger one for
// destructive that says what may be lost and shows the instance's id back), and the list of instances to link.
// Tokens and atoms only; every word comes from machines-cloud.
import { t } from "../../i18n";
import { actionLabel, confirmBody, gateOf, hostKey, hostName, lifecycleNotes, placeLine, primaryAddress, specLine, stateTone, stateWord, type CloudFailure } from "../../lib/cloud-hosts";
import type { CloudHost, CloudHostAction, CloudInstance, CloudInstanceState, CloudProviderProblem } from "../../lib/extension-wire";
import { Button, Dialog, Loading, Pill } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export function StateChip({ state }: { state: CloudInstanceState | undefined }) {
  const tone = stateTone(state);
  return (
    <span data-cloud-state={state ?? "unknown"}>
      <Pill tone={tone === "neutral" ? undefined : tone}>{stateWord(state)}</Pill>
    </span>
  );
}

/** The vendor id as a small badge (drawn as text: core names no vendor). */
export function VendorBadge({ vendor, className }: { vendor: string; className?: string }) {
  return (
    <span className={className} data-cloud-vendor={vendor}>
      <Pill size="sm">{vendor}</Pill>
    </span>
  );
}

/** Address, place and size, then the lifecycle notes: what is known, one line each. */
export function InstanceFacts({ instance }: { instance: CloudInstance }) {
  const address = primaryAddress(instance);
  const facts = [placeLine(instance), specLine(instance.spec), instance.spec?.size].filter(Boolean);
  const notes = lifecycleNotes(instance.lifecycle);
  return (
    <div className="min-w-0 text-label text-ink-2">
      {address && (
        <p className="truncate">
          <span className="text-ink-3">{t("machines-cloud.address")}</span> <span className="select-text font-mono text-ink" data-testid="cloud-address">{address}</span>
        </p>
      )}
      {facts.length > 0 && <p className="truncate text-ink-3" data-testid="cloud-facts">{facts.join(" · ")}</p>}
      {instance.state_detail && <p className="break-words text-ink-3">{instance.state_detail}</p>}
      {notes.map((n) => (
        <p key={n} className="flex items-start gap-1 text-orange" data-testid="cloud-note">
          <Icon name="clock" size={12} className="mt-0.5 shrink-0" />
          <span className="min-w-0">{n}</span>
        </p>
      ))}
    </div>
  );
}

/** A failure in the server's words, its detail small under it. */
export function FailureLine({ failure, className }: { failure: CloudFailure | null; className?: string }) {
  if (!failure) return null;
  return (
    <div role="alert" className={className} data-cloud-error={failure.code || undefined}>
      <p className="flex items-start gap-1.5 text-label text-red">
        <Icon name="warning" size={13} className="mt-0.5 shrink-0" />
        <span className="min-w-0 break-words">{failure.error}</span>
      </p>
      {failure.detail && <p className="mt-0.5 break-words pl-5 font-mono text-submeta text-ink-3">{failure.detail}</p>}
    </div>
  );
}

/** An action's gate as a dialog: what happens to which computer; a destructive one in the danger tone, saying what may
 *  be lost and showing the instance's id, which the request sends back as its acknowledgement. */
export function ActionConfirm({ action, machine, instance, busy, failure, onCancel, onConfirm }: {
  action: CloudHostAction;
  machine: string;
  instance: CloudInstance;
  busy: boolean;
  failure: CloudFailure | null;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const destructive = gateOf(action) === "destructive";
  const label = actionLabel(action);
  return (
    <Dialog
      open
      onClose={() => !busy && onCancel()}
      title={t(destructive ? "machines-cloud.confirm_title_destructive" : "machines-cloud.confirm_title", { action: label, machine })}
      footer={
        <>
          <Button tone="quiet" disabled={busy} onClick={onCancel}>{t("common.cancel")}</Button>
          <Button tone={destructive ? "danger" : "primary"} busy={busy} onClick={onConfirm} testId="cloud-confirm">{label}</Button>
        </>
      }
    >
      <div data-testid="cloud-confirm-dialog" data-gate={destructive ? "destructive" : "confirm"}>
        <p className="text-meta leading-relaxed text-ink-2">{confirmBody(action, machine, hostName(instance))}</p>
        {destructive && (
          <>
            <p className="mt-2 flex items-start gap-2 rounded-control bg-red-tint px-3 py-2 text-meta text-red" data-testid="cloud-loss">
              <Icon name="warning" size={14} className="mt-0.5 shrink-0" />
              <span>{t("machines-cloud.confirm_loss")}</span>
            </p>
            <p className="mt-2 text-submeta text-ink-3">
              {t("machines-cloud.confirm_instance")} <span className="select-text font-mono text-ink-2" data-testid="cloud-ack">{instance.id}</span>
            </p>
          </>
        )}
        <FailureLine failure={failure} className="mt-2" />
      </div>
    </Dialog>
  );
}

/** One instance in a list to link: its name, provider, state, address and size, and the row's own action. */
export function HostRow({ host, action }: { host: CloudHost; action: React.ReactNode }) {
  const i = host.instance;
  const facts = [primaryAddress(i), placeLine(i), specLine(i.spec)].filter(Boolean);
  return (
    <li className="flex items-center gap-3 py-2" data-cloud-host={hostKey(host)}>
      <Icon name="cloud" size={15} className="shrink-0 text-ink-3" />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-meta font-medium text-ink">{hostName(i)}</span>
          <StateChip state={i.state} />
        </div>
        <p className="truncate text-submeta text-ink-3">{[host.provider_name, i.account, ...facts].filter(Boolean).join(" · ")}</p>
      </div>
      {action}
    </li>
  );
}

/** The instances to choose from, with the providers that could not be listed said quietly under them. */
export function HostList({ hosts, problems, partial, loading, failure, empty, action }: {
  hosts: readonly CloudHost[];
  problems: readonly CloudProviderProblem[];
  partial?: boolean;
  loading: boolean;
  failure: CloudFailure | null;
  empty: string;
  action: (host: CloudHost) => React.ReactNode;
}) {
  if (loading && !hosts.length) return <div className="py-3"><Loading /></div>;
  return (
    <div data-testid="cloud-host-list">
      <FailureLine failure={failure} className="mb-2" />
      {!failure && hosts.length === 0 && <p className="py-2 text-meta text-ink-3" data-testid="cloud-host-list-empty">{empty}</p>}
      {hosts.length > 0 && (
        <ul className="divide-y divide-line">
          {hosts.map((h) => <HostRow key={hostKey(h)} host={h} action={action(h)} />)}
        </ul>
      )}
      {(problems.length > 0 || partial) && (
        <ul className="mt-2 space-y-0.5 text-submeta text-ink-3" data-testid="cloud-problems">
          {problems.map((p) => (
            <li key={`${p.extension}/${p.provider}`} data-cloud-problem={p.code}>{t("machines-cloud.problem_line", { provider: p.provider_name, error: p.error })}</li>
          ))}
          {partial && <li>{t("machines-cloud.partial")}</li>}
        </ul>
      )}
    </div>
  );
}
