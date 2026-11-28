import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { credentialOwner, subscribeCredentialOwner } from "../../../lib/api";
import { useShownManager } from "../../../lib/manager-choice";
import { multiManager, useRoster } from "../../../lib/managers";
import { bindScopedAssistant, currentAssistant, ownedAssistant, verifiedDefaultAssistant, type AssistantScope } from "../../../lib/scoped-assistant-api";

export function useScopedAssistant(scope: AssistantScope, kind: string, id: string) {
  const credential = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  const shown = useShownManager();
  const explicit = scope.kind === "owned" ? scope.managerId : undefined;
  const managerId = explicit ?? shown;
  const roster = useRoster();
  const client = useMemo(() => bindScopedAssistant(explicit === undefined ? currentAssistant : ownedAssistant(explicit), { kind, id }),
    [credential, explicit, managerId, kind, id]);
  const record = roster?.managers.find((m) => m.id === managerId);
  const named = Boolean(multiManager(roster));
  const routing = JSON.stringify([roster !== null, named, named ? undefined : shown, record?.online, record?.installing]);
  return { client, routing };
}

/** The default-only grant card captures its owner when opened, including on a hub from before the roster. */
export function useOpeningDefaultAssistant(resourceId: string): string | undefined {
  const credential = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  const roster = useRoster();
  const key = JSON.stringify([credential, resourceId]);
  const [opening, setOpening] = useState<{ key: string; managerId: string }>();
  const managerId = opening?.key === key ? opening.managerId : undefined;
  useEffect(() => {
    if (managerId !== undefined) return;
    let active = true;
    void verifiedDefaultAssistant().then((id) => {
      if (active) setOpening({ key, managerId: id });
    }, () => {});
    return () => { active = false; };
  }, [key, managerId, roster]);
  return managerId;
}
