package persona.feature.chat

import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*

class TeamChatTest {
    private val lead = Session("server", "lead", children = SessionChildren(workers = 2))
    private val first = Session("server", "one", foldedUnder = "lead", foldedAs = FoldedAs.WORKER)
    private val second = first.copy(sessionId = "two")
    private val other = first.copy(machineId = "server~g00000002")
    private fun card(s: Session) = Interaction(s.key.path, machineId = s.machineId, sessionId = s.sessionId,
        kind = "permission", foldedUnder = s.foldedUnder, foldedAs = s.foldedAs)
    private val rows = listOf(lead, first, second, other)
    private val cards = rows.map(::card)

    @Test fun exactColdLookupFindsLeadAndRequestedMemberWhileOrdinaryAndSideLinksKeepTheirDestinations() = runTest {
        val reads = mutableListOf<SessionKey>()
        val home = resolveChildSessionHome(first.key, emptyList()) { target ->
            reads += target
            rows.firstOrNull { it.key == target }
        }
        assertEquals(listOf(first.key, lead.key), reads)
        assertEquals(lead.key, home.key)
        assertEquals(first.key, home.shownWorker(emptyList()))
        assertNull(home.shownWorker(listOf(first.copy(foldedAs = null))))
        val through = first.copy(foldedSide = "side")
        assertNull(home.shownWorker(listOf(through)))
        assertEquals(SessionKey("server", "side"), home.shownSide(listOf(through), listOf("sessions_side_fold")))
        assertNull(childSessionHome(other.key, other, lead).shownWorker(rows))
    }

    @Test fun permissionMovesBetweenRealWorkerAndLeadWithoutCrossingPairingOrDuplicating() {
        fun leadCards(shown: SessionKey?) = chatPageCards(cards, lead.key, emptySet(), true, false, shown)
        val shown = chatPageCards(cards, first.key, emptySet(), true, false, workerPage = true)
        assertEquals(listOf(card(lead), card(first), card(second)), leadCards(null))
        assertEquals(listOf(card(lead), card(second)), leadCards(first.key))
        assertEquals(listOf(card(lead), card(first)), leadCards(second.key))
        assertEquals(listOf(card(lead), card(first), card(second)), leadCards(other.key))
        assertEquals(card(first).id, shown.single().id)
        assertEquals(first.key, shown.single().sessionKey)
        val original = shown.single()
        assertSame(cards[1], original)
        assertTrue(persona.core.domain.ChatTaps().canTap(original, cards, java.time.Instant.now()))
        assertNull(original.onWorkerPage(first.key).foldedAs)
        assertSame(original, original.onWorkerPage(other.key))
        assertTrue(leadCards(first.key).intersect(shown.toSet()).isEmpty())
    }

    @Test fun arrivalManualCloseNewTeamAndDirectLinkHaveDistinctBehaviorAtEachWidth() {
        assertTrue(revealTeam(3, null, wide = true, closed = false, asked = false))
        assertFalse(revealTeam(3, null, wide = true, closed = true, asked = false))
        assertTrue(revealTeam(1, 0, wide = true, closed = true, asked = false))
        assertFalse(revealTeam(4, 3, wide = true, closed = true, asked = false))
        assertFalse(revealTeam(1, 0, wide = false, closed = false, asked = false))
        assertFalse(revealTeam(3, null, wide = false, closed = false, asked = false))
        assertTrue(revealTeam(3, null, wide = false, closed = true, asked = true))
        assertFalse(revealTeam(0, 3, wide = true, closed = false, asked = true))
    }
}
