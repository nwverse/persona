package persona.core.designsystem

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/**
 * A section header that folds its rows tells TalkBack whether the section is open ("Expanded" / "Collapsed") and what
 * a tap does ("Collapse" / "Expand"), in every style (#4100); a header that does not fold says neither.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class SectionFoldSemanticsTest {
    @get:Rule val rule = createComposeRule()

    private fun SemanticsNodeInteraction.reads(state: String, clickLabel: String) =
        assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state))
            .assert(SemanticsMatcher("click label is $clickLabel") { it.config.getOrNull(SemanticsActions.OnClick)?.label == clickLabel })

    @Test fun foldingHeaderSaysItsStateAndWhatATapDoes() {
        var open by mutableStateOf(true)
        rule.setContent {
            Column {
                for (style in AppStyle.entries) PersonaTheme(darkTheme = false, reduceMotion = true, style = style) {
                    JSectionHeader("Done", Modifier.testTag("$style"), count = 3, onClick = { open = !open }, open = open)
                    JSectionHeader("Today", Modifier.testTag("sub-$style"), sub = true, onClick = { open = !open }, open = open)
                }
            }
        }
        for (style in AppStyle.entries) {
            rule.onNodeWithTag("$style").reads("Expanded", "Collapse")
            rule.onNodeWithTag("sub-$style").reads("Expanded", "Collapse")
        }
        rule.onNodeWithTag("${AppStyle.Glass}").performClick()
        for (style in AppStyle.entries) {
            rule.onNodeWithTag("$style").reads("Collapsed", "Expand")
            rule.onNodeWithTag("sub-$style").reads("Collapsed", "Expand")
        }
    }

    @Test fun headerThatDoesNotFoldSaysNoState() {
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                Column {
                    JSectionHeader("Agents", Modifier.testTag("plain"), count = 2)
                    JSectionHeader("Pinned", Modifier.testTag("tap"), onClick = {})
                }
            }
        }
        rule.onNodeWithTag("plain").assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.StateDescription))
        rule.onNodeWithTag("tap").assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.StateDescription))
            .assert(SemanticsMatcher("no click label") { it.config.getOrNull(SemanticsActions.OnClick)?.label == null })
    }

    /** A header drawn around a [JSectionHeader] with its own caret folds with [foldToggle] and reads the same. */
    @Test fun foldToggleSaysItsStateAndWhatATapDoes() {
        var open by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                Box(Modifier.testTag("row").foldToggle(open) { open = !open }) { JSectionHeader("Past agents", count = 1) }
            }
        }
        rule.onNodeWithTag("row").reads("Collapsed", "Expand").performClick()
        rule.onNodeWithTag("row").reads("Expanded", "Collapse")
    }
}
