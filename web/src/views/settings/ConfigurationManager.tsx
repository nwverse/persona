import { useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { useKnownDefault, useShownManager } from "../../lib/manager-choice";
import { managerOrder, useRoster } from "../../lib/managers";
import { Loading } from "../../ui/atoms";
import { Group, Row } from "./SettingsParts";

/** A configuration editor owns its target, independently of the chat switcher and late requests. */
export function ConfigurationManager({ children }: { children: (manager: string) => ReactNode }) {
  const roster = useRoster();
  const knownDefault = useKnownDefault();
  const shown = useShownManager();
  const [picked, setPicked] = useState<string | null>(() => new URLSearchParams(location.search).get("manager"));
  const manager = picked ?? shown;
  const managers = roster ? managerOrder(roster) : knownDefault ? [{ id: knownDefault, name: knownDefault }] : [];
  const valid = managers.some((m) => m.id === manager);
  return <>
    <Group title={t("settings-assistantsettings.configuration_target")} footer={t("settings-assistantsettings.configuration_target_hint")}>
      <Row label={t("settings-assistantsettings.configuration_target")}>
        <select aria-label={t("settings-assistantsettings.configuration_target")} value={manager} disabled={!managers.length}
          className="min-w-0 max-w-full rounded-control bg-field px-3 py-2 text-[13px] text-ink shadow-inset-field"
          onChange={(e) => {
            const target = e.target.value;
            setPicked(target);
            const url = new URL(location.href);
            url.searchParams.set("manager", target);
            history.replaceState(history.state, "", `${url.pathname}${url.search}${url.hash}`);
          }}>
          {!valid && <option value={manager}>{manager}</option>}
          {managers.map((m) => <option key={m.id} value={m.id}>{m.name} · {m.id}</option>)}
        </select>
      </Row>
    </Group>
    {!managers.length ? <Loading /> : !valid ? <p role="alert" className="text-[13px] text-red">{t("settings-assistantsettings.configuration_target_missing")}</p> : <div key={manager}>{children(manager)}</div>}
  </>;
}
