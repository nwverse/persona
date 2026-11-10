package persona.core.designsystem

import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusManager
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-mdpi")
class HomeChromeTest {
    @get:Rule val rule = createComposeRule()

    @Test fun openedDrawerPreventsKeyboardFocusFromEnteringTheHomeBehindIt() {
        var opened by mutableStateOf(false)
        var homeFocused = false
        val home = FocusRequester()
        val drawer = FocusRequester()
        lateinit var focus: FocusManager
        rule.setContent {
            focus = LocalFocusManager.current
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                JShellDrawer(opened, onDismiss = { opened = false }, drawer = {
                    Box(Modifier.fillMaxSize().focusRequester(drawer).focusable())
                }) {
                    Box(Modifier.fillMaxSize().focusRequester(home)
                        .onFocusChanged { homeFocused = it.isFocused }.focusable())
                }
            }
        }
        rule.runOnIdle { assertTrue(home.requestFocus()) }
        rule.runOnIdle {
            assertTrue(homeFocused)
            focus.clearFocus(force = true)
            opened = true
        }
        rule.waitForIdle()
        rule.runOnIdle {
            assertFalse(home.requestFocus())
            assertFalse(homeFocused)
            assertTrue(drawer.requestFocus())
            focus.clearFocus(force = true)
            opened = false
        }
        rule.waitForIdle()
        rule.runOnIdle { assertTrue(home.requestFocus()) }
        rule.runOnIdle { assertTrue(homeFocused) }
    }
}
