package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

/**
 * Resting frames for #9803 before/after review: phone drawer and 951×673 foldable column, Glass light and dark.
 * Writes under `/tmp/persona-leader/shots-9803/` (or `ROOT_EDGE_SHOTS` when set). Exclusion does not change pixels.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35])
class RootEdgeExclusionShotsTest {
    @get:Rule val rule = createComposeRule()

    private val outDir = File(
        System.getenv("ROOT_EDGE_SHOTS") ?: "/tmp/persona-leader/shots-9803",
    ).also { it.mkdirs() }

    private val phase = System.getenv("ROOT_EDGE_SHOTS_PHASE") ?: "after"

    @Config(sdk = [35], qualifiers = "en-w411dp-h891dp-mdpi")
    @Test fun phoneDrawerAtRest() {
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                JShellDrawer(
                    opened = false,
                    onDismiss = {},
                    modifier = Modifier.testTag("shot").width(411.dp).height(891.dp),
                    dragEnabled = true,
                    drawer = { Box(Modifier.fillMaxSize().background(Color(0xFFCC4444))) },
                ) {
                    Box(Modifier.fillMaxSize().background(Color(0xFF4466CC)))
                }
            }
        }
        for (isDark in listOf(false, true)) {
            dark = isDark
            rule.waitForIdle()
            save("phone-glass-${if (isDark) "dark" else "light"}-$phase")
        }
    }

    @Config(sdk = [35], qualifiers = "en-w951dp-h673dp-mdpi")
    @Test fun foldableColumnAtRest() {
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.fillMaxSize().testTag("shot").background(PersonaTheme.palette.background)) {
                    JSidebarColumn(
                        sidebar = { Box(Modifier.fillMaxSize().background(Color(0xFF44AA66))) },
                        width = 320.dp,
                        pageStart = 320.dp,
                        shown = false,
                        overlay = false,
                        drags = true,
                        detailAtRoot = true,
                    ) {
                        Box(Modifier.fillMaxSize().background(Color(0xFF4466CC)))
                    }
                }
            }
        }
        for (isDark in listOf(false, true)) {
            dark = isDark
            rule.waitForIdle()
            save("foldable-951x673-glass-${if (isDark) "dark" else "light"}-$phase")
        }
    }

    private fun save(name: String) {
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(outDir, "$name.png").outputStream().use {
            bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)
        }
        println("wrote ${File(outDir, "$name.png").absolutePath} ${bitmap.width}x${bitmap.height}")
    }
}
