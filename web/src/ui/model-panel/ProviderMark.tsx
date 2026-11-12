// A model provider's mark (#5032, #5497): the Agent its launcher runs, as `AgentMark` draws it in the Agents list, so every
// place that needs one (the model pill, the panel's rail, rows and flyout) says which Agent a pick runs on with its real
// mark, drawn once and first. An Agent with no mark of its own gets the generic `agent` icon, not a letter. Its own file
// so the pill imports it without the panel.
import { agentId, isMarked } from "../../lib/agent-kind";
import { AgentMark } from "../AgentMark";
import { cx } from "../atoms";
import { Icon } from "../icons";

/** Decorative: the provider's name is said by what it stands beside. `engine` is the launcher's (AgentMark ids). */
export function ProviderMark({ engine, size = 15, className }: { engine: string; size?: number; className?: string }) {
  if (isMarked(agentId(engine))) return <AgentMark agent={engine} size={size} decorative className={cx("shrink-0", className)} />;
  return (
    <span aria-hidden data-agent-mark="generic" className={cx("inline-flex shrink-0 items-center justify-center", className)} style={{ width: size, height: size }}>
      <Icon name="agent" size={size} />
    </span>
  );
}
