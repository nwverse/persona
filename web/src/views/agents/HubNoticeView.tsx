import { t } from "../../i18n";
import { hubNoticeKey, hubNoticeText, type HubNotice } from "../../lib/hub-update";
import { Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export default function HubNoticeView({ notice, onOpen, onAutoOff, onSeen, onUpdate, onDismiss }: {
  notice: HubNotice;
  onOpen: () => void;
  onAutoOff: () => void;
  onSeen: () => void;
  onUpdate: () => void;
  onDismiss: (key: string) => void;
}) {
  const warn = notice.kind === "rolled_back";
  const key = hubNoticeKey(notice);
  const action = "shrink-0 whitespace-nowrap rounded-control px-2 py-0.5 text-label [@media(pointer:coarse)]:min-h-11 font-medium text-accent-ink hover:bg-hover";
  return (
    <div data-testid="hub-notice" data-kind={notice.kind} className="relative z-10 ms-(--sidebar-indent) flex flex-wrap items-center gap-x-1 rounded-control border-s border-line ps-2 pe-1 py-1 hover:bg-hover [@media(pointer:coarse)]:min-h-11">
      <button type="button" onClick={onOpen} title={t("lib-hub-update.notice_open")} className="flex min-w-0 grow basis-24 items-start gap-2 rounded-control text-left [@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:items-center" aria-live="polite">
        <span className={`mt-[3px] flex size-3.5 shrink-0 items-center justify-center ${warn ? "text-orange" : "text-accent-ink"}`}>
          {notice.kind === "updating" ? <Spinner size={11} /> : <Icon name={warn ? "warning" : "retry"} size={13} stroke={2.2} />}
        </span>
        <span className="min-w-0 flex-1 text-meta leading-snug wrap-anywhere text-ink-2">{hubNoticeText(notice)}</span>
      </button>
      {notice.kind === "auto_on" && (
        <>
          <button type="button" data-testid="hub-notice-auto-off" onClick={onAutoOff} className={action}>{t("lib-hub-update.notice_turn_off")}</button>
          <button type="button" data-testid="hub-notice-seen" onClick={onSeen} className={action}>{t("lib-hub-update.notice_got_it")}</button>
        </>
      )}
      {notice.kind === "available" && (
        <button type="button" data-testid="hub-notice-update" onClick={onUpdate} className={action}>{t("lib-hub-update.notice_update")}</button>
      )}
      {key && (
        <button type="button" data-testid="hub-notice-dismiss" aria-label={t("lib-hub-update.notice_dismiss")} title={t("lib-hub-update.notice_dismiss")} onClick={() => onDismiss(key)} className="shrink-0 rounded-control p-1 [@media(pointer:coarse)]:size-11 inline-grid place-items-center text-ink-3 hover:bg-hover hover:text-ink-2">
          <Icon name="x" size={12} stroke={2} />
        </button>
      )}
    </div>
  );
}
