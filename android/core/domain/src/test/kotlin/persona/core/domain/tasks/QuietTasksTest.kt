package persona.core.domain.tasks

import java.time.Instant
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.FeedItem
import persona.core.contracts.Session
import persona.core.contracts.Supervision
import persona.core.contracts.TaskReview
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.WorkState

/** #8312: receipts, reports and reviews stay in the feed and are not drawn; Just finished and a usage limit. */
class QuietTasksTest {
    private val now = Instant.parse("2026-11-10T09:00:00Z")
    private val nowMs = now.toEpochMilli()

    private fun item(id: String, kind: String) = FeedItem(id, "main", now.toString(), kind, "t")

    @Test fun drawnFeedDropsReceiptsReportsAndReviewsAndKeepsTheList() {
        val feed = listOf(item("handoff", "routed"), item("receipt", "receipt"), item("report", "report"),
            item("review", "review"), item("notice", "notice"))
        assertEquals(listOf("handoff", "notice"), AssistantTaskCards.drawnFeed(feed).map { it.id })
        assertEquals(listOf("handoff", "receipt", "report", "review", "notice"), feed.map { it.id })
        assertEquals(listOf("handoff", "notice"),
            AssistantTaskCards.lines(emptyList(), feed, emptyMap(), emptyList(), inline = true)
                .filterIsInstance<persona.core.domain.lines.ChatLine.Extra>()
                .mapNotNull { (it.payload as? AssistantExtra.Feed)?.item?.id })
        assertEquals(listOf("notice"), AssistantTaskCards.backgroundFeed(feed).map { it.id })
        assertEquals(emptyList<String>(), AssistantTaskCards.backgroundFeed(feed, inline = true).map { it.id })
    }

    @Test fun aReceiptStillSaysWhatAFinishedTaskDid() {
        val todo = Todo("t", "Rename", origin = "main", doneReason = "Closed",
            supervision = Supervision(reviews = listOf(TaskReview(note = "Kept the wording", verdict = "accepted"))))
        val failed = FeedItem("bad", "main", now.toString(), "receipt", "t", buildJsonObject {
            put("state", "failed"); put("summary", "The start failed")
        })
        val receipt = FeedItem("ok", "main", now.toString(), "receipt", "t", buildJsonObject { put("summary", "Renamed the button") })
        val lead = Session("desk", "a", lastText = "Last words")
        assertEquals("Kept the wording", finishedSummary(todo, listOf(receipt), lead))
        assertEquals("Closed", finishedSummary(todo.copy(supervision = null), listOf(receipt), lead))
        assertEquals("Renamed the button", finishedSummary(todo.copy(supervision = null, doneReason = null), listOf(failed, receipt), lead))
        assertEquals("Last words", finishedSummary(todo.copy(supervision = null, doneReason = null), listOf(failed), lead))
        assertNull(finishedSummary(todo.copy(supervision = null, doneReason = null), emptyList(), null))
    }

    @Test fun startFailureWordsMatchTheKnownCauses() {
        assertEquals(StartFailurePlain.Known(StartCause.ConnectionLost), startFailurePlain("rpc: connection closed"))
        assertEquals(StartFailurePlain.Known(StartCause.TimedOut), startFailurePlain("context deadline exceeded"))
        assertEquals(StartFailurePlain.Known(StartCause.NotAllowed), startFailurePlain("permission denied"))
        assertEquals(StartFailurePlain.Known(StartCause.NeedsUpdate), startFailurePlain("unknown method"))
        assertEquals(StartFailurePlain.Known(StartCause.Offline), startFailurePlain("connection refused"))
        assertEquals(StartFailurePlain.Known(StartCause.ConnectionLost), startFailurePlain("unexpected eof"))
        assertEquals(StartFailurePlain.Sentence("电脑拒绝了启动。"), startFailurePlain("电脑拒绝了启动。")) // i18n-ignore: a sentence the hub already wrote
        assertEquals(StartFailurePlain.Sentence("The computer refused the start."), startFailurePlain("The computer refused the start."))
        assertEquals(StartFailurePlain.Details, startFailurePlain("launcher missing"))
        assertEquals(StartFailurePlain.Details, startFailurePlain("  "))
    }

