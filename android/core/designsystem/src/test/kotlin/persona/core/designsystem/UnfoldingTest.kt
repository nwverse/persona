package persona.core.designsystem

import android.graphics.Color as AndroidColor
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * `.unfolding` takes its full height on the first frame and only fades (#9574, #3005). Conceal keeps its space.
 * Expand grows in place, and with reduced motion every one of them is instant.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
class UnfoldingTest {
    @get:Rule val rule = createComposeRule()

    @Test fun openingIsFullHeightOnTheFirstFrameAndOnlyFades() {
        rule.mainClock.autoAdvance = false
        var open by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = false) {
                val edge = rememberOpenEdge()
                val revealing = edge.revealing(open)
                Column(Modifier.testTag("box").width(160.dp).background(Color.White)) {
                    if (open) Box(
                        Modifier.testTag("detail").unfolding(revealing).fillMaxWidth().height(100.dp).background(Color.Black),
                    )
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        val closed = height("box")
        rule.runOnIdle { open = true }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeByFrame()
        val first = height("box")
        assertTrue("opened $first from $closed", first > closed)
        assertTrue(height("detail") > 0)
        val faded = pixel("box")
        rule.mainClock.advanceTimeBy(2_000)
        assertEquals("height must not animate", first, height("box"))
        val solid = pixel("box")
        assertNotEquals("mid fade $faded settled $solid", faded, solid)
        assertEquals(0, AndroidColor.red(solid))
        assertTrue(AndroidColor.alpha(solid) > 250)
    }

    @Test fun whatWasAlreadyOpenDoesNotFade() {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = false) {
                val edge = rememberOpenEdge()
                val revealing = edge.revealing(true)
                Column(Modifier.testTag("box").width(80.dp).background(Color.White)) {
                    Box(Modifier.unfolding(revealing).fillMaxWidth().height(40.dp).background(Color.Black))
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        val first = pixel("box")
        rule.mainClock.advanceTimeBy(2_000)
        assertEquals(first, pixel("box"))
        assertEquals(0, AndroidColor.red(first))
        assertTrue(AndroidColor.alpha(first) > 250)
    }

    @Test fun closingDropsItsSpaceOnTheNextFrame() {
        rule.mainClock.autoAdvance = false
        var open by mutableStateOf(true)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = false) {
                val edge = rememberOpenEdge()
                val revealing = edge.revealing(open)
                Column(Modifier.testTag("box").width(120.dp).background(Color.White)) {
                    Text("Header")
                    if (open) Box(Modifier.testTag("detail").unfolding(revealing).fillMaxWidth().height(80.dp).background(Color.Black))
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        val openHeight = height("box")
        rule.runOnIdle { open = false }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeByFrame()
        assertEquals(0, rule.onAllNodesWithTag("detail").fetchSemanticsNodes().size)
        val closed = height("box")
        assertTrue("$closed < $openHeight", closed < openHeight)
        rule.mainClock.advanceTimeBy(140)
        assertEquals("no mid-height close", closed, height("box"))
    }

    @Test fun reducedMotionIsOpaqueAndFullOnTheFirstFrame() {
        rule.mainClock.autoAdvance = false
        var open by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                val edge = rememberOpenEdge()
                val revealing = edge.revealing(open)
                Column(Modifier.testTag("box").width(120.dp).background(Color.White)) {
                    if (open) Box(Modifier.testTag("detail").unfolding(revealing).fillMaxWidth().height(60.dp).background(Color.Black))
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        rule.runOnIdle { open = true }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeByFrame()
        val first = height("detail")
        val ink = pixel("detail")
        rule.mainClock.advanceTimeBy(2_000)
        assertEquals(first, height("detail"))
        assertEquals(ink, pixel("detail"))
        assertEquals(0, AndroidColor.red(ink))
        assertTrue(AndroidColor.alpha(ink) > 250)
    }

    @Test fun concealingKeepsItsSpace() {
        rule.mainClock.autoAdvance = false
        var leaving by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = false) {
                Column(Modifier.width(80.dp).background(Color.White)) {
                    Box(Modifier.testTag("box").concealing(leaving).fillMaxWidth().height(40.dp).background(Color.Black))
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        val full = height("box")
        val solid = pixel("box")
        rule.runOnIdle { leaving = true }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(80)
        assertEquals(full, height("box"))
        assertNotEquals(solid, pixel("box"))
        rule.mainClock.advanceTimeBy(2_000)
        assertEquals(full, height("box"))
    }

    @Test fun concealingWithReducedMotionIsInstant() {
        rule.mainClock.autoAdvance = false
        var leaving by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = AppStyle.Brief, reduceMotion = true) {
                Column(Modifier.width(80.dp).background(Color.White)) {
                    Box(Modifier.testTag("box").concealing(leaving).fillMaxWidth().height(40.dp).background(Color.Black))
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        val full = height("box")
        val solid = pixel("box")
        rule.runOnIdle { leaving = true }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeByFrame()
        val gone = pixel("box")
        assertEquals(full, height("box"))
        assertNotEquals("reduced conceal still solid", solid, gone)
        rule.mainClock.advanceTimeBy(2_000)
        assertEquals(gone, pixel("box"))
        assertEquals(full, height("box"))
    }

    @Test fun expandingGrowsOnGlass() = grows(AppStyle.Glass, reduce = false)

    @Test fun expandingOnBriefIsNotInstant() = grows(AppStyle.Brief, reduce = false)

    @Test fun expandingIsInstantWhenReduced() = grows(AppStyle.Board, reduce = true)

    private fun grows(style: AppStyle, reduce: Boolean) {
        rule.mainClock.autoAdvance = false
        var tall by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, reduceMotion = reduce) {
                Column(Modifier.testTag("box").expanding().width(80.dp).background(Color.Black)) {
                    Box(Modifier.fillMaxWidth().height(24.dp))
                    if (tall) Box(Modifier.fillMaxWidth().height(76.dp))
                }
            }
        }
        rule.mainClock.advanceTimeByFrame()
        val short = height("box")
        rule.runOnIdle { tall = true }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeByFrame()
        val first = height("box")
        rule.mainClock.advanceTimeBy(120)
        val mid = height("box")
        rule.mainClock.advanceTimeBy(2_000)
        val full = height("box")
        assertTrue("$style $short $first $mid $full", full > short)
        if (reduce) assertEquals(full, first) else assertTrue("$style mid $mid of $short..$full", mid in (short + 1) until full)
    }

    private fun height(tag: String): Int = rule.onNodeWithTag(tag).fetchSemanticsNode().size.height

    private fun pixel(tag: String): Int {
        val bitmap = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()
        return bitmap.getPixel(bitmap.width / 2, bitmap.height / 2)
    }
}
