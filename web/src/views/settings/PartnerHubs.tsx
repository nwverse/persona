// Settings › This hub › Partner hubs (docs/server/partner-hubs.md, lib/partner-hubs.ts): the independent hubs this one is
// paired with, each with its link's state, "Ask me before sending" (the default manager's), "Connect to this hub" (whether
// this hub dials it) and Unpair; Show my code (a one-time code the other hub pastes) and Pair… (paste theirs). Only on a hub
// with the partner_hubs feature. Follows the hub's partner_hubs pushes and a reconnect it could not resume; a switch shows
// what the hub answered, never what was asked for.
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { t } from "../../i18n";
import { clock, secondsLeft } from "../../lib/connect-device";
import {
  confirmFor, listPartnerHubs, listPartnerPrefs, makePartnerCode, pairPartnerHub, parsePartnerCode, partnerError, partnerLine,
  partnerStatus, PARTNER_HUBS_EVENT, PARTNER_HUBS_FEATURE, setPartnerConfirm, setPartnerDial, unpairPartnerHub, withPref,
  type PartnerCode, type PartnerHub, type PartnerHubList, type PartnerPref,
} from "../../lib/partner-hubs";
import { isResumed } from "../../lib/reconnect";
import { hubHas, onHubEvent, useStore } from "../../lib/store";
import { Button, cx, Dialog, Loading, SECRET_INPUT, toast, useNow } from "../../ui/atoms";
import { textareaClass } from "../assistant/parts";
import { CopyLine } from "../ManagerInstall";
import { Labelled } from "./ProviderFields";
import { Group, Pill, Row, RowConfirm, RowFailed, SettingsHint, Switch } from "./SettingsParts";

export function PartnerHubs() {
  const on = useStore((s) => hubHas(s.features, PARTNER_HUBS_FEATURE));
  return on ? <PartnerHubsGroup /> : null;
}

