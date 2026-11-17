import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { MANAGER_HEADER } from "../../lib/manager-choice";
import { Button, Loading } from "../../ui/atoms";
import { Group, SettingsHint } from "./SettingsParts";

type Identity = "bot" | "user";
type Scope = { channel: "larkcli"; target: string; scenario: "inbox_reply" | "assistant_message"; identity: Identity };
type Registry = { revision: number; authorizations: { id: string; messages?: Scope }[] };
const path = "/assistant/outbound-messages";
const selectClass = "min-h-11 w-full min-w-0 rounded-control bg-field px-3 py-2 text-meta text-ink shadow-inset-field";

export function OutboundMessages({ manager, changed }: { manager: string; changed: () => void }) {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [choice, setChoice] = useState<Identity>("bot");
  const [registry, setRegistry] = useState<Registry | null>(null);
  const [target, setTarget] = useState("");
  const [all, setAll] = useState(false);
  const [scenario, setScenario] = useState<Scope["scenario"]>("inbox_reply");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    const options = { headers: { [MANAGER_HEADER]: manager }, signal: controller.signal };
    setIdentity(null);
    setRegistry(null);
    Promise.all([api<{ identity: Identity }>(path, options), api<Registry>("/assistant/standing-authorizations", options)]).then(
      ([current, grants]) => {
        if (controller.signal.aborted) return;
        if (!["bot", "user"].includes(current.identity) || !Array.isArray(grants.authorizations)) throw new Error("Invalid settings");
        setIdentity(current.identity);
        setChoice(current.identity);
        setRegistry(grants);
      },
    ).catch(() => { if (!controller.signal.aborted) setNotice(t("settings-outbound.failed")); });
    return () => controller.abort();
  }, [manager, reload]);

  const request = async (scope?: Scope) => {
    if (busy || !registry) return;
    setBusy(true);
    setNotice(t("settings-outbound.pending"));
    try {
      await api(path, {
        method: "POST", headers: { [MANAGER_HEADER]: manager },
        body: JSON.stringify(scope ? { revision: registry.revision, scope } : { identity: choice }),
      });
      const options = { headers: { [MANAGER_HEADER]: manager } };
      const current = await api<{ identity: Identity }>(path, options);
      const grants = await api<Registry>("/assistant/standing-authorizations", options);
      const approved = scope
        ? grants.authorizations.some(g => g.messages && g.messages.channel === scope.channel && g.messages.target === scope.target &&
          g.messages.scenario === scope.scenario && g.messages.identity === scope.identity && !registry.authorizations.some(old => old.id === g.id))
        : current.identity === choice;
      setIdentity(current.identity);
      setChoice(current.identity);
      setRegistry(grants);
      setNotice(t(approved ? "settings-outbound.saved" : "settings-outbound.declined"));
      if (approved) changed();
    } catch {
      setNotice(t("settings-outbound.failed"));
    } finally {
      setBusy(false);
    }
  };
  const scope: Scope = { channel: "larkcli", target: all ? "" : target.trim(), scenario, identity: identity ?? "bot" };
  return <Group anchor="outbound-messages" title={t("settings-outbound.title")} footer={t("settings-outbound.footer")}>
    <SettingsHint>{t("settings-outbound.description")}</SettingsHint>
    {!identity && !notice ? <div className="p-4"><Loading /></div> : <>
      <div className="space-y-3 p-4">
        <label className="block space-y-2 text-meta text-ink">
          <span>{t("settings-outbound.identity")}</span>
          <select aria-label={t("settings-outbound.identity")} className={selectClass} value={choice} disabled={busy || !identity} onChange={e => setChoice(e.target.value as Identity)}>
            <option value="bot">{t("settings-outbound.bot")}</option>
            <option value="user">{t("settings-outbound.user")}</option>
          </select>
        </label>
        <Button className="min-h-11" disabled={busy || !identity || identity === choice} onClick={() => void request()}>{t("settings-outbound.save_identity")}</Button>
      </div>
      <div className="space-y-3 p-4">
        <h3 className="text-meta font-medium text-ink">{t("settings-outbound.automatic")}</h3>
        <p className="text-meta text-ink-3">{t("settings-outbound.channel")}: {t("settings-outbound.lark")}</p>
        <label className="block space-y-2 text-meta text-ink">
          <span>{t("settings-outbound.target")}</span>
          <input aria-label={t("settings-outbound.target")} className={selectClass} value={target} placeholder="oc_example" disabled={busy || all} onChange={e => setTarget(e.target.value)} />
        </label>
        <label className="flex min-h-11 items-center gap-3 text-meta text-ink">
          <input type="checkbox" checked={all} disabled={busy} onChange={e => setAll(e.target.checked)} />
          {t("settings-outbound.all_chats")}
        </label>
        <label className="block space-y-2 text-meta text-ink">
          <span>{t("settings-outbound.scenario")}</span>
          <select aria-label={t("settings-outbound.scenario")} className={selectClass} value={scenario} disabled={busy} onChange={e => setScenario(e.target.value as Scope["scenario"])}>
            <option value="inbox_reply">{t("settings-outbound.inbox_reply")}</option>
            <option value="assistant_message">{t("settings-outbound.assistant_message")}</option>
          </select>
        </label>
        <p className="text-meta text-ink-3">{t("settings-outbound.bound_identity")}: {t(`settings-outbound.${identity ?? "bot"}`)}</p>
        <Button className="min-h-11" disabled={busy || !identity || !registry || (!all && !/^oc_[A-Za-z0-9_]+$/.test(scope.target))} onClick={() => void request(scope)}>{t("settings-outbound.request")}</Button>
      </div>
    </>}
    {notice && <SettingsHint alert={notice === t("settings-outbound.failed")}>{notice}</SettingsHint>}
    {notice === t("settings-outbound.failed") && <div className="p-4"><Button className="min-h-11" disabled={busy} onClick={() => { setNotice(""); setReload(n => n + 1); }}>{t("settings-standing.refresh")}</Button></div>}
  </Group>;
}
