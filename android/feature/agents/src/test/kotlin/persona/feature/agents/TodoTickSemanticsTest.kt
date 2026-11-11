package persona.feature.agents

import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Todo
import persona.core.designsystem.PersonaTheme
import java.time.Instant

/**
 * The tick circle on a to-do row (#4077): TalkBack reads it as a checkbox named by what a tap does ("Done" on an open
 * to-do, "Reopen" on a finished one) with its checked state, instead of an unnamed control.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp")
class TodoTickSemanticsTest {
    @get:Rule val rule = createComposeRule()

    private val now = Instant.parse("2026-11-09T09:30:00Z")

    private fun show(todo: Todo) {
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                TodosScreen(listOf(todo), emptyList(), null, onBack = {}, onOpenTodo = {}, now = now)
            }
        }
        rule.waitForIdle()
    }

    private fun tick(label: String, state: ToggleableState) {
        rule.onNode(hasContentDescription(label) and SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Checkbox))
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.ToggleableState, state))
            .assertHasClickAction()
    }

    @Test fun openTodoTickReadsDone() {
        show(Todo("open", title = "Write the release notes", createdAt = "2026-11-09T08:30:00Z"))
        tick("Done", ToggleableState.Off)
    }

    @Test fun finishedTodoTickReadsReopen() {
        show(Todo("done", title = "Write the release notes", createdAt = "2026-11-09T04:30:00Z",
            doneAt = "2026-11-09T09:00:00Z", doneBy = "user"))
        tick("Reopen", ToggleableState.On)
    }
}
