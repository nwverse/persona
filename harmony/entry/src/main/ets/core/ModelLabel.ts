// One-line labels for the models a launcher lists (GET /machines/{mid}/launchers/{name}/models). The native Select has
// a single line per option, where iOS shows a second line with the resolved model (LauncherModelPicker `detail`).
// Claude Code lists aliases ("Opus") and names the version only in the description ("Opus 5.5 · Best for …") and the
// resolved ID (claude-opus-5-5), so the label adds it: "Opus 5.5", and "Default (recommended) · Opus 5.5".

type Payload = Record<string, Object>;

function text(source: Payload, key: string): string {
  const value = source[key];
  return typeof value === 'string' ? value.trim() : '';
}

/** "Opus 5.5" from a Claude model ID (claude-opus-5-5, with an optional date or [1m] suffix); '' for anything else. */
export function claudeModelName(id: string): string {
  const match = /^claude-([a-z]+)-(\d+)-(\d+)(?:-\d{8})?(?:\[1m\])?$/i.exec(id.trim());
  if (!match) return '';
  return match[1].charAt(0).toUpperCase() + match[1].slice(1).toLowerCase() + ' ' + match[2] + '.' + match[3];
}

/** The option's label: the name with the version the agent names for it, else the name (or the ID). */
export function launcherModelLabel(model: Payload): string {
  const id = text(model, 'id'), name = text(model, 'name') || id;
  const resolved = claudeModelName(text(model, 'resolved'));
  if (id === 'default' || model['default'] === true && /^default\b/i.test(name)) {
    return resolved && !name.includes(resolved) ? name + ' · ' + resolved : name;
  }
  const lead = text(model, 'description').split(' · ')[0].trim();
  if (lead.length > name.length && lead.length <= 40 && lead.toLowerCase().startsWith(name.toLowerCase() + ' ')) return lead;
  if (resolved && resolved.toLowerCase().startsWith(name.toLowerCase() + ' ')) return resolved;
  return name;
}

/** The reasoning efforts New session offers: the picked model's, or the agent default's row when none is picked (iOS
 * NewChatView reads the chosen model's `efforts` the same way). [] for a typed model ID, whose efforts nobody listed. */
export function startEfforts(models: Payload[], picked: string, typed: boolean = false): string[] {
  if (typed) return [];
  const row = picked ? models.find(item => text(item, 'id') === picked)
    : models.find(item => item['default'] === true) || models.find(item => text(item, 'id') === 'default');
  const efforts = row ? row['efforts'] : undefined;
  if (!Array.isArray(efforts)) return [];
  const out: string[] = [];
  (efforts as Object[]).forEach(item => { if (typeof item === 'string' && item.trim() && !out.includes(item.trim())) out.push(item.trim()); });
  return out;
}
