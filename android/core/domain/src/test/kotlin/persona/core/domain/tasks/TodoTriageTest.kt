package persona.core.domain.tasks

import java.time.Instant
import java.time.ZoneOffset
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.ReviewCheck
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.Supervision
import persona.core.contracts.TaskProgress
import persona.core.contracts.TaskReview
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.TodoAsk
import persona.core.contracts.TodoDelivery

// The same cases as web/src/lib/todo-triage.test.ts (docs/assistant/todo-status-sop.md, decision table), so the clients
// cannot drift apart.
class TodoTriageTest {
    private val now = Instant.parse("2026-11-10T12:00:00Z").toEpochMilli()
    private val zone = ZoneOffset.UTC
    private fun hoursAgo(h: Int) = Instant.ofEpochMilli(now - h * 3_600_000L).toString()

    private fun session(sid: String, state: String, updatedHoursAgo: Int = 1) =
        Session(machineId = "m", sessionId = sid, state = state, status = if (state == "working") "working" else "idle", updatedAt = hoursAgo(updatedHoursAgo))

    private fun agent(sid: String) = TodoAgent(machineId = "m", sessionId = sid, linkedAt = hoursAgo(200))

    private fun todo(
        id: String, title: String = id, updated: Int = 1, doneAt: String? = null, agents: List<TodoAgent> = emptyList(),
        origin: String? = "conv", supervision: Supervision? = null, ask: TodoAsk? = null, delivery: TodoDelivery? = null,
        prUrl: String? = null, reopenedAt: String? = null, dueDate: String? = null, startError: String? = null, quiet: Boolean = false,
        planId: String? = null,
    ) = Todo(
        id = id, title = title, createdAt = hoursAgo(300), updatedAt = hoursAgo(updated), doneAt = doneAt, agents = agents,
        origin = origin, supervision = supervision, ask = ask, delivery = delivery, prUrl = prUrl, reopenedAt = reopenedAt,
        dueDate = dueDate, startError = startError, quiet = quiet, planId = planId,
    )

    /** An accepted task the hub reopened when its agent was sent more work: eight criteria, the review judged five. */
    private fun acceptedReopened(): Supervision {
        val crit = (1..8).map { "c$it" }
        return Supervision(
            supervisor = "conv", doneWhen = crit, state = "accepted", round = 1,
            reviews = listOf(TaskReview(at = hoursAgo(5), verdict = "accepted", checks = crit.take(5).map { ReviewCheck(it, true) })),
        )
    }

    private fun lookupOf(vararg sessions: Session): (SessionKey) -> Session? {
        val map = sessions.associateBy { it.key }
        return { map[it] }
    }

    private val lookup = lookupOf(
        session("ask", "needs_you"), session("run", "working"), session("unseen", "unread"),
        session("idle", "idle", 2), session("old", "idle", 100),
    )

    private fun at(td: Todo, restoredAt: Long = 0) = td.triage(lookup, now, restoredAt, zone)

    @Test
    fun anAgentAskingIsWaitingWhateverElseHolds() {
        val t = at(todo("t", agents = listOf(agent("ask"), agent("run"))))
        assertEquals(TriageSection.Waiting, t.section)
        assertEquals(TriageRule.AgentAsks, t.rule)
    }

    @Test
    fun aDecisionForTheOwnerIsWaiting() {
        assertEquals(TriageRule.OwnerDecides, at(todo("t", agents = listOf(agent("idle")), supervision = Supervision(supervisor = "c", state = "needs_user"))).rule)
        assertEquals(TriageSection.Waiting, at(todo("t", ask = TodoAsk(outcome = "decide", verdict = "v", askedAt = hoursAgo(1)))).section)
    }

    @Test
    fun pleaseVerifyOrADoneConclusionIsJustFinishedNotWaiting() {
        val idle = listOf(agent("idle"))
        val verify = at(todo("t", agents = idle, ask = TodoAsk(verdict = "done, deployed: verify below", askedAt = hoursAgo(1))))
        assertEquals(TriageSection.JustDone, verify.section)
        assertEquals(TriageRule.FinishedUnseen, verify.rule)
        assertEquals(TriageSection.JustDone, at(todo("t", agents = idle, ask = TodoAsk(outcome = "done", verdict = "done", askedAt = hoursAgo(1)))).section)
    }

