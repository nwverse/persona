package persona.core.designsystem

import android.view.View
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toPixelMap
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.dp
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.icons.PhIcons
import kotlin.math.abs
import kotlin.math.min

/**
 * The drawer's finger on the JVM. Progress is the blue page's left edge in the picture: `graphicsLayer` translation
 * does not move layout bounds. The rules copy `DrawerPan` (`apple/Persona/Views/Assistant/AssistantSidebar.swift`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-mdpi")
class DrawerDragGestureTest {
    @get:Rule val rule = createComposeRule()

    private val shellWidth = 360.dp
    private val shellHeight = 640.dp
    private val page = Color.Blue
    private val sidebar = Color.Red

    @Test fun rightwardDragTracksTheFingerAndAVerticalDragDoesNot() {
        val vertical = ScrollState(0)
        show(reduce = true) {
            Column(Modifier.fillMaxSize().verticalScroll(vertical)) {
                Box(Modifier.fillMaxWidth().height(1600.dp))
            }
        }
        // Finger moving up scrolls the list. A downward drag at the top does not, and iOS would not take it either.
        drag(from = Offset(80f, 400f), by = Offset(8f, -180f))
        assertTrue("vertical scroll moved ${vertical.value}", vertical.value > 20)
        assertEquals(0f, edge().toFloat(), 3f)

        val drawer = drawerPx()
        drag(from = Offset(80f, 240f), by = Offset(drawer / 2f, 6f), hold = true)
        frame()
        assertEquals(drawer / 2f, edge().toFloat(), 4f)
        val held = edge()
        rule.mainClock.advanceTimeBy(200)
        assertEquals(held.toFloat(), edge().toFloat(), 2f)
    }

    @Test fun horizontalScrollerWithRoomKeepsTheDrag() {
        val scroll = ScrollState(0)
        show(reduce = true) {
            Box(Modifier.horizontalScroll(scroll)) {
                Box(Modifier.width(900.dp).height(120.dp).background(Color.Yellow))
            }
        }
        assertTrue("scroller max ${scroll.maxValue}", scroll.maxValue > 200)
        // Room back toward the start. iOS refuses (`sidewaysScroller`, AssistantSidebar.swift:556-559, line 526).
        rule.runOnIdle { scroll.dispatchRawDelta(200f - scroll.value) }
        val before = scroll.value
        drag(from = Offset(120f, 40f), by = Offset(90f, 0f))
        assertTrue("scroll $before -> ${scroll.value}", scroll.value < before - 10)
        assertEquals(0f, edge().toFloat(), 4f)
    }

    @Test fun horizontalScrollerAtItsStartIsRecorded() {
        val scroll = ScrollState(0)
        show(reduce = true) {
            Box(Modifier.horizontalScroll(scroll)) {
                Box(Modifier.width(900.dp).height(120.dp).background(Color.Yellow))
            }
        }
        assertEquals(0, scroll.value)
        drag(from = Offset(120f, 40f), by = Offset(90f, 0f))
        // iOS takes a rightward drag when the scroller is already at its start (no room, AssistantSidebar.swift:558).
        // Compose's horizontalScroll consumes that overscroll, so this drawer yields the whole gesture.
        assertEquals("at-start edge=${edge()} scroll=${scroll.value}", 0f, edge().toFloat(), 4f)
    }

    @Test fun swipeToReplyKeepsALeftwardDrag() {
        var replied = false
        show(reduce = true) {
            Box(Modifier.fillMaxWidth().height(88.dp).background(Color.Green).swipeToReply("Reply") { replied = true })
        }
        drag(from = Offset(200f, 40f), by = Offset(-140f, 0f))
        assertTrue(replied)
        assertEquals(0f, edge().toFloat(), 3f)
    }

    @Test fun swipeRowKeepsTheOpenDrawer() {
        var opened by mutableStateOf(true)
        show(
            reduce = true,
            opened = { opened },
            onDismiss = { opened = false },
            drawer = {
                JSwipeActions(end = SwipeAction("Done", PhIcons.Check, Status.Unread, {})) {
                    Box(Modifier.fillMaxWidth().height(80.dp).background(sidebar))
                }
            },
        ) { Box(Modifier.fillMaxSize()) }
        rule.mainClock.advanceTimeBy(50)
        val openEdge = edge()
        assertTrue("open edge $openEdge", openEdge > 200)
        // The row is in the uncovered sidebar. iOS refuses here (`DrawerPan`, AssistantSidebar.swift:500).
        drag(from = Offset(40f, 36f), by = Offset(-70f, 0f))
        assertEquals(openEdge.toFloat(), edge().toFloat(), 4f)
        assertTrue(opened)
    }

    @Test fun leftwardDragOnTheOpenPageCloses() {
        var opened by mutableStateOf(true)
        show(reduce = true, opened = { opened }, onDismiss = { opened = false }) { Box(Modifier.fillMaxSize()) }
        rule.mainClock.advanceTimeBy(50)
        val openEdge = edge()
        drag(from = Offset(openEdge + 30f, 300f), by = Offset(-160f, 0f))
        rule.mainClock.advanceTimeBy(50)
        assertEquals(0f, edge().toFloat(), 4f)
        assertTrue(!opened)
    }

    @Test fun pushedPageDoesNotDrag() {
        show(reduce = true, dragEnabled = false) { Box(Modifier.fillMaxSize()) }
        drag(from = Offset(80f, 200f), by = Offset(180f, 0f))
        assertEquals(0f, edge().toFloat(), 3f)
    }

    @Test fun systemBackInsetIsLeftAlone() {
        lateinit var host: View
        show(reduce = true, onView = { host = it }) { Box(Modifier.fillMaxSize()) }
        val insets = WindowInsetsCompat.Builder()
            .setInsets(WindowInsetsCompat.Type.systemGestures(), Insets.of(48, 0, 48, 0))
            .setInsets(WindowInsetsCompat.Type.mandatorySystemGestures(), Insets.of(48, 0, 48, 0))
            .build()
        rule.runOnIdle { ViewCompat.dispatchApplyWindowInsets(host, insets) }
        rule.mainClock.advanceTimeBy(50)
        drag(from = Offset(10f, 240f), by = Offset(160f, 0f))
        assertEquals(0f, edge().toFloat(), 3f)
        drag(from = Offset(80f, 280f), by = Offset(180f, 0f))
        rule.mainClock.advanceTimeBy(50)
        assertTrue("past the inset, edge ${edge()}", edge() > 200)
    }

    @Test fun glassCarriesTheFingerVelocity() {
        val (slow, fast) = pairedTravel(AppStyle.Glass)
        // Same clock on both: the spring that kept the finger's speed is further along.
        assertTrue("glass slow $slow fast $fast", fast.second > slow.second + 6)
    }

    @Test fun briefIgnoresFingerVelocity() {
        val (slow, fast) = pairedTravel(AppStyle.Brief)
        // Brief's tween ignores the finger's speed, so the two releases are in the same place.
        assertTrue("brief slow $slow fast $fast", abs(fast.second - slow.second) < 14)
    }

    @Test fun reducedMotionSettlesOnTheNextFrame() {
        var opened by mutableStateOf(false)
        show(reduce = true, opened = { opened }, onOpen = { opened = true }) { Box(Modifier.fillMaxSize()) }
        // Under half, faster than 300 dp/s. The flick opens and the reduced tween is instant.
        drag(from = Offset(60f, 220f), by = Offset(40f, 0f), delayMillis = 5)
        rule.mainClock.advanceTimeBy(32)
        assertTrue("flick edge ${edge()}", edge() > 200)
        assertTrue(opened)
    }

    @Test fun fingerTakesOverMidSpring() {
        var opened by mutableStateOf(false)
        show(style = AppStyle.Glass, reduce = false, opened = { opened }, onOpen = { opened = true }) {
            Box(Modifier.fillMaxSize())
        }
        drag(from = Offset(60f, 220f), by = Offset(40f, 0f), delayMillis = 5)
        rule.mainClock.advanceTimeBy(40)
        assertTrue("mid ${edge()}", edge() > 30)
        drag(from = Offset(70f, 300f), by = Offset(24f, 0f), hold = true)
        frame()
        val held = edge()
        rule.mainClock.advanceTimeBy(280)
        assertEquals(held.toFloat(), edge().toFloat(), 2f)
    }

    @Test fun hapticOnlyWhenAClosedDragSettlesOpen() {
        val recorded = RecordingHaptics()
        var opened by mutableStateOf(false)
        show(
            reduce = true,
            opened = { opened },
            onOpen = { opened = true },
            onDismiss = { opened = false },
            haptics = recorded,
        ) { Box(Modifier.fillMaxSize()) }
        drag(from = Offset(60f, 220f), by = Offset(40f, 0f), delayMillis = 5)
        rule.mainClock.advanceTimeBy(32)
        assertEquals(listOf(HapticFeedbackType.ContextClick), recorded.types)
        recorded.types.clear()
        opened = false
        rule.mainClock.advanceTimeBy(32)
        assertTrue(recorded.types.isEmpty())
        // Slow, and short of half: projection closes, and a drag that does not open plays nothing (line 110).
        drag(from = Offset(60f, 260f), by = Offset(70f, 0f), delayMillis = 400)
        rule.mainClock.advanceTimeBy(32)
        assertEquals(0f, edge().toFloat(), 4f)
        assertTrue(recorded.types.isEmpty())
    }

    /** Slow settle, then a flick, each from about 0.7 of the width, sampled 40 ms later. One composition. */
    private fun pairedTravel(style: AppStyle): Pair<Pair<Int, Int>, Pair<Int, Int>> {
        var opened by mutableStateOf(false)
        show(
            style = style,
            reduce = false,
            opened = { opened },
            onOpen = { opened = true },
            onDismiss = { opened = false },
        ) { Box(Modifier.fillMaxSize()) }
        val slow = travel(fast = false)
        opened = false
        rule.mainClock.advanceTimeBy(800)
        assertEquals("did not reclose from $slow", 0f, edge().toFloat(), 6f)
        return slow to travel(fast = true)
    }

    /** Where the page is 16 ms after release, and again 64 ms after release. */
    private fun travel(fast: Boolean): Pair<Int, Int> {
        val distance = drawerPx() * 0.7f
        // About 200 px in 120 ms: faster than 300 dp/s, slow enough for the 0.32 s spring to stay on screen.
        if (fast) drag(from = Offset(40f, 200f), by = Offset(distance, 0f), delayMillis = 120)
        else drag(from = Offset(40f, 200f), by = Offset(distance, 0f), delayMillis = 16, settleMillis = 400)
        frame()
        val start = edge()
        rule.mainClock.advanceTimeBy(48)
        return start to edge()
    }

    private fun frame() {
        rule.mainClock.advanceTimeBy(16)
    }

    private fun show(
        style: AppStyle = AppStyle.Glass,
        reduce: Boolean = true,
        dragEnabled: Boolean = true,
        opened: () -> Boolean = { false },
        onOpen: () -> Unit = {},
        onDismiss: () -> Unit = {},
        haptics: HapticFeedback? = null,
        onView: (View) -> Unit = {},
        drawer: @Composable () -> Unit = { Box(Modifier.fillMaxSize().background(sidebar)) },
        content: @Composable () -> Unit,
    ) {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            onView(LocalView.current)
            val shell: @Composable () -> Unit = {
                PersonaTheme(darkTheme = false, reduceMotion = reduce, style = style) {
                    JShellDrawer(
                        opened(),
                        onDismiss = onDismiss,
                        modifier = Modifier.testTag("shell").width(shellWidth).height(shellHeight),
                        dragEnabled = dragEnabled,
                        onOpen = onOpen,
                        drawer = drawer,
                    ) {
                        Box(Modifier.fillMaxSize().background(page)) { content() }
                    }
                }
            }
            if (haptics == null) shell()
            else CompositionLocalProvider(LocalHapticFeedback provides haptics) { shell() }
        }
        rule.mainClock.advanceTimeBy(100)
    }

    private fun drag(
        from: Offset,
        by: Offset,
        hold: Boolean = false,
        delayMillis: Long = 16,
        settleMillis: Long = 0,
    ) {
        rule.onNodeWithTag("shell").performTouchInput {
            down(from)
            // Lsq2 needs three samples. One moveBy plus the down is not a flick.
            val steps = 6
            val piece = by / steps.toFloat()
            val each = (delayMillis / steps).coerceAtLeast(1)
            repeat(steps) { moveBy(piece, delayMillis = each) }
            if (settleMillis > 0) moveBy(Offset(1f, 0f), delayMillis = settleMillis)
            if (!hold) up()
        }
    }

    private fun drawerPx(): Float {
        val width = with(rule.density) { shellWidth.toPx() }
        val cap = with(rule.density) { HomeTokens.drawerMaximum.toPx() }
        return min(width * HomeTokens.drawerFraction, cap)
    }

    /** Left edge of the blue page at mid-height, in px. The test window is mdpi, so px and dp match. */
    private fun edge(): Int {
        val map = rule.onNodeWithTag("shell").captureToImage().toPixelMap()
        val y = map.height / 2
        for (x in 0 until map.width) {
            val color = map[x, y]
            if (color.blue > color.red + 0.15f && color.alpha > 0.5f) return x
        }
        return -1
    }
}

private class RecordingHaptics : HapticFeedback {
    val types = mutableListOf<HapticFeedbackType>()
    override fun performHapticFeedback(hapticFeedbackType: HapticFeedbackType) {
        types += hapticFeedbackType
    }
}