function PartnerHubsGroup() {
  const [list, setList] = useState<PartnerHubList | null>(null);
  const [error, setError] = useState("");
  const [prefs, setPrefs] = useState<PartnerPref[] | null>(null);
  const [prefsFailed, setPrefsFailed] = useState(false);
  // what is being saved: "<hub_id>:confirm" | ":dial" | ":unpair"
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  const [unpairing, setUnpairing] = useState<string | null>(null);
  const [showCode, setShowCode] = useState(false);
  const [pairing, setPairing] = useState(false);
  const now = useNow(30_000);
  // the latest read: the answer to an older one (a push, then a hello; one that left before a change) is dropped
  const reads = useRef(0);

  const load = useCallback(() => {
    const n = ++reads.current;
    listPartnerHubs().then(
      (r) => n === reads.current && (setList({ ...r, hubs: r.hubs ?? [] }), setError("")),
      (e) => n === reads.current && setError(partnerError(e)),
    );
    // the default manager's: without it the list still shows, and the switch says it could not be read
    listPartnerPrefs().then(
      (r) => n === reads.current && (setPrefs(r.prefs ?? []), setPrefsFailed(false)),
      () => n === reads.current && setPrefsFailed(true),
    );
  }, []);
  useEffect(() => {
    load();
    const off = onHubEvent(PARTNER_HUBS_EVENT, () => load());
    // a reconnect the hub could not resume: a change it pushed meanwhile is not replayed
    const offHello = onHubEvent("hello", (m) => !isResumed(m) && load());
    return () => {
      off();
      offHello();
    };
  }, [load]);

  const run = async (key: string, work: () => Promise<void>, failed: (error: string) => string) => {
    setBusy((b) => new Set(b).add(key));
    try {
      await work();
      // a read already on its way may predate the change (and the manager pushes nothing for its prefs): read anew
      load();
    } catch (e) {
      toast(failed(partnerError(e)), "error");
    } finally {
      setBusy((b) => {
        const next = new Set(b);
        next.delete(key);
        return next;
      });
    }
  };
  const saveFailed = (error: string) => t("settings-partnerhubs.save_failed", { error });
  const confirm = (h: PartnerHub, on: boolean) =>
    run(`${h.hub_id}:confirm`, async () => {
      const saved = await setPartnerConfirm(h.hub_id, on);
      setPrefs((p) => withPref(p, { hub_id: h.hub_id, confirm: saved.confirm }));
      setPrefsFailed(false);
    }, saveFailed);
  const dial = (h: PartnerHub, on: boolean) =>
    run(`${h.hub_id}:dial`, async () => {
      const saved = await setPartnerDial(h.hub_id, on ? "auto" : "never");
      setList((l) => l && { ...l, hubs: l.hubs.map((x) => (x.hub_id === h.hub_id ? { ...x, ...saved } : x)) });
    }, saveFailed);
  const unpair = (h: PartnerHub) =>
    run(`${h.hub_id}:unpair`, async () => {
      await unpairPartnerHub(h.hub_id);
      setUnpairing(null);
      setList((l) => l && { ...l, hubs: l.hubs.filter((x) => x.hub_id !== h.hub_id) });
      toast(t("settings-partnerhubs.unpaired", { name: h.name || h.hub_id }), "ok");
    }, (error) => t("settings-partnerhubs.unpair_failed", { error }));

  const usable = !list || (list.enabled && !list.member);
  const canIssue = !!list?.can_issue;
  const noIssue = t("settings-partnerhubs.no_issue");
  return (
    <>
      <Group
        anchor="partner-hubs"
        title={t("settings-partnerhubs.title")}
        footer={t("settings-partnerhubs.footer")}
        right={
          list && usable ? (
            <span className="flex items-center gap-1">
              <Button tone="quiet" icon="key" disabled={!canIssue} title={canIssue ? undefined : noIssue} onClick={() => setShowCode(true)}>
                {t("settings-partnerhubs.show_code")}
              </Button>
              <Button tone="quiet" icon="link" onClick={() => setPairing(true)}>
                {t("settings-partnerhubs.pair")}
              </Button>
            </span>
          ) : undefined
        }
      >
        {!list ? (
          error ? (
            <RowFailed text={t("settings-partnerhubs.load_failed", { error })} retry={t("settings-partnerhubs.retry")} onRetry={load} />
          ) : (
            <div className="px-4 py-3">
              <Loading />
            </div>
          )
        ) : (
          <>
            {!usable && <SettingsHint>{list.member ? t("settings-partnerhubs.member") : t("settings-partnerhubs.off")}</SettingsHint>}
            {usable && !list.hubs.length && <SettingsHint>{t("settings-partnerhubs.empty")}</SettingsHint>}
            {list.hubs.map((h) => (
              <PartnerRow
                key={h.hub_id}
                h={h}
                now={now}
                asks={confirmFor(prefs, h.hub_id)}
                prefsFailed={prefsFailed}
                busy={(what) => busy.has(`${h.hub_id}:${what}`)}
                unpairOpen={unpairing === h.hub_id}
                onUnpairOpen={() => setUnpairing(h.hub_id)}
                onUnpairCancel={() => setUnpairing(null)}
                onUnpair={() => void unpair(h)}
                onConfirm={(on) => void confirm(h, on)}
                onDial={(on) => void dial(h, on)}
              />
            ))}
            {usable && !canIssue && <SettingsHint>{noIssue}</SettingsHint>}
            {error && <SettingsHint alert>{t("settings-partnerhubs.load_failed", { error })}</SettingsHint>}
          </>
        )}
      </Group>
      <CodeDialog open={showCode} onClose={() => setShowCode(false)} />
      <PairDialog open={pairing} onClose={() => setPairing(false)} onPaired={load} />
    </>
  );
}

