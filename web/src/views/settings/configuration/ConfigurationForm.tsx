// A configuration form (lib/configuration, docs/web/web.md "Configuration forms"; docs/design/DESIGN_SYSTEM.md
// "Configuration forms"): one Document from an injected source, drawn with the settings pieces every page uses. It knows
// no module: titles, descriptions, details and notices are the source's words, shown as given.
//
// A single-step choice (switch, selection) is written at once and shows as pending until the source has committed it;
// typed text and numbers are drafts until Save (Enter on one line), and a multiselect's checks are a pending selection
// with Save and Cancel, so a fixed count can move from one value to another without an invalid write in between. Drafts
// stay through any failure or conflict. One write at a time, and a conflict or an uncertain write needs Reload first.
import { useEffect, useId, useState, useSyncExternalStore } from "react";
import { formatList, t } from "../../../i18n";
import { ConfigurationController, draftValue, type FieldState, type FormState, type Problem } from "../../../lib/configuration/controller";
import { ERROR_CODES, fieldValue, sameValue, scalarLength, type ConfigurationField, type ConfigurationSource, type FieldValue, type KnownField } from "../../../lib/configuration/contract";
import { Button, Loading, Spinner } from "../../../ui/atoms";
import { CheckMark } from "../../../ui/CheckMark";
import { inputClass, Segmented, textareaClass } from "../../assistant/parts";
import { ChoiceRows } from "../ProviderFields";
import { Group, RowFailed, settingsInputClass, Switch } from "../SettingsParts";

/** What went wrong, in this client's words for a code it knows, else the source's own (localized) message, else a
 *  generic line. */
export function problemText(p: Problem): string {
  if ((ERROR_CODES as readonly string[]).includes(p.code)) return t(`settings-configuration.error_${p.code}`);
  return p.message || t("settings-configuration.error_failed");
}

/** The form for `source`. A source with another `key` is another resource: the form starts over for it. */
export function ConfigurationForm({ source }: { source: ConfigurationSource }) {
  const [ctl, setCtl] = useState<ConfigurationController | null>(null);
  useEffect(() => {
    const c = new ConfigurationController(source);
    setCtl(c);
    c.start();
    return () => c.dispose();
    // one controller per resource: the same key is the same resource
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source.key]);
  if (!ctl || ctl.source.key !== source.key) return <FormLoading />;
  return <Form ctl={ctl} />;
}

function FormLoading() {
  return (
    <Group>
      <div className="px-4 py-3">
        <Loading />
      </div>
    </Group>
  );
}

function Form({ ctl }: { ctl: ConfigurationController }) {
  const s = useSyncExternalStore(ctl.subscribe, ctl.snapshot);
  if (s.phase === "loading" || !s.doc) {
    if (s.phase !== "failed") return <FormLoading />;
    return (
      <Group>
        <RowFailed text={problemText(s.readError ?? { code: "failed" })} retry={t("settings-configuration.retry")} onRetry={() => ctl.reload()} />
      </Group>
    );
  }
  const doc = s.doc;
  return (
    <section aria-label={doc.title} data-configuration={doc.id}>
      <div className="mb-1.5 px-1">
        <h2 className="text-[12px] font-medium text-ink-3">{doc.title}</h2>
        {doc.description && <p className="mt-0.5 text-[11.5px] leading-relaxed text-ink-3">{doc.description}</p>}
      </div>
      {!s.writable && <Banner tone="caution">{t("settings-configuration.unsupported_version")}</Banner>}
      {s.blocked && (
        <Banner tone="alert" action={<Button size="sm" icon="retry" onClick={() => ctl.reload()}>{t("settings-configuration.reload")}</Button>}>
          {problemText(s.blocked)}
        </Banner>
      )}
      {s.readError && (
        <Banner tone="alert" action={<Button size="sm" icon="retry" onClick={() => ctl.reload()}>{t("settings-configuration.retry")}</Button>}>
          {t("settings-configuration.refresh_failed", { error: problemText(s.readError) })}
        </Banner>
      )}
      {s.formError && <Banner tone="alert">{problemText(s.formError)}</Banner>}
      {doc.groups.map((g) => (
        <Group key={g.id} title={g.title || undefined} footer={g.description} anchor={`configuration-${doc.id}-${g.id}`}>
          {g.fields.map((f) => (
            <FieldRow key={f.id} ctl={ctl} s={s} field={f} />
          ))}
        </Group>
      ))}
    </section>
  );
}

