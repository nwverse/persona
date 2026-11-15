package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.foundation.gestures.ScrollScope
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertDoesNotExist
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.SessionListStatus
import persona.core.designsystem.PersonaTheme
import persona.core.domain.sort.AgentGrouping
import persona.core.domain.sort.AgentListSettings
import persona.core.domain.sort.AgentSession
import persona.core.domain.sort.buildAgentList

/**
 * The Agents feed over a real list: the page is held while the list is scrolled and lands when it rests, at the cap, at
 * the top or when a control changes; what came meanwhile is a pill that never moves the list. Time is the test clock's,
 * so the waits are the configured ones ([AgentsFeedTiming]), not measurements.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AgentsFeedHoldTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val mac = Machine("mac", "Mac", online = true)
    private fun session(id: String, state: String = "idle") =
        Session(machineId = "mac", sessionId = id, state = state, spaceId = "app", spaceName = "app", updatedAt = "2026-01-01T00:00:00Z")

    private fun page(sessions: List<Session>, paged: Boolean = false): AgentsPageState = AgentsPageState(
        sessions = sessions, machines = listOf(mac), loaded = true,
        list = buildAgentList(sessions.map(::AgentSession), listOf(mac), settings = AgentListSettings(grouping = AgentGrouping.MachineSpace)),
        pagination = SessionListStatus(paged = paged, loaded = true),
    )

    private val ids = (0 until 30).map { "s%02d".format(it) }
    private val base = ids.map { session(it) }

    private var latest by mutableStateOf(page(base))
    private var requests by mutableStateOf(emptySet<String>())
    private var scope by mutableStateOf<List<Any?>>(listOf("controls"))

    private lateinit var state: LazyListState
    private lateinit var uiScope: CoroutineScope
    private lateinit var shownPage: AgentsPageState
    private var gate = CompletableDeferred<Unit>()

    @Before fun start() {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                state = rememberLazyListState()
                uiScope = rememberCoroutineScope()
                val feed = rememberAgentsFeed(latest, requests, scope, state)
                SideEffect { shownPage = feed.page }
                Box {
                    LazyColumn(Modifier.height(400.dp), state = state) {
                        items(feed.page.rowIds(), key = { it }) { id -> Box(Modifier.fillMaxWidth().height(60.dp).testTag(id)) }
                    }
                    AgentsFeedNote(feed, Modifier.align(Alignment.BottomCenter).testTag("note"))
                }
            }
        }
        advance(100)
    }

    private fun advance(millis: Long) {
        rule.mainClock.advanceTimeBy(millis)
        rule.waitForIdle()
    }

    private fun scrollAway() {
        rule.runOnUiThread { uiScope.launch { state.scrollToItem(12) } }
        advance(1_000)
        assertTrue("the list rests below the top", state.canScrollBackward)
    }

    /** A scroll that stays in progress until [release]; [steps] run first, inside it. */
    private fun holdScroll(steps: suspend ScrollScope.() -> Unit = {}) {
        gate = CompletableDeferred()
        val mine = gate
        rule.runOnUiThread { uiScope.launch { state.scroll { steps(); mine.await() } } }
        advance(100)
        assertTrue(state.isScrollInProgress)
    }

    private fun release() {
        gate.complete(Unit)
        advance(50)
    }

    private fun push(change: (Session) -> Session = { it }, extra: List<Session> = emptyList()) {
        rule.runOnUiThread { latest = page(base.map(change) + extra) }
        advance(100)
    }

    private fun stateOf(id: String) = shownPage.sessions.single { it.sessionId == id }.state

    private fun needsYou(id: String) = { s: Session -> if (s.sessionId == id) s.copy(state = "needs_you") else s }

    @Test fun aPushDuringAScrollWaitsUntilTheListHasRestedThenLands() {
        scrollAway()
        holdScroll()
        push(needsYou("s03"))
        assertEquals("held while the list moves", "idle", stateOf("s03"))
        release()
        advance(AgentsFeedTiming.SETTLE_MILLIS - 150)
        assertEquals("still inside the rest", "idle", stateOf("s03"))
        advance(300)
        assertEquals("landed after the rest", "needs_you", stateOf("s03"))
    }

    @Test fun aHoldLandsAtTheCapEvenWhileTheListKeepsMoving() {
        scrollAway()
        holdScroll()
        push(needsYou("s03"))
        advance(AgentsFeedTiming.CAP_MILLIS - 1_000)
        assertEquals("not yet", "idle", stateOf("s03"))
        assertTrue("the list is still being scrolled", state.isScrollInProgress)
        advance(1_500)
        assertEquals("the cap passed", "needs_you", stateOf("s03"))
        release()
    }

    @Test fun everyPushThatWaitedLandsTogetherNothingIsDropped() {
        scrollAway()
        holdScroll()
        push(needsYou("s03"))
        rule.runOnUiThread { latest = page(base.map { if (it.sessionId == "s07") it.copy(state = "working") else needsYou("s03")(it) } + session("n1")) }
        advance(100)
        release()
        advance(AgentsFeedTiming.SETTLE_MILLIS + 200)
        assertEquals("needs_you", stateOf("s03"))
        assertEquals("working", stateOf("s07"))
        assertTrue(shownPage.sessions.any { it.sessionId == "n1" })
    }

    @Test fun aChangedControlLandsAtOnceWhileScrolling() {
        scrollAway()
        holdScroll()
        push(needsYou("s03"))
        assertEquals("idle", stateOf("s03"))
        rule.runOnUiThread { scope = listOf("another grouping") }
        advance(100)
        assertEquals("a control changed: what the list draws is the new list", "needs_you", stateOf("s03"))
        release()
    }

    @Test fun aFetchedPageLandsAtOnceWhileScrolling() {
        rule.runOnUiThread { latest = page(base, paged = true) }
        advance(100)
        scrollAway()
        holdScroll()
        rule.runOnUiThread { latest = page(base + session("more1") + session("more2"), paged = true) }
        advance(100)
        assertTrue("the page is in under the finger, with no gap", shownPage.sessions.any { it.sessionId == "more2" })
        release()
    }

    @Test fun anEmptiedOrReplacedListLandsAtOnceWhileScrolling() {
        scrollAway()
        holdScroll()
        rule.runOnUiThread { latest = page(listOf(session("x1"), session("x2"))) }
        advance(100)
        assertEquals(setOf("x1", "x2"), shownPage.sessions.map { it.sessionId }.toSet())
        rule.runOnUiThread { latest = page(emptyList()) }
        advance(100)
        assertTrue(shownPage.sessions.isEmpty())
        release()
    }

    @Test fun reachingTheTopLetsTheHoldGoWhileTheFingerIsStillDown() {
        scrollAway()
        val toTop = CompletableDeferred<Unit>()
        holdScroll { toTop.await(); scrollBy(-1_000_000f) }
        push(needsYou("s03"))
        assertEquals("idle", stateOf("s03"))
        toTop.complete(Unit)
        advance(100)
        assertFalse(state.canScrollBackward)
        assertEquals("the latest is what the top shows", "needs_you", stateOf("s03"))
        release()
    }

    @Test fun aNewNeedsYouAwayFromTheTopIsAPillAtOnceAndTheListStaysPut() {
        scrollAway()
        val (index, offset) = state.firstVisibleItemIndex to state.firstVisibleItemScrollOffset
        rule.onNodeWithTag("note").assertDoesNotExist()
        push(needsYou("s20"))
        rule.onNodeWithTag("note").assertIsDisplayed().assertTextContains("1 needs you")
        assertEquals(index, state.firstVisibleItemIndex)
        assertEquals(offset, state.firstVisibleItemScrollOffset)
    }

    @Test fun aRequestOfASessionThatAlreadyNeededYouIsNotNewButOneOfAnotherIs() {
        rule.runOnUiThread { latest = page(base.map(needsYou("s01"))) }
        advance(100)
        scrollAway()
        rule.runOnUiThread { requests = setOf("mac/s01") }
        advance(100)
        rule.onNodeWithTag("note").assertDoesNotExist()
        rule.runOnUiThread { requests = setOf("mac/s01", "mac/s05") }
        advance(100)
        rule.onNodeWithTag("note").assertIsDisplayed().assertTextContains("1 needs you")
    }

    @Test fun aNewRowAloneIsTheQuietKindOfNote() {
        scrollAway()
        push(extra = listOf(session("n1")))
        rule.onNodeWithTag("note").assertIsDisplayed().assertTextContains("1 new")
    }

    @Test fun nothingIsToldAtTheTop() {
        push(needsYou("s02"), extra = listOf(session("n1")))
        rule.onNodeWithTag("note").assertDoesNotExist()
    }

    @Test fun tappingThePillGoesToTheLatestAndTheNoteGoes() {
        scrollAway()
        push(needsYou("s20"))
        rule.onNodeWithTag("note").performClick()
        advance(500)
        assertFalse("back at the latest", state.canScrollBackward)
        rule.onNodeWithTag("note").assertDoesNotExist()
        assertEquals("what was held lands", "needs_you", stateOf("s20"))
    }

    @Test fun theNoteStartsOverFromWhereTheUserIsWhenAControlChanges() {
        scrollAway()
        push(extra = listOf(session("n1")))
        rule.onNodeWithTag("note").assertIsDisplayed()
        rule.runOnUiThread { scope = listOf("another grouping") }
        advance(100)
        rule.onNodeWithTag("note").assertDoesNotExist()
    }
}
