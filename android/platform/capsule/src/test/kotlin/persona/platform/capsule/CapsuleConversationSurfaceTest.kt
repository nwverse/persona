package persona.platform.capsule

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w411dp-h900dp-xhdpi")
class CapsuleConversationSurfaceTest {
    @get:Rule val rule = createComposeRule()
    private val entry = CapsuleEntry("group", "Release group", "A real reply", CapsuleState.Done, unread = true)
    private val sections = listOf(CapsuleSection("inbox", "收件箱"))
    private fun chat() = CapsuleContent(entries = listOf(entry), selectedId = entry.id, sections = sections,
        sectionId = "inbox", messages = listOf(CapsuleMessage("reply", "Please review the release", false, unread = true)),
        readVersion = "v1", nextAvailable = true)

    @Test fun aLargeExtensionLeavesTheLatestShortReplyVisibleAndReadable() {
        assertLatestReplyWithLargeExtension("A short latest reply")
    }

    @Test fun aLargeExtensionLeavesTheLatestLongReplyVisibleAndReadable() {
        assertLatestReplyWithLargeExtension("A long latest reply.\n".repeat(30) + "Latest conclusion")
    }

    private fun assertLatestReplyWithLargeExtension(body: String) {
        val receipts = mutableListOf<String>()
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(chat().copy(messages = listOf(CapsuleMessage("latest", body, false, unread = true))),
                true, {}, {}, {}, {}, {}, { _, _ -> }, { _, _ -> },
                { _, message, _ -> receipts += message; true }, true, remember { mutableStateMapOf() },
                extraContent = {
                    Column(Modifier.height(600.dp)) {
                        Text("A large extension card")
                        Spacer(Modifier.weight(1f))
                        Text("The end of the extension card")
                    }
                })
        } }
        rule.onNodeWithTag("capsule.message.latest").assertIsDisplayed()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf("latest"), receipts) }
    }

    @Test fun nextOnlyDispatchesTheProviderActionAndUnreadDotsWaitForProviderConfirmation() {
        val actions = mutableListOf<Pair<String?, String>>()
        var content by mutableStateOf(chat())
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(content, true, {}, {}, {}, {}, {}, { _, _ -> },
                { id, action -> actions += id to action }, { _, _, _ -> true }, false, remember { mutableStateMapOf() })
        } }
        rule.onNodeWithTag("capsule.next").assertIsDisplayed().performClick()
        rule.runOnIdle { assertEquals(listOf(entry.id to "next"), actions) }
        rule.onNodeWithTag("capsule.message.reply.unread", useUnmergedTree = true).assertExists()
        rule.runOnIdle { content = content.copy(messages = content.messages.map { it.copy(unread = false) }, nextAvailable = false) }
        rule.onNodeWithTag("capsule.message.reply.unread", useUnmergedTree = true).assertDoesNotExist()
        rule.onNodeWithTag("capsule.next").assertIsNotEnabled()
        rule.onNodeWithText("已读完").assertExists()
    }

    @Test fun coveringTheReplyCancelsTheReadDelayAndClosingTheMenuStartsAFreshDelay() {
        val receipts = mutableListOf<String>()
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(chat(), true, {}, {}, {}, {}, {}, { _, _ -> }, { _, _ -> },
                { _, message, _ -> receipts += message; true }, true, remember { mutableStateMapOf() })
        } }
        rule.onNodeWithTag("capsule.more").performClick()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertTrue(receipts.isEmpty()) }
        rule.onNodeWithTag("capsule.menu.close").performClick()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf("reply"), receipts) }
    }

    @Test fun aMenuCancelsThePendingNextReceiptRatherThanSwitchingLate() {
        var content by mutableStateOf(chat())
        var cancelled = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(content, true, {}, {}, {}, {}, {}, { _, _ -> }, { _, action ->
                if (action == "next") {
                    content = content.copy(nextBusy = true)
                    try { awaitCancellation() } finally { cancelled = true; content = content.copy(nextBusy = false) }
                }
            }, { _, _, _ -> true }, false, remember { mutableStateMapOf() })
        } }
        rule.onNodeWithTag("capsule.next").performClick()
        rule.onNodeWithTag("capsule.next").assertIsNotEnabled()
        rule.onNodeWithTag("capsule.more").performClick()
        rule.runOnIdle { assertTrue(cancelled); assertEquals(entry.id, content.selectedId) }
        rule.onNodeWithTag("capsule.menu.close").performClick()
        rule.onNodeWithTag("capsule.next").assertIsEnabled()
    }

    @Test fun focusLossCancelsReadingAndPendingNextAndVisibleFocusRestorationCanRead() {
        val receipts = mutableListOf<String>()
        var focused by mutableStateOf(false)
        var content by mutableStateOf(chat().copy(canSend = false))
        var nextCancelled = false
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(content, true, {}, {}, {}, {}, {}, { _, _ -> }, { _, action ->
                if (action == "next") {
                    content = content.copy(nextBusy = true)
                    try { awaitCancellation() } finally { nextCancelled = true; content = content.copy(nextBusy = false) }
                }
            }, { _, message, _ -> receipts += message; true }, focused, remember { mutableStateMapOf() })
        } }
        rule.onNodeWithText("Please review the release").assertIsDisplayed()
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertTrue(receipts.isEmpty()); focused = true }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(300)
        rule.onNodeWithTag("capsule.next").performClick()
        rule.runOnIdle { focused = false }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertTrue(receipts.isEmpty()); assertTrue(nextCancelled); focused = true }
        rule.waitForIdle()
        rule.mainClock.advanceTimeBy(1_000)
        rule.runOnIdle { assertEquals(listOf("reply"), receipts) }
    }

    @Test fun targetDraftsAreIsolatedAndExternalSendingConfirmsRecipientAndCapturedText() {
        var content by mutableStateOf(chat().copy(replyTargets = listOf(CapsuleReplyTarget("source", "原会话"),
            CapsuleReplyTarget("persona", "Persona")), replyTargetId = "source", recipient = "Release group", confirmSend = true))
        val drafts = mutableStateMapOf<Pair<String?, String?>, String>()
        val sent = mutableListOf<Pair<String?, String>>()
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(content, true, {}, {}, {}, {}, {}, { id, text -> sent += id to text }, { _, action ->
                content = content.copy(replyTargetId = action.removePrefix("target:"), confirmSend = action == "target:source")
            }, { _, _, _ -> true }, false, drafts)
        } }
        rule.onNodeWithTag("capsule.input").performTextInput("Reply publicly")
        rule.onNodeWithTag("capsule.target").performClick()
        rule.onNodeWithTag("capsule.target.persona").performClick()
        rule.onNodeWithTag("capsule.input").assert(SemanticsMatcher.expectValue(
            SemanticsProperties.EditableText, AnnotatedString(""))).performTextInput("Private question")
        rule.onNodeWithTag("capsule.target").performClick()
        rule.onNodeWithTag("capsule.target.source").performClick()
        rule.onNodeWithTag("capsule.input").assertTextEquals("Reply publicly")
        rule.onNodeWithTag("capsule.send").performClick()
        rule.runOnIdle { assertTrue(sent.isEmpty()) }
        rule.onNodeWithText("发送给：Release group").assertIsDisplayed()
        rule.onNodeWithTag("capsule.confirm.text", useUnmergedTree = true).assertTextEquals("Reply publicly")
        rule.onNodeWithTag("capsule.confirm.send").performClick()
        rule.runOnIdle { assertEquals(listOf(entry.id to "Reply publicly"), sent) }
        rule.onNodeWithTag("capsule.target").performClick()
        rule.onNodeWithTag("capsule.target.persona").performClick()
        rule.onNodeWithTag("capsule.input").assertTextEquals("Private question")
    }

    @Test fun fyiCanCollapseAndReadAllDoesNotLocallyClearUnreadOrPendingEntries() {
        val action = entry.copy(id = "action", group = "action")
        val fyi = entry.copy(id = "fyi", title = "FYI group", group = "fyi", unreadCount = 2)
        val pending = entry.copy(id = "pending", title = "Read but pending", group = "read-pending", unread = false)
        val calls = mutableListOf<Pair<String?, String>>()
        rule.setContent { PersonaTheme(reduceMotion = true) {
            CapsuleSurface(chat().copy(selectedId = null, entries = listOf(action, fyi, pending)), true, {}, {}, {}, {}, {},
                { _, _ -> }, { id, actionId -> calls += id to actionId }, { _, _, _ -> true }, false, remember { mutableStateMapOf() })
        } }
        rule.onNodeWithTag("capsule.fyi.toggle").performClick()
        rule.onNodeWithTag("capsule.entry.fyi").assertDoesNotExist()
        rule.onNodeWithTag("capsule.fyi.read").performClick()
        rule.runOnIdle { assertEquals(listOf(null to "read-fyi"), calls) }
        rule.onNodeWithTag("capsule.fyi.toggle").performClick()
        rule.onNodeWithTag("capsule.entry.fyi.unread", useUnmergedTree = true).assertExists()
        rule.onNodeWithTag("capsule.entry.pending").assertExists()
        rule.onNodeWithText("2 条消息").assertExists()
    }
}
