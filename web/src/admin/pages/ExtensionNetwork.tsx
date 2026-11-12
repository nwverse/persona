// "Network and isolation" in an installed extension's details (#5696): whether it runs in a sandbox, which the owner
// switches here (turning it off is confirmed; turning it on back to the default asks nothing), and what went through the
// egress proxy. The web's settings page links here (`?extension=<id>#network`) when an address was blocked.
import { t } from "../../i18n";
import { useEffect, useRef, useState } from "react";
import { Button, cx, Dialog, toast } from "../../ui/atoms";
import { Switch } from "../../views/settings/SettingsParts";
import { errorText, setExtensionSandbox } from "../api";
import { AdminIcon } from "../icons";
import type { AdminExtension } from "../types";

/** The extension the page was opened for (`/admin/extensions?extension=<id>`), "" none. */
export function linkedExtension(): string {
  return new URLSearchParams(location.search).get("extension") ?? "";
}

export function NetworkBlock({ p, onChanged, linked }: { p: AdminExtension; onChanged: (p: AdminExtension) => void; linked?: boolean }) {
  const s = p.sandbox;
  const box = useRef<HTMLDivElement>(null);
  const [lit, setLit] = useState(false);
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!linked || !box.current) return;
    // The markets above load after the list and push it down: keep the block in view while the page settles, until the
    // user scrolls themselves.
    const show = () => box.current?.scrollIntoView({ block: "center", behavior: "smooth" });
    let moved = false;
    const mine = () => (moved = true);
    addEventListener("wheel", mine, { once: true, passive: true });
    addEventListener("touchmove", mine, { once: true, passive: true });
    const settle = [0, 400, 1000, 1800].map((ms) => setTimeout(() => !moved && show(), ms));
    setLit(true);
    const off = setTimeout(() => setLit(false), 2400);
    return () => {
      settle.forEach(clearTimeout);
      clearTimeout(off);
      removeEventListener("wheel", mine);
      removeEventListener("touchmove", mine);
    };
  }, [linked]);
  if (!s || p.source !== "installed") return null;
  // Back to the default when the new value is the default: the switch has no separate "default" position.
  const set = async (on: boolean) => {
    setBusy(true);
    try {
      onChanged(await setExtensionSandbox(p.id, on === (s.default ?? true) ? null : on));
      toast(t(on ? "admin-pages-extensions.sandbox_turned_on" : "admin-pages-extensions.sandbox_turned_off", { name: p.name }), "ok");
      setAsking(false);
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      setBusy(false);
    }
  };
  const note = s.enabled && !s.active ? (s.why ?? "") : t(
    s.chosen
      ? s.enabled ? "admin-pages-extensions.sandbox_you_on" : "admin-pages-extensions.sandbox_you_off"
      : s.enabled ? "admin-pages-extensions.sandbox_default_on" : "admin-pages-extensions.sandbox_default_off",
  );
  const e = s.egress;
  const blocked = e?.denied ?? 0;
  return (
    <div id="network" ref={box} data-extension-network={p.id}>
      <p className="mb-1.5 text-[12px] font-medium text-ink-2">{t("admin-pages-extensions.network_title")}</p>
      {/* the highlight is an inset ring: the details' collapse clips anything drawn outside the block */}
      <div className={cx("divide-y divide-line overflow-hidden rounded-control transition-shadow duration-[400ms]", lit ? "shadow-[inset_0_0_0_2px_var(--accent)]" : "shadow-hairline")}>
        <div className="flex items-center gap-3 px-3 py-2.5">
          <AdminIcon name="shield" size={14} className={cx("shrink-0", !s.enabled ? "text-ink-3" : s.active ? "text-green" : "text-orange")} />
          <div className="min-w-0 flex-1">
            <div className="text-[13px] text-ink">{t("admin-pages-extensions.sandbox_label")}</div>
            {note && <div className="mt-0.5 text-[11.5px] leading-snug break-words text-ink-3">{note}</div>}
            {s.enabled && !s.active && <div className="mt-0.5 text-[11.5px] leading-snug text-ink-3">{t("admin-pages-extensions.sandbox_proxy_still")}</div>}
          </div>
          <Switch on={s.enabled} disabled={busy} onChange={(on) => (on ? void set(true) : setAsking(true))} label={t("admin-pages-extensions.sandbox_switch_label", { name: p.name })} />
        </div>
        {!s.enabled && (
          <div className="px-3 py-2.5">
            <p className="flex items-start gap-2 rounded-control bg-orange-tint px-3 py-2 text-[12.5px] leading-relaxed text-ink">
              <AdminIcon name="alert" size={13} className="mt-0.5 shrink-0 text-orange" />
              {t("admin-pages-extensions.sandbox_off_note", { name: p.name })}
            </p>
          </div>
        )}
        {s.enabled && e && (
          <div className="flex items-center gap-3 px-3 py-2.5">
            <span className="min-w-0 flex-1 text-[13px] text-ink">{t("admin-pages-extensions.network_egress")}</span>
            <span className="shrink-0 text-[12.5px] whitespace-nowrap tabular-nums">
              <span className="text-green">{t("admin-pages-extensions.network_allowed", { count: e.allowed ?? 0 })}</span>
              <span className="text-ink-3"> · </span>
              <span className={blocked ? "text-red" : "text-ink-3"}>{t("admin-pages-extensions.network_blocked", { count: blocked })}</span>
            </span>
          </div>
        )}
      </div>
      <Dialog
        open={asking}
        onClose={() => !busy && setAsking(false)}
        title={t("admin-pages-extensions.sandbox_off_title", { name: p.name })}
        footer={<>
          <Button tone="quiet" onClick={() => setAsking(false)} disabled={busy}>{t("admin-parts.cancel")}</Button>
          <Button tone="danger" busy={busy} onClick={() => void set(false)}>{t("admin-pages-extensions.sandbox_off_confirm")}</Button>
        </>}
      >
        <p className="text-[12.5px] leading-relaxed text-ink-2">{t("admin-pages-extensions.sandbox_off_body")}</p>
      </Dialog>
    </div>
  );
}
