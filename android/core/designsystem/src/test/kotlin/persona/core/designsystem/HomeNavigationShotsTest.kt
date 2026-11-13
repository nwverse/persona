package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcons
import java.io.File

/** Narrow panes, regular phones, and Agents with two trailing actions, in every style and color scheme. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-mdpi")
class HomeNavigationShotsTest {
    @get:Rule val rule = createComposeRule()
    private data class ShotCase(val width: Dp, val fontScale: Float, val trailingActions: Int, val label: String)

    @Test fun narrowAndPhoneHomeNavigation() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var paneWidth by mutableStateOf(200.dp)
        var fontScale by mutableFloatStateOf(2f)
        var trailingActions by mutableStateOf(1)
        rule.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = fontScale)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Column(Modifier.testTag("shot").width(paneWidth).background(PersonaTheme.palette.background)) {
                        HomeNavigation(
                            items = listOf(
                                NavItem("assistant", "Assistant", PhIcons.ChatCircle),
                                NavItem("agents", "Agents", PhIcons.Robot, badge = 2),
                            ),
                            selected = "agents", onSelect = {},
                            leading = { JPersonaButton(PersonaState.Idle, "Sidebar", {}) },
                            trailing = {
                                JIconButton(PhIcons.NotePencil, "New session", {})
                                if (trailingActions == 2) JIconButton(PersonaIcons.moreMenu, "List options", {})
                            },
                        )
                    }
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/home-navigation-shots").also { it.mkdirs() }
        val cases = listOf(
            ShotCase(200.dp, 2f, 1, "200-max-font"),
            ShotCase(360.dp, 1f, 1, "360-default"),
            ShotCase(280.dp, 1f, 1, "280-default"),
            ShotCase(280.dp, 1f, 2, "280-two-actions"),
            ShotCase(400.dp, 1f, 2, "400-two-actions"),
        )
        for (case in cases) {
            rule.runOnIdle {
                paneWidth = case.width
                fontScale = case.fontScale
                trailingActions = case.trailingActions
            }
            for (s in AppStyle.entries) for (d in listOf(false, true)) {
                rule.runOnIdle { style = s; dark = d }
                rule.waitForIdle()
                val name = "home-navigation-${case.label}-${s.name.lowercase()}-${if (d) "dark" else "light"}"
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            }
        }
    }
}
