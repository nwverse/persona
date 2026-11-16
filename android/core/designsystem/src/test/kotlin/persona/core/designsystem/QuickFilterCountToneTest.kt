package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File

/**
 * A quick filter's toned count (#9053, the Inbox's section bar): a badge in its state's colours that hides at 0 and caps
 * at 99+, with the exact number in the spoken name; a bay without a tone keeps its quiet digits and its old semantics,
 * so the Agents list's filters draw as before. Every style, light and dark; PNGs in build/quick-filter-count-tone-shots/.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h400dp-xhdpi")
class QuickFilterCountToneTest {
    @get:Rule val rule = createComposeRule()

    private val toned = listOf(
        Bay("Needs you", 120, Status.NeedsYou, countTone = Status.NeedsYou),
        Bay("FYI", 0, countTone = Status.Idle),
        Bay("In progress", 4, countTone = Status.Idle),
        Bay("All"),
    )

    private fun texts(text: String) = rule.onAllNodes(hasText(text), useUnmergedTree = true).fetchSemanticsNodes()
    private fun spoken(text: String) = rule.onAllNodes(hasContentDescription(text)).fetchSemanticsNodes()

    @Test fun aTonedCountHidesAtZeroCapsAndIsSpokenWithItsName() {
        var style by mutableStateOf(AppStyle.Glass)
        rule.setContent {
            PersonaTheme(style = style, reduceMotion = true) { QuickFilterBar(toned, 0, onSelect = {}) }
        }
        for (s in AppStyle.entries) {
            style = s
            rule.waitForIdle()
            assertTrue("${s.key}: 99+ past 99", texts("99+").isNotEmpty())
            assertTrue("${s.key}: never the raw number", texts("120").isEmpty())
            assertTrue("${s.key}: no badge at 0", texts("0").isEmpty())
            assertEquals("${s.key}: the exact number spoken", 1, spoken("Needs you, 120").size)
            assertEquals("${s.key}: the name alone at 0", 1, spoken("FYI").size)
            assertEquals("${s.key}: a neutral count spoken too", 1, spoken("In progress, 4").size)
        }
    }

    @Test fun aBayWithoutAToneKeepsItsDigitsAndSemantics() {
        var style by mutableStateOf(AppStyle.Glass)
        rule.setContent {
            PersonaTheme(style = style, reduceMotion = true) {
                QuickFilterBar(listOf(Bay("All", 0), Bay("Needs you", 120, Status.NeedsYou), Bay("Working", 3, Status.Working)), 0, onSelect = {})
            }
        }
        for (s in AppStyle.entries) {
            style = s
            rule.waitForIdle()
            // Brief's tabs read "Needs you 120" in one text.
            assertTrue("${s.key}: the plain count as it was",
                rule.onAllNodes(hasText("120", substring = true), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty())
            assertTrue("${s.key}: no cap without a tone", rule.onAllNodes(hasText("99+", substring = true), useUnmergedTree = true).fetchSemanticsNodes().isEmpty())
            val described = rule.onAllNodes(hasText("Needs you")).fetchSemanticsNodes()
                .filter { SemanticsProperties.ContentDescription in it.config }
            assertTrue("${s.key}: no spoken name added", described.isEmpty())
        }
    }

    @Test fun drawsInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    QuickFilterBar(listOf(Bay("Needs you", 12, Status.NeedsYou, countTone = Status.NeedsYou),
                        Bay("In progress", 3, countTone = Status.Idle), Bay("FYI", 140, countTone = Status.Idle), Bay("Done"), Bay("All")), 0, onSelect = {})
                }
            }
        }
        val dir = File(System.getProperty("persona.shots") ?: "build/quick-filter-count-tone-shots").also { it.mkdirs() }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(dir, "count-tone-${s.key}-${if (d) "dark" else "light"}.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
