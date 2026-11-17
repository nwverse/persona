package persona.core.domain.tasks

import org.junit.Test
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import persona.core.contracts.JsonCodec
import persona.core.contracts.Todo

class ReviewTest {
    private fun todo(json: String) = JsonCodec.decodeFromString(Todo.serializer(), json)

    @Test
    fun anOlderHubSendsNoSupervision() {
        val t = todo("""{"id":"todo_1","title":"部署"}""")
        assertNull(t.supervision)
        assertNull(t.reviewStage)
    }

    @Test
    fun readsTheLedgerAndMarksEachCriterion() {
        val t = todo("""{"id":"todo_1","title":"部署","supervision":{"supervisor":"main","done_when":["PR 合并到 master","截图贴进 PR"],
            "expect_minutes":30,"state":"rework","round":2,"something_new":1,
            "reviews":[{"at":"2026-11-05T09:00:00Z","verdict":"rework","round":1,"checks":[{"criterion":"PR 合并到 master","met":true},{"criterion":"截图贴进 PR","met":false,"evidence":"只有浅色"}]},
                       {"at":"2026-11-05T10:00:00Z","verdict":"rework","round":2,"note":"深色也补","checks":[{"criterion":"截图贴进 PR","met":false,"evidence":"深色的糊了"},{"criterion":"另外的","met":true}]}]}}""")
        val sup = t.supervision!!
        assertEquals(ReviewStage.Rework, t.reviewStage)
        assertEquals(30, sup.expectMinutes)
        assertEquals(listOf(
            CheckLine("PR 合并到 master", CheckMark.Met, null),
            CheckLine("截图贴进 PR", CheckMark.Missing, "深色的糊了"),
            CheckLine("另外的", CheckMark.Met, null),
        ), sup.checklist())
        assertEquals(2 to 3, sup.checkCount())
        assertEquals("深色也补", sup.latestReview?.note)
    }

    @Test
    fun personalVerificationIsNotAcceptanceUntilPass() {
        val waiting = todo("""{"id":"t","supervision":{"supervisor":"m","state":"user_check","reviews":[{"at":"2026-11-05T09:00:00Z","verdict":"accepted"}]}}""")
        assertEquals(ReviewStage.UserCheck, waiting.reviewStage)
        assertEquals("user_check", waiting.reviewState)
        assertFalse(waiting.acceptanceStands)
        assertEquals(ReviewStage.Accepted, waiting.copy(doneAt = "2026-11-05T10:00:00Z").reviewStage)
        assertEquals("accepted", waiting.copy(doneAt = "2026-11-05T10:00:00Z").reviewState)
    }

    @Test
    fun aReopenedAcceptedTaskGoesOn() {
        assertEquals(ReviewStage.Ongoing, todo("""{"id":"t","supervision":{"supervisor":"m","state":"accepted"}}""").reviewStage)
        assertEquals(ReviewStage.Accepted, todo("""{"id":"t","done_at":"2026-11-05T10:00:00Z","supervision":{"supervisor":"m","state":"accepted"}}""").reviewStage)
        assertEquals(ReviewStage.Pending, todo("""{"id":"t","supervision":{"supervisor":"m","state":"pending_review"}}""").reviewStage)
        assertEquals(CheckMark.Open, todo("""{"id":"t","supervision":{"supervisor":"m","done_when":["a"]}}""").supervision!!.checklist().single().mark)
        assertNull(todo("""{"id":"t","supervision":{"supervisor":"m"}}""").supervision!!.checkCount())
    }
}
