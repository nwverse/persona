import type { ReactNode } from "react";
import { useChannelController, type ChannelController } from "./useChannelController";

/** Open the scoped editor when its channel detail is requested. */
export function ChannelResource({ id, manager, children }: { id: string; manager?: string; children: (controller: ChannelController) => ReactNode }) {
  return children(useChannelController(id, manager));
}
