// Active model-switcher providers without subscription usage windows yet, loaded after mount by SidebarUsage so the
// cold first screen stays within FIRST_SCREEN_BUDGET (#10082, #10349).
import { useEffect } from "react";
import { assistantLaunchers, readAssistantLaunchers, useModelLists } from "../lib/session-models";
import { useStore } from "../lib/store";
import { liveAccounts, useHiddenUsageProviders, useUsageWindowsReady, type UnconnectedProvider, type UsageAccount } from "../lib/usage-windows";

export interface UsageProviderCandidate {
  name: string;
  display_name?: string;
  agent?: string;
  agent_type?: string;
  base_url?: string;
}

const BUILTIN_TITLES: Readonly<Record<string, string>> = {
  claude: "Claude Code",
  codex: "Codex",
  agy: "Antigravity",
};

/** Whether the owner hid this launcher's provider from the usage bars (`usage.hidden_providers`, #13179): by its own name or
 *  title, or, for one that is the agent's own subscription (no relay address), by the agent's provider name. */
function hiddenLauncher(l: UsageProviderCandidate, title: string, hidden: readonly string[]): boolean {
  if (!hidden.length) return false;
  if (hidden.includes(l.name.toLowerCase()) || hidden.includes(title.toLowerCase())) return true;
  if (l.base_url) return false;
  const agent = (l.agent_type || l.agent || "claude").toLowerCase();
  return hidden.includes(agent === "antigravity" ? "agy" : agent);
}

/** Providers offered in the model switcher that do not yet have a live subscription account in `live`, leaving out the
 *  ones the owner hid from the bars (`hidden`, lowercase). */
export function unconnectedUsageProviders(launchers: readonly UsageProviderCandidate[], live: readonly UsageAccount[], hidden: readonly string[] = []): UnconnectedProvider[] {
  const out: UnconnectedProvider[] = [];
  const seen = new Set<string>();
  for (const l of launchers) {
    if (!l.name || seen.has(l.name)) continue;
    seen.add(l.name);
    const title = l.display_name?.trim() || BUILTIN_TITLES[l.name] || l.name;
    if (hiddenLauncher(l, title, hidden)) continue;
    const agent = (l.agent_type || l.agent || "claude").toLowerCase();
    const idLower = l.name.toLowerCase();
    const titleLower = title.toLowerCase();
    const matchedByProvider =
      !l.base_url &&
      ((agent === "codex" && live.some((a) => a.provider === "codex")) ||
        (agent === "claude" && live.some((a) => a.provider === "claude")) ||
        ((agent === "agy" || agent === "antigravity") && live.some((a) => a.provider === "agy" || a.provider === "antigravity")));
    const matchedByName = live.some(
      (a) =>
        a.name.toLowerCase() === titleLower ||
        a.name.toLowerCase() === idLower ||
        a.sources?.some((s) => s.toLowerCase() === titleLower || s.toLowerCase() === idLower),
    );
    if (matchedByProvider || matchedByName) continue;
    out.push({ id: l.name, title, agent });
  }
  return out;
}

export function UsageUnconnected({ accounts, onChange }: { accounts: UsageAccount[]; onChange: (next: readonly UnconnectedProvider[]) => void }) {
  const version = useModelLists();
  const usageReady = useUsageWindowsReady();
  const hidden = useHiddenUsageProviders();
  const mid = useStore((s) => s.assistant?.machine_id || s.machines.find((m) => m.online)?.id || s.machines[0]?.id || "");
  useEffect(() => {
    if (mid) void readAssistantLaunchers(mid);
  }, [mid]);
  const launchers = mid ? assistantLaunchers(mid).info?.launchers : undefined;
  useEffect(() => {
    onChange(usageReady && launchers ? unconnectedUsageProviders(launchers, liveAccounts(accounts), hidden) : []);
  }, [usageReady, launchers, accounts, hidden, version, onChange]);
  return null;
}
