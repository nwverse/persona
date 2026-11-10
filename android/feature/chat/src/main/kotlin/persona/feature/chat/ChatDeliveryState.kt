package persona.feature.chat

import androidx.compose.ui.res.stringResource
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.resultUnknown
import persona.core.designsystem.PendingStatus

internal val DeliveryState.pendingStatus: PendingStatus?
    get() = when (this) {
        DeliveryState.Queued, DeliveryState.WaitingForNetwork -> PendingStatus.Waiting
        DeliveryState.Sending -> PendingStatus.Sending
        DeliveryState.Unknown -> PendingStatus.Unconfirmed
        DeliveryState.Verifying -> PendingStatus.Verifying
        DeliveryState.NeedsConfirmation -> PendingStatus.NeedsReview
        DeliveryState.Rejected, DeliveryState.Refused -> PendingStatus.Refused
        DeliveryState.Sent -> null
    }

internal enum class DeliveryAction { Cancel, Edit, Retry, Delete, Discard, ConfirmDelivered, ConfirmNotDelivered }

/**
 * Cancel and edit are for what has not gone out yet; a refused message (refused by the computer, or confirmed as never
 * delivered) reached nothing either, so it can be edited (back into the box, the bubble gone) or deleted outright.
 * One whose delivery is unknown can be discarded: the phone stops asking and retrying, whatever the computer got.
 */
internal fun DeliveryState.allows(action: DeliveryAction): Boolean = when (action) {
    DeliveryAction.Cancel -> this == DeliveryState.Queued || this == DeliveryState.WaitingForNetwork
    DeliveryAction.Edit -> this == DeliveryState.Queued || this == DeliveryState.WaitingForNetwork || this == DeliveryState.Refused
    DeliveryAction.Delete -> this == DeliveryState.Refused
    DeliveryAction.Retry -> this == DeliveryState.Refused || this == DeliveryState.Rejected || this == DeliveryState.NeedsConfirmation
    DeliveryAction.Discard, DeliveryAction.ConfirmDelivered, DeliveryAction.ConfirmNotDelivered -> resultUnknown
}

/** Both the timeline and the sheet share the same in-flight guard and authoritative transitions. */
internal class ChatDeliveryActions(private val sender: MessageSender) {
    var busyIds by mutableStateOf(emptySet<String>())
        private set

    suspend fun perform(id: String, action: DeliveryAction, restore: (String) -> Unit): Boolean {
        if (id in busyIds) return false
        val message = sender.outbox.value.firstOrNull { it.clientMessageId == id } ?: return false
        if (!message.state.allows(action)) return false
        busyIds = busyIds + id
        try {
            val changed = when (action) {
                DeliveryAction.Cancel, DeliveryAction.Edit, DeliveryAction.Delete -> sender.cancel(id)
                DeliveryAction.Retry -> sender.retry(id)
                DeliveryAction.Discard -> sender.discard(id)
                DeliveryAction.ConfirmDelivered -> sender.confirm(id, delivered = true)
                DeliveryAction.ConfirmNotDelivered -> sender.confirm(id, delivered = false)
            }
            if (changed && action in setOf(DeliveryAction.Edit, DeliveryAction.ConfirmNotDelivered)) restore(message.text)
            return changed
        } finally {
            busyIds = busyIds - id
        }
    }
}

/** Why the computer refused the message, by the hub's code; the hub's own words for a code it does not know. */
@androidx.compose.runtime.Composable
internal fun OutboxMessage.refusalReason(): String = when (error) {
    "input_draft" -> stringResource(R.string.chat_delivery_input_draft)
    "dialog_open" -> stringResource(R.string.chat_delivery_dialog_open)
    "op_id_reused" -> stringResource(R.string.chat_delivery_op_id_reused)
    "invalid" -> stringResource(R.string.chat_delivery_invalid)
    // The provider does not run on this computer (docs/integrations/launchers.md "Device scope and on-demand delivery").
    "launcher_out_of_scope", "launcher_own_service" -> stringResource(R.string.chat_delivery_out_of_scope)
    else -> error?.takeIf(String::isNotBlank) ?: stringResource(R.string.chat_delivery_refused)
}
