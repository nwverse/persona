// The sidebar head and conversation title share the same live or remembered assistant identity.
// Display only: the remembered identity never changes routing or the current manager.
import { useSyncExternalStore } from "react";
import { pickedName, useChosenManager, useShownIdentity, type ShownIdentity } from "./manager-choice";
import { currentRoster, subscribeRoster } from "./managers";

const hasRoster = () => currentRoster() !== null;

export function useAssistantIdentity(): ShownIdentity | null {
  const loaded = useSyncExternalStore(subscribeRoster, hasRoster, hasRoster);
  const remembered = useShownIdentity();
  const picked = useChosenManager();
  // setRoster records the live identity separately, so status and count pushes leave the chat's title alone.
  return !loaded && picked && remembered?.id !== picked ? { id: picked, name: pickedName() ?? picked } : remembered;
}
