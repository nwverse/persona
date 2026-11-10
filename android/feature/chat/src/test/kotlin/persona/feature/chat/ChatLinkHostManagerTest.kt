package persona.feature.chat

import androidx.compose.foundation.clickable
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasParent
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.addJsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.json.putJsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.InboxAssistantTransport
import persona.core.contracts.api.InboxEntry
import persona.core.contracts.api.RequestManager
import persona.core.designsystem.PersonaTheme
import persona.feature.inbox.InboxRepository
import persona.feature.inbox.InboxSnapshot
import persona.feature.inbox.LarkContentSource
import persona.feature.inbox.LarkConversation
import persona.feature.inbox.LarkPart
import persona.feature.inbox.LocalInboxRepository
import persona.feature.inbox.ManagerLinks
import persona.feature.inbox.ReplyDraft
import persona.feature.inbox.ReplyRequest
import persona.feature.inbox.ReplyTarget

/** A document another manager's item page opened natively is read as that manager, and so is a document linked inside it. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
class ChatLinkHostManagerTest {
    @get:Rule val rule = createComposeRule()

    @Before fun choose() = AssistantManagerChoice.choose(null, null, "assistant")
    @After fun reset() = AssistantManagerChoice.reset()

    private val first = "https://example.feishu.cn/docx/FirstLinkHostDoc"
    private val second = "https://example.feishu.cn/docx/SecondLinkHostDoc"

    /** Each document read with the manager it named; the first one links to the second. */
    private inner class Documents : InboxAssistantTransport {
        val reads = mutableListOf<Pair<String, String?>>()
        override val scope = "link-host-manager-test"
        override suspend fun readDocument(url: String): JsonObject {
            val manager = currentCoroutineContext()[RequestManager]?.id
            synchronized(reads) { reads += url to manager }
            return buildJsonObject {
                put("title", if (url == first) "First" else "Second")
                putJsonObject("content") {
                    put("type", "doc")
                    if (url == first) putJsonArray("content") { addJsonObject {
                        put("type", "paragraph")
                        putJsonArray("content") { addJsonObject { put("type", "text"); put("text", second) } }
                    } }
                }
            }
        }
        override suspend fun documentExtra(path: String, url: String) = buildJsonObject { }
        override suspend fun inbox() = error("unused")
        override suspend fun setDone(itemId: String, done: Boolean) = error("unused")
        override suspend fun reply(itemId: String?, feedId: String?, text: String, clientId: String) = error("unused")
        override suspend fun messages(feedId: String, around: Long?, before: String?, after: String?) = error("unused")
        override suspend fun assistant() = error("unused")
        override suspend fun createConversation(main: Boolean) = error("unused")
        override suspend fun pins() = error("unused")
        override suspend fun mutes() = error("unused")
        override suspend fun media(ref: String) = error("unused")
    }

    private class Paired(transport: InboxAssistantTransport) : InboxRepository {
        override val entries = MutableStateFlow(emptyList<InboxEntry>())
        override val snapshot = MutableStateFlow(InboxSnapshot(contentSource = LarkContentSource(transport)))
        override val connectionState = flowOf(ConnectionState.Online)
        override suspend fun refresh() = Unit
        override fun conversation(feedId: String, startPosition: Long?) = flowOf(LarkConversation())
        override suspend fun loadOlder(feedId: String) = Unit
        override suspend fun refreshConversation(feedId: String) = Unit
        override suspend fun setDone(itemId: String, done: Boolean) = Unit
        override suspend fun readDraft(target: ReplyTarget): ReplyDraft? = null
        override suspend fun saveDraft(target: ReplyTarget, draft: ReplyDraft) = Unit
        override suspend fun reply(target: ReplyTarget, request: ReplyRequest) = Unit
        override suspend fun downloadFile(file: LarkPart.File): String = error("unused")
    }

    @Test fun aDocumentLinkedInsideAnotherManagersDocumentIsReadAsThatManager() {
        val documents = Documents()
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalInboxRepository provides Paired(documents)) {
                    ChatLinkHost { _ ->
                        // An item page of m-3 (ManagerLinks), as Root.kt draws an extension's item page.
                        ManagerLinks("m-3") {
                            val uri = LocalUriHandler.current
                            Text("Open the first document", Modifier.clickable { uri.openUri(first) })
                        }
                    }
                }
            }
        }
        rule.onNodeWithText("Open the first document").performClick()
        rule.waitForIdle()
        rule.waitUntil(5_000) { rule.onAllNodes(hasText(second)).fetchSemanticsNodes().isNotEmpty() }
        // The link inside the first document's text (its own node, under the text's).
        rule.onNode(hasClickAction() and hasParent(hasText(second)), useUnmergedTree = true).performSemanticsAction(SemanticsActions.OnClick)
        rule.waitForIdle()
        rule.waitUntil(5_000) { synchronized(documents.reads) { documents.reads.any { it.first == second } } }
        assertEquals(listOf(first to "m-3", second to "m-3"), synchronized(documents.reads) { documents.reads.toList() })
    }
}
