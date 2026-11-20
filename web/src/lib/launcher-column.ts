// The on-demand launcher column reuses the existing setup and family projection (#10249).
import { panelSections, sameSetup, type PanelConfig, type PanelProvider, type SectionsInput, type PanelSection, type PanelRow } from "./model-panel";

/** Launcher-first projection. Preserve row setup and family rules without promoting models out of their launcher. */
export function launcherSections(
  input: Omit<SectionsInput, "view">,
): PanelSection[] {
  return input.providers.map((provider) => {
    const section = panelSections({
      ...input,
      view: { kind: "provider", provider: provider.id },
      query: "",
    }).find((s) => s.kind === "provider");
    return (
      section ?? {
        key: `provider:${provider.id}`,
        kind: "provider",
        provider,
        rows: [],
      }
    );
  });
}

/** Favorites first, then recent. A caller that merges effective setups can apply the display limit afterwards. */
export function quickPicks(input: Omit<SectionsInput, "view">, limit = 3): PanelRow[] {
  const saved = ["favorites", "recent"].flatMap((kind) =>
    panelSections({
      ...input,
      query: "",
      view: { kind: kind as "favorites" | "recent" },
    }).flatMap((s) => s.rows),
  );
  return saved
    .filter(
      (row, i) =>
        !saved.slice(0, i).some((before) => sameSetup(before.setup, row.setup)),
    )
    .slice(0, limit);
}

/** Search is one flat model list in provider order, with no family headings or recommended duplicates. */
export function launcherSearch(input: Omit<SectionsInput, "view">): PanelRow[] {
  return panelSections({
    ...input,
    promote: false,
    view: { kind: "all" },
    favorites: [],
    presets: [],
  }).flatMap((s) => s.rows);
}

/** A catalog without choices stays reachable for Retry, help or a manual default. Loading and older hubs stay in place. */
export const unavailableCatalog = (p: PanelProvider) => Boolean(p.unavailable || p.error || p.catalogCount === 0);

export interface ShortcutAliases {
  recent: string[];
  presets: string[];
}

/** Keep the first shortcut's action. Following a setting stays dynamic, and favorites keep their edit/remove controls. */
export function newAgentShortcuts(quick: PanelRow[], presets: PanelRow[], setupKey: (setup: PanelConfig | null, preset?: string) => string | undefined, follow: boolean) {
  const first = new Map<string, string>();
  const aliases = new Map<string, ShortcutAliases>();
  const followed = follow && setupKey(null);
  if (followed) first.set(followed, "follow");
  const keep = (row: PanelRow, room: boolean) => {
    if (unavailableCatalog(row.provider)) return false;
    const key = setupKey(row.setup, row.preset?.key);
    if (!key) return room;
    const previous = first.get(key);
    if (!previous || row.kind === "favorite") {
      if (!previous && room) first.set(key, row.key);
      return room;
    }
    const names = aliases.get(previous) ?? { recent: [], presets: [] };
    const group = row.preset ? names.presets : names.recent;
    const name = row.preset?.name ?? row.model.name;
    if (!group.includes(name)) group.push(name);
    aliases.set(previous, names);
    return false;
  };
  const saved: PanelRow[] = [];
  for (const row of quick) if (keep(row, saved.length < 3)) saved.push(row);
  return { quick: saved, presets: presets.filter((row) => keep(row, true)), aliases };
}
