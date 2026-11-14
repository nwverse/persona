package persona.core.designsystem

import android.os.Process
import android.os.SystemClock
import android.view.InputDevice
import android.view.KeyEvent
import android.view.MotionEvent
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Real-window captures of the large-window layers: the Settings card with its single scrim, shortcut help above it, and
 * a pointer menu opened by a real secondary button. Each capture is the whole device screen, written with a sidecar
 * that names the window, the process and the theme, in the test app's external files directory.
 */
@RunWith(AndroidJUnit4::class)
class LargeWindowShotsInstrumentedTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()

    @Test fun settingsCardAndHelpLayersLight() = settingsCardAndHelpLayers(dark = false)

    @Test fun settingsCardAndHelpLayersDark() = settingsCardAndHelpLayers(dark = true)

    @Test fun pointerMenuFromARealSecondaryButtonLight() = pointerMenu(dark = false)

    @Test fun pointerMenuFromARealSecondaryButtonDark() = pointerMenu(dark = true)

    private fun settingsCardAndHelpLayers(dark: Boolean) {
        run {
            var help by mutableStateOf(false)
            rule.setContent {
                CompositionLocalProvider(LocalLargeWindowInput provides true) {
                    PersonaTheme(darkTheme = dark, reduceMotion = true) {
                        Box(Modifier.fillMaxSize()) { Text("Assistant page behind the card", Modifier.testTag("page-behind"), color = PersonaTheme.palette.ink) }
                        HardwareShortcutHost(Modifier.fillMaxSize(), requestFocusWhenLarge = false) {
                            JSettingsSheet(onDismiss = {}) {
                                val field = remember { FocusRequester() }
                                LaunchedEffect(Unit) { runCatching { field.requestFocus() } }
                                Column {
                                    Text("Settings", color = PersonaTheme.palette.ink)
                                    BasicTextField("first field", {}, Modifier.testTag("first"), textStyle = TextStyle(color = PersonaTheme.palette.ink))
                                    BasicTextField("second field", {}, Modifier.testTag("second").focusRequester(field), textStyle = TextStyle(color = PersonaTheme.palette.ink))
                                }
                            }
                        }
                    }
                }
            }
            rule.waitForIdle()
            Thread.sleep(800)
            capture("settings-card", dark, "Settings card over the page; one scrim, second field focused")
            sendKey(KeyEvent.KEYCODE_SLASH, KeyEvent.META_CTRL_ON or KeyEvent.META_CTRL_LEFT_ON)
            Thread.sleep(800)
            capture("help-over-settings", dark, "Ctrl+/ opens shortcut help above the Settings card")
            sendKey(KeyEvent.KEYCODE_ESCAPE, 0)
            Thread.sleep(500)
            capture("settings-after-help", dark, "Escape closes only the help; the card and the field focus stay")
        }
    }

    private fun pointerMenu(dark: Boolean) {
        run {
            var anchor by mutableStateOf<Offset?>(null)
            rule.setContent {
                PersonaTheme(darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.fillMaxSize().onSecondaryClickAt { anchor = it }.testTag("target")) {
                        Text("Message text; secondary-click anywhere", Modifier.testTag("label"), color = PersonaTheme.palette.ink)
                        anchor?.let { point ->
                            JPointerMenu(point, { anchor = null }, listOf("Copy", "Select text", "Share", "Reply", "Delete")
                                .map { name -> JAction(name) {} })
                        }
                    }
                }
            }
            rule.waitForIdle()
            val bounds = rule.onNodeWithTagBounds("target")
            secondaryClick(bounds.first + bounds.third * 0.62f, bounds.second + bounds.fourth * 0.55f)
            rule.waitForIdle()
            Thread.sleep(600)
            capture("pointer-menu", dark, "Pointer menu opened by a real secondary mouse button at the click point")
        }
    }

    private fun androidx.compose.ui.test.junit4.AndroidComposeTestRule<*, *>.onNodeWithTagBounds(tag: String): List4 {
        val node = onNodeWithTag(tag).fetchSemanticsNode()
        return List4(node.positionOnScreen.x, node.positionOnScreen.y, node.size.width.toFloat(), node.size.height.toFloat())
    }

    private data class List4(val first: Float, val second: Float, val third: Float, val fourth: Float)

    private fun sendKey(code: Int, meta: Int) {
        val now = SystemClock.uptimeMillis()
        instrumentation.sendKeySync(KeyEvent(now, now, KeyEvent.ACTION_DOWN, code, 0, meta, 0, 0, 0, InputDevice.SOURCE_KEYBOARD))
        instrumentation.sendKeySync(KeyEvent(now, now + 20, KeyEvent.ACTION_UP, code, 0, meta, 0, 0, 0, InputDevice.SOURCE_KEYBOARD))
        instrumentation.waitForIdleSync()
        rule.waitForIdle()
    }

    private fun secondaryClick(x: Float, y: Float) {
        val downTime = SystemClock.uptimeMillis()
        fun event(action: Int, time: Long, buttons: Int): MotionEvent {
            val properties = MotionEvent.PointerProperties().apply { id = 0; toolType = MotionEvent.TOOL_TYPE_MOUSE }
            val coordinates = MotionEvent.PointerCoords().apply { this.x = x; this.y = y }
            return MotionEvent.obtain(downTime, time, action, 1, arrayOf(properties), arrayOf(coordinates),
                0, buttons, 1f, 1f, 0, 0, InputDevice.SOURCE_MOUSE, 0)
        }
        val down = event(MotionEvent.ACTION_DOWN, downTime, MotionEvent.BUTTON_SECONDARY)
        val up = event(MotionEvent.ACTION_UP, downTime + 10, 0)
        try {
            instrumentation.sendPointerSync(down)
            instrumentation.sendPointerSync(up)
            instrumentation.waitForIdleSync()
        } finally {
            down.recycle()
            up.recycle()
        }
    }

    private fun capture(name: String, dark: Boolean, meaning: String) {
        val theme = if (dark) "dark" else "light"
        val folder = java.io.File(instrumentation.targetContext.getExternalFilesDir(null), "large-window-shots").apply { mkdirs() }
        val bitmap = requireNotNull(instrumentation.uiAutomation.takeScreenshot()) { "no screenshot" }
        java.io.File(folder, "$name-$theme.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        val metrics = instrumentation.targetContext.resources.displayMetrics
        val widthDp = (metrics.widthPixels / metrics.density).toInt()
        val heightDp = (metrics.heightPixels / metrics.density).toInt()
        java.io.File(folder, "$name-$theme.json").writeText(
            """{"name":"$name","theme":"$theme","pid":${Process.myPid()},"widthDp":$widthDp,"heightDp":$heightDp,"meaning":"$meaning"}""")
    }
}
