// Above the composer while the host stopped the session's turn on an error it will not retry by itself (a botmux
// notice, iOS FailureNote): the reason, "Retry" (sends "Continue" alone, not what is in the box) and "Open in Lark". The
// same error notice as the model error banner (#5313), so one card shape says what stopped a turn.
import { externalLink, failureReason } from "../../lib/format";
import type { Session } from "../../lib/types";
import { Button } from "../../ui/atoms";
import { ErrorNotice } from "../../ui/ErrorNotice";
import { t } from "../../i18n";

export function FailureNote({ session, onRetry }: { session: Session; onRetry: () => void }) {
  const reason = failureReason(session);
  if (!reason) return null;
  const link = externalLink(session);
  return (
    <ErrorNotice
      role="status"
      data-testid="chat-failure"
      message={reason}
      actions={
        <>
          <Button icon="retry" size="sm" className="touch-hit" testId="chat-failure-retry" title={t("chat-lines.retry_title")} onClick={onRetry}>
            {t("chat-chatview.retry")}
          </Button>
          {link && (
            <Button icon="external" size="sm" className="touch-hit" testId="chat-failure-lark" href={link}>
              {t("chat-chatview.open_in_lark")}
            </Button>
          )}
        </>
      }
    />
  );
}
