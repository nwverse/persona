package persona.feature.settings

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.ConnectionState
import persona.core.designsystem.*
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h860dp-xhdpi")
class PresentedSettingsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private fun checkHome(style: AppStyle, dark: Boolean) {
        val services = demoSettingsServices(rule.activity.resources)
        var dismissed = 0
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("settings-root")) {
                    SettingsHomePage(services, "", {}, false, false, null, false, null, false,
                        ConnectionState.Online, null, null, null, null, {}, null, { dismissed++ }, null, null, {})
                }
            }
        }
        val page = rule.onNodeWithTag("settings-root")
        val close = rule.onNodeWithContentDescription("Close").assertIsEnabled()
        val surface = page.fetchSemanticsNode().boundsInRoot
        val exit = close.fetchSemanticsNode().boundsInRoot
        val touch = with(rule.density) { 48.dp.toPx() }
        assertTrue("Settings must close at the top start: $exit in $surface",
            exit.right < surface.center.x && exit.top - surface.top <= touch && exit.width >= touch && exit.height >= touch)
        rule.onNodeWithContentDescription("Back").assertDoesNotExist()
        rule.onNodeWithText("Done").assertDoesNotExist()
        val out = File(System.getProperty("persona.shots") ?: "build/presented-page-shots").also { it.mkdirs() }
        val scale = if (rule.activity.resources.configuration.fontScale > 1f) "-en-large" else ""
        val name = "read-${style.name.lowercase()}-${if (dark) "dark" else "light"}$scale.png"
        File(out, name).outputStream().use { assertTrue(page.captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it)) }
        close.performClick()
        rule.runOnIdle { assertEquals(1, dismissed) }
    }

    @Test @Config(fontScale = 2f) fun glassLightLargeType() = checkHome(AppStyle.Glass, false)
    @Test @Config(fontScale = 2f) fun glassDarkLargeType() = checkHome(AppStyle.Glass, true)
    @Test @Config(fontScale = 2f) fun briefLightLargeType() = checkHome(AppStyle.Brief, false)
    @Test @Config(fontScale = 2f) fun briefDarkLargeType() = checkHome(AppStyle.Brief, true)
    @Test @Config(fontScale = 2f) fun boardLightLargeType() = checkHome(AppStyle.Board, false)
    @Test @Config(fontScale = 2f) fun boardDarkLargeType() = checkHome(AppStyle.Board, true)

    @Test fun glassLight() = checkHome(AppStyle.Glass, false)
    @Test fun glassDark() = checkHome(AppStyle.Glass, true)
    @Test fun briefLight() = checkHome(AppStyle.Brief, false)
    @Test fun briefDark() = checkHome(AppStyle.Brief, true)
    @Test fun boardLight() = checkHome(AppStyle.Board, false)
    @Test fun boardDark() = checkHome(AppStyle.Board, true)
}
