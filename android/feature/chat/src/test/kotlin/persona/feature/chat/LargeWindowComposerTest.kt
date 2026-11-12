package persona.feature.chat

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.isFocused
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNode
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.SessionKey
import persona.core.designsystem.LocalLargeWindowInput
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w640dp-h800dp-xhdpi")
class LargeWindowComposerTest {
    @get:Rule val rule = createComposeRule()

    private fun show(
        state: ChatComposerState,
        large: Boolean = true,
        sending: Boolean = false,
        enabled: Boolean = true,
        extraContent: Boolean = false,
        blockedLabel: String? = null,
        onSend: () -> Unit,
    ) {
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides large) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    LargeWindowComposer(SessionKey("box", "s1"), state, working = false, sending = sending,
                        canClear = false, service = null, onSend = onSend, onCommand = {}, onNotice = {},
                        enabled = enabled, extraContent = extraContent, blockedLabel = blockedLabel)
                }
            }
        }
        if (enabled) {
            rule.onNode(hasSetTextAction()).performClick()
            rule.waitForIdle()
        }
    }

    @Test fun returnAndShiftReturnInsertLinesWithoutSending() {
        val state = ChatComposerState().apply { value = TextFieldValue("hello", TextRange(5)) }
        var sent = 0
        show(state) { sent++ }
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            pressKey(Key.Enter)
            withKeyDown(Key.ShiftLeft) { pressKey(Key.Enter) }
        }
        assertEquals(0, sent)
        assertEquals("hello\n\n", state.value.text)
    }

    @Test fun ctrlAndMetaReturnEachSendOnceAndExtraContentUsesTheSameGate() {
        val state = ChatComposerState().apply { value = TextFieldValue("hello", TextRange(5)) }
        var sent = 0
        show(state, extraContent = true) { sent++ }
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(1, sent)
        sent = 0
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.MetaLeft) { pressKey(Key.Enter) }
        }
        assertEquals(1, sent)
        state.value = TextFieldValue()
        rule.waitForIdle()
        sent = 0
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals("extra content alone remains sendable", 1, sent)
    }

    @Test fun emptySendingAndBlockedDraftsDoNotSend() {
        val state = ChatComposerState()
        var sent = 0
        show(state, sending = true, blockedLabel = "Connect a model first") { sent++ }
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(0, sent)
    }

    @Test fun pendingAttachmentsUseTheOrdinarySendReadinessGate() {
        val state = ChatComposerState().apply {
            attachments = listOf(ComposerAttachment("pending", "photo.jpg", "", image = true))
        }
        var sent = 0
        show(state) { sent++ }
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(0, sent)
    }

    @Test fun readOnlyComposerDoesNotSend() {
        var sent = 0
        show(ChatComposerState(), enabled = false) { sent++ }
        rule.onNode(hasSetTextAction()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(0, sent)
    }

    @Test fun composingTextIsNotSentUntilTheCompositionCommits() {
        val state = ChatComposerState().apply {
            value = TextFieldValue("中文", TextRange(2), TextRange(0, 2))
        }
        var sent = 0
        show(state) { sent++ }
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(0, sent)
        state.value = TextFieldValue(state.value.text, state.value.selection)
        rule.waitForIdle()
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(1, sent)
        assertEquals(TextRange(2), state.value.selection)
    }

    @Test fun expandedEditorSendsOnceAndEscapeOnlyCollapses() {
        val long = (1..9).joinToString("\n") { "line $it with enough words to wrap in the editor" }
        val state = ChatComposerState().apply { value = TextFieldValue(long, TextRange(12)) }
        var sent = 0
        show(state) { sent++ }
        rule.onNodeWithTag("composer-expand").performClick()
        rule.onNodeWithTag("composer-expanded").assertIsDisplayed()
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        rule.onNodeWithTag("composer-expanded").assertDoesNotExist()
        assertEquals(1, sent)
        assertEquals(long, state.value.text)
        assertEquals(TextRange(12), state.value.selection)

        rule.onNodeWithTag("composer-expand").performClick()
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput { pressKey(Key.Escape) }
        rule.onNodeWithTag("composer-expanded").assertDoesNotExist()
        assertEquals(1, sent)
        assertEquals(long, state.value.text)
        assertEquals(TextRange(12), state.value.selection)
    }

    @Test fun compactDoesNotInstallTheNewSendShortcut() {
        val state = ChatComposerState().apply { value = TextFieldValue("hello", TextRange(5)) }
        var sent = 0
        show(state, large = false) { sent++ }
        rule.onNode(hasSetTextAction() and isFocused()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(0, sent)
    }
}
