package persona.feature.inbox

import androidx.annotation.StringRes
import persona.core.contracts.generated.InboxStoredSources

/** Whether an existing source entry or empty-state mention stays visible (#17399). Unknown means visible. */
data class InboxSourceVisibility(val lark: Boolean, val mail: Boolean)

fun inboxSourceVisibility(larkOffered: Boolean, mailOffered: Boolean, stored: InboxStoredSources?): InboxSourceVisibility =
    InboxSourceVisibility(
        lark = larkOffered || (stored?.lark ?: true),
        mail = mailOffered || (stored?.mail ?: true),
    )

/** The zero-record setup sentence. The original both-sources text is unchanged when both remain visible. */
@StringRes
fun inboxEmptyText(visibility: InboxSourceVisibility): Int = when {
    visibility.lark && visibility.mail -> R.string.inbox_empty_text
    visibility.lark -> R.string.inbox_empty_text_lark
    visibility.mail -> R.string.inbox_empty_text_mail
    else -> R.string.inbox_empty_text_generic
}
