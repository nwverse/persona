package persona.android.capsule

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Item
import persona.core.contracts.Session
import persona.core.contracts.api.ChatSnapshot

class CapsuleReadTrackerTest {
    private val session = Session("machine", "task", state = "unread", rev = 1)
    private fun reply(id: String = "reply", text: String = "Answer", verified: Boolean = true) =
        ChatSnapshot(loading = false, verified = verified, items = listOf(Item(id, role = "assistant", kind = "text", text = text)))

    @Test fun aSessionRevisionArrivingBeforeNewItemsCannotReadTheOldReply() {
        val tracker = CapsuleReadTracker()
        val initial = requireNotNull(tracker.version(session.key, reply(), session))
        assertNull(tracker.version(session.key, reply(), session.copy(rev = 2)))
        val next = requireNotNull(tracker.version(session.key, reply("new"), session.copy(rev = 2)))
        assertNotEquals(initial, next)
    }

    @Test fun anUnverifiedWindowDoesNotConsumeTheReplyBeforeItBecomesEligible() {
        val tracker = CapsuleReadTracker()
        assertNull(tracker.version(session.key, reply(verified = false), session))
        assertNotNull(tracker.version(session.key, reply(), session))
    }

    @Test fun aStreamedWorkingReplyCanFinishButCannotReadASecondRevisionWithoutNewContent() {
        val tracker = CapsuleReadTracker()
        assertNull(tracker.version(session.key, reply(), session.copy(state = "working")))
        assertNotNull(tracker.version(session.key, reply(), session.copy(rev = 2, lastText = "Answer")))
        assertNull(tracker.version(session.key, reply(), session.copy(rev = 3, lastText = "Another answer")))
        assertNotNull(tracker.version(session.key, reply(text = "Another answer"), session.copy(rev = 3, lastText = "Another answer")))
    }

    @Test fun aReplyBeforeTheLatestQuestionCannotReadTheCurrentTurn() {
        val tracker = CapsuleReadTracker()
        val old = reply().copy(items = reply().items + Item("question", role = "user", kind = "text", text = "Continue"))
        assertNull(tracker.version(session.key, old, session))
    }

    @Test fun aMultilineWorkingReplyMatchesTheHubsWhitespaceCollapsedSnippet() {
        val tracker = CapsuleReadTracker()
        val window = reply(text = "First line\nSecond\tline\n\nFinished")
        assertNull(tracker.version(session.key, window, session.copy(state = "working")))
        assertNotNull(tracker.version(session.key, window,
            session.copy(rev = 2, lastText = "First line Second line Finished")))
    }

    @Test fun firstOpeningSwitchingAndReopeningAnOldVerifiedEntryCannotReadTheNewReply() {
        val tracker = CapsuleReadTracker()
        val current = session.copy(rev = 2, lastText = "New answer")
        assertNull(tracker.version(current.key, reply(text = "Old answer"), current))
        assertNotNull(tracker.version(current.key, reply("new", "New answer"), current))
        val other = current.copy(machineId = "machine~g0a1b2c3d")
        assertNull(tracker.version(other.key, reply(text = "Old answer"), other))
        tracker.version(null, ChatSnapshot(loading = false), null)
        assertNull(tracker.version(current.key, reply(text = "Old answer"), current))
        assertNotNull(tracker.version(current.key, reply("new", "New answer"), current))
    }
}
