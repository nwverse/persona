package persona.android.capsule

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.async
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import persona.platform.capsule.*

/** V7 regressions exercise the coordinator against independent, observable repository boundaries. */
@OptIn(ExperimentalCoroutinesApi::class)
class CapsuleConversationsSourceTest {
    private fun entry(id: String, unread: Boolean = true, detail: Boolean = false) =
        CapsuleEntry(id, "Title $id", "Preview $id", CapsuleState.NeedsYou, unread = unread, detailOnly = detail)

    @Test fun oneGlobalQueueDeduplicatesBothSourcesAndCountsConversationsRatherThanMessages() = runTest {
        val agents = Source(listOf(entry("agent"), entry("shared")))
        val inbox = Source(listOf(entry("shared"), entry("inbox").copy(unreadCount = 17)))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        runCurrent()

        val value = source.content.value
        assertEquals(listOf("agent", "shared", "inbox"), value.unreadEntries.map { it.id })
        assertEquals(3, value.badgeCount)
        assertEquals(3, value.sections.first().count)
        // 收件箱 joined 未读 (a grouped list) as its 飞书消息 group.
        assertEquals(listOf(UnreadSection, MainSection), value.sections.map { it.id })
        assertTrue(value.sections.first().list)
        assertEquals(listOf("agent", "shared", LarkGroup), value.unreadEntries.map { it.group ?: it.id })
        assertTrue(value.hasActivity)
    }

    @Test fun mainInboxAndUnreadEntrancesUseTheirOwnSourceWithoutLosingTheGlobalBadge() = runTest {
        val agents = Source(listOf(entry("agent"), entry(MainEntry, unread = false)))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        runCurrent()

        source.selectSection(MainSection)
        runCurrent()
        assertEquals(MainEntry, source.content.value.selectedId)
        assertEquals(MainSection, source.content.value.sectionId)
        assertEquals(listOf(MainSection), agents.sections)
        assertEquals(true, agents.visibility.last())
        assertEquals(false, inbox.visibility.last())
        assertEquals(2, source.content.value.badgeCount)

        // 未读 opens its grouped list; picking a Feishu conversation uses the inbox source, and back returns to the list.
        source.selectSection(UnreadSection)
        runCurrent()
        assertEquals(UnreadSection, source.content.value.sectionId)
        assertNull(source.content.value.selectedId)
        assertTrue(source.content.value.listing)
        assertEquals(2, source.content.value.badgeCount)

        source.select("inbox")
        runCurrent()
        assertEquals("inbox", source.content.value.selectedId)
        assertEquals(UnreadSection, source.content.value.sectionId)
        assertEquals(false, agents.visibility.last())
        assertEquals(true, inbox.visibility.last())

        source.select(null)
        runCurrent()
        assertTrue(source.content.value.listing)
        assertEquals(2, source.content.value.badgeCount)
    }

    @Test fun aPassedConversationReturnsOnlyWhenItsVersionMovesNotWhenItsSummaryChanges() = runTest {
        val agents = Source(listOf(entry("agent").copy(version = "v1"), entry("other")))
        val inbox = Source(emptyList())
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        runCurrent()
        source.perform("agent", "next")
        runCurrent()
        // The hub still lists it as unread for a while (Feishu's topic replies keep it unread for good).
        fun hubLists(entry: CapsuleEntry) = agents.content.update { value ->
            value.copy(unreadEntries = value.unreadEntries.filterNot { it.id == entry.id } + entry) }
        val passed = entry("agent").copy(version = "v1")
        hubLists(passed)
        runCurrent()
        assertFalse(source.content.value.unreadEntries.any { it.id == "agent" })

        // A topic group's preview switches between its newest message and its name: still the same conversation.
        hubLists(passed.copy(summary = "Group name"))
        runCurrent()
        assertFalse(source.content.value.unreadEntries.any { it.id == "agent" })

        // Something new arrived: its version moved, so it is unread again.
        hubLists(passed.copy(version = "v2"))
        runCurrent()
        assertTrue(source.content.value.unreadEntries.any { it.id == "agent" })
    }

    @Test fun fromTheMainChatNextAndTheListBothOpenTheUnreadConversation() = runTest {
        val agents = Source(listOf(entry("agent"), entry(MainEntry, unread = false)))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.selectSection(MainSection)
        runCurrent()
        assertEquals(MainEntry, source.content.value.selectedId)

        // 下一条 in the main chat goes to the first unread conversation and leaves 主对话.
        source.perform(MainEntry, "next")
        runCurrent()
        assertEquals("agent", source.content.value.selectedId)
        assertEquals(UnreadSection, source.content.value.sectionId)

        // Back in the main chat, picking a conversation from the list opens it too.
        source.selectSection(MainSection)
        runCurrent()
        source.select("inbox")
        runCurrent()
        assertEquals("inbox", source.content.value.selectedId)
        assertEquals(UnreadSection, source.content.value.sectionId)
    }

