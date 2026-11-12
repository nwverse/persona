package persona.core.designsystem

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getUnclippedBounds
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTextReplacement
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w840dp-h900dp-xhdpi")
class PageSizedSettingsSheetTest {
    @get:Rule val rule = createComposeRule()

    @Test fun expandedSettingsIsACenteredPageWithWindowMargins() {
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides true) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    JSettingsSheet(onDismiss = {}) { Box(Modifier.testTag("settings-content")) }
                }
            }
        }
        rule.onNodeWithTag("settings-content").assertExists()
        val bounds = rule.onNodeWithTag("settings-page-card").getUnclippedBounds()
        assertTrue(bounds.width <= SettingsPageSizing.maximumWidth)
        assertTrue(bounds.height <= SettingsPageSizing.maximumHeight)
        assertTrue(bounds.left >= SettingsPageSizing.minimumWindowMargin)
        assertTrue(bounds.top >= SettingsPageSizing.minimumWindowMargin)
    }

    @Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
    @Test fun compactSettingsKeepsTheOriginalFullWindowSheet() {
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides false) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    JSettingsSheet(onDismiss = {}) { Box(Modifier.testTag("settings-content")) }
                }
            }
        }
        rule.onNodeWithTag("settings-content").assertExists()
        rule.onNodeWithTag("settings-page-card").assertDoesNotExist()
    }

    @Test fun presentationChangesKeepTheSameStackAndSaveableDraft() {
        var large by mutableStateOf(true)
        val stack = mutableListOf("root", "agent", "editor")
        val originalStack = stack.toList()
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides large) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    JSettingsSheet(onDismiss = {}) {
                        var draft by rememberSaveable { mutableStateOf("draft") }
                        BasicTextField(draft, { draft = it }, Modifier.testTag("settings-draft"))
                    }
                }
            }
        }
        rule.onNodeWithTag("settings-draft").performTextReplacement("edited")
        large = false
        rule.waitForIdle()
        rule.onNodeWithTag("settings-draft").assertTextEquals("edited")
        large = true
        rule.waitForIdle()
        rule.onNodeWithTag("settings-draft").assertTextEquals("edited")
        assertTrue(stack == originalStack)
    }
}
