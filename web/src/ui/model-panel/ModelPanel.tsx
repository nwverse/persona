// The unified model panel (#5032, after Cindy's UnifiedModelPanel): search on top, a rail on the left (Recent, Favorites,
// All, one cell per provider), two-line model rows on the right, "Configure models" at the bottom; a row's Customize opens
// its setup flyout. Opens above its anchor (the composer's model pill), right edges aligned. docs/web/web.md "Model pill
// and model panel"; spec of record: Cindy's model-selector-unified.md as recorded in docs/design/DESIGN_SYSTEM.md.
import type { ReactNode } from "react";
import type { PanelConfig, PanelProvider, PanelScope, PanelState } from "../../lib/model-panel";

export interface ModelPanelProps {
  /** The element it opens from (the pill); the panel opens above it, right edges aligned. */
  anchor: HTMLElement;
  onClose: () => void;
  providers: PanelProvider[];
  state: PanelState;
  scope: PanelScope;
  /** Applies a whole setup (a row, a favorite, a recent entry); true when it was applied: the panel then records it in
   *  Recent and closes. false: the caller has said why; the panel stays open with the old setup. */
  onPick: (config: PanelConfig) => Promise<boolean>;
  /** A flyout edit (effort, Fast) on the model that runs now: applied at once, the flyout stays open. */
  onApplyCurrent?: (patch: { effort?: string; fast?: boolean }) => Promise<boolean>;
  /** Reads a provider's list again (its group's Retry). */
  onReload?: (provider: string) => void;
  /** Forgets a typed model ID (#1644). */
  onForget?: (provider: string, model: string) => void;
  /** Drawn inside the flyout of the model that runs now, under its controls (Claude Code's ultracode switch, #3428). */
  currentExtra?: ReactNode;
  /** A strip above the search (a Persona chat's "Following the default" / "Picked for this chat", #5033). */
  header?: ReactNode;
}

export function ModelPanel(_props: ModelPanelProps): ReactNode {
  return null;
}
