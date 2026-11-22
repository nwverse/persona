import { lazy, Suspense, useEffect, useState } from "react";
import { ApiError, errorText } from "../lib/api";
import { pair } from "../lib/auth";
import { here, rememberHub } from "../lib/hubs";
import { Button, Collapse, cx, Spinner } from "../ui/atoms";
import { AppMark } from "../ui/AppMark";
import { Icon } from "../ui/icons";
import { OtherHubs } from "./Hubs";
import { t } from "../i18n";
import { tRich } from "../i18n/rich";
import type { PasswordState } from "../lib/password";

import { BrowserLoginQR } from "./BrowserLoginQR";

const PasswordLogin = lazy(() => import("./PasswordLogin").then((m) => ({ default: m.PasswordLogin })));

const code = "whitespace-nowrap rounded-[5px] bg-field px-1 py-0.5 font-mono text-[11.5px] text-ink-2 shadow-hairline";
// what went wrong, under the button of the form it came from (the link's, or the master key's)
const formError = "text-center text-[12.5px] text-red";

// Sign in without a terminal (docs/assistant/onboarding.md 4.2, 4.5): first a login code from a device that is already signed
// in (Settings › Devices › Connect a new device › Browser makes a `#token=jp_…` link: opened here it signs in by itself, App.tsx), then a
// pasted pairing / login link. The hub's own token and the commands that print it are folded under "Advanced". `error` is
// what went wrong with a token that came in the link. Each form says what went wrong under its own button, where the eye
// is, and editing its field takes that away (web usability audit item 41).
export function Login({ onDone, error: linkError = "" }: { onDone: () => void; error?: string }) {
  const [passwordMode, setPasswordMode] = useState(false);
  useEffect(() => {
    let live = true;
    import("../lib/password").then((m) => m.passwordApi<PasswordState>("/state")).then((s) => { if (live) setPasswordMode(s.enabled); }).catch(() => {});
    return () => { live = false; };
  }, []);
  const [link, setLink] = useState("");
  const [master, setMaster] = useState("");
  const [busy, setBusy] = useState<"" | "link" | "master">("");
  // the link form's error (a link's token that didn't work starts there) and the master key's
  const [error, setError] = useState(linkError);
  const [masterError, setMasterError] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [noHub, setNoHub] = useState(false);
  const [qrOpen, setQrOpen] = useState(false);
  useEffect(() => void rememberHub(here()), []);

  const submit = (from: "link" | "master") => async (e: React.FormEvent) => {
    e.preventDefault();
    const tok = (from === "link" ? link : master).trim();
    if (!tok || busy) return;
    setBusy(from);
    const fail = from === "link" ? setError : setMasterError;
    fail("");
    try {
      await pair(tok);
      onDone();
    } catch (err) {
      const msg = errorText(err);
      const invalid = (err instanceof ApiError && err.status === 401) || msg === "invalid token";
      fail(invalid ? t(from === "link" ? "login.invalid_link" : "login.invalid_token") : msg);
    } finally {
      setBusy("");
    }
  };

  const field = "flex min-h-11 items-center gap-2 rounded-control bg-inset px-3 shadow-hairline focus-within:shadow-[0_0_0_1px_var(--line-strong)]";
  const input = "min-w-0 flex-1 bg-transparent font-mono text-meta text-ink outline-none placeholder:font-sans placeholder:text-ink-3";

  if (passwordMode) return <div><Suspense fallback={<Spinner />}><PasswordLogin onDone={onDone} error={linkError} /></Suspense><button type="button" onClick={() => setPasswordMode(false)} className="mx-auto mb-6 block text-ink-2">{t("login.password_pairing")}</button></div>;

  return (
    <div className="flex min-h-full items-center justify-center bg-page px-5 py-16">
      <div className="w-full max-w-[380px]" style={{ animation: "fade-up 450ms cubic-bezier(0.23,1,0.32,1) both" }}>
        <header className="mb-5 flex items-center gap-3">
          <AppMark size={44} radius={12} className="shadow-raised" />
          <h1 id="login-title" className="text-xl font-semibold tracking-tight text-ink">{t("login.title")}</h1>
        </header>

        <div className="rounded-card bg-surface shadow-card">
          <form onSubmit={submit("link")} aria-labelledby="login-title" className="space-y-2 p-3">
            <label className={field}>
              <Icon name="link" size={14} className="text-ink-3" />
              <input
                autoFocus
                type="text"
                value={link}
                onChange={(e) => {
                  setLink(e.target.value);
                  setError("");
                }}
                placeholder={t("login.link_placeholder")}
                aria-label={t("login.link_placeholder")}
                aria-describedby="login-link-error"
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                className={input}
              />
            </label>
            <Button type="submit" tone="primary" disabled={!link.trim() || busy !== ""} className="min-h-11 w-full">
              {busy === "link" ? <Spinner /> : t("login.sign_in")}
            </Button>
            <p id="login-link-error" className={`empty:hidden ${formError}`} role="alert">{error}</p>
          </form>
          <details className="group border-t border-line">
            <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between gap-2 rounded-control px-4 text-meta text-ink-2 hover:bg-hover [&::-webkit-details-marker]:hidden">
              {t("login.get_link")}
              <Icon name="chevronRight" size={14} className="group-open:rotate-90" />
            </summary>
            <ol className="list-decimal space-y-2 pb-2 pl-9 pr-4 text-meta leading-relaxed text-ink-2">
              <li>{tRich("login.phone_step_open", { path: <strong className="font-medium text-ink">{t("login.phone_path")}</strong> })}</li>
              <li>{t("login.phone_step_copy")}</li>
            </ol>
            <p className="px-4 pb-4 text-meta leading-relaxed text-ink-2">{t("login.phone_step_scan")}</p>
          </details>
        </div>

        <div className="mt-4 divide-y divide-line">
          {/* A code is made only once this is opened, by someone who means to scan it (#10291). */}
          <Disclosure open={qrOpen} onToggle={() => setQrOpen((v) => !v)} label={t("login.qr_open")} id="login-qr">
            {qrOpen && <div className="px-1 pb-2"><BrowserLoginQR onDone={onDone} /></div>}
          </Disclosure>
          <Disclosure open={noHub} onToggle={() => setNoHub((v) => !v)} label={t("login.no_hub")} id="login-no-hub">
            <div className="space-y-1.5 px-3 pb-2 text-[12px] leading-relaxed text-ink-2">
              <p>{t("login.no_hub_what")}</p>
              <p>{t("login.no_hub_yours")}</p>
              <p>{t("login.no_hub_others")}</p>
            </div>
          </Disclosure>
          <Disclosure open={advanced} onToggle={() => setAdvanced((v) => !v)} label={t("login.advanced")} id="login-advanced">
            <form onSubmit={submit("master")} aria-label={t("login.connect")} className="px-3 pb-2">
              <p className="flex items-start gap-1.5 text-[12px] leading-relaxed text-ink-2">
                <Icon name="warning" size={13} className="mt-0.5 shrink-0 text-orange" />
                <span>{t("login.master_warning")}</span>
              </p>
              <p className="mt-1.5 text-[11.5px] leading-[1.9] text-ink-3">
                {tRich("login.master_where", {
                  pair: <code className={code}>personad hub pair --legacy</code>,
                  jq: <code className={code}>jq -r .token ~/.persona/hub.json</code>,
                })}
              </p>
              <div className="mt-2 rounded-card bg-surface p-1.5 shadow-card">
                <label className={field}>
                  <Icon name="key" size={14} className="text-ink-3" />
                  <input
                    type="password"
                    value={master}
                    onChange={(e) => {
                      setMaster(e.target.value);
                      setMasterError("");
                    }}
                    placeholder={t("login.master_placeholder")}
                    aria-label={t("login.master_placeholder")}
                    autoComplete="off"
                    className={input}
                  />
                </label>
                <Button type="submit" tone="secondary" disabled={!master.trim() || busy !== ""} className="mt-1.5 min-h-11 w-full">
                  {busy === "master" ? <Spinner /> : t("login.connect")}
                </Button>
              </div>
              <p className={`mt-2 ${formError}`} role="alert">
                {masterError}
              </p>
            </form>
          </Disclosure>
        </div>
        <p className="mt-5 px-3 text-meta leading-relaxed text-ink-3">{t("login.credential_note")}</p>
        <OtherHubs />
      </div>
    </div>
  );
}

/** A quiet row that folds what is under it up and down. */
function Disclosure({ open, onToggle, label, id, children }: { open: boolean; onToggle: () => void; label: string; id: string; children: React.ReactNode }) {
  return (
    <div>
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        aria-controls={id}
        className="flex min-h-11 w-full items-center justify-between gap-2 rounded-control px-3 text-left text-[12.5px] text-ink-2 transition-colors hover:bg-hover hover:text-ink"
      >
        <span>{label}</span>
        <Icon name="chevronRight" size={14} className={cx("shrink-0 text-ink-3 transition-transform duration-200", open && "rotate-90")} />
      </button>
      <div id={id}>
        <Collapse open={open}>{children}</Collapse>
      </div>
    </div>
  );
}