    @Test
    fun anAgentRunningIsWorkingEvenOnAnOldTodo() {
        val t = at(todo("t", updated = 400, agents = listOf(agent("run"))))
        assertEquals(TriageSection.Working, t.section)
        assertEquals(TriageRule.AgentRunning, t.rule)
    }

    @Test
    fun aClosedTodoIsJustFinishedForADayThenDone() {
        assertEquals(TriageSection.JustDone, at(todo("t", doneAt = hoursAgo(3))).section)
        assertEquals(TriageSection.Done, at(todo("t", doneAt = hoursAgo(30), updated = 30)).section)
    }

    @Test
    fun anAcceptedTaskReopenedByMoreWorkIsFinishedNotFollowUp() {
        val td = todo("t", agents = listOf(agent("idle")), reopenedAt = hoursAgo(4), supervision = acceptedReopened())
        val t = at(td)
        assertEquals(TriageSection.JustDone, t.section)
        assertEquals(TriageRule.Accepted, t.rule)
        assertTrue(td.acceptedAtRest(lookup))
        assertEquals(ReviewStage.Accepted, td.reviewStage)
    }

    @Test
    fun anAcceptedTaskWhoseAgentRunsAgainIsWorking() {
        val td = todo("t", agents = listOf(agent("run")), supervision = acceptedReopened())
        assertEquals(TriageSection.Working, at(td).section)
        assertFalse(td.acceptedAtRest(lookup))
    }

    @Test
    fun aDeployedDeliveryIsFinished() {
        assertEquals(TriageRule.Deployed, at(todo("t", delivery = TodoDelivery(state = "live", at = hoursAgo(2)))).rule)
    }

    @Test
    fun aFinishedTurnNobodySawIsJustFinished() {
        val t = at(todo("t", agents = listOf(agent("unseen"))))
        assertEquals(TriageSection.JustDone, t.section)
        assertEquals(TriageRule.FinishedUnseen, t.rule)
    }

    @Test
    fun anOpenPRIsPrToMergeHoweverOldAMergedOneIsNot() {
        val old = listOf(agent("old"))
        assertEquals(TriageSection.Pr, at(todo("t", updated = 300, agents = old, delivery = TodoDelivery(state = "pr"))).section)
        assertEquals(TriageSection.Pr, at(todo("t", updated = 300, agents = old, prUrl = "https://example.com/pull/1")).section)
        assertEquals(TriageSection.Shelved, at(todo("t", updated = 300, agents = old, prUrl = "https://example.com/pull/1", delivery = TodoDelivery(state = "merged"))).section)
    }

    @Test
    fun aTaskWithNoMoveFor48HoursIsShelvedANewerAgentMoveKeepsIt() {
        val t = at(todo("t", updated = 100, agents = listOf(agent("old"))))
        assertEquals(TriageSection.Shelved, t.section)
        assertEquals(TriageRule.Stale, t.rule)
        assertEquals(TriageSection.Other, at(todo("t", updated = 100, agents = listOf(agent("idle")))).section)
    }

    @Test
    fun theUsersOwnTodoWithoutAgentsNeverShelvesNorOneDueAhead() {
        assertEquals(TriageSection.Other, at(todo("t", updated = 100, origin = null)).section)
        assertEquals(TriageSection.Other, at(todo("t", updated = 100, agents = listOf(agent("old")), dueDate = "2026-12-01")).section)
    }

    @Test
    fun aRestoreCountsAsAMove() {
        val td = todo("t", updated = 100, agents = listOf(agent("old")))
        assertEquals(TriageSection.Other, at(td, now - 3_600_000L).section)
        assertEquals(TriageSection.Shelved, at(td, now - SHELVE_AFTER_MS - 1).section)
    }

    @Test
    fun lastActivityTakesTheLatestOfTodoSupervisionAndAgents() {
        val td = todo("t", updated = 50, agents = listOf(agent("idle")),
            supervision = Supervision(supervisor = "c", progress = TaskProgress(kind = "progress", text = "x", at = hoursAgo(1))))
        assertEquals(now - 3_600_000L, td.lastActivity(lookup))
    }

    @Test
    fun criteriaAddedAfterAStandingAcceptanceAreUncheckedAndNotCounted() {
        val sup = acceptedReopened()
        assertEquals(List(5) { CheckMark.Met } + List(3) { CheckMark.Unchecked }, sup.checklist().map { it.mark })
        assertEquals(5 to 5, sup.checkCount())
    }

