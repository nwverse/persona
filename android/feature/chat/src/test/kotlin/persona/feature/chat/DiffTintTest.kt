package persona.feature.chat

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaPalette
import persona.core.designsystem.PersonaTheme

/**
 * What an added or removed row is drawn on: an edit's rows use the style's diff tints over their own background, in every
 * style and both appearances; a Codex patch's rows stay plain, as they were. Read from the rendered pixels, beside an
 * unchanged row of the same diff.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1200dp-xhdpi")
class DiffTintTest {
    @get:Rule val rule = createComposeRule()

    private val edit = toolDiff(Item("call", kind = "tool_use", toolName = "Edit",
        toolInput = buildJsonObject { put("old_string", "keep\nremoved row"); put("new_string", "keep\nadded row") }))!!
    private val patch = patchDiff("@@ -1,2 +1,2 @@\n keep\n-removed row\n+added row", null)

    private var palette: PersonaPalette? = null
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)

    /** One composition (a rule takes one setContent); the style and appearance move under it. */
    private fun show(diff: ToolDiff) = rule.setContent {
        PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
            palette = PersonaTheme.palette
            DiffView(diff, "call")
        }
    }

    private fun each(check: (AppStyle, Boolean) -> Unit) {
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            check(s, d)
        }
    }

    /** A pixel at the right end of the row's code, past its last glyph: the row's own background. */
    private fun rowColour(text: String): Color {
        val image = rule.onNodeWithText(text).captureToImage()
        return Color(image.asAndroidBitmap().getPixel(image.width - 4, image.height / 2))
    }

    private fun assertClose(message: String, expected: Color, actual: Color, tolerance: Int = 3) {
        for ((name, a, b) in listOf(Triple("a", expected.alpha, actual.alpha), Triple("r", expected.red, actual.red),
            Triple("g", expected.green, actual.green), Triple("b", expected.blue, actual.blue))) {
            val delta = Math.abs(a * 255 - b * 255)
            assertTrue("$message: $name expected ${expected.toArgb().toUInt().toString(16)} got ${actual.toArgb().toUInt().toString(16)}", delta <= tolerance)
        }
    }

    @Test fun anEditsRowsUseTheStylesDiffTintsInEveryStyleAndAppearance() {
        show(edit)
        each { s, d ->
            val p = palette!!
            val plain = rowColour("keep")
            assertClose("$s dark=$d added", p.diffAddFill.compositeOver(plain), rowColour("added row"))
            assertClose("$s dark=$d removed", p.diffDelFill.compositeOver(plain), rowColour("removed row"))
        }
    }

    @Test fun aCodexPatchsRowsKeepNoTint() {
        show(patch)
        each { s, d ->
            val plain = rowColour("keep")
            assertClose("$s dark=$d added", plain, rowColour("added row"), tolerance = 1)
            assertClose("$s dark=$d removed", plain, rowColour("removed row"), tolerance = 1)
        }
    }
}
