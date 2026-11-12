// Several forwards of one chat over a box as one bar (#3838; iOS QuoteBar's group row), in ForwardBar's shape. Its own
// chunk: only several forwarded at once draw it, and Forward to, which puts them there, loads it first.
import { t } from "../../i18n";
import { forwardGroupHeading, forwardLine } from "../../lib/forward";
import { Bar, type Forward } from "./ForwardBar";

/** The messages of a group over a box with a line each; the rest are counted ("2 more"). */
const GROUP_LINES = 3;

/** Several forwards of one chat as one bar (#3838; iOS QuoteBar's group row): "3 forwarded messages · chat", a line for
 *  each of the first GROUP_LINES ("Claude: Three times…") and how many more, the route, and × dropping them all. */
export function ForwardGroupBar({ forwards, onRemove }: { forwards: readonly Forward[]; onRemove?: () => void }) {
  const heading = forwardGroupHeading(forwards);
  const lines = forwards.map(forwardLine);
  const more = lines.length - GROUP_LINES;
  return (
    <Bar
      heading={heading}
      title={[heading, ...lines].join("\n")}
      route={forwards.find((f) => f.route)?.route}
      remove={onRemove && { label: t("chat-forwardto.remove_group"), run: onRemove }}
      group={forwards.length}
    >
      {lines.slice(0, GROUP_LINES).map((l, i) => (
        <span key={i} className="line-clamp-1 text-[12.5px] leading-[1.5] text-ink-2 [overflow-wrap:anywhere]">
          {l}
        </span>
      ))}
      {more > 0 && <span className="text-[11.5px] text-ink-3">{t("chat-forward.more", { count: more })}</span>}
    </Bar>
  );
}
