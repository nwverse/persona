package persona.core.designsystem

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.LayoutDirection
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h860dp")
class PresentedNavigationTest {
    @get:Rule val rule = createComposeRule()

    @Test fun fullScreenRootClosesAndPushedChildGoesBack() {
        var open by mutableStateOf(true)
        var child by mutableStateOf(false)
        var closed = 0
        val close = { closed++; open = false }
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                if (open) JFullScreen(close) {
                    if (child) JPage("Child", onBack = { child = false }) {}
                    else JPage("Root", onBack = close) { insets ->
                        Box(Modifier.padding(insets)) { JButton("Open child", { child = true }) }
                    }
                }
            }
        }
        rule.onNodeWithContentDescription("Close").assertIsEnabled()
        rule.onNodeWithContentDescription("Back").assertDoesNotExist()
        rule.onNodeWithText("Open child").performClick()
        rule.onNodeWithContentDescription("Close").assertDoesNotExist()
        rule.onNodeWithContentDescription("Back").performClick()
        rule.onNodeWithContentDescription("Close").performClick()
        rule.runOnIdle { assertEquals(1, closed); assertFalse(open) }
    }

    @Test fun rtlMirrorsTheActionEdgesAndSaveKeepsItsSpokenName() {
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Rtl) {
                    SheetPageBar("Title", {}, true) { JSheetConfirm("Add", {}) }
                }
            }
        }
        val close = rule.onNodeWithContentDescription("Close").assertIsEnabled().fetchSemanticsNode().boundsInRoot
        val add = rule.onNodeWithContentDescription("Add").assertIsEnabled().fetchSemanticsNode().boundsInRoot
        assertTrue("RTL start is right of end: $close, $add", close.left >= add.right)
    }

    @Test fun busySheetCannotCloseOrConfirmAgain() {
        var calls = 0
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                JSheet({ calls++ }, title = "Edit", dismissible = false,
                    pageActions = { JSheetConfirm("Save", { calls++ }, loading = true) }) {}
            }
        }
        rule.onNodeWithContentDescription("Close").assertIsNotEnabled()
        rule.onNodeWithContentDescription("Save").assertIsNotEnabled()
        rule.runOnIdle { assertEquals(0, calls) }
    }

    @Test fun questionStillCancelsUnderItsAnswer() {
        var answered = false
        var cancelled = false
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                JDialog({ cancelled = true }, "Remove?", confirm = "Remove" to { answered = true })
            }
        }
        rule.onNodeWithContentDescription("Close").assertDoesNotExist()
        rule.onNodeWithText("Remove").assertIsEnabled()
        rule.onNodeWithText("Cancel").performClick()
        rule.runOnIdle { assertTrue(cancelled); assertFalse(answered) }
    }
}