/** A line across the form: a version it can't write, a conflict, a failed refresh. */
function Banner({ tone, action, children }: { tone: "caution" | "alert"; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-card bg-surface px-4 py-2.5 shadow-card">
      <p role={tone === "alert" ? "alert" : undefined} className={tone === "alert" ? "min-w-0 flex-1 text-[12.5px] leading-snug text-red" : "min-w-0 flex-1 text-[12.5px] leading-snug text-orange"}>{children}</p>
      {action}
    </div>
  );
}

/** A value as a line of text (the Default row). */
function valueText(field: KnownField, v: FieldValue): string {
  switch (field.kind) {
    case "toggle":
      return v ? t("settings-configuration.on") : t("settings-configuration.off");
    case "select":
      return field.select.options.find((o) => o.value === v)?.title ?? String(v);
    case "multiselect": {
      const titles = field.multiselect.options.filter((o) => (v as string[]).includes(o.value)).map((o) => o.title);
      return titles.length ? formatList(titles) : t("settings-configuration.none");
    }
    case "text":
      return (v as string) || t("settings-configuration.empty");
    case "number":
      return String(v);
  }
}

function FieldRow({ ctl, s, field }: { ctl: ConfigurationController; s: FormState; field: ConfigurationField }) {
  const titleId = useId();
  const descId = useId();
  const doc = s.doc!;
  const fs: FieldState = s.fields[field.id] ?? {};
  const notice = doc.notices?.[field.id];
  const known = field.kind === "unsupported" ? null : field;
  const value = known ? fieldValue(doc, known) : undefined;
  const can = known ? ctl.canWrite(field.id) : false;
  // typing and checking stay possible while another field is being written (a draft is local); only Save waits
  const editable = !!known && s.writable && !known.read_only && !known.disabled_reason;
  // a multiselect's unsaved checks are what it shows; its pending write once committed
  const checked = known?.kind === "multiselect" && fs.selection !== undefined ? (fs.selection as FieldValue) : undefined;
  const shown = fs.pending !== undefined ? fs.pending : checked !== undefined ? checked : value;
  const inline = known && (known.kind === "toggle" || (known.kind === "select" && (known.select.presentation === "segmented" || known.select.presentation === "dropdown")));
  const selectionDirty = !!known && known.kind === "multiselect" && fs.selection !== undefined && !sameValue(fs.selection, value);
  const resettable = !!known && s.writable && !known.read_only && !known.disabled_reason && !selectionDirty && (value === undefined || !sameValue(value, known.default));

  return (
    <div data-settings-row={`configuration-${doc.id}-${field.id}`} data-configuration-field={field.id} data-kind={field.kind} className="px-4 py-3">
      <div className="flex flex-col gap-x-3 gap-y-2 sm:flex-row sm:items-start">
        <div className="min-w-0 flex-1">
          <div id={titleId} className="text-[13px] text-ink">{field.title}</div>
          {field.description && <p id={descId} className="mt-0.5 text-[11.5px] leading-snug text-ink-3">{field.description}</p>}
        </div>
        {inline && shown !== undefined && <div className="shrink-0"><InlineControl ctl={ctl} field={known!} shown={shown} can={can} titleId={titleId} descId={field.description ? descId : undefined} /></div>}
      </div>
      {known && !inline && shown !== undefined && <div className="mt-2.5"><BlockControl ctl={ctl} field={known} shown={shown} fs={fs} can={can} editable={editable} selectionDirty={selectionDirty} titleId={titleId} descId={field.description ? descId : undefined} /></div>}
      {known && (
        <dl className="mt-2.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[11.5px] leading-snug">
          <Detail term={t("settings-configuration.default")}>{valueText(known, known.default)}</Detail>
          {known.details?.map((d, i) => <Detail key={i} term={d.title}>{d.description}</Detail>)}
        </dl>
      )}
      {!known && field.details?.length ? (
        <dl className="mt-2.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[11.5px] leading-snug">
          {field.details.map((d, i) => <Detail key={i} term={d.title}>{d.description}</Detail>)}
        </dl>
      ) : null}
      {field.kind === "unsupported" && <Note tone="caution">{field.reason === "kind" ? t("settings-configuration.unsupported_kind") : t("settings-configuration.unsupported_definition")}</Note>}
      {known && value === undefined && <Note tone="caution">{t("settings-configuration.invalid_value")}</Note>}
      {notice && <Note tone={notice.kind === "warning" ? "caution" : undefined}>{notice.message}</Note>}
      {known?.disabled_reason ? <Note>{known.disabled_reason}</Note> : known?.read_only ? <Note>{t("settings-configuration.read_only")}</Note> : null}
      <div aria-live="polite" className="empty:hidden">
        {s.busy === field.id && (
          <p className="mt-2 flex items-center gap-1.5 text-[11.5px] text-ink-2"><Spinner />{t("settings-configuration.saving")}</p>
        )}
        {fs.saved && <p className="mt-2 text-[11.5px] leading-snug text-green">{t("settings-configuration.saved")}</p>}
      </div>
      {fs.error && <p role="alert" className="mt-2 text-[11.5px] leading-snug text-red">{problemText(fs.error)}</p>}
      {resettable && (
        <div className="mt-2">
          <Button size="sm" tone="quiet" icon="undo" disabled={!can} onClick={() => ctl.reset(field.id)}>{t("settings-configuration.reset")}</Button>
        </div>
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

function Note({ tone, children }: { tone?: "caution"; children: React.ReactNode }) {
  return <p className={tone ? "mt-2 text-[11.5px] leading-snug text-orange" : "mt-2 text-[11.5px] leading-snug text-ink-3"}>{children}</p>;
}

interface ControlProps {
  ctl: ConfigurationController;
  field: KnownField;
  shown: FieldValue;
  can: boolean;
  titleId: string;
  descId?: string;
}

/** Controls at the end of the title row: a switch, a segmented choice, a dropdown. */
function InlineControl({ ctl, field, shown, can, titleId, descId }: ControlProps) {
  if (field.kind === "toggle") return <Switch on={shown as boolean} onChange={(on) => ctl.choose(field.id, on)} disabled={!can} label={field.title} describedBy={descId} />;
  if (field.kind !== "select") return null;
  if (field.select.presentation === "dropdown") {
    return (
      <select aria-labelledby={titleId} aria-describedby={descId} className={settingsInputClass} value={shown as string} disabled={!can} onChange={(e) => ctl.choose(field.id, e.target.value)}>
        {field.select.options.map((o) => <option key={o.value} value={o.value}>{o.title}</option>)}
      </select>
    );
  }
  return (
    <fieldset disabled={!can} aria-describedby={descId}>
      <Segmented<string> label={field.title} value={shown as string} options={field.select.options.map((o) => ({ v: o.value, label: o.title }))} onChange={(v) => ctl.choose(field.id, v)} />
    </fieldset>
  );
}

/** Controls under the title: a list of choices, a list of checks, a text or number field. */
function BlockControl({ ctl, field, shown, fs, can, editable, selectionDirty, titleId, descId }: ControlProps & { fs: FieldState; editable: boolean; selectionDirty: boolean }) {
  if (field.kind === "select") {
    const options = field.select.options;
    return (
      <fieldset disabled={!can}>
        <ChoiceRows label={field.title} options={options.map((o) => ({ label: o.title, hint: o.description ?? "" }))} at={options.findIndex((o) => o.value === shown)} onPick={(i) => ctl.choose(field.id, options[i].value)} />
      </fieldset>
    );
  }
  if (field.kind === "multiselect") return <Checks ctl={ctl} field={field} chosen={shown as string[]} dirty={selectionDirty} can={can} editable={editable} titleId={titleId} descId={descId} />;
  if (field.kind === "text" || field.kind === "number") return <Typed ctl={ctl} field={field} shown={shown} fs={fs} can={can} editable={editable} titleId={titleId} descId={descId} />;
  return null;
}

function Checks({ ctl, field, chosen, dirty, can, editable, titleId, descId }: { ctl: ConfigurationController; field: Extract<KnownField, { kind: "multiselect" }>; chosen: string[]; dirty: boolean; can: boolean; editable: boolean; titleId: string; descId?: string }) {
  const options = field.multiselect.options;
  // Checking is local until Save: a one-of-one choice moves A → B by unchecking A and checking B, and only the result is
  // written (an invalid one is said before anything is sent).
  const flip = (v: string) => {
    const on = chosen.includes(v);
    // in the options' order, whatever order they were picked in
    ctl.setSelection(field.id, options.map((o) => o.value).filter((x) => (x === v ? !on : chosen.includes(x))));
  };
  return (
    <div>
      <div role="group" aria-labelledby={titleId} aria-describedby={descId} className="divide-y divide-line overflow-hidden rounded-card bg-inset shadow-hairline">
        {options.map((o) => {
          const on = chosen.includes(o.value);
          return (
            <button key={o.value} type="button" role="checkbox" aria-checked={on} disabled={!editable} onClick={() => flip(o.value)}
              className="touch-hit flex w-full items-start gap-2.5 px-3 py-2 text-left transition-colors hover:bg-hover disabled:opacity-60">
              <CheckMark on={on} className="mt-0.5" />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[12.5px] font-medium text-ink">{o.title}</span>
                {o.description && <span className="block text-[11.5px] text-ink-3">{o.description}</span>}
              </span>
            </button>
          );
        })}
      </div>
      {dirty && (
        <div className="mt-1.5 flex flex-wrap items-center gap-2">
          <span className="ml-auto flex shrink-0 items-center gap-2">
            <Button size="sm" tone="quiet" onClick={() => ctl.discard(field.id)}>{t("settings-configuration.cancel")}</Button>
            <Button size="sm" tone="primary" disabled={!can} onClick={() => ctl.saveDraft(field.id)}>{t("settings-configuration.save")}</Button>
          </span>
        </div>
      )}
    </div>
  );
}

function Typed({ ctl, field, shown, fs, can, editable, titleId, descId }: ControlProps & { fs: FieldState; editable: boolean; field: Extract<KnownField, { kind: "text" | "number" }> }) {
  const committed = String(shown);
  const text = fs.draft ?? committed;
  const dirty = fs.draft !== undefined && fs.draft !== committed;
  const multiline = field.kind === "text" && field.text?.multiline;
  const max = field.kind === "text" ? field.text?.max_length : undefined;
  const countId = useId();
  const onKey = (e: React.KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key === "Escape" && dirty) {
      e.preventDefault();
      e.stopPropagation();
      ctl.discard(field.id);
    } else if (e.key === "Enter" && !multiline) {
      e.preventDefault();
      if (dirty) ctl.saveDraft(field.id);
    } else if (field.kind === "number" && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      // the step is a nudge, not a rule: the result is checked like anything typed
      const r = draftValue(field, text);
      const base = "value" in r ? (r.value as number) : field.default;
      const step = field.number?.step ?? 1;
      e.preventDefault();
      ctl.setDraft(field.id, String(base + (e.key === "ArrowUp" ? step : -step)));
    }
  };
  const common = {
    "aria-labelledby": titleId,
    "aria-describedby": [descId, max !== undefined ? countId : undefined].filter(Boolean).join(" ") || undefined,
    "aria-invalid": fs.error ? true : undefined,
    value: text,
    disabled: !editable,
    onKeyDown: onKey,
    placeholder: field.kind === "text" ? field.text?.placeholder : undefined,
    spellCheck: false,
    autoComplete: "off",
  };
  return (
    <div>
      {multiline ? (
        <textarea {...common} rows={3} className={textareaClass} onChange={(e) => ctl.setDraft(field.id, e.target.value)} />
      ) : (
        <input {...common} inputMode={field.kind === "number" ? (field.number?.integer ? "numeric" : "decimal") : undefined} className={inputClass} onChange={(e) => ctl.setDraft(field.id, e.target.value)} />
      )}
      <div className="mt-1.5 flex flex-wrap items-center gap-2">
        {max !== undefined && <span id={countId} className="min-w-0 flex-1 text-[11px] text-ink-3 tabular-nums">{t("settings-configuration.count", { n: scalarLength(text), max })}</span>}
        {dirty && (
          <span className="ml-auto flex shrink-0 items-center gap-2">
            <Button size="sm" tone="quiet" onClick={() => ctl.discard(field.id)}>{t("settings-configuration.cancel")}</Button>
            <Button size="sm" tone="primary" disabled={!can} onClick={() => ctl.saveDraft(field.id)}>{t("settings-configuration.save")}</Button>
          </span>
        )}
      </div>
    </div>
  );
}
