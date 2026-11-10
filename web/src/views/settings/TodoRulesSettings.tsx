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
  prompt: string;
};

type TodoRuleConfig = { rules?: TodoRule[]; dry_run?: boolean };

type TodoRuleDecision = {
  at: string;
  item_id: string;
  source?: string;
  kind?: string;
  title?: string;
  rule_name?: string;
  verdict: string;
  reason?: string;
  todo_id?: string;
};

const blankRule = (): TodoRule => ({ id: `rule_${Date.now()}`, name: "", enabled: true, sources: [], kinds: [], prompt: "" });

const splitValues = (value: string) => value.split(",").map((v) => v.trim()).filter(Boolean);
const joinValues = (value?: string[]) => (value ?? []).join(", ");

/** The sources a rule can pick: the hub's connectors (mail and Lark included), plus any id the rule already names that
 *  the hub no longer reports, so saving never drops it silently. */
function sourceChoices(connectors: HubConnector[], picked: string[]): { id: string; name: string }[] {
  const out = connectors.map((c) => ({ id: c.id, name: c.name || c.id }));
  for (const id of picked) if (!out.some((c) => c.id === id)) out.push({ id, name: id });
  return out;
}

function SourcePicker({ connectors, value, disabled, onChange }: { connectors: HubConnector[]; value: string[]; disabled?: boolean; onChange: (next: string[]) => void }) {
  const choices = sourceChoices(connectors, value);
  const toggle = (id: string) => onChange(value.includes(id) ? value.filter((v) => v !== id) : [...value, id]);
  return (
    <div role="group" aria-label={t("settings-todorules.sources")} className="flex flex-wrap items-center gap-1.5">
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
      {value.length === 0 && <span className="text-[11.5px] text-ink-3">{t("settings-todorules.all_sources")}</span>}
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
            <label className="flex flex-col gap-1 text-[11.5px] text-ink-3">
              {t("settings-todorules.kinds")}
              <input className={settingsInputClass} value={joinValues(rule.kinds)} placeholder={t("settings-todorules.all_kinds")} onChange={(e) => updateRule(index, { kinds: splitValues(e.target.value) })} disabled={busy} />
            </label>
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
