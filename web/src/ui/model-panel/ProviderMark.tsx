// A model provider's mark (#5032): Persona has no vendor logos for launchers, so every place that needs one (the model
// pill, the panel's rail, rows and flyout) draws the provider's first letter in a hairline tile, the way Cindy draws a
// provider it has no logo for: 15 × 15, 4 px corners, a 1 px border and the letter at 10 px semibold, all in the current
// text color. Its own file so the pill imports it without the panel.
import type { CSSProperties } from "react";
import { agentLetter } from "../../lib/agent-kind";
import { cx } from "../atoms";

/**
 * `size` is the tile's side in px (default 15; the letter is two thirds of it). It is the `--provider-mark` variable, so
 * a class on the mark or an ancestor (a container query) can set it instead. Decorative unless `label` is given: then it
 * says the provider's name (role=img).
 */
export function ProviderMark({ name, size, className, label }: { name: string; size?: number; className?: string; label?: string }) {
  const side = "var(--provider-mark, 15px)";
  const style: CSSProperties & Record<"--provider-mark", string | undefined> = {
    "--provider-mark": size ? `${size}px` : undefined,
    width: side,
    height: side,
    fontSize: `calc(${side} * 2 / 3)`,
  };
  const a11y = label ? { role: "img" as const, "aria-label": label } : { "aria-hidden": true as const };
  return (
    <span {...a11y} data-provider-mark className={cx("inline-flex shrink-0 items-center justify-center rounded-[4px] border border-current leading-none font-semibold", className)} style={style}>
      {agentLetter(name)}
    </span>
  );
}
