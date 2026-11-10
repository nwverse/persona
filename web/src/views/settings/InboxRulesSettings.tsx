// Settings › Inbox rules (docs/assistant/inbox.md "Corrections and learned rules"): the user's own triage rules
// (inbox.rules), one a line, the default manager's. The ones the AI drafted from the user's moves in the inbox land
// here once approved on the phone; here each can be edited or removed, and new ones added. The list is saved whole
// (PUT /inbox/rules), so a failed read shows no list to edit (saving it would wipe the rules the hub has).
import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { loadInboxRules, saveInboxRules } from "../../lib/inbox";
import { Button, IconButton, Loading, toast } from "../../ui/atoms";
import { Group, RowFailed, settingsInputClass } from "./SettingsParts";

export function InboxRulesSettings() {
  const [rules, setRules] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  const [busy, setBusy] = useState(false);
  // the rule being edited (its index; -1 a new one) and its text
  const [editing, setEditing] = useState<{ at: number; text: string } | null>(null);

  useEffect(() => {
    let dead = false;
    setError("");
    loadInboxRules()
      .then((r) => !dead && setRules(r))
      .catch((e) => !dead && setError(errorText(e)));
    return () => {
      dead = true;
    };
  }, [reload]);

  const save = async (next: string[]) => {
    const before = rules;
    setRules(next);
    setBusy(true);
    try {
      setRules(await saveInboxRules(next));
      setEditing(null);
    } catch (e) {
      setRules(before);
      toast(t("settings-inboxrules.save_failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(false);
    }
  };
  const commit = () => {
    if (!rules || !editing) return;
    const text = editing.text.trim();
    if (editing.at < 0) {
      if (text) void save([...rules, text]);
      else setEditing(null);
      return;
    }
    void save(text ? rules.map((r, i) => (i === editing.at ? text : r)) : rules.filter((_, i) => i !== editing.at));
  };

  const editor = (
    <form className="flex items-center gap-2 px-4 py-2.5" onSubmit={(e) => { e.preventDefault(); commit(); }}>
      <input autoFocus className={settingsInputClass} value={editing?.text ?? ""} disabled={busy} placeholder={t("settings-inboxrules.placeholder")}
        aria-label={t("settings-inboxrules.rule")} onChange={(e) => setEditing((x) => (x ? { ...x, text: e.target.value } : x))}
        onKeyDown={(e) => e.key === "Escape" && setEditing(null)} />
      <Button tone="primary" type="submit" busy={busy}>{t("settings-inboxrules.save")}</Button>
      <Button tone="quiet" onClick={() => setEditing(null)} disabled={busy}>{t("settings-inboxrules.cancel")}</Button>
    </form>
  );

  return (
    <Group anchor="inbox-rules" title={t("settings-inboxrules.title")} footer={t("settings-inboxrules.footer")}
      right={rules && !editing ? <Button tone="quiet" icon="plus" onClick={() => setEditing({ at: -1, text: "" })}>{t("settings-inboxrules.add")}</Button> : undefined}>
      {!rules ? (
        error ? (
          <RowFailed text={t("settings-inboxrules.load_failed", { error })} retry={t("settings-inboxrules.retry")} onRetry={() => setReload((n) => n + 1)} />
        ) : (
          <div className="px-4 py-3"><Loading /></div>
        )
      ) : (
        <>
          {rules.map((rule, i) => (editing?.at === i ? <div key={i}>{editor}</div> : (
            <div key={i} data-rule className="flex items-center gap-2 px-4 py-2.5">
              <p className="min-w-0 flex-1 text-[13px] leading-snug break-words text-ink">{rule}</p>
              <IconButton icon="pencil" label={t("settings-inboxrules.edit")} disabled={busy || Boolean(editing)} onClick={() => setEditing({ at: i, text: rule })} />
              <IconButton icon="trash" label={t("settings-inboxrules.remove")} disabled={busy || Boolean(editing)} onClick={() => void save(rules.filter((_, j) => j !== i))} />
            </div>
          )))}
          {editing?.at === -1 && editor}
          {!rules.length && !editing && <p className="px-4 py-3 text-[12.5px] text-ink-3">{t("settings-inboxrules.empty")}</p>}
        </>
      )}
    </Group>
  );
}
