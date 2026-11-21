// The sidebar observes requests for a name or confirmation. Load the dialog when a tree action needs it.
import { Suspense, useSyncExternalStore } from "react";
import { getAsk, subscribeAsk } from "../lib/node-palette";
import { lazyPage } from "../lib/lazy-page";

const NodePromptDialog = lazyPage(() => import("./NodePromptDialog").then(m => m.NodePromptDialog));

export function NodePrompt() {
  const ask = useSyncExternalStore(subscribeAsk, getAsk, getAsk);
  return ask && <Suspense fallback={null}><NodePromptDialog ask={ask} /></Suspense>;
}
