package dev.persona.watch.data

import org.junit.Assert.*
import org.junit.Test

class HubGroupsTest {
    private fun sample() = WatchSnapshot(
        listOf(WatchSession("computer", "same-session", "Sample", "working", canSend = true, canInterrupt = true, canApprove = true)),
        listOf(WatchInteraction("same-request", "computer", "same-session", "plan", "Sample plan", "Review", canAnswer = true, canCancel = true)),
        listOf(WatchMachine("computer", "Sample computer", true)),
        listOf(InboxItem("mail", "Sample item", "Preview", "mail", true)), 20)

    @Test fun collidingServerIdsRetainDistinctOwnersAndMatchTheirOwnSession() {
        val merged = HubGroups.merge(listOf(HubGroupSnapshot("", "", sample(), true), HubGroupSnapshot("second", "Other", sample(), true)), 30)
        assertEquals(2, merged.sessions.map { it.key }.distinct().size)
        assertEquals(2, merged.interactions.map { it.key }.distinct().size)
        assertEquals(merged.sessions.map { it.key }, merged.interactions.map { it.sessionKey })
        assertEquals(listOf("", "second"), merged.sessions.map { it.groupId })
        assertEquals("Other", merged.interactions.last().groupName)
        assertEquals(1, merged.inbox.size)
    }

    @Test fun offlineGroupPreservesContentButNeverEnablesCachedMutations() {
        val merged = HubGroups.merge(listOf(HubGroupSnapshot("", "", sample(), true), HubGroupSnapshot("lost", "Lost", sample(), false)), 30)
        assertTrue(merged.sessions.first().canSend)
        val lost = merged.sessions.last()
        assertEquals("offline", lost.state)
        assertFalse(lost.canSend); assertFalse(lost.canInterrupt); assertFalse(lost.canApprove)
        assertEquals("Sample", lost.title)
        assertFalse(merged.interactions.last().canAnswer)
        assertFalse(merged.interactions.last().canCancel)
        assertTrue(merged.interactions.first().canAnswer)
        assertTrue(merged.interactions.first().canCancel)
        assertFalse(merged.machines.last().online)
        assertFalse(merged.inboxUnavailable)
    }

    @Test fun offlinePrimaryNeverSubstitutesAnotherHubsInbox() {
        val merged = HubGroups.merge(listOf(HubGroupSnapshot("", "", null, false), HubGroupSnapshot("other", "Other", sample(), true)), 30)
        assertTrue(merged.inboxUnavailable)
        assertTrue(merged.inbox.isEmpty())
        assertEquals("other", merged.sessions.single().groupId)
    }

    @Test fun cachedPrimaryInboxIsExplicitlyStale() {
        val merged = HubGroups.merge(listOf(HubGroupSnapshot("", "", sample(), false)), 30)
        assertEquals(1, merged.inbox.size)
        assertTrue(merged.inboxUnavailable)
        assertFalse(merged.sessions.single().canSend)
    }

    @Test(expected = IllegalArgumentException::class) fun rejectsDuplicateGroupIdentity() {
        HubGroups.merge(listOf(HubGroupSnapshot("same", "One", sample(), true), HubGroupSnapshot("same", "Two", sample(), true)), 30)
    }
}
