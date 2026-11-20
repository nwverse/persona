package persona.core.domain.tasks

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Interaction
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import persona.core.contracts.TaskReview
import persona.core.contracts.Todo
import java.time.Instant

/** A decision the owner marked Don't handle on another device leaves Needs you here too (#8262, web "review dismissal" cases). */
class AssistantEntriesDecisionDismissalTest {
    private val at = "2026-11-10T08:00:00.123456789Z"
    private val idle = Session(machineId = "box", sessionId = "worker", title = "Example Agent", cwd = "/example",
        state = "idle", status = "idle", updatedAt = at)

    /** As the hub sends it (docs/protocol/protocol.md `GET /api/v1/todos`), through the app's own decoder. */
    private fun todo(decisionReview: String?) = JsonCodec.decodeFromString(Todo.serializer(), """
        {"id":"task","title":"Example rollout","origin":"main","created_at":"$at","updated_at":"$at",
         "agents":[{"machine_id":"box","session_id":"worker","linked_at":"$at"}],
         "supervision":{"supervisor":"main","state":"needs_user","reviews":[{"at":"$at","verdict":"needs_user","note":"Choose the rollout route."}]}
         ${decisionReview?.let { ""","decision_review":$it""" } ?: ""}}
    """.trimIndent())

    private val dismissed = todo("""{"review_at":"$at","dismissal_id":"dismiss-demo"}""")

    private fun run(todo: Todo, session: Session = idle, interactions: List<Interaction> = emptyList()) =
        assistantEntries("main", "main", listOf(todo), listOf(session), interactions, emptyList(), emptyList(),
            Instant.parse(at).toEpochMilli())

    @Test fun decodesTheHubsDecisionReview() {
        assertEquals("dismiss-demo", dismissed.decisionReview?.dismissalId)
        assertEquals(Instant.parse(at), dismissed.decisionReview?.reviewAt?.let(Instant::parse))
        assertNull(todo(null).decisionReview)
        assertTrue(dismissed.decisionDismissed())
    }

    @Test fun waitsOnTheOwnerUntilDismissed() {
        assertEquals(listOf("task:task"), run(todo(null)).needsYou.map { it.key })
        assertEquals(listOf("task:task"), run(todo("""{"review_at":"$at"}""")).needsYou.map { it.key })
    }

    @Test fun hidesASameRoundDismissalAndDoesNotCountItInProgress() {
        val got = run(dismissed)
        assertTrue(got.needsYou.isEmpty())
        assertTrue(got.running.isEmpty())
    }

    @Test fun showsANewReviewOneNanosecondLater() {
        val next = "2026-11-10T08:00:00.123456790Z"
        val newer = dismissed.copy(supervision = dismissed.supervision!!.copy(
            reviews = listOf(TaskReview(at = next, verdict = "needs_user"))))
        assertFalse(newer.decisionDismissed())
        assertEquals(listOf("task:task"), run(newer).needsYou.map { it.key })
    }

    @Test fun keepsAPendingAgentQuestionVisible() {
        val question = Interaction("question", machineId = "box", sessionId = "worker", kind = "question", createdAt = at)
        val got = run(dismissed, interactions = listOf(question))
        assertEquals(listOf("task:task"), got.needsYou.map { it.key })
        assertEquals(listOf(question), (got.needsYou.single() as NeedsYouEntry.Task).asks)
    }

    @Test fun keepsAgentInputVisibleBeforeItsInteractionLoads() {
        val got = run(dismissed, idle.copy(state = "needs_you", status = "blocked"))
        assertEquals(listOf("task:task"), got.needsYou.map { it.key })
    }

    /** A reported finish is the supervisor's acceptance work, not an owner decision that ages into Needs you. */
    @Test fun dismissalDoesNotHideAnAcceptanceOrTurnItIntoAnOwnerDecision() {
        val reported = idle.copy(work = persona.core.contracts.WorkState("done",
            closeout = persona.core.contracts.Closeout("done")))
        for (age in listOf(REVIEW_OVERDUE_MS - 1, REVIEW_OVERDUE_MS, REVIEW_OVERDUE_MS + 1)) {
            val pending = dismissed.copy(supervision = dismissed.supervision!!.copy(state = "pending_review",
                pendingSince = Instant.parse(at).minusMillis(age).toString()))
            for (todo in listOf(pending, pending.copy(decisionReview = null))) {
                val got = run(todo, reported)
                assertTrue(got.needsYou.isEmpty())
                assertTrue(got.running.isEmpty())
                assertEquals(listOf("task:task"), got.justFinished.map { it.key })
            }
        }
    }
}
