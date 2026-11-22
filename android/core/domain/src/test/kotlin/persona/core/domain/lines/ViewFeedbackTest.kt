package persona.core.domain.lines

import persona.core.contracts.Item
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

/**
 * #4315: the node's `<persona-view-errors>` feedback before an assistant message is for the model. An older hub gave it
 * as a user item of its own (the line's block #0) or at the start of the message, and the bubble showed its raw tags.
 */
class ViewFeedbackTest {
    private val feedback = "<persona-view-errors>\nThe view v1 failed its check.\n</persona-view-errors>"
    private fun text(id: String, seq: Int, role: String, text: String) =
        LineItem(Item(id, seq, role = role, kind = "text", text = text, clientMsgId = if (role == "user") "m1" else null))

    @Test fun anItemOfOnlyTheFeedbackGoes() {
        val items = listOf(text("l5#0", 5, "user", feedback), text("l5#1", 5, "user", "Add a bakery stop."),
            text("l6#0", 6, "assistant", "Bakery stop added."))
        val users = chatLines(items).lines.filterIsInstance<ChatLine.Row>().filter { it.row.type == "user" }
            .map { it.row.item?.item?.text }
        assertEquals(listOf("Add a bakery stop."), users)
        assertEquals(listOf("l5#1"), chatTurns(items).flatMap { turn -> turn.users.map { it.id } })
    }

    @Test fun theFeedbackBeforeTheMessageIsTakenOut() {
        val kept = withoutViewFeedback(listOf(text("l5#0", 5, "user", "$feedback\nAdd a bakery stop.")))
        assertEquals(listOf("Add a bakery stop."), kept.map { it.item.text })
    }

    @Test fun otherItemsStayAsTheyAre() {
        val items = listOf(text("l1#0", 1, "user", "Plan a weekend trip."), text("l2#0", 2, "assistant", feedback))
        assertEquals(items, withoutViewFeedback(items))
        assertFalse(chatLines(items).lines.isEmpty())
    }
}