function PartnerRow({ h, now, asks, prefsFailed, busy, unpairOpen, onUnpairOpen, onUnpairCancel, onUnpair, onConfirm, onDial }: {
  h: PartnerHub;
  now: number;
  /** Whether messages to it wait for the owner's OK, as the manager said; undefined until it has. */
  asks: boolean | undefined;
  prefsFailed: boolean;
  busy: (what: "confirm" | "dial" | "unpair") => boolean;
  unpairOpen: boolean;
  onUnpairOpen: () => void;
  onUnpairCancel: () => void;
  onUnpair: () => void;
  onConfirm: (on: boolean) => void;
  onDial: (on: boolean) => void;
}) {
  const nameId = useId();
  const status = partnerStatus(h);
  const line = partnerLine(h, now);
  return (
    <div data-partner-hub={h.hub_id} data-partner-state={h.state}>
      <div className="flex items-center gap-3 px-4 py-2.5">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span id={nameId} className="truncate text-meta font-medium text-ink" title={h.hub_id}>
              {h.name || h.hub_id}
            </span>
            <Pill tone={status.tone} animated>
              {status.label}
            </Pill>
          </div>
          {line && <div className="mt-0.5 truncate text-submeta text-ink-3">{line}</div>}
          {h.error && <div className={cx("mt-0.5 text-submeta leading-snug break-words", h.state === "refused" ? "text-red" : "text-ink-3")}>{h.error}</div>}
        </div>
        <RowConfirm
          open={unpairOpen}
          busy={busy("unpair")}
          confirm={t("settings-partnerhubs.unpair")}
          onCancel={onUnpairCancel}
          onConfirm={onUnpair}
          trigger={
            <Button tone="quiet" className="text-red enabled:hover:text-red" onClick={onUnpairOpen}>
              {t("settings-partnerhubs.unpair")}
            </Button>
          }
        />
      </div>
      <Row label={t("settings-partnerhubs.confirm")} hint={asks === undefined && prefsFailed ? t("settings-partnerhubs.confirm_unknown") : t("settings-partnerhubs.confirm_hint")} wrapHint>
        <Switch on={asks ?? true} disabled={asks === undefined || busy("confirm")} label={t("settings-partnerhubs.confirm")} describedBy={nameId} onChange={onConfirm} />
      </Row>
      <Row label={t("settings-partnerhubs.dial")} hint={t("settings-partnerhubs.dial_hint")} wrapHint>
        <Switch on={h.dial === "auto"} disabled={busy("dial")} label={t("settings-partnerhubs.dial")} describedBy={nameId} onChange={onDial} />
      </Row>
    </div>
  );
}

/** This hub's one-time code, its countdown and copy; once it ran out, New code. */
function CodeDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  // `at`: when it came, as the clock below only ticks while a code is shown and starts from when the dialog was made
  const [code, setCode] = useState<(PartnerCode & { at: number }) | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const now = useNow(1000, open && !!code);
  const asked = useRef(0);
  const make = useCallback(async () => {
    const n = ++asked.current;
    setBusy(true);
    setError("");
    try {
      const made = await makePartnerCode();
      if (n === asked.current) setCode({ ...made, at: Date.now() });
    } catch (e) {
      if (n === asked.current) setError(partnerError(e));
    } finally {
      if (n === asked.current) setBusy(false);
    }
  }, []);
  useEffect(() => {
    if (!open) return;
    void make();
    return () => {
      // closed: an answer still on its way is dropped, and the next opening starts without this code (the dialog keeps
      // what it showed while it leaves)
      asked.current++;
      setCode(null);
      setError("");
      setBusy(false);
    };
  }, [open, make]);
  const left = code ? secondsLeft(code.expires_at, Math.max(now, code.at)) : 0;
  const fp = code ? parsePartnerCode(code.code)?.fp : undefined;
  return (
    <Dialog open={open} onClose={onClose} width={480} title={t("settings-partnerhubs.code_title")} footer={<Button tone="primary" onClick={onClose}>{t("settings-partnerhubs.done")}</Button>}>
      <div className="flex flex-col gap-3" data-partner-code-dialog>
        {!code && !error && <p className="py-6 text-center text-meta text-ink-3">{t("settings-partnerhubs.code_making")}</p>}
        {code && left === 0 && (
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <p className="text-meta text-ink-2">{t("settings-partnerhubs.code_expired")}</p>
            <Button busy={busy} onClick={() => void make()}>
              {t("settings-partnerhubs.new_code")}
            </Button>
          </div>
        )}
        {code && left > 0 && (
          <>
            <div>
              <div className="mb-1 flex justify-end text-label text-ink-3">
                <span className="tabular-nums">{t("settings-partnerhubs.expires_in", { time: clock(left) })}</span>
              </div>
              <CopyLine text={code.code} />
            </div>
            {fp && <CodeFacts name={null} fp={fp} />}
            <p className="text-label leading-relaxed text-ink-3">{t("settings-partnerhubs.code_note")}</p>
            <p className="text-label leading-relaxed text-orange">{t("settings-partnerhubs.code_warning")}</p>
          </>
        )}
        {error && (
          <div className="flex flex-col items-start gap-2">
            <p role="alert" className="text-meta text-red">{error}</p>
            {/* this hub could not make one (a database that did not answer): ask again without closing */}
            {!code && (
              <Button busy={busy} onClick={() => void make()}>
                {t("settings-partnerhubs.retry")}
              </Button>
            )}
          </div>
        )}
      </div>
    </Dialog>
  );
}

