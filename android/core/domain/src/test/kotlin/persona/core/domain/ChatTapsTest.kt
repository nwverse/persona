package persona.core.domain

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import java.time.Instant

class ChatTapsTest {
    private val now = Instant.parse("2026-01-02T03:04:05Z")
    private val first = Interaction("ix1", "m1", "s1", kind = "permission", expiresAt = "2026-01-02T05:04:05Z")
    private val second = Interaction("ix2", "m1", "s1", kind = "permission")
    private val pending = listOf(first, second)

    @Test fun aTapLocksEveryCardUntilItsRequestIsOver() {
        val taps = ChatTaps()
        assertTrue(taps.canTap(first, pending, now))
        val tapped = taps.tapped(first.id)
        assertTrue(tapped.busy)
        assertFalse(tapped.canTap(first, pending, now))
        assertFalse(tapped.answerable(second, now))
        assertTrue(tapped.idle().canTap(second, pending, now))
    }

    @Test fun anAnswerTheHubTookTakesTheCardOffThePage() {
        val taps = ChatTaps().tapped(first.id).answered(first.id, CardAnswer.Withdrawn)
        // still in flight until the request is over (the page may read the list again first)
        assertTrue(taps.busy)
        val done = taps.idle()
        assertEquals(listOf(second), done.shown(pending))
        assertFalse(done.canTap(first, pending, now))
    }

    @Test fun anUnconfirmedAnswerLocksItsCardUntilTheHubStopsListingIt() {
        val taps = ChatTaps().tapped(first.id).answered(first.id, CardAnswer.Unconfirmed).idle()
        assertEquals(pending, taps.shown(pending))
        assertFalse(taps.answerable(first, now))
        assertTrue(taps.answerable(second, now))
        // a refusal on a later try unlocks it
        assertTrue(taps.tapped(first.id).answered(first.id, CardAnswer.Refused).idle().answerable(first, now))
        // the hub no longer lists it: its mark goes
        assertEquals(ChatTaps(), taps.pending(setOf(second.id)))
    }

    @Test fun aRefusedAnswerLeavesTheCardAnswerable() {
        val taps = ChatTaps().tapped(first.id).answered(first.id, CardAnswer.Refused).idle()
        assertEquals(ChatTaps(), taps)
        assertTrue(taps.canTap(first, pending, now))
    }

    @Test fun aDiscardedCardLeavesThePage() {
        val taps = ChatTaps().tapped(second.id).discarded(second.id).idle()
        assertEquals(listOf(first), taps.shown(pending))
    }

    @Test fun onlyTheHubsCurrentUnexpiredCardIsTapped() {
        val taps = ChatTaps()
        assertFalse(taps.canTap(first, listOf(second), now))
        assertFalse(taps.canTap(first.copy(summary = "changed"), pending, now))
        assertFalse(taps.canTap(first, pending, Instant.parse("2026-01-02T05:04:05Z")))
        assertTrue(taps.canTap(second, pending, Instant.parse("2027-01-01T00:00:00Z")))
    }

    @Test fun aBubbleGoesOnceTheTranscriptHasItsCopy() {
        val key = SessionKey("m1", "s1")
        val outbox = listOf(
            OutboxMessage("c1", key, "Book the tickets.", DeliveryState.Sent),
            OutboxMessage("c2", key, "Pack the jackets.", DeliveryState.Sending),
            OutboxMessage("c3", key, "Own id.", DeliveryState.Sent),
            OutboxMessage("c4", SessionKey("m1", "other"), "Elsewhere.", DeliveryState.Sending),
        )
        val items = listOf(
            Item("l4#0", 4, role = "user", kind = "text", text = "Book the tickets.", clientMsgId = "c1"),
            Item("c3", 5, role = "user", kind = "text", text = "Own id."),
        )
        assertEquals(listOf("c2"), chatBubbles(outbox, key, items).map { it.clientMessageId })
        assertEquals(listOf("c1", "c2", "c3"), chatBubbles(outbox, key, emptyList()).map { it.clientMessageId })
    }
}
