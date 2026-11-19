import { useState } from "react";
import { t } from "../../i18n";
import { isBackgroundSupervisor } from "../../lib/background-supervisor";
import { NEW_MAIN_FEATURE } from "../../lib/compaction";
import { openNewAgent } from "../../lib/new-agent";
import { navigate } from "../../lib/router";
import { hubHas, useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import { Button, IconButton } from "../../ui/atoms";
import { lazyDialog } from "../LazyOverlay";

const NewMainDialog = lazyDialog(() => import("../assistant/NewMainDialog").then((m) => m.NewMainDialog));

export function ContextRestartNotice({ session, assistant, isMain }: { session: Session; assistant?: boolean; isMain: boolean }) {
  const supervisor = useStore((s) => isBackgroundSupervisor(s.assistant, session.session_id));
  const canNewMain = useStore((s) => hubHas(s.features, NEW_MAIN_FEATURE));
  const key = "context-restart-dismissed:" + JSON.stringify([session.manager, session.machine_id, session.session_id]);
  const [dismissed, setDismissed] = useState(() => {
    try { return localStorage.getItem(key) === "1"; } catch { return false; }
  });
  const [newMain, setNewMain] = useState(false);
  if (dismissed || session.archived || session.is_subagent || session.read_only_reason ||
      session.space === "health" || supervisor ||
      session.compaction?.state === "running" || (isMain && !canNewMain)) return null;

  const dismiss = () => {
    setDismissed(true);
    try { localStorage.setItem(key, "1"); } catch { /* The notice stays dismissed while this chat is open. */ }
  };
  const start = () => {
    if (isMain) setNewMain(true);
    else if (assistant) navigate({ name: "persona-new" });
    else openNewAgent({ machineId: session.machine_id, cwd: session.cwd });
  };
  const action = isMain ? t("assistant-assistantchat.new_main_ellipsis")
    : assistant ? t("chat-chatview.new_chat") : t("chat-contextrestart.new_agent");

  return (
    <>
      <div data-context-restart-notice role="status" className="flex flex-wrap items-center gap-2 rounded-control bg-orange-tint px-3 py-1.5 text-xs text-ink">
        <span className="min-w-32 flex-1">
          {isMain ? t("chat-contextrestart.main") : t("chat-contextrestart.regular")}
        </span>
        <Button tone="quiet" className="touch-hit shrink-0 whitespace-nowrap" onClick={start}>{action}</Button>
        <IconButton icon="x" label={t("chat-contextrestart.dismiss")} onClick={dismiss} />
      </div>
      {newMain && isMain && <NewMainDialog open onClose={() => setNewMain(false)} session={session} />}
    </>
  );
}
