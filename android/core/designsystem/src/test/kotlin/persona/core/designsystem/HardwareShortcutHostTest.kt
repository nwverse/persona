package persona.core.designsystem

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertIsFocused
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performKeyInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w840dp-h900dp-xhdpi")
class HardwareShortcutHostTest {
    @get:Rule val rule = createComposeRule()

    @Test fun helpClosesBeforeTheSheetAndTheSecondEscapeClosesTheSheet() {
        var dismissed = 0
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides true) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    HardwareShortcutHost(Modifier.fillMaxSize()) {
                        JSheet(onDismiss = { dismissed++ }) {
                            BasicTextField("draft", {}, Modifier.testTag("sheet-field"))
                        }
                    }
                }
            }
        }
        rule.onNodeWithTag("sheet-field").performClick()
        rule.onNodeWithTag("sheet-field").performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Slash) }
        }
        rule.onNodeWithTag("keyboard-shortcuts").assertExists()
        assertEquals(0, dismissed)
        rule.onNodeWithTag("keyboard-shortcuts").performKeyInput { pressKey(Key.Escape) }
        rule.waitForIdle()
        rule.onNodeWithTag("keyboard-shortcuts").assertDoesNotExist()
        assertEquals(0, dismissed)
        rule.onNodeWithTag("sheet-field").performKeyInput { pressKey(Key.Escape) }
        assertEquals(1, dismissed)
    }

    @Test fun focusedTerminalKeysNeverFallThroughToTheSheetOrHelp() {
        var dismissed = 0
        val consumed = mutableListOf<Key>()
        val terminalKeys = setOf(Key.Escape, Key.Tab, Key.C, Key.DirectionUp, Key.DirectionDown, Key.DirectionLeft, Key.DirectionRight, Key.Slash)
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides true) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    HardwareShortcutHost(Modifier.fillMaxSize()) {
                        JSheet(onDismiss = { dismissed++ }) {
                            BasicTextField("terminal", {}, Modifier.testTag("terminal-field").onPreviewKeyEvent { event ->
                                if (event.type == KeyEventType.KeyDown && event.key in terminalKeys) {
                                    consumed += event.key
                                    true
                                } else false
                            })
                        }
                    }
                }
            }
        }
        rule.onNodeWithTag("terminal-field").performClick()
        rule.onNodeWithTag("terminal-field").performKeyInput {
            pressKey(Key.Escape)
            pressKey(Key.Tab)
            withKeyDown(Key.ShiftLeft) { pressKey(Key.Tab) }
            withKeyDown(Key.CtrlLeft) { pressKey(Key.C) }
            pressKey(Key.DirectionUp)
            pressKey(Key.DirectionDown)
            pressKey(Key.DirectionLeft)
            pressKey(Key.DirectionRight)
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Slash) }
        }
        assertTrue(consumed.containsAll(terminalKeys))
        assertEquals(2, consumed.count { it == Key.Tab })
        assertEquals(0, dismissed)
        rule.onNodeWithTag("keyboard-shortcuts").assertDoesNotExist()
    }

    @Test fun compactDoesNotRegisterEscapeOrHelp() {
        var dismissed = 0
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides false) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    HardwareShortcutHost(Modifier.fillMaxSize()) {
                        Box(Modifier.hardwareShortcutDismiss { dismissed++ }) {
                            BasicTextField("draft", {}, Modifier.testTag("compact-field"))
                        }
                    }
                }
            }
        }
        rule.onNodeWithTag("compact-field").performClick()
        rule.onNodeWithTag("compact-field").performKeyInput {
            pressKey(Key.Escape)
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Slash) }
        }
        assertEquals(0, dismissed)
        rule.onNodeWithTag("keyboard-shortcuts").assertDoesNotExist()
    }

    @Test fun closingHelpRestoresTheDraftAndItsFocusedField() {
        var draft by mutableStateOf("draft")
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides true) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    HardwareShortcutHost(Modifier.fillMaxSize()) {
                        JSheet(onDismiss = {}) {
                            BasicTextField(draft, { draft = it }, Modifier.testTag("draft-field"))
                        }
                    }
                }
            }
        }
        rule.onNodeWithTag("draft-field").performClick()
        rule.onNodeWithTag("draft-field").performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Slash) }
        }
        rule.onNodeWithTag("keyboard-shortcuts").performKeyInput { pressKey(Key.Escape) }
        rule.waitForIdle()
        rule.onNodeWithTag("draft-field").assertTextEquals("draft")
        rule.onNodeWithTag("draft-field").assertIsFocused()
    }
}