    @Test fun automaticallyReadingTheVisibleReplyClearsOnlyItsDotAndNeverAdvances() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        runCurrent()

        assertTrue(source.viewed("agent", "reply-agent", "v1-agent"))
        runCurrent()
        assertEquals("agent", source.content.value.selectedId)
        assertEquals(listOf("inbox"), source.content.value.unreadEntries.map { it.id })
        assertEquals("Body agent", source.content.value.messages.single().text)
        assertFalse(source.content.value.selected!!.unread)
        assertEquals(1, source.content.value.badgeCount)
        assertTrue(inbox.receipts.isEmpty())
    }

    @Test fun autoReadAndTwoNextClicksShareOneReceiptBeforeAdvancingAcrossSources() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val gate = CompletableDeferred<Unit>()
        agents.read = { id, _, version -> gate.await(); agents.acknowledge(id, version); true }
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        runCurrent()

        val automatic = async { source.viewed("agent", "reply-agent", "v1-agent") }
        runCurrent()
        val next = async { source.perform("agent", "next") }
        runCurrent()
        val duplicateClick = async { source.perform("agent", "next") }
        runCurrent()
        assertEquals(1, agents.receipts.size)
        assertTrue(source.content.value.nextBusy)

        gate.complete(Unit)
        runCurrent()
        assertTrue(automatic.await())
        next.await()
        duplicateClick.await()
        assertEquals(1, agents.receipts.size)
        assertEquals("inbox", source.content.value.selectedId)
        assertEquals(listOf("inbox"), inbox.selections)
        assertEquals(1, source.content.value.badgeCount)
        assertFalse(source.content.value.nextBusy)
    }

    @Test fun nextFromAnInboxConversationCanAdvanceBackToAnAgent() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("inbox")
        runCurrent()

        source.perform("inbox", "next")
        runCurrent()
        assertEquals("agent", source.content.value.selectedId)
        assertEquals(listOf(Receipt("inbox", "reply-inbox", "v1-inbox")), inbox.receipts)
        assertTrue(agents.receipts.isEmpty())
        assertEquals("Body agent", source.content.value.messages.single().text)
    }

    @Test fun failedReceiptsKeepTheSameBodyAndUnreadAndCanBeRetried() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        agents.read = { _, _, _ -> false }
        runCurrent()

        source.perform("agent", "next")
        runCurrent()
        assertEquals("agent", source.content.value.selectedId)
        assertEquals(2, source.content.value.badgeCount)
        assertEquals("Body agent", source.content.value.messages.single().text)
        assertNotNull(source.content.value.nextError)
        assertFalse(source.content.value.nextBusy)
        assertTrue(inbox.selections.isEmpty())

        agents.read = { id, _, version -> agents.acknowledge(id, version); true }
        source.perform("agent", "next")
        runCurrent()
        assertEquals(2, agents.receipts.size)
        assertEquals("inbox", source.content.value.selectedId)
        assertNull(source.content.value.nextError)
    }

    @Test fun aNewRevisionArrivingDuringTheReceiptStaysVisibleInsteadOfBeingSkipped() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val gate = CompletableDeferred<Unit>()
        agents.read = { id, _, version -> gate.await(); agents.acknowledge(id, version); true }
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        runCurrent()

        val next = async { source.perform("agent", "next") }
        runCurrent()
        agents.revise("agent", "reply-agent-new", "New body", "v2-agent")
        runCurrent()
        gate.complete(Unit)
        runCurrent()
        next.await()

        assertEquals("agent", source.content.value.selectedId)
        assertEquals("New body", source.content.value.messages.single().text)
        assertEquals("v2-agent", source.content.value.readVersion)
        assertEquals(2, source.content.value.badgeCount)
        assertNotNull(source.content.value.nextError)
        assertTrue(inbox.selections.isEmpty())
    }

    @Test fun anUnreadWithoutAVerifiedReadVersionCannotBeSkippedByNext() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        agents.content.update { it.copy(readVersion = null) }
        runCurrent()

        source.perform("agent", "next")
        runCurrent()
        assertEquals("agent", source.content.value.selectedId)
        assertEquals(2, source.content.value.badgeCount)
        assertNotNull(source.content.value.nextError)
        assertTrue(agents.receipts.isEmpty())
        assertTrue(inbox.selections.isEmpty())
    }

    @Test fun cancellingNextDoesNotAdvanceOrConsumeTheUnreadVersion() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val gate = CompletableDeferred<Unit>()
        agents.read = { id, _, version -> gate.await(); agents.acknowledge(id, version); true }
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        runCurrent()

        val next = async { source.perform("agent", "next") }
        runCurrent()
        next.cancelAndJoin()
        gate.complete(Unit)
        runCurrent()
        assertEquals("agent", source.content.value.selectedId)
        assertEquals(2, source.content.value.badgeCount)
        assertEquals("v1-agent", source.content.value.readVersion)
        assertFalse(source.content.value.nextBusy)
        assertNull(source.content.value.nextError)
        assertTrue(inbox.selections.isEmpty())
    }

    @Test fun changingEntrancesWhileNextWaitsCannotReplaceTheNewSelection() = runTest {
        val agents = Source(listOf(entry("agent"), entry(MainEntry, unread = false)))
        val inbox = Source(listOf(entry("inbox")))
        val gate = CompletableDeferred<Unit>()
        agents.read = { id, _, version -> gate.await(); agents.acknowledge(id, version); true }
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        runCurrent()

        val next = async { source.perform("agent", "next") }
        runCurrent()
        source.selectSection(MainSection)
        runCurrent()
        gate.complete(Unit)
        runCurrent()
        next.await()
        assertEquals(MainEntry, source.content.value.selectedId)
        assertEquals(MainSection, source.content.value.sectionId)
        assertTrue(inbox.selections.isEmpty())
        assertFalse(source.content.value.nextBusy)
    }

    @Test fun collapsingWhileNextWaitsCannotNavigateToTheNextUnreadOnReopen() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val gate = CompletableDeferred<Unit>()
        agents.read = { id, _, version -> gate.await(); agents.acknowledge(id, version); true }
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("agent")
        runCurrent()

        val next = async { source.perform("agent", "next") }
        runCurrent()
        source.shown(false)
        gate.complete(Unit)
        runCurrent()
        next.await()
        source.shown(true)
        runCurrent()

        assertEquals("agent", source.content.value.selectedId)
        assertEquals("Body agent", source.content.value.messages.single().text)
        assertFalse(source.content.value.nextBusy)
        assertTrue(inbox.selections.isEmpty())
    }

    @Test fun readingTheFinalUnreadKeepsItsBodyInsteadOfReturningToTheMainChat() = runTest {
        val agents = Source(listOf(entry("last")))
        val inbox = Source(emptyList())
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("last")
        runCurrent()

        source.perform("last", "next")
        runCurrent()
        val value = source.content.value
        assertEquals("last", value.selectedId)
        assertEquals(listOf("last"), value.entries.map { it.id })
        assertEquals("Body last", value.messages.single().text)
        assertFalse(value.selected!!.unread)
        assertEquals(0, value.badgeCount)
        assertTrue(value.unreadEntries.isEmpty())
        assertFalse(value.nextAvailable)
        assertNull(value.nextError)
    }

    @Test fun draftsAndSendsStayWithTheSelectedRepositoryAndReplyTarget() = runTest {
        val agents = Source(listOf(entry("agent"), entry(MainEntry, unread = false)))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("inbox")
        runCurrent()

        source.updateDraft("inbox", "Source draft")
        source.perform("inbox", "target:persona")
        source.updateDraft("inbox", "Private question")
        source.send("inbox", "Private question")
        source.perform("inbox", "target:source")
        source.send("inbox", "Source draft")
        source.selectSection(MainSection)
        runCurrent()
        source.updateDraft(MainEntry, "Main draft")
        source.send(MainEntry, "Main draft")

        assertEquals(listOf(Send("inbox", "persona", "Private question"), Send("inbox", "source", "Source draft")), inbox.sends)
        assertEquals(listOf(Send(MainEntry, "source", "Main draft")), agents.sends)
        assertEquals("Source draft", inbox.drafts["inbox" to "source"])
        assertEquals("Private question", inbox.drafts["inbox" to "persona"])
        assertFalse(agents.drafts.keys.any { it.first == "inbox" })
        assertFalse(inbox.drafts.keys.any { it.first == MainEntry })
    }

    @Test fun detailItemsKeepTheirOriginalRouteWhileChatExpansionPreservesTheSource() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox"), entry("document", detail = true)))
        var expansions = 0
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) { expansions++ }
        source.selectSection(UnreadSection)
        runCurrent()

        source.open("document")
        assertEquals(listOf("document"), inbox.opened)
        assertEquals(0, expansions)
        source.select("inbox")
        runCurrent()
        source.open("inbox")
        assertEquals(1, expansions)
        assertEquals("inbox", source.content.value.selectedId)
        assertEquals(listOf("document"), inbox.opened)
        assertTrue(agents.opened.isEmpty())
    }

    @Test fun fullScreenAndOverlayHandoffKeepOnlyTheSelectedRepositoryObserving() = runTest {
        val agents = Source(listOf(entry("agent")))
        val inbox = Source(listOf(entry("inbox")))
        val source = CapsuleConversationsSource(agents, inbox, backgroundScope) {}
        source.shown(true)
        source.select("inbox")
        source.fullScreen(true)
        source.shown(false)
        runCurrent()
        assertTrue(inbox.visibility.last())
        assertFalse(agents.visibility.last())

        source.fullScreen(false)
        assertFalse(inbox.visibility.last())
        assertFalse(agents.visibility.last())
        source.shown(true)
        runCurrent()
        assertTrue(inbox.visibility.last())
        assertEquals("inbox", source.content.value.selectedId)
        assertEquals("Body inbox", source.content.value.messages.single().text)
    }

    private data class Receipt(val id: String?, val message: String, val version: String)
    private data class Send(val id: String?, val target: String, val text: String)
    private data class Page(val reply: CapsuleMessage, val version: String)

    private class Source(entries: List<CapsuleEntry>) : CapsuleContentSource {
        override val content = MutableStateFlow(CapsuleContent(entries = entries, available = true,
            sectionId = UnreadSection, unreadEntries = entries.filter { it.unread }, hasActivity = entries.any { it.unread },
            noticeTargetIds = entries.map { it.id }.toSet()))
        override val completions = MutableSharedFlow<CapsuleCompletion>()
        val selections = mutableListOf<String?>()
        val sections = mutableListOf<String>()
        val visibility = mutableListOf<Boolean>()
        val receipts = mutableListOf<Receipt>()
        val sends = mutableListOf<Send>()
        val opened = mutableListOf<String?>()
        val drafts = mutableMapOf<Pair<String?, String>, String>()
        private val pages = entries.associate { it.id to Page(CapsuleMessage("reply-${it.id}", "Body ${it.id}", false, unread = it.unread), "v1-${it.id}") }.toMutableMap()
        var read: suspend (String?, String, String) -> Boolean = { id, _, version -> acknowledge(id, version); true }

        override fun shown(expanded: Boolean) { visibility += expanded }
        override fun selectSection(id: String) {
            sections += id
            select(if (id == MainSection) MainEntry else null)
            content.update { it.copy(sectionId = id) }
        }
        override fun select(id: String?) {
            selections += id
            check(id == null || content.value.entries.any { it.id == id })
            val page = pages[id]
            content.update { value -> value.copy(selectedId = id, messages = listOfNotNull(page?.reply),
                readVersion = page?.version?.takeIf { value.entries.any { it.id == id && it.unread } },
                replyTargetId = "source", draftText = drafts[id to "source"].orEmpty()) }
        }
        fun acknowledge(id: String?, version: String) {
            if (pages[id]?.version != version) return
            pages[id]?.let { pages[id!!] = it.copy(reply = it.reply.copy(unread = false)) }
            content.update { value ->
                val entries = value.entries.map { if (it.id == id) it.copy(unread = false) else it }
                value.copy(entries = entries, unreadEntries = entries.filter { it.unread },
                    readVersion = value.readVersion.takeUnless { value.selectedId == id },
                    messages = if (value.selectedId == id) value.messages.map { it.copy(unread = false) } else value.messages)
            }
        }
        fun revise(id: String, replyId: String, text: String, version: String) {
            val page = Page(CapsuleMessage(replyId, text, false, unread = true), version)
            pages[id] = page
            content.update { value ->
                val entries = value.entries.map { if (it.id == id) it.copy(unread = true) else it }
                value.copy(entries = entries, unreadEntries = entries.filter { it.unread },
                    messages = if (value.selectedId == id) listOf(page.reply) else value.messages,
                    readVersion = if (value.selectedId == id) version else value.readVersion)
            }
        }
        override suspend fun viewed(id: String?, messageId: String, version: String): Boolean {
            receipts += Receipt(id, messageId, version)
            check(content.value.selectedId == id)
            return read(id, messageId, version)
        }
        override fun updateDraft(id: String?, text: String) {
            check(content.value.selectedId == id)
            val target = content.value.replyTargetId ?: "source"
            drafts[id to target] = text
            content.update { it.copy(draftText = text) }
        }
        override suspend fun send(id: String?, text: String) {
            check(content.value.selectedId == id)
            sends += Send(id, content.value.replyTargetId ?: "source", text)
        }
        override suspend fun perform(id: String?, actionId: String) {
            check(content.value.selectedId == id)
            check(actionId.startsWith("target:"))
            val target = actionId.removePrefix("target:")
            content.update { it.copy(replyTargetId = target, draftText = drafts[id to target].orEmpty()) }
        }
        override fun open(id: String?) { opened += id }
        override fun openCompletion(completion: CapsuleCompletion) { select(completion.targetId) }
        override fun noticeShown(id: String) = Unit
    }
}
