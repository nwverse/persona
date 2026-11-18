// A permission mode change in an Agent's chat (#10297, mode-changes.ts): a hairline across the chat with what happened
// in the middle, as the compaction divider draws (Steps.tsx CompactDivider), on more than one line where it must. Not a
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

/** The line's words for a change. */
export function modeChangeLabel(c: ModeChange, session?: Session): string {
  const to = modeName(c.to, session);
  switch (c.kind) {
    case "restart_done":
      return t("chat-modes.restart_done", { to });
    case "restart_failed":
      return t("chat-modes.restart_failed", { to });
    case "restart_dropped":
      return t("chat-modes.restart_dropped", { to });
  }
  if (c.applied === "restart") return t("chat-modes.restart_queued", { who: who(c), to });
  const from = modeName(c.from, session);
  return from && from !== to ? t("chat-modes.changed_from", { who: who(c), from, to }) : t("chat-modes.changed_to", { who: who(c), to });
}

export function ModeChangeDivider({ change, session }: { change: ModeChange; session?: Session }) {
  const label = modeChangeLabel(change, session);
  return (
    <div role="separator" aria-label={label} title={clock(change.at) || undefined} className="flex min-w-0 items-center gap-3 py-1 text-submeta text-ink-3" data-mode-change={change.kind}>
      <span aria-hidden className="h-px min-w-4 flex-1 bg-line-strong" />
      {/* wraps rather than cuts: on a phone the restart's "once this turn ends" is the part that matters */}
      <span className="min-w-0 text-center">{label}</span>
      <span aria-hidden className="h-px min-w-4 flex-1 bg-line-strong" />
    </div>
  );
}
