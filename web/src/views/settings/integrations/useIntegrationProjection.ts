import { useCallback, useEffect, useState } from "react";
import { errorText } from "../../../lib/api";
import { extRoot } from "../../../lib/extension-api";
import { readIntegrationDetail, readIntegrationsCatalog, supportsIntegrationsProjection } from "../../../lib/integrations-projection";
import type { IntegrationKind } from "../../../lib/integrations-projection-wire";
import { setRoster, useRoster } from "../../../lib/managers";
import { ownedAssistant, rosterForCredential, verifiedAssistantRoster, type ScopedAssistantClient } from "../../../lib/scoped-assistant-api";
import { useScopedAssistant } from "./useScopedAssistant";

type Decision = { mode: "pending" | "projection" | "legacy" } | { mode: "error"; error: string };

/** An unknown roster waits for negotiation. Only a confirmed absence selects the legacy catalog. */
export function useIntegrationProjectionMode(manager: string) {
  useRoster();
  const roster = rosterForCredential();
  const target = roster?.managers.find(({ id }) => id === manager);
  const advertised = supportsIntegrationsProjection(roster, manager);
  const [decision, setDecision] = useState<Decision>({ mode: "pending" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (roster) {
      setDecision((previous) => target?.online === false && previous.mode === "projection" ? previous
        : { mode: advertised ? "projection" : "legacy" });
      return;
    }
    let active = true;
    void verifiedAssistantRoster().then(
      (checked) => {
        if (!active) return;
        if (checked && !rosterForCredential()) setRoster(checked);
        setDecision({ mode: supportsIntegrationsProjection(checked, manager) ? "projection" : "legacy" });
      },
      (error: unknown) => { if (active) setDecision({ mode: "error", error: errorText(error) }); },
    );
    return () => { active = false; };
  }, [roster, manager, target?.online, advertised, attempt]);

  return {
    ...decision,
    retry: () => {
      setDecision({ mode: "pending" });
      setAttempt((n) => n + 1);
    },
  };
}

function useProjectionRead<T>(manager: string, kind: string, id: string, read?: (client: ScopedAssistantClient) => Promise<T>) {
  const { client, routing } = useScopedAssistant(ownedAssistant(manager), kind, id);
  const [attempt, setAttempt] = useState(0);
  const [snapshot, setSnapshot] = useState<{ key: string; value?: T; loading: boolean; error?: string }>();
  const refresh = useCallback(() => setAttempt((n) => n + 1), []);

  useEffect(() => {
    if (!read) {
      setSnapshot(undefined);
      return;
    }
    let active = true;
    const key = client.key;
    setSnapshot((previous) => ({ key, loading: true, value: previous?.key === key ? previous.value : undefined }));
    void read(client).then(
      (value) => { if (active) setSnapshot({ key, value, loading: false }); },
      (error: unknown) => {
        if (active) setSnapshot((previous) => ({ key, loading: false, value: previous?.value, error: errorText(error) }));
      },
    );
    return () => { active = false; };
  }, [client, routing, read, attempt]);

  const current = read && snapshot?.key === client.key ? snapshot : undefined;
  return { value: current?.value, error: current?.error, loading: !!read && (current?.loading ?? true), refresh };
}

export function useIntegrationCatalogProjection(manager: string, enabled: boolean) {
  return useProjectionRead(manager, "integrations", "", enabled ? readIntegrationsCatalog : undefined);
}

export function useIntegrationDetailProjection(manager: string, resource: { kind: IntegrationKind; id: string } | null) {
  const kind = resource?.kind;
  const id = resource?.id ?? "";
  const read = useCallback((client: ScopedAssistantClient) => readIntegrationDetail(client, kind!, id), [kind, id]);
  return useProjectionRead(manager, kind ?? "", id, resource ? read : undefined);
}

export function useProjectedMarkets(manager: string, enabled: boolean) {
  const root = extRoot();
  const read = useCallback(async (client: ScopedAssistantClient) => {
    const result = await client.request<{ markets?: unknown[] }>(`${root}/-/markets`);
    return Array.isArray(result.markets) && result.markets.length > 0;
  }, [root]);
  return useProjectionRead(manager, "extension-markets", "", enabled ? read : undefined);
}
