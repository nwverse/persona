package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.InboxEventFields
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.InboxEntry
import persona.core.domain.inbox.InboxItem
import persona.core.navigation.InboxItemRoute
import persona.core.navigation.LarkChatRoute
import persona.feature.inbox.InboxEventRoute
import persona.feature.inbox.InboxItemPages
import persona.feature.inbox.InboxRecord
import persona.feature.inbox.InboxRepository
import persona.feature.inbox.InboxSnapshot
import persona.feature.inbox.LarkConversation
import persona.feature.inbox.LarkPart
import persona.feature.inbox.LarkRoute
import persona.feature.inbox.ReplyDraft
import persona.feature.inbox.ReplyRequest
import persona.feature.inbox.ReplyTarget
import java.io.IOException

/** Deep links resolve a historical item through the same lookup and states as the event page. */
class InboxHistoryLinksTest {
    private fun record(event: Boolean) = InboxRecord(
        InboxItem("history", kind = "dm", title = "Historical event", status = if (event) "action" else null),
        feedId = "room", source = "lark", event = if (event) InboxEventFields(status = "action") else InboxEventFields.None,
    )

    private class LookupInbox(var record: InboxRecord?, var failure: IOException? = null) : InboxRepository {
        override val entries = MutableStateFlow(emptyList<InboxEntry>())
        override val snapshot = MutableStateFlow(InboxSnapshot())
        override val connectionState = flowOf(ConnectionState.Online)
        val calls = mutableListOf<String>()
        override suspend fun item(itemId: String): InboxRecord? {
            calls += itemId
            failure?.let { throw it }
            return record
        }
        override suspend fun refresh() = Unit
        override fun conversation(feedId: String, startPosition: Long?) = flowOf(LarkConversation())
        override suspend fun loadOlder(feedId: String) = Unit
        override suspend fun refreshConversation(feedId: String) = Unit
        override suspend fun setDone(itemId: String, done: Boolean) = Unit
        override suspend fun readDraft(target: ReplyTarget): ReplyDraft? = null
        override suspend fun saveDraft(target: ReplyTarget, draft: ReplyDraft) = Unit
        override suspend fun reply(target: ReplyTarget, request: ReplyRequest) = Unit
        override suspend fun downloadFile(file: LarkPart.File): String = error("not used")
    }

    private suspend fun resolve(key: NavKey, inbox: LookupInbox, records: List<InboxRecord> = emptyList(), loaded: Boolean = true) =
        inboxDestinationWithLookup(key, records, loaded, InboxItemPages(), inbox)

    @Test fun aListedEventNeedsNoSingleRead() = runTest {
        val inbox = LookupInbox(null, IOException("must not be called"))
        assertEquals(InboxDestination.Open(InboxEventRoute("history")), resolve(InboxItemRoute("history"), inbox, listOf(record(true))))
        assertTrue(inbox.calls.isEmpty())
    }

    @Test fun aHistoricalEventOpensItsEventPage() = runTest {
        val inbox = LookupInbox(record(true))
        assertEquals(InboxDestination.Open(InboxEventRoute("history")), resolve(InboxItemRoute("history"), inbox))
        assertEquals(listOf("history"), inbox.calls)
    }

    @Test fun aHistoricalChatItemWithoutEventsOpensItsCollapsedPage() = runTest {
        val inbox = LookupInbox(record(false))
        assertEquals(InboxDestination.Open(InboxEventRoute("history")), resolve(InboxItemRoute("history"), inbox))
    }

    @Test fun missingAndFailureStayDistinct() = runTest {
        assertEquals(InboxDestination.Missing, resolve(InboxItemRoute("history"), LookupInbox(null)))
        assertEquals(InboxDestination.Failed, resolve(InboxItemRoute("history"), LookupInbox(null, IOException("offline"))))
    }

    /** #9053: the item a link opens moves the Inbox list to where it is, listed, read by id, or a chat's current item. */
    @Test fun theItemALinkOpensLandsTheList() = runTest {
        val done = InboxRecord(InboxItem("history", kind = "dm", title = "Historical event", doneAt = "2026-11-01T10:00:00Z"),
            feedId = "room", source = "lark")
        val collapsed = InboxRecord(InboxItem("note", kind = "dm", title = "A notice", tier = "noise"), feedId = "notices", source = "lark")
        val landing = persona.feature.inbox.InboxLanding()
        inboxDestinationWithLookup(InboxItemRoute("history"), emptyList(), true, InboxItemPages(), LookupInbox(done), found = landing::land)
        assertEquals(persona.core.domain.inbox.InboxBar.Landing("done", false), landing.take())
        inboxDestinationWithLookup(InboxItemRoute("note"), listOf(collapsed), true, InboxItemPages(), LookupInbox(null), found = landing::land)
        assertEquals(persona.core.domain.inbox.InboxBar.Landing("all", true), landing.take())
        inboxDestinationWithLookup(LarkChatRoute("notices"), listOf(collapsed), true, InboxItemPages(), LookupInbox(null), found = landing::land)
        assertEquals(persona.core.domain.inbox.InboxBar.Landing("all", true), landing.take())
        // Nothing found, nothing moves.
        inboxDestinationWithLookup(InboxItemRoute("gone"), emptyList(), true, InboxItemPages(), LookupInbox(null), found = landing::land)
        assertNull(landing.take())
    }

    /**
     * #9053: a stack that let go of its Inbox list (the drawer, a link home) drops the landing nobody took, so a list
     * entered later opens on Needs you; a stack still holding the list under the item keeps it for Back. (Putting a
     * choice back when a page opened beside the list closes: InboxSectionBarTest.)
     */
    @Test fun aLandingNobodyTookIsDropped() {
        val done = InboxRecord(InboxItem("history", kind = "dm", title = "Historical event", doneAt = "2026-11-01T10:00:00Z"),
            feedId = "room", source = "lark")
        val landings = persona.core.navigation.AppTab.entries.associateWith { persona.feature.inbox.InboxLanding().apply { land(done) } }
        settleLandings(mapOf(
            persona.core.navigation.AppTab.Persona to listOf(persona.core.navigation.AssistantRoute),
            persona.core.navigation.AppTab.Agents to listOf(persona.core.navigation.AgentsRoute, persona.core.navigation.InboxRoute, InboxItemRoute("history")),
        ), landings)
        assertNull(landings.getValue(persona.core.navigation.AppTab.Persona).pending)
        assertEquals(persona.core.domain.inbox.InboxBar.Landing("done", false), landings.getValue(persona.core.navigation.AppTab.Agents).pending)
    }

    @Test fun loadingWaitsAndAChatLinkSkipsTheItemLookup() = runTest {
        val inbox = LookupInbox(null, IOException("must not be called"))
        assertNull(resolve(InboxItemRoute("history"), inbox, loaded = false))
        assertEquals(InboxDestination.Open(LarkRoute("room")), resolve(LarkChatRoute("room"), inbox))
        assertTrue(inbox.calls.isEmpty())
    }
}
