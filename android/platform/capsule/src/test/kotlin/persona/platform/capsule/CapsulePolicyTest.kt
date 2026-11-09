package persona.platform.capsule

import org.junit.Assert.*
import org.junit.Test

class CapsulePolicyTest {
    @Test fun unreadKeepsAnInactiveCapsuleVisibleUntilReadWithoutBypassingHardGates() {
        assertTrue(CapsuleVisibility.visible(CapsuleMode.Active, false, true, false, unread = true))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Active, false, true, false, unread = false))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Off, false, true, false, unread = true))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Active, true, true, false, unread = true))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Active, false, false, false, unread = true))
    }
    @Test fun sectionModeUsesGlobalActivityEvenWhenTheSelectedListIsEmpty() {
        val sections = listOf(CapsuleSection("unread", "未读", 3), CapsuleSection("main", "主对话", list = false), CapsuleSection("inbox", "收件箱", 6))
        val global = CapsuleEntry("urgent", "Waiting", "", CapsuleState.NeedsYou)
        val content = CapsuleContent(sections = sections, sectionId = "unread", hasActivity = true, headline = global)
        assertTrue(content.active)
        assertTrue(content.listing)
        assertEquals(global, content.urgent)
        assertFalse(content.copy(sectionId = "main").listing)
        assertFalse(content.copy(selectedId = "chat").listing)
        assertFalse(content.copy(hasActivity = false, entries = listOf(global)).active)
    }
    @Test fun imeTopUsesDisplayCoordinatesInsteadOfSubtractingAClippedViewInsetFromTheScreen() {
        // Captured emulator geometry: display bottom 2400, IME top 1517, overlay y 940 + height 1365.
        assertEquals(1517, CapsuleIme.top(2400, 883, 940, 1365, 788, true, null))
        assertEquals(1517, CapsuleIme.top(2400, 0, 940, 1365, 788, true, null))
        // After moving above the keyboard, a zero local overlap keeps the same global keyboard edge.
        assertEquals(1517, CapsuleIme.top(2400, 0, 168, 1317, 0, true, 1517))
        assertNull(CapsuleIme.top(2400, 0, 168, 1317, 0, false, 1517))
    }
    @Test fun everyVisibilityGateAppliesToEveryMode() {
        for (mode in CapsuleMode.entries) for (foreground in listOf(false, true))
            for (permission in listOf(false, true)) for (active in listOf(false, true)) {
                val expected = !foreground && permission && when (mode) {
                    CapsuleMode.Off -> false
                    CapsuleMode.Active -> active
                    CapsuleMode.Always -> true
                }
                assertEquals("$mode foreground=$foreground permission=$permission active=$active", expected,
                    CapsuleVisibility.visible(mode, foreground, permission, active))
            }
    }
    @Test fun expandedActiveCardSurvivesTheLastTaskFinishingButStillHonorsVisibilityGates() {
        assertTrue(CapsuleVisibility.visible(CapsuleMode.Active, false, true, false, expanded = true))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Active, false, true, false, expanded = false))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Off, false, true, false, expanded = true))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Active, true, true, false, expanded = true))
        assertFalse(CapsuleVisibility.visible(CapsuleMode.Active, false, false, false, expanded = true))
    }
    @Test fun countBadgeDistinguishesTheMostUrgentClassFromAllActiveTasks() {
        val content = CapsuleContent(entries = listOf(
            CapsuleEntry("one", "One", "", CapsuleState.NeedsYou),
            CapsuleEntry("two", "Two", "", CapsuleState.NeedsYou),
            CapsuleEntry("three", "Three", "", CapsuleState.Working),
            CapsuleEntry("four", "Four", "", CapsuleState.Done),
            CapsuleEntry("idle", "Persona", "", CapsuleState.Idle),
        ))
        assertEquals(4, content.activeCount)
        assertEquals(2, content.urgentCount)
        assertEquals(0, CapsuleContent().urgentCount)
    }
    @Test fun freshInstallAndUnpairedContentStayDisabled() {
        assertEquals(CapsuleMode.Off, CapsuleMode.fromKey(null))
        assertFalse(CapsuleContent().available)
        assertFalse(CapsuleContent().active)
    }
    @Test fun needsYouWinsAndDoneRemainsAnActiveTask() {
        val idle = CapsuleEntry("idle", "Idle", "", CapsuleState.Idle)
        val done = CapsuleEntry("done", "Done", "", CapsuleState.Done)
        val working = CapsuleEntry("working", "Working", "", CapsuleState.Working)
        val waiting = CapsuleEntry("waiting", "Needs you", "", CapsuleState.NeedsYou)
        val content = CapsuleContent(entries = listOf(idle, done, working, waiting), selectedId = "done")
        assertEquals(waiting, content.urgent)
        assertEquals(done, content.selected)
        assertTrue(CapsuleContent(entries = listOf(done)).active)
        assertFalse(CapsuleContent(entries = listOf(idle)).active)
    }
    @Test fun removedSelectionDoesNotSelectAnotherConversation() {
        val content = CapsuleContent(listOf(CapsuleEntry("another", "Other", "", CapsuleState.Working)), selectedId = "gone")
        assertNull(content.selected)
    }
    @Test fun dockingSnapsAndPersistsTheRelativeHeightAfterRotation() {
        val portrait = CapsuleArea(12, 36, 388, 780)
        val dock = portrait.dock(x = 220, y = 336, width = 48, height = 48)
        assertTrue(dock.right)
        assertEquals(340, portrait.position(dock, 48, 48).first)
        assertEquals(336, portrait.position(dock, 48, 48).second)
        val landscape = CapsuleArea(36, 12, 780, 388)
        val (x, y) = landscape.position(dock, 48, 48)
        assertEquals(732, x)
        assertTrue(y in 12..340)
        assertEquals(dock.fraction, (y - 12f) / (388 - 12 - 48), .01f)
    }
    @Test fun draggingBeyondSafeAreaClampsBothEnds() {
        val area = CapsuleArea(10, 20, 300, 500)
        val topLeft = area.dock(-100, -400, 48, 48)
        assertFalse(topLeft.right)
        assertEquals(10 to 20, area.position(topLeft, 48, 48))
        val bottomRight = area.dock(900, 1000, 48, 48)
        assertTrue(bottomRight.right)
        assertEquals(252 to 452, area.position(bottomRight, 48, 48))
    }
    @Test fun oversizedCardPinsToSafeTopLeftInsteadOfLeavingTheDisplay() {
        val area = CapsuleArea(10, 20, 200, 300)
        assertEquals(10 to 20, area.position(CapsuleDock(true, 1f), 400, 600))
        assertEquals(0f, area.dock(20, 100, 400, 600).fraction)
    }
}
