// The sidebar head and conversation title share the same live or remembered assistant identity.
// Display only: the remembered identity never changes routing or the current manager.
import { pickedName, useChosenManager, useShownIdentity, useShownManager, type ShownIdentity } from "./manager-choice";
import { managerOf, useRoster } from "./managers";

export function useAssistantIdentity(): ShownIdentity | null {
  const roster = useRoster();
  const current = useShownManager();
  const remembered = useShownIdentity();
  const picked = useChosenManager();
  const record = managerOf(roster, current);
  const known = record?.installing ? null : record;
  if (known) return { id: known.id, name: known.name, isDefault: known.id === roster?.default };
  if (roster) return null;
  if (picked) {
    const same = remembered?.id === picked;
    return {
      id: picked,
      name: same ? remembered.name : pickedName() ?? picked,
      isDefault: false,
      ...(same && remembered.line ? { line: remembered.line } : {}),
      ...(same && remembered.tone ? { tone: remembered.tone } : {}),
    };
  }
  return remembered ? { ...remembered, isDefault: remembered.isDefault ?? true } : null;
}
