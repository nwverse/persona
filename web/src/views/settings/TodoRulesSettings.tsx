import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { api, errorText } from "../../lib/api";
import { listConnectors, type HubConnector } from "../../lib/connectors";
import { clock } from "../../lib/format";
import { Button, cx, Loading, toast } from "../../ui/atoms";
import { Group, Row, SettingsHint, settingsInputClass, Switch } from "./SettingsParts";

type TodoRule = {
  id: string;
  name: string;
  enabled?: boolean;
  sources?: string[];
  kinds?: string[];
  skip_senders?: string[];
  prompt: string;
};

type TodoRuleConfig = { rules?: TodoRule[]; dry_run?: boolean };

type TodoRuleDecision = {
  at: string;
  item_id: string;
  source?: string;
  kind?: string;
  sender?: string;
  title?: string;
  rule_name?: string;
  verdict: string;
  reason?: string;
  todo_id?: string;
};

const blankRule = (): TodoRule => ({ id: `rule_${Date.now()}`, name: "", enabled: true, sources: [], kinds: [], prompt: "" });


/** The sources a rule can pick: the hub's connectors (mail and Lark included), plus any id the rule already names that
 *  the hub no longer reports, so saving never drops it silently. */
function sourceChoices(connectors: HubConnector[], picked: string[]): { id: string; name: string }[] {
  const out = connectors.map((c) => ({ id: c.id, name: c.name || c.id }));
  for (const id of picked) if (!out.some((c) => c.id === id)) out.push({ id, name: id });
  return out;
}

function SourcePicker({ connectors, value, disabled, onChange }: { connectors: HubConnector[]; value: string[]; disabled?: boolean; onChange: (next: string[]) => void }) {
  return <ChipPicker label={t("settings-todorules.sources")} choices={sourceChoices(connectors, value)} value={value} empty={t("settings-todorules.all_sources")} disabled={disabled} onChange={onChange} />;
}

/** The Inbox kinds a rule can pick, by their names; a kind the rule names that this list lacks still shows. */
const KINDS = ["dm", "mention", "group", "bot", "doc"];
function kindChoices(picked: string[]): { id: string; name: string }[] {
  const out = KINDS.map((id) => ({ id, name: t(`settings-todorules.kind_${id}`) }));
  for (const id of picked) if (!out.some((c) => c.id === id)) out.push({ id, name: id });
  return out;
}

/** Senders a rule leaves alone: the bots recent decisions saw, plus any name typed in. A chip on is skipped. */
function SkipSenders({ seen, value, disabled, onChange }: { seen: string[]; value: string[]; disabled?: boolean; onChange: (next: string[]) => void }) {
  const [name, setName] = useState("");
  const choices = [...new Set([...value, ...seen])].map((n) => ({ id: n, name: n }));
  const typed = name.trim();
  return (
    <div className="flex flex-col gap-1.5">
      <ChipPicker label={t("settings-todorules.skip_senders")} choices={choices} value={value} empty={t("settings-todorules.skip_none")} disabled={disabled} onChange={onChange} />
      <form
        className="flex items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (!typed || value.includes(typed)) return;
          setName("");
          onChange([...value, typed]);
        }}
      >
        <input className={settingsInputClass} value={name} placeholder={t("settings-todorules.skip_placeholder")} aria-label={t("settings-todorules.skip_add")} onChange={(e) => setName(e.target.value)} disabled={disabled} />
        <Button type="submit" tone="secondary" icon="plus" disabled={disabled || !typed || value.includes(typed)}>{t("settings-todorules.skip_add")}</Button>
      </form>
    </div>
  );
}

