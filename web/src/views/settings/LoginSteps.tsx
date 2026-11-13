// Pieces the two step-by-step sign-ins share (Settings › Connectors' login card in HubConnector.tsx, Settings › Channels'
// binding in Channels.tsx; docs/extensions/connectors.md "Login supports"): the QR code drawn from the hub's modules, saving it as a picture,
// asking a step again every interval until it is scanned / entered, and the note for a step this page cannot draw.
import { useEffect } from "react";
import type { ConnectorPrompt } from "../../lib/connectors";
import { t } from "../../i18n";
import { Button, copyText, IconButton, Spinner } from "../../ui/atoms";

/** A QR code from the hub's modules (rows of "0" / "1"), in the page's own colors, with its quiet zone. */
export function QRCode({ rows, size = 184, label = t("settings-loginsteps.qr_code") }: { rows: string[]; size?: number; label?: string }) {
  const n = rows.length;
  const quiet = 2;
  const cells: string[] = [];
  rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === "1") cells.push(`M${x + quiet} ${y + quiet}h1v1h-1z`);
  });
  return (
    <svg role="img" aria-label={label} width={size} height={size} viewBox={`0 0 ${n + quiet * 2} ${n + quiet * 2}`} shapeRendering="crispEdges" className="rounded-[10px] bg-white">
      <path d={cells.join("")} fill="#000" />
    </svg>
  );
}

export function saveQR(rows: string[], name: string) {
  const n = rows.length + 4;
  const scale = 12;
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = n * scale;
  const g = canvas.getContext("2d");
  if (!g) return;
  g.fillStyle = "#fff";
  g.fillRect(0, 0, canvas.width, canvas.height);
  g.fillStyle = "#000";
  rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === "1") g.fillRect((x + 2) * scale, (y + 2) * scale, scale, scale);
  });
  const a = document.createElement("a");
  a.href = canvas.toDataURL("image/png");
  a.download = t("settings-loginsteps.qr_file", { name });
  a.click();
}

/**
 * Asks `polled` (a step the page sends again by itself, or null) again every `interval` seconds the hub gave
 * (`fallback` when it gave none) until `send` says it is done or the step changes. `send` resolves true when done.
 */
export function useStepPolling(polled: ConnectorPrompt | null, send: (step: string) => Promise<boolean>, fallback: number) {
  useEffect(() => {
    if (!polled) return;
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {
      timer = setTimeout(async () => {
        if (stop) return;
        if (!(await send(polled.step ?? "")) && !stop) tick();
      }, Math.max(polled.interval ?? fallback, 1) * 1000);
    };
    tick();
    return () => {
      stop = true;
      clearTimeout(timer);
    };
  }, [polled?.step, polled?.user_code, polled?.url]); // eslint-disable-line react-hooks/exhaustive-deps
}

/** A QR code to scan in `app`, asked again by the page; "Save QR code" for scanning it from the same phone's photos. */
export function QRStep({ rows, app, label, onCancel }: { rows: string[]; app: string; label?: string; onCancel: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 px-4 py-4">
      <QRCode rows={rows} label={label} />
      <p className="flex items-center gap-2 text-[12.5px] text-ink-3"><Spinner size={11} />{t("settings-loginsteps.waiting_scan", { app })}</p>
      <div className="flex items-center gap-2">
        <Button tone="secondary" icon="download" onClick={() => saveQR(rows, app)}>{t("settings-loginsteps.save_qr")}</Button>
        <Button tone="quiet" onClick={onCancel}>{t("common.cancel")}</Button>
      </div>
    </div>
  );
}

/** A QR step whose modules did not come with it: the link it encodes, to open (or send to yourself) in `app`. */
export function QRLinkStep({ url, app, onCancel }: { url: string; app: string; onCancel: () => void }) {
  return (
    <div className="flex flex-col gap-3 px-4 py-3">
      <p className="text-[12.5px] leading-relaxed text-ink-2">{t("settings-loginsteps.qr_link", { app })}</p>
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate font-mono text-[12px] text-ink select-all">{url}</span>
        <IconButton
          icon="copy"
          label={t("settings-loginsteps.copy_link")}
          onClick={() => void copyText(url, { done: t("settings-loginsteps.link_copied"), failed: t("settings-loginsteps.copy_failed") })}
        />
      </div>
      <p className="flex items-center gap-2 text-[12.5px] text-ink-3"><Spinner size={11} />{t("settings-loginsteps.waiting_confirm", { app })}</p>
      <div>
        <Button tone="quiet" onClick={onCancel} className="-ml-2">{t("common.cancel")}</Button>
      </div>
    </div>
  );
}

/**
 * A step this page cannot draw (a kind it does not know, a QR code without its modules or link, a form with no
 * fields): says so and what to do next, with a way to start over — never a "Next" that can never light up.
 */
export function UnsupportedStep({ elsewhere, onRestart, onCancel }: { elsewhere?: string; onRestart: () => void; onCancel: () => void }) {
  return (
    <div className="flex flex-col gap-2.5 px-4 py-3">
      <p role="alert" className="text-[12.5px] leading-relaxed text-ink-2">
        {elsewhere ? t("settings-loginsteps.unsupported_elsewhere", { elsewhere }) : t("settings-loginsteps.unsupported")}
      </p>
      <div className="flex items-center gap-2">
        <Button tone="secondary" icon="retry" onClick={onRestart}>{t("settings-loginsteps.restart")}</Button>
        <Button tone="quiet" onClick={onCancel}>{t("common.cancel")}</Button>
      </div>
    </div>
  );
}
