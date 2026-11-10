package persona.feature.inbox

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.PersonaTheme

/** The play / pause icons of Lark voice and video messages have no description; TalkBack still names them (#3904). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class LarkMediaA11yTest {
    @get:Rule val rule = createComposeRule()

    private fun clickLabel(label: String) = SemanticsMatcher("click label is $label") {
        it.config.getOrNull(SemanticsActions.OnClick)?.label == label
    }

    @Test fun aVideoIsNamedAndSaysATapPlaysIt() {
        rule.setContent { PersonaTheme(reduceMotion = true) { LarkVideoView(LarkPart.Video("video", 12_000), onPlay = {}) } }
        rule.onNodeWithContentDescription("Video").assert(clickLabel("Play"))
    }

    @Test fun aVoiceMessageIsNamedAndSaysATapPlaysIt() {
        rule.setContent { PersonaTheme(reduceMotion = true) { LarkVoiceView(LarkPart.Audio("audio", 5_000)) } }
        rule.onNodeWithContentDescription("Voice").assert(clickLabel("Play"))
    }
}
