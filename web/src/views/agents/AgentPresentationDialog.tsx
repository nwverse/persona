// Presentation-only agent naming and tags. Loaded as a lazy overlay so the editor stays out of the Agents page's chunk.
import { useEffect, useId, useRef, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { composing } from "../../lib/send-history";
import type { Session } from "../../lib/types";
import { Button, Dialog, IconButton } from "../../ui/atoms";

const NAME_MAX = 120;
const TAG_MAX = 32;
const TAGS_MAX = 12;

export interface AgentPresentationInput {
  display_name: string;
  tags: string[];
}

export interface AgentPresentationDialogProps {
  session: Session;
  onClose(): void;
  onSave(input: AgentPresentationInput): Promise<void>;
}

// The shared Session type gains these wire fields with the hub/session-list wiring. Keeping the local view of them here
// lets this independently loaded editor land without coupling it to that change.
type PresentationSession = Session & Partial<AgentPresentationInput>;

const presentation = (session: Session) => session as PresentationSession;
const tagKey = (tag: string) => tag.toLocaleLowerCase();
const parseTags = (value: string) => value.split(/[,\r\n]+/).map((tag) => tag.trim()).filter(Boolean);
const nameDraft = (value: string) => [...value.replace(/[\r\n]+/g, " ")].slice(0, NAME_MAX).join("");

export function AgentPresentationDialog({ session, onClose, onSave }: AgentPresentationDialogProps) {
  const current = presentation(session);
  const [name, setName] = useState(() => nameDraft(current.display_name ?? ""));
  const [tags, setTags] = useState<string[]>(() => [...(current.tags ?? [])]);
  const [tagDraft, setTagDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const saving = useRef(false);
  const nameId = useId();
  const nameHintId = useId();
  const tagsId = useId();
  const tagsHintId = useId();

  useEffect(() => {
    const initial = presentation(session);
    setName(nameDraft(initial.display_name ?? ""));
    setTags([...(initial.tags ?? [])]);
    setTagDraft("");
    setError("");
    saving.current = false;
    setBusy(false);
    // A session push while editing must not overwrite the drafts; a different target starts fresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.machine_id, session.session_id]);

  const close = () => {
    if (!saving.current) onClose();
  };

  const addTags = () => {
    if (saving.current) return;
    const additions = parseTags(tagDraft);
    if (!additions.length) return;
    const next = [...tags];
    const seen = new Set(next.map(tagKey));
    for (const tag of additions) {
      const key = tagKey(tag);
      if (seen.has(key)) continue;
      if ([...tag].length > TAG_MAX) {
        setError(t("agent-presentation.tag_too_long", { max: TAG_MAX }));
        return;
      }
      if (next.length >= TAGS_MAX) {
        setError(t("agent-presentation.too_many_tags", { max: TAGS_MAX }));
        return;
      }
      seen.add(key);
      next.push(tag);
    }
    setTags(next);
    setTagDraft("");
    setError("");
  };

  const save = async () => {
    if (saving.current) return;
    saving.current = true;
    setBusy(true);
    setError("");
    try {
      await onSave({ display_name: name.trim(), tags: [...tags] });
      onClose();
    } catch (e) {
      setError(t("agent-presentation.save_failed", { error: errorText(e) }));
    } finally {
      saving.current = false;
      setBusy(false);
    }
  };

  const customNameActive = Boolean(current.display_name?.trim());
  const fieldClass = "w-full rounded-control bg-surface px-3 text-sm text-ink shadow-btn outline-none placeholder:text-ink-3 focus:shadow-[inset_0_0_0_1px_var(--accent)] disabled:opacity-60";
  const actionClass = "touch-hit pointer-coarse:min-h-11";

  return (
    <Dialog
      open
      onClose={close}
      title={t("agent-presentation.title")}
      width={460}
      footer={
        <>
          <Button tone="quiet" className={actionClass} disabled={busy} onClick={close}>
            {t("common.cancel")}
          </Button>
          <Button tone="primary" className={actionClass} busy={busy} onClick={() => void save()}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div className="flex flex-col gap-1.5">
          <label htmlFor={nameId} className="text-xs font-medium text-ink-2">
            {t("agent-presentation.display_name")}
          </label>
          <input
            id={nameId}
            type="text"
            autoFocus
            value={name}
            maxLength={NAME_MAX * 2}
            disabled={busy}
            onChange={(e) => {
              setName(nameDraft(e.target.value));
              setError("");
            }}
            placeholder={t("agent-presentation.name_placeholder")}
            aria-describedby={nameHintId}
            className={`${fieldClass} h-9 pointer-coarse:min-h-11`}
          />
          <div id={nameHintId} className="flex flex-col gap-1 text-xs leading-relaxed text-ink-3">
            <p>{t("agent-presentation.name_hint", { max: NAME_MAX })}</p>
            <p>
              {customNameActive
                ? t("agent-presentation.title_context_custom", { title: session.title })
                : t("agent-presentation.title_context", { title: session.title })}
            </p>
          </div>
        </div>

        <div className="flex flex-col gap-1.5">
          <label htmlFor={tagsId} className="text-xs font-medium text-ink-2">
            {t("agent-presentation.tags")}
          </label>
          {tags.length > 0 && (
            <ul className="flex flex-wrap gap-1.5" aria-label={t("agent-presentation.tags")}>
              {tags.map((tag, index) => (
                <li
                  key={`${tagKey(tag)}-${index}`}
                  data-agent-tag={tag}
                  className="inline-flex max-w-full items-center gap-1 rounded-full bg-inset py-0.5 pr-1 pl-2.5 text-xs font-medium text-ink shadow-hairline"
                >
                  <span className="min-w-0 break-all">{tag}</span>
                  <IconButton
                    icon="x"
                    size={20}
                    iconSize={11}
                    disabled={busy}
                    label={t("agent-presentation.remove_tag", { tag })}
                    onClick={() => {
                      setTags((currentTags) => currentTags.filter((_, i) => i !== index));
                      setError("");
                    }}
                  />
                </li>
              ))}
            </ul>
          )}
          <textarea
            id={tagsId}
            rows={2}
            value={tagDraft}
            disabled={busy}
            onChange={(e) => {
              setTagDraft(e.target.value);
              setError("");
            }}
            onKeyDown={(e) => {
              if (e.key !== "Enter" || e.shiftKey || composing(e.nativeEvent)) return;
              e.preventDefault();
              addTags();
            }}
            placeholder={t("agent-presentation.tags_placeholder")}
            aria-describedby={tagsHintId}
            className={`${fieldClass} min-h-18 resize-y py-2 leading-relaxed`}
          />
          <div className="flex items-start justify-between gap-3">
            <p id={tagsHintId} className="text-xs leading-relaxed text-ink-3">
              {t("agent-presentation.tags_hint", { maxTags: TAGS_MAX, maxChars: TAG_MAX })}
            </p>
            <Button tone="secondary" className={actionClass} disabled={busy || !tagDraft.trim()} onClick={addTags}>
              {t("agent-presentation.add_tags")}
            </Button>
          </div>
        </div>
        {error && (
          <p role="alert" className="text-sm leading-relaxed text-red">
            {error}
          </p>
        )}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
