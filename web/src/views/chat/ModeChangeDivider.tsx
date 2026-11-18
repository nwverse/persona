// A permission mode change in an Agent's chat (#10297, mode-changes.ts): a hairline across the chat with what happened
// in the middle, as the compaction divider draws (Steps.tsx CompactDivider); words that wrap have no hairlines. Not a
// control: there is nothing to open.
import { t } from "../../i18n";
import { clock } from "../../lib/format";
import { listedModeOption, listedModesOf, modeTitle } from "../../lib/session-actions";
import type { Session } from "../../lib/types";
import type { ModeChange } from "./mode-changes";

/** A mode's name as the session's own menu calls it (a session that lists its modes names them its way). */
function modeName(id: string | undefined, session: Session | undefined): string {
  if (!id) return "";
  return listedModesOf(session)?.includes(id) ? listedModeOption(id).title : modeTitle(id);
}

/** Who made the change: the device by its name, the hub's own token, an extension by its name. */
function who(c: ModeChange): string {
  if (c.actor_kind === "hub") return t("chat-modes.by_hub");
  return c.device_name?.trim() || t("chat-modes.by_unknown");
}

/** Why a restart went as it did, in words, for the codes the node sends (docs/protocol/protocol.md "Session mode
 *  changes"); "" for none or one this app does not know (rows from before the codes hold English words). */
const WHY = new Set(["replaced", "process_exited", "process_replaced", "process_unknown", "expired", "not_stopped", "start_failed", "stopped"]);
function why(c: ModeChange): string {
  return c.reason && WHY.has(c.reason) ? t(`chat-modes.why_${c.reason}`) : "";
}

/** The line's words for a change: the modes in quotes, what became of a restart with why. */
export function modeChangeLabel(c: ModeChange, session?: Session): string {
  const to = modeName(c.to, session);
  const because = why(c);
  switch (c.kind) {
    case "restart_done":
      return t(c.reason === "already_in" ? "chat-modes.restart_already" : "chat-modes.restart_done", { to });
    case "restart_failed":
      return because ? t("chat-modes.restart_failed_why", { to, why: because }) : t("chat-modes.restart_failed", { to });
    case "restart_dropped":
      return because ? t("chat-modes.restart_dropped_why", { to, why: because }) : t("chat-modes.restart_dropped", { to });
  }
  if (c.applied === "restart") return t("chat-modes.restart_queued", { who: who(c), to });
  const from = modeName(c.from, session);
  return from && from !== to ? t("chat-modes.changed_from", { who: who(c), from, to }) : t("chat-modes.changed_to", { who: who(c), to });
}

export function ModeChangeDivider({ change, session }: { change: ModeChange; session?: Session }) {
  const label = modeChangeLabel(change, session);
  return (
    <div role="separator" aria-label={label} title={clock(change.at) || undefined} className="flex min-w-0 items-center gap-3 py-1 text-submeta text-ink-3" data-mode-change={change.kind}>
      {/* The words wrap rather than cut (on a phone the restart's "once this turn ends" is what matters); the hairlines
          only take the room the words leave, so a line that wraps has none instead of rules off its middle. */}
      <span aria-hidden className="h-px min-w-0 flex-1 bg-line-strong" />
      <span className="text-center">{label}</span>
      <span aria-hidden className="h-px min-w-0 flex-1 bg-line-strong" />
    </div>
  );
}
