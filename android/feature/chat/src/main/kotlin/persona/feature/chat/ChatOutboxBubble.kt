package persona.feature.chat

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import persona.core.designsystem.JAction
import persona.core.designsystem.JIcon
import persona.core.designsystem.JMenu
import persona.core.designsystem.PendingStatus
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.lines.ChatLine

/**
 * The mark before an unsent message's bubble on Glass (iOS `PendingBubble`): a quiet clock while it sends; a clock with a
 * small menu while it waits (queued, or for the network); a red mark with a small menu once it did not go (refused, or
 * turned down). A delivery the phone has to check keeps Android's status line, and a delivered one has no mark.
 */
internal enum class OutboxMark { Sending, Waiting, Failed }

internal fun outboxMark(state: DeliveryState): OutboxMark? = when (state) {
    DeliveryState.Sending -> OutboxMark.Sending
    DeliveryState.Queued, DeliveryState.WaitingForNetwork -> OutboxMark.Waiting
    DeliveryState.Refused, DeliveryState.Rejected -> OutboxMark.Failed
    DeliveryState.Sent, DeliveryState.Unknown, DeliveryState.Verifying, DeliveryState.NeedsConfirmation -> null
}

/** Glass draws [state] as iOS's bubble: one with a [OutboxMark], or one delivered and waiting for the chat history. */
internal fun drawsAsPendingBubble(state: DeliveryState): Boolean = state == DeliveryState.Sent || outboxMark(state) != null

/** The mark's menu, in iOS's order: Retry now and Don't send while it waits, Resend and Delete once it failed. */
internal fun outboxMarkActions(mark: OutboxMark): List<DeliveryAction> = when (mark) {
    OutboxMark.Sending -> emptyList()
    OutboxMark.Waiting -> listOf(DeliveryAction.RetryNow, DeliveryAction.Cancel)
    OutboxMark.Failed -> listOf(DeliveryAction.Retry, DeliveryAction.Delete)
}

/** Where the unsent messages go: [before] by the id of the line each goes before, the [rest] at the chat's newest end. */
internal data class PlacedOutbox(val before: Map<String, List<OutboxMessage>>, val rest: List<OutboxMessage>)

/**
 * Where unsent messages go on Glass (iOS `ChatLines.placeFailed`): one that did not go with a message of yours after it in
 * the chat goes right before the first such message, at the end of the exchange it was written in, so the chat reads in
 * the order things were written. One with nothing after it, one without a time and every one still on its way stay at the
 * newest end, in the order they were written. A message in the chat goes by the computer's time less two minutes for its
 * clock, so a near tie leaves the failed one below (iOS goes by this phone's own time for what it sent, which Android's
 * outbox does not keep once a message is in the chat). [lines] in reading order.
 */
internal fun placeFailed(lines: List<ChatLine>, pending: List<OutboxMessage>): PlacedOutbox {
    if (pending.none { outboxMark(it.state) == OutboxMark.Failed && it.createdAt != null }) return PlacedOutbox(emptyMap(), pending)
    val after = lines.mapNotNull { line -> placeTime(line)?.let { line.id to it } }
    val before = linkedMapOf<String, MutableList<OutboxMessage>>()
    val rest = mutableListOf<OutboxMessage>()
    for (message in pending) {
        val at = message.createdAt
        val next = if (outboxMark(message.state) == OutboxMark.Failed && at != null) after.firstOrNull { it.second > at } else null
        if (next == null) rest += message else before.getOrPut(next.first) { mutableListOf() } += message
    }
    return PlacedOutbox(before, rest)
}

/** When a line was written, for [placeFailed]: a message the user sent, by the computer's time less two minutes; null for others. */
private fun placeTime(line: ChatLine): Long? {
    val row = (line as? ChatLine.Row)?.row?.takeIf { it.type == "user" } ?: return null
    return row.item?.time?.minusSeconds(CLOCK_SLACK_SECONDS)?.toEpochMilli()
}

private const val CLOCK_SLACK_SECONDS = 120L

/**
 * An unsent message on Glass, as iOS's `PendingBubble`: its [OutboxMark] before the bubble, a tap on it opens the small menu
 * ([outboxMarkActions], also TalkBack's actions); the bubble faded while it waits or after it failed, "Sends when connected"
 * under it while it waits. Its long press is the bubble's own (Copy, Select, Share, then Don't send or Delete). [onAction]
 * null: a read-only chat, the mark has no menu.
 */
@Composable
internal fun OutboxBubble(text: String, id: String, state: DeliveryState, busy: Boolean, onAction: ((DeliveryAction) -> Unit)?) {
    val mark = outboxMark(state)
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        UserBubble(text, id, state, faded = mark == OutboxMark.Waiting || mark == OutboxMark.Failed,
            leading = mark?.let { { OutboxMarkButton(it, busy, onAction) } })
        if (mark == OutboxMark.Waiting) Text(PendingStatus.Waiting.localizedLabel, style = PersonaTheme.type.caption,
            color = PersonaTheme.palette.ink2, maxLines = 1)
    }
}

@Composable
private fun OutboxMarkButton(mark: OutboxMark, busy: Boolean, onAction: ((DeliveryAction) -> Unit)?) {
    val palette = PersonaTheme.palette
    val res = LocalResources.current
    var open by remember { mutableStateOf(false) }
    val actions = if (onAction == null || busy) emptyList() else outboxMarkActions(mark)
    fun label(action: DeliveryAction) = res.getString(when (action) {
        DeliveryAction.RetryNow -> R.string.chat_outbox_retry_now
        DeliveryAction.Cancel -> R.string.chat_outbox_dont_send
        DeliveryAction.Retry -> R.string.chat_outbox_resend
        else -> R.string.chat_delivery_delete
    })
    val spoken = when (mark) {
        OutboxMark.Sending -> PendingStatus.Sending.localizedLabel
        OutboxMark.Waiting -> stringResource(R.string.chat_outbox_waiting_spoken)
        OutboxMark.Failed -> stringResource(R.string.chat_outbox_failed_spoken)
    }
    Box(
        Modifier.size(Tokens.MinTouch.android).clip(CircleShape)
            .then(if (actions.isEmpty()) Modifier else Modifier.clickable(role = Role.Button) { open = true })
            .semantics {
                contentDescription = spoken
                if (actions.isNotEmpty()) customActions = actions.map { action ->
                    CustomAccessibilityAction(label(action)) { onAction?.invoke(action); true }
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        when (mark) {
            OutboxMark.Sending -> JIcon(PersonaIcons.time, null, size = Tokens.Size.markS, tint = palette.ink3, decorative = true)
            OutboxMark.Waiting -> JIcon(PersonaIcons.time, null, size = Tokens.Size.markS, tint = palette.ink2, decorative = true)
            OutboxMark.Failed -> JIcon(PersonaIcons.errorFilled, null, size = Tokens.Size.iconXS, tint = palette.danger.dot, decorative = true)
        }
        JMenu(open, { open = false }, actions.map { action ->
            JAction(label(action), if (action == DeliveryAction.Cancel || action == DeliveryAction.Delete) PersonaIcons.delete else PersonaIcons.retry,
                destructive = action == DeliveryAction.Cancel || action == DeliveryAction.Delete) { onAction?.invoke(action) }
        })
    }
}
