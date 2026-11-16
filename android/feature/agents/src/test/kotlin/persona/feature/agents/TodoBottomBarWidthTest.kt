package persona.feature.agents

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Todo
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import java.time.Instant

/**
 * The to-do page's bottom bar across window widths (#9009, iOS #9008): Complete (Reopen once done) and Hand to agent stop at
 * `Tokens.Size.readable`, centered, where they had been about 370dp wide each across a tablet or an unfolded screen; on a phone,
 * and on any window narrower than the cap, they keep the width of their bar. It reads where the buttons stand, the row they
 * make, in every style, with an open to-do (two buttons) and a done one (Reopen alone).
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class TodoBottomBarWidthTest {
    @get:Rule val rule = createComposeRule()

    private val now = Instant.parse("2026-11-16T09:30:00Z")
    private val todos = listOf(
        Todo("open", title = "Plan the release checklist", createdAt = "2026-11-16T07:30:00Z", spaces = emptyList()),
        Todo("done", title = "Write the release notes", createdAt = "2026-11-16T04:30:00Z", doneAt = "2026-11-16T09:00:00Z",
            doneBy = "user", spaces = emptyList()),
    )

    /** The most a bar's own padding takes from each side of the window it fills: Glass's floating capsule, 10 + 12 + 4 = 26dp. */
    private val barPadding = 32.dp

    /** The row the buttons make, from the window's left edge to its right edge. */
    private class ButtonRow(val left: Dp, val right: Dp) { val width get() = right - left }

    /** Runs [check] for every style and both states of the page, on the window this test's qualifiers give. */
    private fun forEachBar(check: (where: String, row: ButtonRow, window: Dp) -> Unit) {
        var style by mutableStateOf(AppStyle.Glass)
        var id by mutableStateOf("open")
        rule.setContent {
            PersonaTheme(reduceMotion = true, style = style) {
                TodoDetailScreen(id, todos, emptyList(), emptyList(), null, onBack = {}, onOpenSession = {}, onHandOff = {}, now = now)
            }
        }
        val context = ApplicationProvider.getApplicationContext<Context>()
        val window = rule.onRoot().getUnclippedBoundsInRoot().let { it.right - it.left }
        for (s in AppStyle.entries) for (state in listOf("open", "done")) {
            rule.runOnIdle { style = s; id = state }
            rule.waitForIdle()
            val words = if (state == "done") listOf(R.string.agents_todo_reopen)
            else listOf(R.string.agents_todo_complete, R.string.agents_todo_hand_to_agent)
            val buttons = words.map { rule.onNode(hasText(context.getString(it)) and hasClickAction()).getUnclippedBoundsInRoot() }
            check("${s.name} $state", ButtonRow(buttons.minOf { it.left }, buttons.maxOf { it.right }), window)
        }
    }

    /** No cap in play: the row reaches the bar's padding on both sides, as it did before the cap. */
    private fun assertFillsItsBar(where: String, row: ButtonRow, window: Dp) {
        assertTrue("$where: the row starts ${row.left} from the left, more than a bar's padding", row.left <= barPadding)
        assertTrue("$where: the row ends ${window - row.right} from the right, more than a bar's padding", window - row.right <= barPadding)
        assertEquals("$where: centered", row.left.value, (window - row.right).value, 0.5f)
    }

    /** Capped: no wider than a readable page, the bar's padding inside it, and in the middle of the window. */
    private fun assertStopsAtTheReadableWidth(where: String, row: ButtonRow, window: Dp) {
        assertTrue("$where: the window is ${window}, not wider than the cap", window > Tokens.Size.readable + barPadding * 2)
        assertTrue("$where: the row is ${row.width} wide, more than ${Tokens.Size.readable}", row.width <= Tokens.Size.readable)
        assertTrue("$where: the row is ${row.width} wide, much less than the cap", row.width >= Tokens.Size.readable - barPadding * 2)
        assertEquals("$where: centered", row.left.value, (window - row.right).value, 0.5f)
    }

    @Test fun aPhoneKeepsTheWidthOfItsBar() = forEachBar(this::assertFillsItsBar)

    @Test @Config(qualifiers = "en-w520dp-h900dp-xhdpi")
    fun aWindowNarrowerThanTheCapIsNotCapped() = forEachBar(this::assertFillsItsBar)

    @Test @Config(qualifiers = "en-w840dp-h700dp-xhdpi")
    fun anUnfoldedScreenStopsAtTheReadableWidth() = forEachBar(this::assertStopsAtTheReadableWidth)

    @Test @Config(qualifiers = "en-w1280dp-h800dp-xhdpi")
    fun aTabletStopsAtTheReadableWidth() = forEachBar(this::assertStopsAtTheReadableWidth)

    /** As iOS's row on an iPhone turned on its side: the cap is the window's width, not the device. */
    @Test @Config(qualifiers = "en-w915dp-h412dp-xhdpi")
    fun aPhoneOnItsSideStopsAtTheReadableWidth() = forEachBar(this::assertStopsAtTheReadableWidth)
}
