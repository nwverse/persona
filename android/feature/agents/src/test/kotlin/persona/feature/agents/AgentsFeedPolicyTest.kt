package persona.feature.agents

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.SessionListStatus
import persona.core.domain.sort.AgentGrouping
import persona.core.domain.sort.AgentListSettings
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.buildAgentList

/** The Agents feed's rules with no list under them: when a hold stands, what counts as news, and where a rebuilt list resumes. */
class AgentsFeedPolicyTest {
    private val mac = Machine("mac", "Mac", online = true)

    private fun session(id: String, state: String = "idle", machine: String = "mac") =
        Session(machineId = machine, sessionId = id, state = state, spaceId = "app", spaceName = "app", updatedAt = "2026-01-01T00:00:00Z")

    private fun page(vararg sessions: Session, machines: List<Machine> = listOf(mac), paged: Boolean = false, loaded: Boolean = true): AgentsPageState {
        val rows = sessions.map(::AgentSession)
        return AgentsPageState(
            sessions = sessions.toList(), machines = machines, loaded = loaded,
            list = buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace)),
            pagination = SessionListStatus(paged = paged, loaded = loaded),
        )
    }

    private val scope = listOf<Any?>("grouping", 360, 800)
    private fun frozen(page: AgentsPageState, scope: List<Any?> = this.scope) = AgentsFeedFrozen(page, scope)

    // The hold

    @Test fun aReorderAndAStateChangeWaitWhileTheRowsHeldAreStillThere() {
        val held = page(session("a"), session("b"), session("c"))
        assertEquals(listOf("mac/a", "mac/b", "mac/c"), held.rowIds().sorted())
        val moved = page(session("c", "working"), session("b"), session("a", "needs_you"))
        assertTrue(holdsAgainst(frozen(held), moved, scope))
    }

    @Test fun aNewRowFromAPushWaitsToo() {
        val held = page(session("a"), session("b"))
        assertTrue(holdsAgainst(frozen(held), page(session("n"), session("a"), session("b")), scope))
    }

    @Test fun aChangedControlOrWindowLetsTheHoldGo() {
        val held = page(session("a"), session("b"))
        assertFalse("grouping, sort, filter, search and folds are in the scope", holdsAgainst(frozen(held), held, listOf("sort", 360, 800)))
        assertFalse("a resized window starts the list over", holdsAgainst(frozen(held), held, listOf("grouping", 360, 600)))
        assertTrue(holdsAgainst(frozen(held), held, scope))
    }

    @Test fun anEmptiedListOrOneWithNoRowHeldLeftLetsTheHoldGo() {
        val held = page(session("a"), session("b"))
        assertFalse("nothing is left to keep still", holdsAgainst(frozen(held), page(), scope))
        assertFalse("every key held is gone", holdsAgainst(frozen(held), page(session("x"), session("y")), scope))
        assertTrue("one survivor is enough to keep the rest still", holdsAgainst(frozen(held), page(session("x"), session("b")), scope))
    }

    @Test fun aHoldOverAnEmptyListHasNothingToKeepStill() {
        assertFalse(holdsAgainst(frozen(page()), page(), scope))
        assertFalse("rows arriving in a list that had none land at once", holdsAgainst(frozen(page()), page(session("a")), scope))
    }

    @Test fun aFirstPageThatIsNotInYetIsNeverHeld() {
        val held = page(session("a"))
        assertFalse(holdsAgainst(frozen(held), page(session("a"), loaded = false), scope))
    }

    @Test fun aFetchedPageLandsAtOnceButAPushedRowDoesNot() {
        val held = page(session("a"), session("b"), paged = true)
        assertFalse("a paged list that grew with every session still in it", holdsAgainst(frozen(held), page(session("a"), session("b"), session("c"), paged = true), scope))
        assertTrue("a paged list whose window changed some other way waits", holdsAgainst(frozen(held), page(session("b"), session("a"), paged = true), scope))
        assertTrue("a list that is not paged only gets pushes", holdsAgainst(frozen(page(session("a"), session("b"))), page(session("a"), session("b"), session("c")), scope))
        assertFalse("the list went from whole to paged", holdsAgainst(frozen(page(session("a"))), page(session("a"), paged = true), scope))
    }

    @Test fun growsPageNeedsEveryHeldSessionAndMoreOfThem() {
        val held = page(session("a"), session("b"), paged = true)
        assertTrue(growsPage(held, page(session("z"), session("a"), session("b"), paged = true)))
        assertFalse("same size", growsPage(held, page(session("a"), session("c"), paged = true)))
        assertFalse("longer but a held session is gone", growsPage(held, page(session("a"), session("c"), session("d"), paged = true)))
    }

    // What came meanwhile

    private fun mark(p: AgentsPageState, vararg requests: String) = agentsFeedMark(p, requests.toSet())

    @Test fun nothingIsNewBeforeTheUserHasLeftTheTop() {
        assertTrue(agentsFeedNotice(null, mark(page(session("a", "needs_you")))).isEmpty)
    }

    @Test fun aSessionThatStartsNeedingYouIsUrgentAndOnesAlreadyWaitingAreNot() {
        val before = mark(page(session("a", "needs_you"), session("b")))
        val notice = agentsFeedNotice(before, mark(page(session("a", "needs_you"), session("b", "needs_you"), session("c"))))
        assertEquals(1, notice.needsYou)
        assertTrue(notice.urgent)
        assertEquals("the one that is only new is told apart", 1, notice.fresh)
    }

    @Test fun aRequestThatArrivesCountsForItsSessionOnce() {
        val p = page(session("a", "needs_you"), session("b"))
        val before = mark(p)
        val notice = agentsFeedNotice(before, mark(p, "mac/a", "mac/b"))
        assertEquals("a already needed you; only b is new", 1, notice.needsYou)
    }

    @Test fun aNewRowThatNeedsYouIsNotCountedAsNewToo() {
        val notice = agentsFeedNotice(mark(page(session("a"))), mark(page(session("a"), session("n", "needs_you"))))
        assertEquals(1, notice.needsYou)
        assertEquals(0, notice.fresh)
    }

    @Test fun aSessionThatStopsRunningCountsButHistoryThePageBringsDoesNot() {
        val before = mark(page(session("a", "working"), session("b")))
        val stopped = agentsFeedNotice(before, mark(page(session("a", "offline"), session("b"))))
        assertEquals(1, stopped.offline)
        assertTrue(stopped.urgent)
        val history = agentsFeedNotice(before, mark(page(session("a", "working"), session("b"), session("old", "offline"))))
        assertEquals("a session the user never saw running did not stop", 0, history.offline)
    }

    @Test fun aComputerGoingDownCounts() {
        val up = page(session("a"), machines = listOf(mac))
        val down = page(session("a"), machines = listOf(mac.copy(online = false)))
        assertEquals(1, agentsFeedNotice(mark(up), mark(down)).offline)
        assertEquals("a computer already down is not news", 0, agentsFeedNotice(mark(down), mark(down)).offline)
    }

    @Test fun newRowsAreCountedInAWholeListButNotInAPagedOne() {
        val before = page(session("a"))
        val after = page(session("a"), session("b"), session("c"))
        assertEquals(2, agentsFeedNotice(mark(before), mark(after)).fresh)
        val pagedBefore = page(session("a"), paged = true)
        val pagedAfter = page(session("a"), session("b"), session("c"), paged = true)
        assertEquals("a fetched page is history, not news", 0, agentsFeedNotice(mark(pagedBefore), mark(pagedAfter)).fresh)
        assertFalse(agentsFeedNotice(mark(before), mark(after)).urgent)
    }

    @Test fun aRowThatLeavesIsNotNews() {
        assertTrue(agentsFeedNotice(mark(page(session("a"), session("b"))), mark(page(session("a")))).isEmpty)
    }

    @Test fun requestSessionsNamesTheSessionsThatWait() {
        val asks = listOf(Interaction(id = "i1", machineId = "mac", sessionId = "a"), Interaction(id = "i2", machineId = "mac", sessionId = "a"))
        assertEquals(setOf("mac/a"), requestSessions(asks))
    }

    // Resuming a rebuilt list

    @Test fun aSavedRowIsPutBackWhereItStood() {
        val keys = listOf("machine-header:mac", "session:mac/a", "session:mac/b")
        assertEquals(ReadingRestore.Scroll(2, -120), readingRestore(SavedReadingAnchor("session:mac/b", -120), keys, loaded = true))
        assertEquals("still loading: the row may yet come", ReadingRestore.Scroll(1, 0), readingRestore(SavedReadingAnchor("session:mac/a", 0), keys, loaded = false))
    }

    @Test fun aRowThatIsNotThereStartsFromTheTopOnceTheListIsIn() {
        val keys = listOf("session:mac/a")
        val saved = SavedReadingAnchor("session:mac/gone", 40)
        assertEquals(ReadingRestore.Top, readingRestore(saved, keys, loaded = true))
        assertEquals("a loaded list that emptied has no place to return to", ReadingRestore.Top, readingRestore(saved, emptyList(), loaded = true))
        assertEquals(ReadingRestore.Wait, readingRestore(saved, keys, loaded = false))
        assertEquals(ReadingRestore.Wait, readingRestore(saved, emptyList(), loaded = false))
    }

    @Test fun theSavedPlaceIsTheFirstTreeRowInViewNotAHeaderAboveTheTree() {
        val treeKeys = setOf("session:mac/a", "session:mac/b")
        val items = listOf<Pair<Any, Int>>("search" to -30, "notice" to -10, "session:mac/a" to 24, "session:mac/b" to 96)
        assertEquals(SavedReadingAnchor("session:mac/a", 24), savedAnchorOf(items, treeKeys))
        assertNull("only headers in view", savedAnchorOf(items.take(2), treeKeys))
        assertNull(savedAnchorOf(emptyList(), treeKeys))
    }

    @Test fun aRowClippedAboveTheViewportKeepsItsNegativeOffset() {
        assertEquals(SavedReadingAnchor("session:mac/a", -35), savedAnchorOf(listOf("session:mac/a" to -35), setOf("session:mac/a")))
    }

    @Test fun theHoldIsBoundedAndSettlesBeforeItsCap() {
        // Configured targets, not measurements: the list rests for half a second, and nothing waits past five seconds.
        assertEquals(500L, AgentsFeedTiming.SETTLE_MILLIS)
        assertEquals(5_000L, AgentsFeedTiming.CAP_MILLIS)
        assertTrue(AgentsFeedTiming.SETTLE_MILLIS < AgentsFeedTiming.CAP_MILLIS)
    }
}
