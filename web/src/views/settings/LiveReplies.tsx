// Settings › Claude Code › Live replies (#1309; docs/integrations/claude-integration.md "Live replies (MessageDisplay)"):
// whether the Claude sessions Persona starts on a computer send their replies as Claude writes them. The web and the
// iPhone and Mac apps draw them (docs/apple/ios.md "An agent's live reply"), so the copy promises those. It
// costs that computer's terminal something (a reply shows a few lines at a time instead of word by word), so it is a
// switch per machine, on by default. GET / PUT /api/v1/machines/{mid}/claude-live-replies {"on"}; the hub keeps
// it and tells the node at once. A change reaches the sessions started after it; a running one keeps what it started
// with until Claude restarts. "Change all online machines" above sends it to each online computer, as the other rows.
import { useEffect, useState } from "react";
import { applySummary, valueText, type ApplyResult } from "../../lib/agent-settings";
import { api, ApiError, enc, errorText } from "../../lib/api";
import { t } from "../../i18n";
import { Loading, Spinner, toast } from "../../ui/atoms";
import { Group, Row, RowFailed, Switch } from "./SettingsParts";

const path = (mid: string) => `/machines/${enc(mid)}/claude-live-replies`;

/** `others`: the other online computers a change also goes to while "Change all online machines" is on. */
export function LiveReplies({ mid, name, others }: { mid: string; name: (id: string) => string; others: string[] }) {
  const [on, setOn] = useState<boolean | null>(null); // null until the hub answered
  const [missing, setMissing] = useState(false); // an older hub without the route: no switch
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    if (!mid) return;
    let dead = false;
    setOn(null);
    setError("");
    setMissing(false);
    api<{ on: boolean }>(path(mid))
      .then((r) => !dead && setOn(r.on))
      .catch((e) => {
        if (dead) return;
        if (e instanceof ApiError && e.status === 404) setMissing(true);
        else setError(errorText(e));
      });
    return () => {
      dead = true;
    };
  }, [mid, reload]);

  if (missing) return null;

  const title = t("settings-livereplies.label");
  const change = async (next: boolean) => {
    if (busy || on === null) return;
    setBusy(true);
    setOn(next);
    const results: ApplyResult[] = await Promise.all(
      [mid, ...others].map(async (id) => {
        try {
          const r = await api<{ on: boolean }>(path(id), { method: "PUT", json: { on: next } });
          if (id === mid) setOn(r.on);
          return { id, name: name(id) };
        } catch (e) {
          if (id === mid) setOn(!next);
          return { id, name: name(id), error: errorText(e) };
        }
      }),
    );
    setBusy(false);
    const sum = applySummary(results);
    toast(
      sum.ok
        ? t("settings-livereplies.toast_value", { title, value: valueText(next), summary: sum.text })
        : t("settings-livereplies.toast_failed", { title, summary: sum.text }),
      sum.ok ? "ok" : "error",
    );
  };

  return (
    <Group title={t("settings-livereplies.title", { name: name(mid) })} footer={t("settings-livereplies.footer")}>
      {error ? (
        <RowFailed text={t("settings-livereplies.load_failed", { error })} retry={t("settings-livereplies.retry")} onRetry={() => setReload((n) => n + 1)} />
      ) : on === null ? (
        <div className="px-4 py-3">
          <Loading />
        </div>
      ) : (
        <Row label={title} hint={on ? t("settings-livereplies.detail_on") : t("settings-livereplies.detail_off")} wrapHint>
          {busy && <Spinner />}
          <Switch on={on} onChange={(v) => void change(v)} disabled={busy} label={title} />
        </Row>
      )}
    </Group>
  );
}
