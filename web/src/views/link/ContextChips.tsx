// Chat and scheduled prompts share the same chip view. Only a non-empty selection loads it.
import { Suspense } from "react";
import { lazyPage } from "../../lib/lazy-page";
import { removeContext, useContexts, type ContextRef } from "../../lib/links";

const ChipList = lazyPage(() => import("./ContextChipList").then((m) => m.ContextChipList));

export function ContextChips({ draftKey }: { draftKey: string }) {
  const refs = useContexts(draftKey).filter((r) => r.kind !== "forward");
  return <ContextChipList refs={refs} onRemove={(id) => removeContext(draftKey, id)} />;
}

export function ContextChipList({ refs, onRemove }: { refs: ContextRef[]; onRemove: (id: string) => void }) {
  if (!refs.length) return null;
  return <Suspense fallback={null}><ChipList refs={refs} onRemove={onRemove} /></Suspense>;
}
