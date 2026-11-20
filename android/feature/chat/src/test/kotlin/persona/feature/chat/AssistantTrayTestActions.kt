package persona.feature.chat

import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo

/** Scroll semantics need frames even when a screenshot test deliberately pauses its clock. */
internal fun ComposeContentTestRule.tapTray(tag: String) {
    val advance = mainClock.autoAdvance
    mainClock.autoAdvance = true
    try {
        onNodeWithTag("assistant-entry-$tag").performScrollTo().performClick()
        waitForIdle()
    } finally {
        mainClock.autoAdvance = advance
    }
}
