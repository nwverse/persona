package persona.feature.inbox

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNode
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.IOException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.InboxEventFields
import persona.core.contracts.api.HubCodedFailure
import persona.core.contracts.api.InboxEntry
import persona.core.contracts.api.InboxParentRef
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.AppStyle
import persona.core.designsystem.FeedbackNotice
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.domain.inbox.InboxItem
import persona.core.navigation.Navigator

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-mdpi")
class InboxSameMatterSheetTest {
    @get:Rule val rule = createComposeRule()

    @Test fun candidatesKeepOnlyOpenSameManagerChoicesNewestFirstAndSearchTitleAndWhere() {
        val current = record("current", "Current", manager = "m2", at = "2026-11-13T00:00:00Z")
        val child = record("child", "Child", manager = "m2", at = "2026-11-13T08:00:00Z")
        val grandchild = record("grandchild", "Grandchild", manager = "m2", at = "2026-11-13T01:00:00Z")
        val done = record("done", "Done", manager = "m2", done = true)
        val other = record("other", "Other", manager = "m1")
        val newer = record("newer", "Alpha", manager = "m2", at = "2026-11-13T07:00:00Z")
        val older = record("older", "Beta", manager = "m2", at = "2026-11-12T07:00:00Z")
        val place = record("place", "Gamma", manager = "m2", at = "2026-11-13T06:00:00Z")
        val records = listOf(current, child, grandchild, done, other, newer, older, place)
        val all = sameMatterCandidates(records, current, listOf(child), "")
        assertEquals(listOf("newer", "place", "grandchild", "older"), all.map { it.hubItemId })
        assertEquals(listOf("newer"), sameMatterCandidates(records, current, listOf(child), "alpha").map { it.hubItemId })
        assertEquals(listOf("place"), sameMatterCandidates(records, current, listOf(child), "WAREHOUSE") {
            if (it.hubItemId == "place") "Warehouse" else null
        }.map { it.hubItemId })
        assertTrue("only direct children are excluded; a deeper cycle is the hub's 409", grandchild.hubItemId in all.map { it.hubItemId })
    }

    @Test fun pickerFiltersSearchesAndLinksTheRawParentWithoutAConfirmation() {
        val current = record("current", "Current", manager = "m2")
        val child = record("child", "Child", manager = "m2")
        val alpha = record("alpha", "Alpha", manager = "m2", sender = "Alice", preview = "Latest", at = "2026-11-13T07:00:00Z")
        val beta = record("beta", "Beta", manager = "m2", at = "2026-11-12T07:00:00Z")
        val done = record("done", "Done", manager = "m2", done = true)
        val tree = InboxLineage(InboxParentRef("inbox", "current"), current, children = listOf(child))
        val repository = FakeRepository(listOf(current, child, alpha, beta, done), tree)
        var dismisses = 0
        val linked = mutableListOf<InboxLineage?>()
        rule.setContent { Sheet(repository, current, tree, { dismisses++ }, { linked += it }) }
        rule.onNodeWithText("Current").assertDoesNotExist()
        rule.onNodeWithText("Child").assertDoesNotExist()
        rule.onNodeWithText("Done").assertDoesNotExist()
        rule.onNodeWithText("Alice：Latest").assertExists()
        rule.onNode(hasSetTextAction()).performTextReplacement("Beta")
        rule.onNodeWithText("Alpha").assertDoesNotExist()
        rule.onNodeWithText("Beta", substring = true).assertExists()
        rule.onNode(hasSetTextAction()).performTextReplacement("Alpha")
        rule.onNode(hasText("Alpha", substring = true) and hasClickAction()).performClick()
        rule.waitForIdle()
        assertEquals(current.item.id, repository.writes.single().first.item.id)
        assertEquals(InboxParentRef("inbox", "alpha"), repository.writes.single().second)
        assertEquals(1, dismisses)
        assertEquals(1, linked.size)
        assertEquals("已连到「Alpha」", feedback.notices.single().message)
        assertEquals(FeedbackStyle.Success, feedback.notices.single().style)
        rule.onNodeWithText("是同一件事…").assertDoesNotExist()
    }

    @Test fun aCycleRefusalKeepsThePickerItsQueryAndUsesExistingFailureFeedback() {
        val current = record("current", "Current")
        val alpha = record("alpha", "Alpha")
        val tree = InboxLineage(InboxParentRef("inbox", "current"), current)
        val repository = FakeRepository(listOf(current, alpha), tree).apply { writeError = CodedFailure(409, "parent_cycle") }
        var dismisses = 0
        rule.setContent { Sheet(repository, current, tree, { dismisses++ }, {}) }
        rule.onNode(hasSetTextAction()).performTextReplacement("Alpha")
        rule.onNode(hasText("Alpha", substring = true) and hasClickAction()).performClick()
        rule.waitForIdle()
        assertEquals(1, feedback.failures)
        assertTrue(feedback.notices.isEmpty())
        assertEquals(0, dismisses)
        rule.onNodeWithText("是同一件事…").assertExists()
        rule.onNodeWithText("Alpha", substring = true).assertExists()
    }

