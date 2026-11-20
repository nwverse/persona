package persona.core.domain.sort

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.domain.answer.requestSourceLabel

class TeamTest {
    private val lead = Session("server", "lead", state = "idle", children = SessionChildren(total = 3, working = 1, needsYou = 1, workers = 3))
    private fun member(id: String, at: Int, state: String) = Session("server", id, state = state,
        paneIndex = at, foldedUnder = lead.sessionId, foldedAs = FoldedAs.WORKER)
    private val review = member("review", 1, "needs_you")
    private val tests = member("tests", 2, "unread")
    private val docs = member("docs", 3, "working")
    private val stopped = Session("server", "stopped", state = "offline")
    private val peer = Session("server", "peer", paneIndex = 1, state = "working")
    private val secondPair = GroupKeys.machine("server", "g00000002")
    private val all = listOf(docs, stopped, tests.copy(machineId = secondPair), tests, peer, lead, review,
        lead.copy(machineId = secondPair), member("stale", 5, "offline"), member("underSide", 6, "working").copy(foldedSide = "side"))
    private val permission = Interaction("permit", machineId = "server", sessionId = "review", sessionTitle = "Review",
        kind = "permission", foldedUnder = "lead", foldedAs = FoldedAs.WORKER)

    @Test fun onlyDeclaredLiveWorkersOfThisPairingAppearInPaneOrder() {
        assertEquals(listOf(review, tests, docs), teamWorkers(all + review, lead.key))
        assertEquals(listOf(tests.copy(machineId = secondPair)), teamWorkers(all, SessionKey(secondPair, "lead")))
        assertTrue(teamWorkers(all, peer.key).isEmpty())
        assertFalse(stopped.isTeamWorker)
        assertFalse(peer.isTeamWorker)
    }

    @Test fun manualSelectionWinsThenLastThenFirstNeedsYouThenFirstPane() {
        val workers = listOf(docs, review, tests)
        assertEquals(tests, shownWorker(workers, "tests", "docs"))
        assertEquals(docs, shownWorker(workers, "gone", "docs"))
        assertEquals(review, shownWorker(workers, null, "gone"))
        assertEquals(docs, shownWorker(listOf(docs, tests), null, null))
        assertNull(shownWorker(emptyList(), "gone", "review"))
    }

    @Test fun foldAndBadgesCountEachLeadOnceAndRetainStoppedAndShellFirstPeers() {
        val folded = foldedSessions(all)
        assertEquals(setOf(lead.key, stopped.key, peer.key, SessionKey(secondPair, "lead")), folded.map { it.key }.toSet())
        assertEquals(AgentState.NeedsYou, folded.single { it.key == lead.key }.agentState)
        val second = permission.copy(id = "paired-permit", machineId = secondPair)
        val rows = needsYouRows(listOf(permission, permission, second), needsYouWaiting(all))
        assertEquals(setOf(lead.key, SessionKey(secondPair, "lead")), rows.map { it.key }.toSet())
        assertEquals(1, rows.single { it.key == lead.key }.cards.size)
        assertEquals(review.key, rows.single { it.key == lead.key }.cards.single().memberTarget)
        assertEquals(permission.sessionKey, permission.memberTarget)
        assertEquals(SessionKey("server", "side"), permission.copy(foldedSide = "side").memberTarget)
        val source = permission.requestSourceLabel()?.args?.filterIsInstance<CoreText>()?.single()
        assertEquals(CoreText.Key.RequestFromWorker, source?.key)
    }
}
