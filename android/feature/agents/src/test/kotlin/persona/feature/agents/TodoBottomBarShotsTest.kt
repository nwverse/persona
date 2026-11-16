package persona.feature.agents

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
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
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.time.Instant

/**
 * The to-do page's bottom bar on a phone and on a wide window, an unfolded screen or a tablet (#9009): one picture per window,
 * state, style and appearance, named `todo-bar-<window>-<state>-<style>-<light|dark>.png` in `build/todo-bottom-bar-shots`.
 * Nothing here asserts a width (`TodoBottomBarWidthTest` does), so the same test can draw `main` and a branch for pictures side
 * by side. Native JVM images, not an emulator's.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class TodoBottomBarShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/todo-bottom-bar-shots").also { it.mkdirs() }

    private val now = Instant.parse("2026-11-16T09:30:00Z")
    private fun ago(minutes: Long) = now.minusSeconds(minutes * 60).toString()
    private val machines = listOf(Machine("mac", "Work laptop", online = true))
    private val sessions = listOf(
        Session("mac", "s1", title = "Fix the sign-in page", cwd = "/code/app", state = "working", spaceId = "w1", spaceName = "app",
            slotName = "1", lastText = "Writing the tests for AuthService", updatedAt = ago(1)),
    )
    private val todos = listOf(
        // Open, with an Agent at work: Complete and Hand to agent.
        Todo("open", title = "Plan the release checklist", notes = "Collect the merged pull requests and check that each one has a note.",
            createdAt = ago(120), updatedAt = ago(3), spaces = emptyList(),
            agents = listOf(TodoAgent("mac", "s1", title = "Fix the sign-in page", cwd = "/code/app"))),
        // Done: Reopen alone.
        Todo("done", title = "Write the release notes", notes = "Everything merged this week should be mentioned.",
            createdAt = ago(300), doneAt = ago(20), doneBy = "user", spaces = emptyList()),
    )

    private fun shot(window: String, widthDp: Int, heightDp: Int, state: String, style: AppStyle, dark: Boolean) {
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(widthDp.dp).height(heightDp.dp).background(PersonaTheme.palette.background)) {
                    TodoDetailScreen(state, todos, machines, sessions, null, onBack = {}, onOpenSession = {}, onHandOff = {}, now = now)
                }
            }
        }
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), "todo-bar-$window-$state-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun phone(state: String, style: AppStyle, dark: Boolean) = shot("phone", 400, 900, state, style, dark)
    private fun wide(state: String, style: AppStyle, dark: Boolean) = shot("wide", 840, 700, state, style, dark)

    // A phone, 400 x 900 dp.
    @Test fun phoneOpenGlass() = phone("open", AppStyle.Glass, false)
    @Test fun phoneOpenGlassDark() = phone("open", AppStyle.Glass, true)
    @Test fun phoneOpenBrief() = phone("open", AppStyle.Brief, false)
    @Test fun phoneOpenBriefDark() = phone("open", AppStyle.Brief, true)
    @Test fun phoneOpenBoard() = phone("open", AppStyle.Board, false)
    @Test fun phoneOpenBoardDark() = phone("open", AppStyle.Board, true)
    @Test fun phoneDoneGlass() = phone("done", AppStyle.Glass, false)

    // An unfolded screen or a tablet, 840 x 700 dp.
    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi") fun wideOpenGlass() = wide("open", AppStyle.Glass, false)
    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi") fun wideOpenGlassDark() = wide("open", AppStyle.Glass, true)
    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi") fun wideOpenBrief() = wide("open", AppStyle.Brief, false)
    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi") fun wideOpenBriefDark() = wide("open", AppStyle.Brief, true)
    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi") fun wideOpenBoard() = wide("open", AppStyle.Board, false)
    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi") fun wideOpenBoardDark() = wide("open", AppStyle.Board, true)
    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi") fun wideDoneGlass() = wide("done", AppStyle.Glass, false)
}