    @Test fun aUsageLimitIsNotAnInterruptionUntilItsGracePasses() {
        val clock = System.currentTimeMillis()
        val soon = Instant.ofEpochMilli(clock + 5 * 60_000).toString()
        val limited = Session("desk", "s", state = "idle", turnError = "limit", turnErrorKind = "usage_limit",
            turnErrorResetsAt = soon)
        assertFalse(limited.interrupted)
        assertTrue(limited.isUsageLimited(clock))
        assertFalse(limited.usageLimitContinues())
        assertTrue(limited.copy(turnErrorResumes = true).usageLimitContinues())
        val scheduled = limited.copy(turnErrorResetsAt = null, work = WorkState(phase = "blocked",
            resetsAt = soon, wakeAt = soon))
        assertTrue(scheduled.usageLimitContinues())
        val past = limited.copy(turnErrorResetsAt = Instant.ofEpochMilli(clock - 11 * 60_000).toString())
        assertFalse(past.isUsageLimited(clock))
        assertTrue(past.interrupted)
        val unknown = limited.copy(turnErrorResetsAt = null)
        assertTrue(unknown.isUsageLimited(clock))
        val cut = Session("desk", "s", state = "idle", turnError = "The model service stopped responding.")
        assertTrue(cut.interrupted)
        assertFalse(cut.isUsageLimited(nowMs))
        val working = limited.copy(state = "working")
        assertFalse(working.isUsageLimited(nowMs))
        assertFalse(working.interrupted)
    }

    @Test fun justFinishedIsNewestFirstAndLeavesTheCountedRows() {
        fun done(id: String, at: String, origin: String? = "main", seenAt: String? = null) =
            Todo(id, title = id, origin = origin, doneAt = at, seenAt = seenAt, createdAt = at)
        val older = done("older", "2026-11-10T08:00:00Z")
        val newer = done("newer", "2026-11-10T08:30:00Z")
        val seen = done("seen", "2026-11-10T08:10:00Z", seenAt = "2026-11-10T08:20:00Z")
        val stale = done("stale", "2026-11-08T08:00:00Z")
        val blank = done("blank", "2026-11-10T08:15:00Z", origin = "")
        val got = assistantEntries("main", "main", listOf(older, newer, seen, stale, blank), emptyList(), emptyList(),
            emptyList(), emptyList(), nowMs)
        assertEquals(listOf("task:newer", "task:older"), got.justFinished.map { it.key })
        assertTrue(got.needsYou.isEmpty())
        assertTrue(got.running.isEmpty())
        assertTrue(got.shelved.isEmpty())
        val side = assistantEntries("side", "main", listOf(newer), emptyList(), emptyList(), emptyList(), emptyList(), nowMs)
        assertTrue(side.justFinished.isEmpty())
    }

    @Test fun anOpenUnseenFinishStaysInProgressAndAFailedStartIsNotNews() {
        val agent = TodoAgent("desk", "cut", linkedAt = "2026-11-10T08:00:00Z")
        val session = Session("desk", "cut", state = "unread", updatedAt = "2026-11-10T08:40:00Z")
        val open = Todo("open", "Still open", origin = "main", createdAt = "2026-11-10T08:00:00Z",
            updatedAt = "2026-11-10T08:40:00Z", agents = listOf(agent))
        val failed = Todo("failed", "Did not start", origin = "main", startError = "rpc: connection closed",
            doneAt = "2026-11-10T08:50:00Z", createdAt = "2026-11-10T08:50:00Z")
        val got = assistantEntries("main", "main", listOf(open, failed), listOf(session), emptyList(), emptyList(), emptyList(), nowMs)
        assertTrue(got.justFinished.isEmpty())
        assertEquals(listOf("task:open"), got.running.map { it.key })
        assertEquals(listOf("task:failed"), got.needsYou.map { it.key })
    }
}
