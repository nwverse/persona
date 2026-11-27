package persona.core.domain.sort

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Session

/** The agents of one herdr tab (#2320): pane order, the initiator, the group (as web/src/lib/herdr-tree.ts). */
class AgentTabTest {
    private fun s(id: String, pane: String? = null, place: Int? = null, slot: String? = "t1", state: String = "idle",
                  space: String = "w1", index: Int? = 2, machine: String = "mac") =
        Session(machineId = machine, sessionId = id, paneId = pane, paneIndex = place, slotId = slot, slotIndex = index,
            spaceId = space, spaceName = "app", state = state)

    private fun row(session: Session) = AgentSession(session)

    @Test fun paneNumbersAreHerdrsBase36CountOrHerdrAppsTerminalNumber() {
        assertEquals(32L, paneNumber(s("a", pane = "w4R:pW")))
        assertEquals(86L, paneNumber(s("a", pane = "w4R:p2E")))
        assertEquals(2L, paneNumber(s("a", pane = "herdr-core:t2")))
        assertEquals(Long.MAX_VALUE, paneNumber(s("a", pane = "botmux:abc-def")))
        assertEquals(Long.MAX_VALUE, paneNumber(s("a")))
    }

    @Test fun panesGoByPlaceThenTheOrderHerdrOpenedThemThenId() {
        val panes = listOf(s("c", pane = "w1:p3A"), s("b", pane = "w1:p2E", place = 1), s("a", pane = "w1:pW", place = 0),
            s("e", pane = "w1:p31"), s("d"))
        assertEquals(listOf("a", "b", "e", "c", "d"), panes.sortedWith(PaneOrder).map { it.sessionId })
    }

    @Test fun theLauncherIsTheTopLeftAgentOfATabOfSeveral() {
        val lead = s("a", place = 0)
        assertEquals(lead, launcherOf(listOf(s("b", place = 1), lead)) { it })
        // one agent: no group, no initiator
        assertNull(launcherOf(listOf(lead)) { it })
        // the top left pane is a shell: the agents are peers
        assertNull(launcherOf(listOf(s("b", place = 1), s("c", place = 2))) { it })
        assertNull(launcherOf(listOf(s("b"), s("c"))) { it })
    }

    @Test fun tabsOfSeveralAgentsGroupByMachineSpaceAndSlot() {
        val rows = listOf(
            row(s("b", place = 1, state = "needs_you")), row(s("a", place = 0, state = "working")), row(s("c", place = 2, state = "unread")),
            row(s("solo", slot = "t2", index = 3)), row(s("other", space = "w2")), row(s("box", machine = "box")),
            row(s("loose", slot = null)),
        )
        val tabs = agentTabs(rows)
        assertEquals(setOf("mac/:w1/t1"), tabs.keys)
        val tab = tabs.getValue("mac/:w1/t1")
        assertEquals(listOf("a", "b", "c"), tab.members.map { it.session.sessionId })
        assertEquals("a", tab.launcher?.session?.sessionId)
        assertEquals("b", tab.lead.session.sessionId) // needs you is the most urgent
        assertFalse(tab.isBranch(tab.members[0]))
        assertTrue(tab.isBranch(tab.members[1]))
        // kept by the tab's number, not its name or ids
        assertEquals("tab:mac/app/2", tab.foldKey)
        assertEquals("tab:mac/app/2", agentTabs(tab.members.map { row(it.session.copy(slotId = "t9", slotName = "改名了", spaceId = "w7")) })
            .values.single().foldKey)
    }

    @Test fun theLeadIsWorkingBeforeUnreadAsOnTheWeb() {
        val tab = agentTabs(listOf(row(s("a", place = 0, state = "unread")), row(s("b", place = 1, state = "working")))).values.single()
        assertEquals("b", tab.lead.session.sessionId)
        val even = agentTabs(listOf(row(s("b", place = 1, state = "idle")), row(s("a", place = 0, state = "idle")))).values.single()
        assertEquals("a", even.lead.session.sessionId) // equals: the first in pane order
    }

    @Test fun aTabWaitingOnTheUsersReplySpeaksForItOverTheSeenOnes() {
        // 待你回复 (#5096) comes after 完成未看 and before 已看: over a seen agent earlier in pane order
        val asks = row(s("b", place = 1).copy(awaitsReply = true, asks = "要不要推到 beta？"))
        val seen = listOf(row(s("a", place = 0, state = "idle")), asks)
        assertEquals("b", agentTabs(seen).values.single().lead.session.sessionId)
        // a muted one is just 已看: the first in pane order leads again
        assertEquals("a", agentTabs(seen, AgentPreferences(sessions = listOf("mac/b"))).values.single().lead.session.sessionId)
        // unread, working and needs-you agents still come first
        listOf("unread", "working", "needs_you").forEach { state ->
            assertEquals(state, "c", agentTabs(seen + row(s("c", place = 2, state = state))).values.single().lead.session.sessionId)
        }
    }

    @Test fun slotLabels() {
        assertEquals("#2", s("a").slotLabel)
        assertEquals("#2", s("a").copy(slotName = "2").slotLabel)
        assertEquals("api", s("a").copy(slotName = "api").slotLabel)
        assertNull(s("a", index = null).slotLabel)
    }

    @Test fun aSpaceListsEachTabsAgentsInPaneOrder() {
        val space = agentSpaces(listOf(row(s("c", place = 2)), row(s("z", slot = "t0", index = 1)), row(s("b", pane = "w1:p2E")),
            row(s("a", place = 0)))).single()
        assertEquals(listOf("z", "a", "c", "b"), space.members.map { it.session.sessionId })
    }
}
