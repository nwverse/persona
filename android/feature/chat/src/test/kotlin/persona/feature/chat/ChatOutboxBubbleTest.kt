package persona.feature.chat

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.ChatRow
import persona.core.domain.lines.LineItem
import java.time.Instant

/**
 * An unsent message on Glass as iOS's `PendingBubble` (#4339 C9): its mark and small menu by state, the long press's last
 * word, and where one that did not go sits among the messages sent after it (iOS `ChatLines.placeFailed`).
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class ChatOutboxBubbleTest {
    @get:Rule val rule = createComposeRule()
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private val key = SessionKey("m", "s")
    private val start = Instant.parse("2026-11-12T01:00:00Z")

    @Test fun eachStateHasIOSsMarkAndMenu() {
        assertEquals(OutboxMark.Sending, outboxMark(DeliveryState.Sending))
        assertEquals(OutboxMark.Waiting, outboxMark(DeliveryState.Queued))
        assertEquals(OutboxMark.Waiting, outboxMark(DeliveryState.WaitingForNetwork))
        assertEquals(OutboxMark.Failed, outboxMark(DeliveryState.Refused))
        assertEquals(OutboxMark.Failed, outboxMark(DeliveryState.Rejected))
        // What the phone has to check keeps Android's status line; a delivered one has no mark and no line.
        for (state in listOf(DeliveryState.Unknown, DeliveryState.Verifying, DeliveryState.NeedsConfirmation)) {
            assertNull("$state", outboxMark(state))
            assertFalse("$state", drawsAsPendingBubble(state))
        }
        assertNull(outboxMark(DeliveryState.Sent))
        assertTrue(drawsAsPendingBubble(DeliveryState.Sent))
        assertEquals(listOf(DeliveryAction.RetryNow, DeliveryAction.Cancel), outboxMarkActions(OutboxMark.Waiting))
        assertEquals(listOf(DeliveryAction.Retry, DeliveryAction.Delete), outboxMarkActions(OutboxMark.Failed))
        assertTrue(outboxMarkActions(OutboxMark.Sending).isEmpty())
        // Every action of a mark's menu is one its state allows.
        for (state in DeliveryState.entries) outboxMark(state)?.let { mark ->
            outboxMarkActions(mark).forEach { assertTrue("$state $it", state.allows(it)) }
        }
        assertTrue(DeliveryState.Queued.allows(DeliveryAction.RetryNow))
        assertFalse(DeliveryState.Refused.allows(DeliveryAction.RetryNow))
    }

    /** The long press ends in the mark's own word on Glass ("Don't send" while it waits), the status line's elsewhere. */
    @Test fun theLongPressSaysDontSendOnGlass() {
        val waiting = MessageTarget("m1", "words", true, "words", DeliveryState.WaitingForNetwork)
        fun last(extras: MessageExtras, target: MessageTarget = waiting) =
            messageMenuActions(target, writes = true, handOff = false, res, extras) {}.last().label
        assertEquals("Don't send", last(MessageExtras(delete = true, dontSend = true)))
        assertEquals("Cancel send", last(MessageExtras(delete = true)))
        assertEquals("Delete", last(MessageExtras(delete = true, dontSend = true), waiting.copy(pending = DeliveryState.Refused)))
    }

    private fun user(seq: Int, minutes: Long) = ChatLine.Row(ChatRow("user",
        LineItem(Item("u$seq", seq, role = "user", kind = "text", text = "message $seq", ts = start.plusSeconds(minutes * 60).toString()))))
    private fun reply(seq: Int) = ChatLine.Row(ChatRow("reply", LineItem(Item("a$seq", seq, kind = "text", text = "answer $seq"))))
    private fun unsent(id: String, state: DeliveryState, minutes: Long?) =
        OutboxMessage(id, key, id, state, createdAt = minutes?.let { start.plusSeconds(it * 60).toEpochMilli() })

    @Test fun aFailedMessageKeepsItsPlace() {
        val lines = listOf(user(1, 0), reply(2), user(3, 10), reply(4), user(5, 20))
        val failed = unsent("failed", DeliveryState.Refused, 5)
        val waiting = unsent("waiting", DeliveryState.WaitingForNetwork, 6)
        val placed = placeFailed(lines, listOf(failed, waiting))
        // Before the first message of yours written after it, at the end of its own exchange; one still going stays at the end.
        assertEquals(mapOf("u3" to listOf(failed)), placed.before)
        assertEquals(listOf(waiting), placed.rest)
        // Two failed in a row go before the same line, in the order written.
        val second = unsent("second", DeliveryState.Rejected, 7)
        assertEquals(mapOf("u3" to listOf(failed, second)), placeFailed(lines, listOf(failed, second)).before)
        // Nothing written after it, no time, or a near tie (inside the two minutes for the computer's clock): at the end.
        for (message in listOf(unsent("late", DeliveryState.Refused, 25), unsent("untimed", DeliveryState.Refused, null),
            unsent("tie", DeliveryState.Refused, 19))) {
            assertEquals(message.clientMessageId, PlacedOutbox(emptyMap(), listOf(message)), placeFailed(lines, listOf(message)))
        }
        // No failed message: all stay where they were.
        assertEquals(PlacedOutbox(emptyMap(), listOf(waiting)), placeFailed(lines, listOf(waiting)))
    }

    /** The waiting mark's menu: Retry now, then Don't send; also TalkBack's actions on the mark. */
    @Test fun theMarksMenuActs() {
        val done = mutableListOf<DeliveryAction>()
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                OutboxBubble("Also add a line", "m1", DeliveryState.WaitingForNetwork, busy = false) { done += it }
            }
        }
        rule.onNodeWithText("Sends when connected").assertExists()
        val mark = rule.onNodeWithContentDescription("Waiting for network, will send once connected")
        assertEquals(listOf("Retry now", "Don't send"), mark.fetchSemanticsNode().config[SemanticsActions.CustomActions].map { it.label })
        mark.performClick()
        rule.onNodeWithText("Retry now").performClick()
        rule.waitForIdle()
        mark.performClick()
        rule.onNodeWithText("Don't send").performClick()
        rule.waitForIdle()
        assertEquals(listOf(DeliveryAction.RetryNow, DeliveryAction.Cancel), done)
    }

    /** One that did not go: a red mark whose menu is Resend and Delete; a read-only chat's mark has no menu. */
    @Test fun aFailedMarkOffersResendAndDelete() {
        val done = mutableListOf<DeliveryAction>()
        var readOnly by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = true, reduceMotion = true, style = AppStyle.Glass) {
                OutboxBubble("Refused words", "m2", DeliveryState.Refused, busy = false, onAction = if (readOnly) null else { a -> done += a })
            }
        }
        val mark = rule.onNodeWithContentDescription("Failed to send. Resend or delete.")
        mark.performClick()
        rule.onNodeWithText("Resend").performClick()
        rule.waitForIdle()
        mark.performClick()
        rule.onNodeWithText("Delete").performClick()
        rule.waitForIdle()
        assertEquals(listOf(DeliveryAction.Retry, DeliveryAction.Delete), done)
        readOnly = true
        rule.waitForIdle()
        assertNull(mark.fetchSemanticsNode().config.getOrNull(SemanticsActions.OnClick))
        rule.onNodeWithText("Sends when connected").assertDoesNotExist()
    }
}
