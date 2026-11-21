package dev.persona.watch.data

import org.junit.Assert.*
import org.junit.Test

class DraftScopeTest {
    @Test fun primaryInboxDraftAndOperationDoNotDependOnAdditionalGroups() {
        for (key in listOf("inbox:manager:item", "send:inbox:manager:item:reply")) {
            assertEquals(DraftScope.hash("http://primary.local", emptySet(), key), DraftScope.hash("http://primary.local", setOf("other"), key))
            assertNotEquals(DraftScope.hash("http://primary.local", emptySet(), key), DraftScope.hash("http://replacement.local", emptySet(), key))
        }
    }
    @Test fun explicitlyOwnedDraftsRemainStableAcrossMembershipChangesAndWithoutPrimary() {
        for (key in listOf("session:same", "send:session:same:text", "inbox:manager:same", "send:inbox:manager:same:text")) {
            assertEquals(DraftScope.ownedHash("", "one", key), DraftScope.ownedHash("http://new-primary.local", "one", key))
            assertNotEquals(DraftScope.ownedHash("", "one", key), DraftScope.ownedHash("", "two", key))
        }
    }
    @Test fun noPrimaryDoesNotExposeAdditionalInboxOrAllowUnownedInboxKeys() {
        val inbox = InboxItem("same", "Sample", "Preview", "mail", true)
        val value = WatchSnapshot(emptyList(), emptyList(), emptyList(), listOf(inbox), 1)
        val merged = HubGroups.merge(listOf(HubGroupSnapshot("one", "One", value, true), HubGroupSnapshot("two", "Two", value, true)), 1)
        assertTrue(merged.inbox.isEmpty())
        assertTrue(merged.inboxUnavailable)
        assertThrows(HubException::class.java) { DraftScope.ownedHash("", "", "inbox:manager:same") }
    }
    @Test fun changingPrimaryDoesNotMoveAnotherHubsDraftOrPendingOperation() {
        for (prefix in listOf("session:", "send:session:", "continue:", "resolve:")) {
            val key = prefix + "group-a\u0000computer\u0000conversation"
            assertEquals(DraftScope.hash("http://first.local", setOf("group-a"), key), DraftScope.hash("http://second.local", setOf("group-a"), key))
        }
    }
    @Test fun primaryDraftsRemainScopedToTheirOwnHub() {
        val key = "session:computer\u0000conversation"
        assertNotEquals(DraftScope.hash("http://first.local", emptySet(), key), DraftScope.hash("http://second.local", emptySet(), key))
    }
    @Test fun sameServerIdsCannotCollideAcrossGroups() {
        val groups = setOf("one", "two")
        assertNotEquals(DraftScope.hash("http://hub.local", groups, "session:one\u0000same"), DraftScope.hash("http://hub.local", groups, "session:two\u0000same"))
    }
}
