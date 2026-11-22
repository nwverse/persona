import { useId, useMemo, useState } from "react";
import { t } from "../../i18n";
import { useComposition } from "../../lib/ime";
import { parseSavedPromptTemplate, renderSavedPromptTemplate, VALUE_MAX } from "../../lib/saved-prompt-template";
import { hasFinePointer } from "../../lib/viewport";
import { Button, Dialog } from "../../ui/atoms";

type Props = { source: string; onClose: () => void; onInsert: (text: string) => void };

export function SavedPromptTemplateDialog({ source, onClose, onInsert }: Props) {
  const id = useId();
  const ime = useComposition();
  const template = useMemo(() => parseSavedPromptTemplate(source), [source]);
  const [values, setValues] = useState<ReadonlyMap<string, string>>(() => new Map());
  const rendered = renderSavedPromptTemplate(template, values);
  const incomplete = template.fields.some((field) => !values.get(field)?.trim());
  const insert = () => {
    if (!incomplete && !ime.tracker.current.active && rendered.text !== undefined) onInsert(rendered.text);
  };

  return <Dialog open title={t("prompt-template.fill_title")} describedBy={template.problem ? undefined : id} onClose={onClose}
    footer={<>
      <Button onClick={onClose}>{t("common.cancel")}</Button>
      <Button tone="primary" disabled={incomplete || !!rendered.problem || ime.composing} onClick={insert}>{t("prompt-template.insert")}</Button>
    </>}
  >
    <div className="min-w-0 space-y-3" onKeyDown={(event) => {
      if (event.key === "Escape" && ime.busy(event.nativeEvent)) event.stopPropagation();
    }}>
      {!template.problem && <p id={id} className="text-sm text-ink-2">{t("prompt-template.fill_hint")}</p>}
      {template.fields.map((field, index) => {
        const fieldID = id + "-" + index;
        return <div key={field}>
          <label htmlFor={fieldID} className="mb-1 block break-words text-sm font-medium text-ink">{field}</label>
          <textarea id={fieldID} rows={2} maxLength={VALUE_MAX * 2} autoFocus={index === 0 && hasFinePointer()} required
            className="w-full resize-y rounded-control bg-field px-3 py-2 text-sm text-ink outline-none focus:ring-2 focus:ring-accent"
            value={values.get(field) ?? ""} aria-invalid={rendered.field === field || undefined} spellCheck={false}
            onBlur={ime.onBlur} {...ime.handlers}
            onChange={(event) => {
              const value = event.target.value;
              setValues((previous) => new Map(previous).set(field, value));
            }} />
        </div>;
      })}
      {rendered.problem && <p role="alert" className="text-sm text-red">{t("prompt-template." + rendered.problem, { field: rendered.field ?? "" })}</p>}
    </div>
  </Dialog>;
}