function ChipPicker({ label, choices, value, empty, disabled, onChange }: { label: string; choices: { id: string; name: string }[]; value: string[]; empty: string; disabled?: boolean; onChange: (next: string[]) => void }) {
  const toggle = (id: string) => onChange(value.includes(id) ? value.filter((v) => v !== id) : [...value, id]);
  return (
    <div role="group" aria-label={label} className="flex flex-wrap items-center gap-1.5">
      {choices.map((c) => {
        const on = value.includes(c.id);
        return (
          <button
            key={c.id}
            type="button"
            aria-pressed={on}
            disabled={disabled}
            onClick={() => toggle(c.id)}
            className={cx(
              "whitespace-nowrap rounded-full px-2.5 py-1 text-[12px] transition-colors disabled:opacity-50",
              on ? "bg-accent-tint text-accent-ink" : "bg-field text-ink-2 hover:bg-hover",
            )}
          >
            {c.name}
          </button>
        );
      })}
      {value.length === 0 && <span className="text-[11.5px] text-ink-3">{empty}</span>}
    </div>
  );
}

function verdictLabel(verdict: string): string {
  const known = ["created", "appended", "not_todo", "dry_run", "no_rule", "skipped", "error"];
  return t(`settings-todorules.verdict_${known.includes(verdict) ? verdict : "unknown"}`);
}

