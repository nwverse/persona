// Settings › Labs (#9877, docs/web/web.md "Labs"): trial features and choices between a stable and a new implementation,
// one card per entry of lib/labs.ts. Each card says what it does, its default, who it affects (this browser only), when a
// change takes effect and how to go back. Saving is checked: a browser that doesn't keep it says so, and nothing changes.
import { useId, useState } from "react";
import { t } from "../../i18n";
import { LAB_IDS, LABS, useLab, writeLab, type LabDefinition, type LabId, type LabSource, type LabValue } from "../../lib/labs";
import { Dialog } from "../../ui/atoms";
import { Segmented } from "../assistant/parts";
import { Group, Pill, SettingsHint, Switch } from "./SettingsParts";

/** Where an entry is listed: the part of Persona it changes, named as the sidebar names it. */
type LabArea = "agents";

interface LabCopy {
  area: LabArea;
  title: string;
  /** What it does, in a sentence or two. */
  hint: string;
  /** A choice's option names, by value (a boolean lab uses a switch and needs none). */
  options?: Record<string, string>;
  /** When a change takes effect, for this entry's activation. */
  activation: string;
  /** How to go back, and what happens on its own when the new way fails. */
  rollback: string;
}

// Every lab has its words here: a new entry in LABS doesn't type-check until it does.
const COPY: Record<LabId, LabCopy> = {
  "terminal-renderer": {
    area: "agents",
    title: t("settings-labs.terminal_renderer"),
    hint: t("settings-labs.terminal_renderer_hint"),
    options: { standard: t("settings-labs.terminal_renderer_standard"), webgl: t("settings-labs.terminal_renderer_webgl") },
    activation: t("settings-labs.terminal_renderer_activation"),
    rollback: t("settings-labs.terminal_renderer_rollback"),
  },
};

const AREAS: { key: LabArea; label: string }[] = [{ key: "agents", label: t("settings-labs.area_agents") }];

export function LabsSettings() {
  return (
    <>
      <Group anchor="labs" footer={t("settings-labs.scope_note")}>
        <SettingsHint>{t("settings-labs.intro")}</SettingsHint>
      </Group>
      {AREAS.map((area) => {
        const ids = LAB_IDS.filter((id) => COPY[id].area === area.key);
        if (!ids.length) return null;
        return (
          <Group key={area.key} title={area.label} anchor={`labs-${area.key}`}>
            {ids.map((id) => (
              <LabCard key={id} id={id} />
            ))}
          </Group>
        );
      })}
    </>
  );
}

/** Labs over the current page (a sheet on a phone), opened from a feature's own menu (Terminal settings › Renderer): the
 *  same cards as Settings › Labs, without leaving the page, so nothing open behind it (a chat's draft) is unmounted. */
export function LabsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onClose={onClose} title={t("settings-nav.tab_labs")} width={600}>
      <div data-labs-dialog className="-mb-6">
        <LabsSettings />
      </div>
    </Dialog>
  );
}

type Status = { kind: "saved" } | { kind: "failed" } | null;

function LabCard<I extends LabId>({ id }: { id: I }) {
  const def: LabDefinition = LABS[id];
  const copy = COPY[id];
  const state = useLab(id);
  const [status, setStatus] = useState<Status>(null);
  const detailsId = useId();

  const choose = (value: LabValue<I>) => {
    if (value === state.value && state.source !== "invalid") return;
    setStatus(writeLab(id, value).ok ? { kind: "saved" } : { kind: "failed" });
  };

  const optionLabel = (value: string | boolean) =>
    typeof value === "boolean" ? (value ? t("settings-labs.on") : t("settings-labs.off")) : (copy.options?.[value] ?? value);

  return (
    <div data-settings-row={`lab-${id}`} data-lab={id} className="px-4 py-3">
      <div className="flex flex-col gap-x-3 gap-y-2 sm:flex-row sm:items-start">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-[13px] text-ink">{copy.title}</span>
            <Pill tone="orange">{t("settings-labs.trial")}</Pill>
          </div>
          <p className="mt-0.5 text-[11.5px] leading-snug text-ink-3">{copy.hint}</p>
        </div>
        <div className="shrink-0">
          {def.kind === "choice" ? (
            <Segmented<string>
              label={copy.title}
              value={state.value as string}
              options={def.options.map((o) => ({ v: o.value, label: optionLabel(o.value) }))}
              onChange={(v) => choose(v as LabValue<I>)}
            />
          ) : (
            <Switch on={state.value as boolean} onChange={(on) => choose(on as LabValue<I>)} label={copy.title} describedBy={detailsId} />
          )}
        </div>
      </div>
      <dl id={detailsId} className="mt-2.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[11.5px] leading-snug">
        <Detail term={t("settings-labs.default")}>{optionLabel(def.default)}</Detail>
        <Detail term={t("settings-labs.scope")}>{def.scope === "browser" ? t("settings-labs.scope_browser") : def.scope}</Detail>
        <Detail term={t("settings-labs.activation")}>{copy.activation}</Detail>
        <Detail term={t("settings-labs.rollback")}>{copy.rollback}</Detail>
      </dl>
      <SourceNote source={state.source} />
      <div aria-live="polite">
        {status?.kind === "saved" && <p className="mt-2 text-[11.5px] leading-snug text-green">{t("settings-labs.saved")}</p>}
      </div>
      {status?.kind === "failed" && (
        <p role="alert" className="mt-2 text-[11.5px] leading-snug text-red">
          {t("settings-labs.save_failed")}
        </p>
      )}
    </div>
  );
}

function Detail({ term, children }: { term: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="text-ink-3">{term}</dt>
      <dd className="min-w-0 text-ink-2">{children}</dd>
    </>
  );
}

/** Why the value shown is the default though something else may be saved. */
function SourceNote({ source }: { source: LabSource }) {
  if (source === "invalid") return <p className="mt-2 text-[11.5px] leading-snug text-orange">{t("settings-labs.invalid")}</p>;
  if (source === "unreadable") return <p className="mt-2 text-[11.5px] leading-snug text-orange">{t("settings-labs.unreadable")}</p>;
  return null;
}
