import { lazy, Suspense, useEffect, useState } from "react";
import { deviceName } from "../lib/auth";
import { errorText, setPairedToken } from "../lib/api";
import { BASE } from "../lib/base";
import { t } from "../i18n";
import { Button, Spinner } from "../ui/atoms";

const QRCode = lazy(() => import("./settings/LoginSteps").then((m) => ({ default: m.QRCode })));
type Challenge = { id: string; secret: string; expires_at: string; qr: { rows: string[] } };

export function BrowserLoginQR({ onDone }: { onDone: () => void }) {
  const [generation, refresh] = useState(0);
  const [challenge, setChallenge] = useState<Challenge>();
  const [state, setState] = useState("loading");
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let done = false;
    async function request<T>(action: string, body: unknown): Promise<T> {
      const response = await fetch(`${BASE}/api/v1/auth/browser-login/${action}`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body), signal: controller.signal, cache: "no-store",
      });
      if (!response.ok) throw new Error(response.status === 410 ? t("login.qr_expired") : t("login.qr_failed"));
      return response.json();
    }
    function failed(err: unknown) {
      if (controller.signal.aborted) return;
      done = true; setState("failed"); setError(errorText(err));
    }
    async function poll(c: Challenge) {
      if (controller.signal.aborted || done) return;
      if (Date.now() >= Date.parse(c.expires_at)) { setState("expired"); done = true; return; }
      if (document.visibilityState !== "hidden") {
        try {
          const result = await request<{ state: string; token?: string; id?: string }>("poll", { id: c.id, secret: c.secret });
          if (controller.signal.aborted) return;
          setState(result.state);
          if (result.state === "approved" && result.token && result.id) {
            done = true; setPairedToken(result.token, result.id); onDone(); return;
          }
          if (result.state === "denied") { done = true; return; }
        } catch (err) { failed(err); return; }
      }
      timer = setTimeout(() => void poll(c), 1500);
    }
    setChallenge(undefined); setState("loading"); setError("");
    request<Challenge>("start", { name: deviceName() }).then((c) => {
      if (controller.signal.aborted) return;
      setChallenge(c); setState("pending"); void poll(c);
    }).catch(failed);
    return () => { controller.abort(); clearTimeout(timer); };
  }, [generation, onDone]);
  const ended = ["expired", "denied", "failed"].includes(state);
  return <section className="rounded-card bg-surface p-4 text-center shadow-card" aria-labelledby="login-qr-title">
    <h2 id="login-qr-title" className="text-[14px] font-semibold text-ink">{t("login.phone_title")}</h2>
    <div className="my-4 flex items-center justify-center" style={{ minHeight: 184 }}>
      {state === "loading" ? <Spinner /> : challenge && !ended ? <Suspense fallback={<Spinner />}><QRCode rows={challenge.qr.rows} label={t("login.phone_title")} /></Suspense> : null}
      {ended && <p className="text-[13px] text-ink-2">{error || t(state === "denied" ? "login.qr_denied" : "login.qr_expired")}</p>}
    </div>
    <p className="text-[13px] text-ink-2" role="status">{t(state === "scanned" ? "login.qr_scanned" : "login.qr_hint")}</p>
    <Button tone="secondary" type="button" className="mt-3" onClick={() => refresh((n) => n + 1)}>{t("login.qr_refresh")}</Button>
  </section>;
}
