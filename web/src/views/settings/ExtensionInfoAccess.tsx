// What an extension may read of the user's personal information (#13500, docs/assistant/health.md "Using information
// by scene", GET / PUT /api/v1/assistant/extension-trust): "This is my own extension" lets it read everything (the user's
// own memory extension, say); otherwise it reads personal information only of the kinds turned on here. Shown on the
// extension's page; an assistant without these grants (an older one) shows nothing.
import { useEffect, useState } from "react";
import { errorText, isAbort } from "../../lib/api";
import { ownedAssistant, type ScopedAssistantClient } from "../../lib/scoped-assistant-api";
import { listScopedExtensionTrust, setScopedExtensionTrust } from "../../lib/scoped-extension-trust";
import { INFO_CATEGORIES, trustOf, type ExtensionTrust } from "../../lib/tracks";
import { Switch, toast } from "../../ui/atoms";
import { categoryLabel } from "../spaces/labels";
import { Group, Row } from "./SettingsParts";
import { t } from "../../i18n";
import { useOpeningDefaultAssistant, useScopedAssistant } from "./integrations/useScopedAssistant";

type Props = { extension: string; editable?: boolean };

export function ExtensionInfoAccess(props: Props) {
  const managerId = useOpeningDefaultAssistant(props.extension);
  return managerId === undefined ? null : <ScopedGrantCard {...props} managerId={managerId} />;
}

function ScopedGrantCard({ managerId, ...props }: Props & { managerId: string }) {
  const { client, routing } = useScopedAssistant(ownedAssistant(managerId), "extension-trust", props.extension);
  return <GrantCard key={client.key} {...props} client={client} routing={routing} />;
}

function GrantCard({ extension, editable = true, client, routing }: Props & { client: ScopedAssistantClient; routing: string }) {
  const [trust, setTrust] = useState<ExtensionTrust | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    const load = () => void listScopedExtensionTrust(client).then((items) => {
      if (active) setTrust(items ? trustOf(items, extension) : null);
    }, (e: unknown) => {
      if (active && !isAbort(e)) setTrust(null);
    });
    load();
    const stop = client.subscribe(load);
    return () => { active = false; stop(); };
  }, [client, extension, routing]);

  const save = async (next: ExtensionTrust) => {
    const was = trust;
    setTrust(next); // shown at once; put back when the hub refuses
    setBusy(true);
    try {
      const r = await setScopedExtensionTrust(client, extension, next);
      setTrust({ trust: r.trust === "owner" ? "owner" : "third_party", categories: r.categories ?? [] });
    } catch (e) {
      setTrust(was);
      if (!isAbort(e)) toast(t("settings-extensioninfoaccess.save_failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(false);
    }
  };

  if (!trust) return null;
  const owner = trust.trust === "owner";
  const disabled = busy || !editable;
  return (
    <div data-extension-info-access>
      <Group title={t("settings-extensioninfoaccess.title")} footer={owner ? t("settings-extensioninfoaccess.owner_footer") : t("settings-extensioninfoaccess.footer")}>
        <Row label={t("settings-extensioninfoaccess.owner")} hint={t("settings-extensioninfoaccess.owner_hint")} wrapHint>
          <Switch on={owner} disabled={disabled} label={t("settings-extensioninfoaccess.owner")} onChange={(on) => void save({ trust: on ? "owner" : "third_party", categories: trust.categories })} />
        </Row>
        {!owner && INFO_CATEGORIES.map((c) => {
          const on = trust.categories.includes(c);
          const label = categoryLabel(c);
          return (
            <Row key={c} label={label}>
              <Switch
                on={on}
                disabled={disabled}
                label={t("settings-extensioninfoaccess.category", { name: label })}
                onChange={(next) => void save({ trust: "third_party", categories: next ? [...trust.categories, c] : trust.categories.filter((x) => x !== c) })}
              />
            </Row>
          );
        })}
      </Group>
    </div>
  );
}