    @Test
    fun beforeAnyAcceptanceTheyStayOpenAndCount() {
        val sup = acceptedReopened().copy(state = "rework", reviews = listOf(TaskReview(at = hoursAgo(5), verdict = "rework", checks = listOf(ReviewCheck("c1", true)))))
        assertEquals(7, sup.checklist().count { it.mark == CheckMark.Open })
        assertEquals(1 to 8, sup.checkCount())
    }

    private val groupLookup = lookupOf(session("run", "working"), session("old", "idle", 100), session("old2", "idle", 120))

    @Test
    fun mergesTheSameIssuePrTitleOrPlanLedByTheMostUrgent() {
        val list = listOf(
            todo("fix", title = "Fix #412 lost pushes", agents = listOf(agent("run"))),
            todo("test", title = "#412 lost pushes: regression test", updated = 100, agents = listOf(agent("old"))),
            todo("pr-a", title = "A", prUrl = "https://example.com/pull/9", agents = listOf(agent("old2")), updated = 120),
            todo("pr-b", title = "B", prUrl = "https://example.com/pull/9/", agents = listOf(agent("old2")), updated = 121),
            todo("lone", title = "Something else", updated = 1),
        )
        val r = triageTodos(list, groupLookup, now, zone = zone)
        assertEquals(listOf("fix" to listOf("fix", "test")), r.rows(TriageSection.Working).map { it.lead.id to it.members.map(Todo::id) })
        assertEquals(listOf(listOf("pr-a", "pr-b")), r.rows(TriageSection.Pr).map { it.members.map(Todo::id) })
        assertEquals(listOf("lone"), r.rows(TriageSection.Other).map { it.lead.id })
    }

    @Test
    fun mergesTasksOfOnePlan() {
        val r = triageTodos(listOf(todo("a", title = "One", planId = "p1"), todo("b", title = "Two", planId = "p1")), groupLookup, now, zone = zone)
        assertEquals(listOf(listOf("a", "b")), r.rows(TriageSection.Other).map { it.members.map(Todo::id).sorted() })
    }

    @Test
    fun neverMergesAClosedTodoIntoAnOpenOnesRow() {
        val r = triageTodos(listOf(todo("a", title = "Same title"), todo("b", title = "Same title", doneAt = hoursAgo(1))), groupLookup, now, zone = zone)
        assertEquals(1, r.rows(TriageSection.Other).size)
        assertEquals(1, r.rows(TriageSection.JustDone).size)
    }

    @Test
    fun countsAFailedStartWaitingOnTheOwnerAsOpen() {
        val r = triageTodos(listOf(todo("f", startError = "no launcher", doneAt = hoursAgo(1))), groupLookup, now, zone = zone)
        assertEquals(listOf("f"), r.rows(TriageSection.Waiting).map { it.lead.id })
        assertEquals(1, r.open)
    }

    @Test
    fun listsQuietTasksOnThePageLeavesThemOutForABadge() {
        val list = listOf(todo("q", quiet = true))
        assertEquals(1, triageTodos(list, groupLookup, now, zone = zone).open)
        assertEquals(0, triageTodos(list, groupLookup, now, quiet = false, zone = zone).open)
    }

    @Test
    fun countsShelvedAndFinishedApartFromOpen() {
        val r = triageTodos(
            listOf(
                todo("run", agents = listOf(agent("run"))),
                todo("stale", updated = 100, agents = listOf(agent("old"))),
                todo("closed", doneAt = hoursAgo(2)),
                todo("accepted", supervision = acceptedReopened(), agents = listOf(agent("old"))),
                todo("quiet", quiet = true),
            ),
            groupLookup, now, quiet = false, zone = zone,
        )
        assertEquals(listOf(1, 1, 2), listOf(r.open, r.shelved, r.finished))
    }

    @Test
    fun relatedKeysIgnoreCaseSpacesPunctuationAndATrailingSlash() {
        assertEquals(
            listOf("pr:https://example.com/pull/3", "title:fixthewidget"),
            todo("a", title = "Fix: the  Widget!", prUrl = "https://Example.com/pull/3/").relatedKeys(),
        )
    }

    @Test
    fun aRestoreDropsOnesThatWouldHaveShelvedAgain() {
        val kept = withRestore(mapOf("old" to now - SHELVE_AFTER_MS - 1), "new", now)
        assertEquals(mapOf("new" to now), kept)
    }
}
