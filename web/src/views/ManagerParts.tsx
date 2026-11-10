// What the pages show of the managers once there is more than one (docs/assistant/managers.md "Clients"): the assistant page's
// title and what an offline manager's assistant page says. With one manager none of it renders, so those pages stay as they were. The switcher itself is the sidebar's
// head (views/AssistantSwitcher.tsx).
import { useRef, useState } from "react";
import { t } from "../i18n";
import { managerOrder, multiManager, useRoster, type Manager } from "../lib/managers";
import { pickedName, useChosenManager, useShownManager } from "../lib/manager-choice";
import { ago } from "../lib/format";
import { api, enc, errorText } from "../lib/api";
import { Button, Menu, toast, useMenu } from "../ui/atoms";
import { MAIN_TITLE } from "./assistant/conversations";

/** The assistant page's title: the current assistant's name once there are several (the sidebar's switcher picks it,
 *  views/AssistantSwitcher.tsx), else Persona as before. */
export function useAssistantTitle(): string {
  const roster = useRoster();
  const shown = useShownManager();
  const picked = useChosenManager();
  // before the roster is read (a reload): a kept pick's name as the roster last gave it
  if (!roster && picked) return pickedName() || MAIN_TITLE;
  return (multiManager(roster) && roster.managers.find((m) => m.id === shown)?.name) || MAIN_TITLE;
}

/** What a composer says for a conversation of manager id while it is offline (several managers only): "<name> · last
 *  synced …"; undefined when it is online, unknown, or the hub has one manager. Such a conversation can't be sent to,
 *  wherever it was opened from (the inbox, a notification, the Agents list). */
export function useManagerOffline(id: string | undefined): string | undefined {
  const roster = useRoster();
  if (!id || !multiManager(roster)) return undefined;
  const m = roster.managers.find((x) => x.id === id);
  if (!m || m.online || m.installing) return undefined;
  const when = ago(m.last_seen);
  return when ? t("settings-managers.offline_composer", { name: m.name, time: when }) : t("settings-managers.offline_composer_never", { name: m.name });
}

/** The shown manager when it is offline (only with more than one): the assistant page then can't send. */
export function useShownOffline(): { manager: Manager; composer: string; body: string } | null {
  const roster = useRoster();
  const shown = useShownManager();
  if (!multiManager(roster)) return null;
  const m = roster.managers.find((x) => x.id === shown);
  if (!m || m.online) return null;
  const when = ago(m.last_seen);
  return {
    manager: m,
    composer: when ? t("settings-managers.offline_composer", { name: m.name, time: when }) : t("settings-managers.offline_composer_never", { name: m.name }),
    body: t("settings-managers.offline_body", { name: m.name }),
  };
}

/** Move to… on a computer's page: gives it to another manager (`POST /managers/{id}/machines`, docs/assistant/managers.md "Moving a
 *  machine or a source"); its sessions and cards follow, the machines push shows where it went. Only with more than
 *  one manager, and not for a linked hub's computer (not movable). */
export function MoveToManager({ machineId, manager, movable = true }: { machineId: string; manager?: string; movable?: boolean }) {
  const roster = useRoster();
  const menu = useMenu();
  const [busy, setBusy] = useState(false);
  const at = useRef<HTMLSpanElement>(null);
  if (!multiManager(roster) || !movable) return null;
  const owner = manager || roster.default;
  const move = async (to: Manager) => {
    setBusy(true);
    try {
      await api(`/managers/${enc(to.id)}/machines`, { method: "POST", json: { machine_id: machineId } });
      toast(t("settings-managers.moved", { name: to.name }), "ok");
    } catch (e) {
      toast(t("settings-managers.move_failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <span ref={at} className="contents">
        <Button tone="secondary" icon="arrowRight" busy={busy} onClick={() => (menu.anchor ? menu.close() : menu.openAt(at.current?.firstElementChild as HTMLElement | null))}>
          {t("settings-managers.move_to")}
        </Button>
      </span>
      {menu.anchor && (
        <Menu
          anchor={menu.anchor}
          onClose={menu.close}
          align="right"
          header={t("settings-managers.move_title")}
          items={managerOrder(roster).map((m) => ({
            key: m.id,
            label: m.name,
            checked: m.id === owner,
            hint: m.online ? undefined : t("settings-managers.move_offline"),
            disabled: m.id === owner,
            onSelect: () => void move(m),
          }))}
        />
      )}
    </>
  );
}