export function TodoRulesSettings() {
  const [config, setConfig] = useState<TodoRuleConfig | null>(null);
  const [decisions, setDecisions] = useState<TodoRuleDecision[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [connectors, setConnectors] = useState<HubConnector[]>([]);
  useEffect(() => {
    // The source chips; without the list (an older hub, an error) the rule's own ids still show and can be removed.
    listConnectors(true).then(setConnectors, () => {});
  }, []);

  const load = async () => {
    setError("");
    try {
      const [next, log] = await Promise.all([
        api<TodoRuleConfig>("/todo-rules"),
        api<{ decisions?: TodoRuleDecision[] }>("/todo-rules/decisions"),
      ]);
      setConfig({ rules: (next.rules ?? []).map((rule, index) => ({ ...rule, id: rule.id || `rule_${index + 1}` })), dry_run: Boolean(next.dry_run) });
      setDecisions(log.decisions ?? []);
    } catch (e) {
      setError(errorText(e));
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const updateRule = (index: number, patch: Partial<TodoRule>) => {
    setConfig((current) => {
      if (!current) return current;
      const rules = [...(current.rules ?? [])];
      rules[index] = { ...rules[index], ...patch };
      return { ...current, rules };
    });
  };

  const save = async () => {
    if (!config || busy) return;
    setBusy(true);
    try {
      const next = await api<TodoRuleConfig>("/todo-rules", {
        method: "PUT",
        json: { dry_run: Boolean(config.dry_run), rules: config.rules ?? [] },
      });
      setConfig({ rules: (next.rules ?? []).map((rule, index) => ({ ...rule, id: rule.id || `rule_${index + 1}` })), dry_run: Boolean(next.dry_run) });
      toast(t("settings-todorules.saved"));
      const log = await api<{ decisions?: TodoRuleDecision[] }>("/todo-rules/decisions");
      setDecisions(log.decisions ?? []);
    } catch (e) {
      toast(t("settings-todorules.save_failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(false);
    }
  };

  if (!config) {
    return (
      <Group title={t("settings-todorules.title")} footer={error || t("settings-todorules.footer")}>
        {error ? <SettingsHint alert>{t("settings-todorules.load_failed", { error })}</SettingsHint> : <div className="px-4 py-3"><Loading /></div>}
        {error && <div className="flex justify-end px-4 pb-3"><Button tone="secondary" icon="retry" onClick={() => void load()}>{t("settings.retry")}</Button></div>}
      </Group>
    );
  }

  const rules = config.rules ?? [];
  // Bots the recent decisions saw, newest first: the senders a rule most likely wants to skip.
  const botSenders = [...new Set(decisions.filter((d) => d.kind === "bot" && d.sender).map((d) => d.sender!))];
  return (
    <>
      <Group title={t("settings-todorules.title")} footer={t("settings-todorules.footer")} anchor="todo-rules">
        <Row label={t("settings-todorules.dry_run")} hint={t("settings-todorules.dry_run_hint")} wrapHint>
          <Switch on={Boolean(config.dry_run)} onChange={(dry_run) => setConfig({ ...config, dry_run })} disabled={busy} label={t("settings-todorules.dry_run")} />
        </Row>
        {rules.length === 0 && <SettingsHint>{t("settings-todorules.empty")}</SettingsHint>}
        {rules.map((rule, index) => (
          <div key={rule.id} className="flex flex-col gap-2.5 px-4 py-3">
            <div className="flex items-center gap-3">
              <label className="min-w-0 flex-1 text-[12.5px] text-ink">
                <span className="sr-only">{t("settings-todorules.rule_name")}</span>
                <input className={settingsInputClass} value={rule.name} placeholder={t("settings-todorules.rule_name")} onChange={(e) => updateRule(index, { name: e.target.value })} disabled={busy} />
              </label>
              <Switch on={rule.enabled !== false} onChange={(enabled) => updateRule(index, { enabled })} disabled={busy} label={t("settings-todorules.enabled")} />
              <Button tone="quiet" icon="trash" title={t("settings-todorules.remove_rule")} onClick={() => setConfig({ ...config, rules: rules.filter((_, i) => i !== index) })} disabled={busy}>{t("settings-todorules.remove_rule")}</Button>
            </div>
            <label className="flex flex-col gap-1 text-[11.5px] text-ink-3">
              {t("settings-todorules.prompt")}
              <textarea className={`${settingsInputClass} min-h-24 resize-y py-2`} value={rule.prompt} placeholder={t("settings-todorules.prompt_placeholder")} onChange={(e) => updateRule(index, { prompt: e.target.value })} disabled={busy} />
            </label>
            <div className="flex flex-col gap-1 text-[11.5px] text-ink-3">
              {t("settings-todorules.sources")}
              <SourcePicker connectors={connectors} value={rule.sources ?? []} onChange={(sources) => updateRule(index, { sources })} disabled={busy} />
            </div>
            <div className="flex flex-col gap-1 text-[11.5px] text-ink-3">
              {t("settings-todorules.kinds")}
              <ChipPicker label={t("settings-todorules.kinds")} choices={kindChoices(rule.kinds ?? [])} value={rule.kinds ?? []} empty={t("settings-todorules.all_kinds")} disabled={busy} onChange={(kinds) => updateRule(index, { kinds })} />
            </div>
            <div className="flex flex-col gap-1 text-[11.5px] text-ink-3">
              {t("settings-todorules.skip_senders")}
              <SkipSenders seen={botSenders} value={rule.skip_senders ?? []} disabled={busy} onChange={(skip_senders) => updateRule(index, { skip_senders })} />
            </div>
          </div>
        ))}
        <div className="flex flex-wrap justify-end gap-2 px-4 py-3">
          <Button tone="quiet" icon="plus" onClick={() => setConfig({ ...config, rules: [...rules, blankRule()] })} disabled={busy}>{t("settings-todorules.add_rule")}</Button>
          <Button tone="primary" busy={busy} onClick={() => void save()}>{t("settings-todorules.save")}</Button>
        </div>
      </Group>

      <Group title={t("settings-todorules.decisions_title")} footer={t("settings-todorules.decisions_footer")} anchor="todo-decisions">
        {decisions.length === 0 ? <SettingsHint>{t("settings-todorules.decisions_empty")}</SettingsHint> : decisions.map((decision, index) => (
          <div key={`${decision.item_id}-${decision.at}-${index}`} className="flex flex-col gap-1 px-4 py-2.5 text-[12px] text-ink-2">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <time className="text-ink-3" dateTime={decision.at}>{clock(decision.at)}</time>
              <span className="font-medium text-ink">{verdictLabel(decision.verdict)}</span>
              {decision.rule_name && <span className="text-ink-3">{decision.rule_name}</span>}
            </div>
            {decision.title && <div className="break-words">{decision.title}</div>}
            {decision.reason && <div className="break-words text-ink-3">{decision.reason}</div>}
          </div>
        ))}
      </Group>
    </>
  );
}
