// A read-only Codex session's footer line (matching StoppedAgentLine): replaces the disabled composer with the
// read-only reason and the lazy-loaded compact takeover / continuation control, reserving height to prevent layout shift.
import { Suspense, lazy } from "react";
import { t } from "../../i18n";
import { sourceSubtitle } from "../../lib/format";
import type { Session } from "../../lib/types";

const TakeoverAction = lazy(() => import("./TakeoverAction"));

export function CodexReadOnlyLine({
  session,
  visible,
  continueSignal,
  onPendingChange,
}: {
  session: Session;
  visible: boolean;
  continueSignal?: number;
  onPendingChange?: (pending: boolean) => void;
}) {
  const reason = visible ? sourceSubtitle(session) || t("chat-chatview.view_only") : "";
  // Keep the same DOM wrapper and child slot when `visible` flips false so an active TakeoverAction dialog
  // (portaled to document.body) never unmounts across SSE updates that remove capabilities.takeover.
  return (
    <div
      role={visible ? "status" : undefined}
      data-codex-readonly-line={visible ? "" : undefined}
      hidden={!visible}
      className={visible ? "flex flex-wrap items-center gap-x-2 gap-y-1.5 rounded-card bg-inset px-3 py-2 text-xs text-ink-2" : undefined}
    >
      {visible ? <span className="min-w-0 flex-1 basis-48 break-words">{reason}</span> : null}
      <span className="flex shrink-0 items-center gap-1.5">
        <Suspense fallback={<span aria-hidden className="inline-block h-7 shrink-0" />}>
          <TakeoverAction
            key={`${session.machine_id}:${session.session_id}`}
            session={session}
            variant="compact"
            continueSignal={continueSignal}
            onPendingChange={onPendingChange}
          />
        </Suspense>
      </span>
    </div>
  );
}
