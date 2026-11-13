// The fold-all button (docs/design/DESIGN_SYSTEM.md 3.4): the Agents title row's and a to-do's Decisions header's
// (#6447). Its icon, label and hint name the next action: corners in (`foldToggle`) for Collapse all, out
// (`unfoldToggle`, `expand`) for Expand all, and the hint under the button says what that changes, on hover after a
// short pause and on keyboard focus (a bare icon read as a dead button). Its parent is the hint's positioning box
// (`relative`), and the hint follows the button so the `peer` variants reach it.
import { useId } from "react";
import { cx } from "./atoms";
import { Icon } from "./icons";

// shown 300 ms after the pointer rests (`visibility` switches after the delay), at once on keyboard focus
const HINT =
  "pointer-events-none invisible absolute top-full right-0 z-30 mt-1 w-max max-w-60 rounded-control bg-tooltip-bg px-2.5 py-1.5 text-xs leading-snug text-tooltip-fg shadow-overlay transition-[visibility] peer-hover/fold:visible peer-hover/fold:delay-300 peer-focus-visible/fold:visible";

export function FoldAllButton({ label, hint, expand, onClick, className, testId }: { label: string; hint: string; expand: boolean; onClick: () => void; className?: string; testId?: string }) {
  const id = useId();
  return (
    <>
      <button type="button" aria-label={label} aria-describedby={id} data-testid={testId} onClick={onClick} className={cx("peer/fold", className)}>
        <Icon name={expand ? "unfoldToggle" : "foldToggle"} size={12} stroke={2} />
      </button>
      <span role="tooltip" data-testid={testId && `${testId}-hint`} className={HINT}>
        <span className="block font-medium">{label}</span>
        <span id={id} className="block text-tooltip-muted">{hint}</span>
      </span>
    </>
  );
}
