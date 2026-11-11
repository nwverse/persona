// What a model provider or launcher is called for people (#5029, docs/integrations/launchers.md "Display names"): its
// display name, in any language, with the startup ID (`name`, what a launch, a preset or the model settings refer to)
// as secondary text. A launcher without a display name (an rc alias, an older hub) keeps showing its ID. Every picker
// that names one goes through these, so a provider reads the same everywhere.

/** Anything with a startup ID and maybe a display name: a `Launcher` (GET /machines/{mid}/launchers), a `ProviderView`
 *  (GET /launchers), a preset's add result. */
export interface Named {
  name: string;
  display_name?: string;
}

const shown = (l: Named) => l.display_name?.trim() ?? "";

/** The name to show first: the display name, else the startup ID. */
export function providerTitle(l: Named): string {
  return shown(l) || l.name;
}

/** The startup ID to show as secondary text (small monospace or a tooltip), or "" when the title already is the ID. */
export function providerIdNote(l: Named): string {
  const d = shown(l);
  return d && d !== l.name ? l.name : "";
}

/** One line for a place that holds only plain text (a native `<option>`, a title attribute): "Team relay (team-relay)". */
export function providerLabel(l: Named): string {
  const id = providerIdNote(l);
  return id ? `${providerTitle(l)} (${id})` : providerTitle(l);
}

/** The launcher with startup ID `name` in `list`, as a `Named`; one that is not in it stands for itself. */
export function namedLauncher(name: string, list: readonly Named[] | null | undefined): Named {
  return list?.find((l) => l.name === name) ?? { name };
}

/** The title of the launcher with startup ID `name`, looked up in `list` (the ID itself when it is not there). */
export function launcherTitle(name: string, list: readonly Named[] | null | undefined): string {
  return providerTitle(namedLauncher(name, list));
}