/** Paste the other hub's code; the hub's name and fingerprint read from it, to check before pairing. */
function PairDialog({ open, onClose, onPaired }: { open: boolean; onClose: () => void; onPaired: () => void }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const id = useId();
  const hintId = useId();
  // this opening of the dialog: a pairing that answers after it was closed (it can take half a minute) says how it went
  // in a toast, and leaves the dialog's next opening alone
  const opening = useRef(0);
  useEffect(() => {
    if (!open) return;
    setText("");
    setError("");
    setBusy(false);
    return () => void opening.current++;
  }, [open]);
  const info = parsePartnerCode(text);
  const wrong = text.trim() !== "" && !info;
  const pair = async () => {
    if (!info) return;
    const n = opening.current;
    setBusy(true);
    setError("");
    try {
      const h = await pairPartnerHub(info.code);
      toast(t("settings-partnerhubs.paired", { name: h.name || info.name }), "ok");
      onPaired();
      if (n === opening.current) onClose();
    } catch (e) {
      if (n === opening.current) setError(partnerError(e));
      else toast(partnerError(e), "error");
    } finally {
      if (n === opening.current) setBusy(false);
    }
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      width={520}
      title={t("settings-partnerhubs.pair_title")}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button tone="primary" busy={busy} disabled={!info} onClick={() => void pair()}>
            {t("settings-partnerhubs.pair_button")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <Labelled label={t("settings-partnerhubs.code_label")} hint={t("settings-partnerhubs.code_hint")} error={wrong ? t("settings-partnerhubs.not_a_code") : undefined} htmlFor={id} describedById={hintId}>
          <textarea
            id={id}
            aria-describedby={hintId}
            aria-invalid={wrong || undefined}
            value={text}
            rows={4}
            autoFocus
            placeholder="persona://partner?…"
            {...SECRET_INPUT}
            autoComplete="off"
            onChange={(e) => (setText(e.target.value), setError(""))}
            className={cx(textareaClass, "font-mono break-all")}
          />
        </Labelled>
        {info && (
          <>
            <CodeFacts name={info.name} fp={info.fp} />
            <p className="text-submeta leading-relaxed text-ink-3">{t("settings-partnerhubs.check_fp")}</p>
          </>
        )}
        {error && <p role="alert" className="text-meta text-red">{error}</p>}
      </div>
    </Dialog>
  );
}

/** A code's hub name (when shown) and fingerprint, one under the other. */
function CodeFacts({ name, fp }: { name: string | null; fp: string }) {
  return (
    <div data-partner-code-facts className="divide-y divide-line overflow-hidden rounded-control bg-inset shadow-hairline">
      {name !== null && (
        <Fact label={t("settings-partnerhubs.name")}>
          <span className="truncate text-meta font-medium text-ink">{name}</span>
        </Fact>
      )}
      <Fact label={t("settings-partnerhubs.fingerprint")}>
        <span className="font-mono text-label break-all text-ink">{fp}</span>
      </Fact>
    </div>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3 px-3 py-2">
      <span className="shrink-0 text-submeta whitespace-nowrap text-ink-3">{label}</span>
      <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>
    </div>
  );
}
