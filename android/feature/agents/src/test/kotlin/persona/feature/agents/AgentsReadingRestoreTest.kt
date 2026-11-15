package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.domain.sort.AgentSession

/**
 * A rebuilt screen (a killed process, a restored back stack) resumes by the row's key and offset, not by the index the
 * list's own saver keeps: the hub's order moves while the app is away.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AgentsReadingRestoreTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private fun entry(number: Int) =
        TreeRowEntry(AgentSession(Session("demo", "s-$number")), depth = 0, first = false, last = false, located = false)

    private val original = (0 until 60).map(::entry)

    private var rows by mutableStateOf(original)
    private var loaded by mutableStateOf(true)
    private lateinit var state: LazyListState
    private lateinit var uiScope: CoroutineScope

    private fun show(restoration: StateRestorationTester) = restoration.setContent {
        state = rememberLazyListState()
        uiScope = rememberCoroutineScope()
        RestoreAgentReadingPosition(state, rows, paginationFooter = false, loaded = loaded)
        // A heading above the tree, as the page has, so the tree does not start at the list's first item.
        LazyColumn(Modifier.height(300.dp), state = state, verticalArrangement = Arrangement.spacedBy(8.dp)) {
            item("search") { Box(Modifier.fillMaxWidth().height(40.dp)) }
            items(rows, key = { it.key }) { Box(Modifier.fillMaxWidth().height(52.dp)) }
        }
    }

    /** The first tree row in view and where it is, in the list's own measure. */
    private fun firstRow(): Pair<Any, Int> = state.layoutInfo.visibleItemsInfo.first { it.key != "search" }.let { it.key to it.offset }

    private fun scrollTo(key: String) {
        val index = rows.indexOfFirst { it.key == key } + 1
        rule.runOnUiThread { uiScope.launch { state.scrollToItem(index, 17) } }
        rule.waitForIdle()
        rule.waitForIdle()
    }

    @Test fun theRowThatWasFirstInViewIsBackWhereItStoodEvenThoughTheOrderMoved() {
        val restoration = StateRestorationTester(rule)
        show(restoration)
        scrollTo("session:demo/s-25")
        val (key, offset) = firstRow()
        assertEquals("session:demo/s-25", key)

        // While the app was away the hub put that row near the top and moved others above it.
        rule.runOnIdle { rows = (listOf(rows[25]) + rows.filterIndexed { index, _ -> index != 25 }.reversed()) }
        restoration.emulateSavedInstanceStateRestore()
        rule.waitForIdle()
        rule.waitForIdle()

        val (after, afterOffset) = firstRow()
        assertEquals("the same row, found by its key", key, after)
        assertEquals("at the same place in the window", offset, afterOffset)
    }

    @Test fun aRowThatIsGoneStartsTheListFromTheTop() {
        val restoration = StateRestorationTester(rule)
        show(restoration)
        scrollTo("session:demo/s-25")
        val (key, _) = firstRow()

        rule.runOnIdle { rows = rows.filterNot { it.key == key } }
        restoration.emulateSavedInstanceStateRestore()
        rule.waitForIdle()
        rule.waitForIdle()

        assertEquals(0, state.firstVisibleItemIndex)
        assertEquals(0, state.firstVisibleItemScrollOffset)
    }

    @Test fun aListThatIsNotInYetWaitsForTheRowAndPutsItBackWhenItArrives() {
        val restoration = StateRestorationTester(rule)
        show(restoration)
        scrollTo("session:demo/s-40")
        val (key, offset) = firstRow()

        rule.runOnIdle { rows = emptyList() }
        rule.runOnIdle { loaded = false }
        restoration.emulateSavedInstanceStateRestore()
        rule.waitForIdle()
        assertEquals("nothing to scroll to yet, nothing decided", 0, state.firstVisibleItemIndex)

        rule.runOnIdle { rows = original }
        rule.runOnIdle { loaded = true }
        rule.waitForIdle()
        rule.waitForIdle()
        assertEquals(key to offset, firstRow())
    }

    @Test fun aListThatLoadedEmptyOpensAtTheTopWithoutFailing() {
        val restoration = StateRestorationTester(rule)
        show(restoration)
        scrollTo("session:demo/s-25")

        rule.runOnIdle { rows = emptyList() }
        restoration.emulateSavedInstanceStateRestore()
        rule.waitForIdle()

        assertEquals(0, state.firstVisibleItemIndex)
        assertEquals(0, state.firstVisibleItemScrollOffset)
        rule.runOnIdle { rows = original }
        rule.waitForIdle()
        assertEquals("the user's list is the list as it is, from the top", 0, state.firstVisibleItemIndex)
    }

    @Test fun aListLeftAtTheTopOpensAtTheTop() {
        val restoration = StateRestorationTester(rule)
        show(restoration)
        rule.waitForIdle()

        rule.runOnIdle { rows = original.reversed() }
        restoration.emulateSavedInstanceStateRestore()
        rule.waitForIdle()

        assertEquals(0, state.firstVisibleItemIndex)
        assertTrue(!state.canScrollBackward)
    }
}