    @Test fun aFailedTreeReadCanBeRetried() {
        val current = record("current", "Current")
        val alpha = record("alpha", "Alpha")
        val tree = InboxLineage(InboxParentRef("inbox", "current"), current)
        val repository = FakeRepository(listOf(current, alpha), tree).apply { failRead = true }
        // Use no already-loaded tree, as the More menu does.
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAppFeedback provides feedback) {
                    InboxSameMatterSheet(repository, current, initialTree = null, onDismiss = {}, onLinked = {})
                }
            }
        }
        repository.failRead = false
        rule.onNodeWithText("重试").performClick()
        rule.onNodeWithText("Alpha", substring = true).assertExists()
        assertEquals(2, repository.lineCalls)
    }

    @Test fun moreMenuLoadsThePickerOnlyWhenItIsOpened() {
        val current = record("current", "Current")
        val repository = FakeRepository(listOf(current), InboxLineage(InboxParentRef("inbox", "current"), current))
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAppFeedback provides feedback) {
                    InboxSameMatterMenu(repository, current, onLinked = {})
                }
            }
        }
        assertEquals(0, repository.lineCalls)
        rule.onNodeWithContentDescription("更多").performClick()
        rule.onNodeWithText("是同一件事…").performClick()
        rule.waitForIdle()
        assertEquals(1, repository.lineCalls)
        rule.onNode(hasSetTextAction()).assertExists()
    }

    @Test fun theSectionsBottomEntryOpensTheSamePickerWithoutAnotherTreeRead() {
        val current = record("current", "Current")
        val parent = record("parent", "Parent")
        val repository = FakeRepository(listOf(current), InboxLineage(InboxParentRef("inbox", "current"), current,
            listOf(InboxLineAncestor(InboxParentRef("inbox", "parent"), parent))))
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAppFeedback provides feedback) {
                    InboxLineSection(repository, current, navigator)
                }
            }
        }
        rule.onNodeWithText("是同一件事…").performClick()
        rule.onNode(hasSetTextAction()).assertExists()
        assertEquals(1, repository.lineCalls)
    }

    @Composable
    private fun Sheet(repository: FakeRepository, current: InboxRecord, tree: InboxLineage, dismiss: () -> Unit, linked: (InboxLineage?) -> Unit) {
        PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
            CompositionLocalProvider(LocalAppFeedback provides feedback) {
                InboxSameMatterSheet(repository, current, tree, dismiss, linked)
            }
        }
    }

    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: persona.core.navigation.AppTab) = Unit
    }
    private val feedback = object : AppFeedback {
        val notices = mutableListOf<FeedbackNotice>()
        var failures = 0
        override fun failure(error: Throwable, operation: Boolean): Boolean { failures++; return true }
        override fun notice(message: String): Boolean = notice(message, FeedbackStyle.Info)
        override fun notice(message: String, style: FeedbackStyle): Boolean { notices += FeedbackNotice(message, style = style); return true }
    }

    private class CodedFailure(override val status: Int, override val code: String) : IOException("refused"), HubCodedFailure

    private class FakeRepository(records: List<InboxRecord>, private val line: InboxLineage) : InboxRepository {
        override val entries = MutableStateFlow(emptyList<InboxEntry>())
        override val snapshot = MutableStateFlow(InboxSnapshot(records = records))
        override suspend fun refresh() = Unit
        override fun conversation(feedId: String, startPosition: Long?) = flowOf(LarkConversation())
        override suspend fun loadOlder(feedId: String) = Unit
        override suspend fun refreshConversation(feedId: String) = Unit
        override suspend fun setDone(itemId: String, done: Boolean) = Unit
        override suspend fun readDraft(target: ReplyTarget): ReplyDraft? = null
        override suspend fun saveDraft(target: ReplyTarget, draft: ReplyDraft) = Unit
        override suspend fun reply(target: ReplyTarget, request: ReplyRequest) = Unit
        override suspend fun downloadFile(file: LarkPart.File): String = error("unused")
        var lineCalls = 0
        var failRead = false
        var writeError: Exception? = null
        val writes = mutableListOf<Pair<InboxRecord, InboxParentRef?>>()
        override suspend fun inboxLine(item: InboxRecord): InboxLineage {
            lineCalls++
            if (failRead) throw IOException("read failed")
            return line
        }
        override suspend fun setInboxParent(item: InboxRecord, parent: InboxParentRef?) {
            writes += item to parent
            writeError?.let { throw it }
        }
    }

    companion object {
        private fun record(id: String, title: String, manager: String? = null, sender: String? = null, preview: String = "", at: String? = null, done: Boolean = false) =
            InboxRecord(InboxItem(InboxItemKey.appId(id, manager), title = title, sender = sender, preview = preview,
                updatedAt = at, doneAt = if (done) "2026-11-13T09:00:00Z" else null, status = if (done) null else "action"),
                event = InboxEventFields(status = if (done) null else "action"), manager = manager)
    }
}
