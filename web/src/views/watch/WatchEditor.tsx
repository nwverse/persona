// Editing a watch card from the card's ⋯ (docs/assistant/watch-cards.md): its name, the question each run asks, how
// often it refreshes, how it shows the answer, when it pushes and which project it belongs to. Saved with one PUT;
// the card's scheduled task follows on the hub.
import { useId, useState } from "react";
import { errorText } from "../../lib/api";
import { scheduleText, updateWatch, type Watch, type WatchAlert, type WatchDisplay } from "../../lib/watches";
import { t } from "../../i18n";
import { Button, Dialog } from "../../ui/atoms";

const DISPLAYS: WatchDisplay[] = ["number", "status", "table", "trend"];
type AlertWhen = "" | WatchAlert["when"];

export function WatchEditor({ w, onClose, onSaved }: { w: Watch; onClose: () => void; onSaved: (w: Watch) => void }) {
  const id = useId();
  const [title, setTitle] = useState(w.title);
  const [question, setQuestion] = useState(w.question);
  const [schedule, setSchedule] = useState(scheduleText(w));
  const [display, setDisplay] = useState<WatchDisplay>(w.display);
  const [unit, setUnit] = useState(w.unit ?? "");
  const [when, setWhen] = useState<AlertWhen>(w.alert?.when ?? "");
  const [value, setValue] = useState(w.alert?.value !== undefined ? String(w.alert.value) : "");
  const [project, setProject] = useState(w.project ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const needsValue = when === "above" || when === "below";
  const invalid = !question.trim() || (needsValue && !Number.isFinite(Number(value)) || (needsValue && value.trim() === ""));

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      const alert: WatchAlert | null = when ? (needsValue ? { when, value: Number(value) } : { when }) : null;
      onSaved(await updateWatch(w.id, { title, question, schedule, display, unit, alert, project }));
      onClose();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const box = "h-8 min-w-0 rounded-control bg-field px-2.5 text-meta text-ink outline-none focus-visible:ring-2 focus-visible:ring-accent";
  const field = `${box} w-full`;
  const label = "text-label text-ink-3";
  return (
    <Dialog
      open
      onClose={onClose}
      title={t("watch.edit_title")}
      footer={
        <>
          <Button onClick={onClose}>{t("common.cancel")}</Button>
          <Button tone="primary" busy={busy} disabled={invalid || busy} onClick={() => void save()}>{t("common.save")}</Button>
        </>
      }
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[112px_1fr] sm:items-center">
        <label htmlFor={`${id}-title`} className={label}>{t("watch.field_title")}</label>
        <input id={`${id}-title`} className={field} value={title} maxLength={60} onChange={(e) => setTitle(e.target.value)} />
        <label htmlFor={`${id}-q`} className={label}>{t("watch.field_question")}</label>
        <textarea id={`${id}-q`} className={`${field} h-20 py-1.5`} value={question} maxLength={500} onChange={(e) => setQuestion(e.target.value)} />
        <label htmlFor={`${id}-s`} className={label}>{t("watch.field_refresh")}</label>
        <div>
          <input id={`${id}-s`} className={field} value={schedule} onChange={(e) => setSchedule(e.target.value)} aria-describedby={`${id}-s-hint`} />
          <p id={`${id}-s-hint`} className="mt-1 text-submeta text-ink-3">{t("watch.schedule_hint")}</p>
        </div>
        <label htmlFor={`${id}-d`} className={label}>{t("watch.field_display")}</label>
        <select id={`${id}-d`} className={field} value={display} onChange={(e) => setDisplay(e.target.value as WatchDisplay)}>
          {DISPLAYS.map((d) => <option key={d} value={d}>{t(`watch.display_${d}`)}</option>)}
        </select>
        <label htmlFor={`${id}-u`} className={label}>{t("watch.field_unit")}</label>
        <input id={`${id}-u`} className={field} value={unit} maxLength={12} onChange={(e) => setUnit(e.target.value)} />
        <label htmlFor={`${id}-a`} className={label}>{t("watch.field_alert")}</label>
        <div className="flex gap-2">
          <select id={`${id}-a`} className={`${box} flex-1`} value={when} onChange={(e) => setWhen(e.target.value as AlertWhen)}>
            <option value="">{t("watch.alert_never")}</option>
            <option value="above">{t("watch.alert_pick_above")}</option>
            <option value="below">{t("watch.alert_pick_below")}</option>
            <option value="bad">{t("watch.alert_bad")}</option>
            <option value="change">{t("watch.alert_change")}</option>
          </select>
          {needsValue && <input aria-label={t("watch.alert_value")} inputMode="decimal" className={`${box} w-24 shrink-0`} value={value} onChange={(e) => setValue(e.target.value)} />}
        </div>
        <label htmlFor={`${id}-p`} className={label}>{t("watch.field_project")}</label>
        <div>
          <input id={`${id}-p`} className={field} value={project} placeholder={t("watch.project_none")} onChange={(e) => setProject(e.target.value)} />
          <p className="mt-1 text-submeta text-ink-3">{t("watch.project_hint")}</p>
        </div>
      </div>
      {error && <p role="alert" className="mt-3 text-meta text-red">{error}</p>}
    </Dialog>
  );
}
