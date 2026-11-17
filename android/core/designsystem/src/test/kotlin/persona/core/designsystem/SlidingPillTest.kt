package persona.core.designsystem

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.DpRect
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.icons.PhIcons
import kotlin.math.abs

private val DpRect.width: Dp get() = right - left
private val DpRect.height: Dp get() = bottom - top

/**
 * Glass draws one pill and moves it on `Theme.Motion.state` (`ShellComponents.swift` `SwitchMotion`).
 * A fraction is iOS `PagerPill` with animations off (`ShellPager.drag`). Brief and Board draw no pill.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-mdpi")
class SlidingPillTest {
    @get:Rule val rule = createComposeRule()

    @Test fun pillFrameLerpsTheSegmentTheFingerIsOver() {
        assertEquals(PillFrame(0, 100, 36), pillFrame(0f, listOf(0, 100), listOf(100, 100), 36))
        assertEquals(PillFrame(50, 100, 36), pillFrame(0.5f, listOf(0, 100), listOf(100, 100), 36))
        assertEquals(PillFrame(100, 100, 36), pillFrame(1f, listOf(0, 100), listOf(100, 100), 36))
        assertEquals(PillFrame(150, 100, 36), pillFrame(1.5f, listOf(0, 100, 200), listOf(100, 100, 100), 36))
        assertEquals(PillFrame(20, 60, 36), pillFrame(0.5f, listOf(0, 40), listOf(40, 80), 36))
        assertEquals(PillFrame(0, 100, 36), pillFrame(-2f, listOf(0, 100), listOf(100, 100), 36))
        assertEquals(PillFrame(100, 100, 36), pillFrame(9f, listOf(0, 100), listOf(100, 100), 36))
        assertEquals(PillFrame(0, 0, 10), pillFrame(0.4f, emptyList(), emptyList(), 10))
        assertEquals(PillFrame(7, 12, 4), pillFrame(3f, listOf(7), listOf(12), 4))
    }

    @Test fun aTapSpringsTheGlassSegmentPill() {
        rule.mainClock.autoAdvance = false
        var selected by mutableIntStateOf(0)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = false) {
                JSegmented(listOf(JSegment("Day"), JSegment("This week")), selected, { selected = it })
            }
        }
        rule.mainClock.advanceTimeBy(300)
        val day = bounds("Day")
        val week = bounds("This week")
        val start = pill()
        assertNear(day.left, start.left)
        assertNear(day.width, start.width)
        assertNear(day.top, start.top)
        assertEquals(1, pillCount())
        rule.onNodeWithText("This week").performClick()
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(80)
        val mid = pill()
        val at80 = progress(day.left, week.left, mid.left)
        assertTrue("80ms left progress $at80", at80 in 0.35f..0.85f)
        val width80 = progress(day.width, week.width, mid.width)
        assertTrue("80ms width progress $width80 vs left $at80", abs(width80 - at80) < 0.2f)
        rule.mainClock.advanceTimeBy(120)
        val at200 = progress(day.left, week.left, pill().left)
        // State spring (0.25s, bounce 0.15) is home by 200ms. The layout spring (0.3s, no bounce) is still ~0.90.
        assertTrue("200ms left progress $at200", at200 >= 0.96f)
        rule.mainClock.advanceTimeBy(800)
        val end = pill()
        assertNear(week.left, end.left)
        assertNear(week.width, end.width)
        assertNear(week.height, end.height)
        assertEquals(1, selected)
    }

    @Test fun aJumpDoesNotGrowThroughTheMiddleSegment() {
        rule.mainClock.autoAdvance = false
        var selected by mutableIntStateOf(0)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = false) {
                JSegmented(
                    listOf(JSegment("Go"), JSegment("A much longer middle"), JSegment("End")),
                    selected, { selected = it },
                )
            }
        }
        rule.mainClock.advanceTimeBy(300)
        val go = bounds("Go")
        val middle = bounds("A much longer middle")
        val end = bounds("End")
        rule.runOnIdle { selected = 2 }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(80)
        val mid = pill()
        val along = progress(go.left, end.left, mid.left)
        assertTrue("jump progress $along width ${mid.width} middle ${middle.width}", along in 0.35f..0.85f && mid.width < middle.width)
        rule.mainClock.advanceTimeBy(920)
        assertNear(end.left, pill().left)
        assertNear(end.width, pill().width)
    }

    @Test fun reduceMotionPutsThePillOnTheChoiceAtOnce() {
        rule.mainClock.autoAdvance = false
        var selected by mutableIntStateOf(0)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = true, reduceMotion = true) {
                JSegmented(listOf(JSegment("Day"), JSegment("This week")), selected, { selected = it })
            }
        }
        rule.mainClock.advanceTimeBy(100)
        rule.onNodeWithText("This week").performClick()
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(16)
        val week = bounds("This week")
        assertNear(week.left, pill().left)
        assertNear(week.width, pill().width)
        rule.mainClock.advanceTimeBy(104)
        assertNear(week.left, pill().left)
        assertNear(week.width, pill().width)
    }

    @Test fun theHomeSwitchUsesTheSameSpring() {
        rule.mainClock.autoAdvance = false
        var selected by mutableStateOf("assistant")
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = false) {
                HomeNavigation(
                    items = listOf(
                        NavItem("assistant", "Assistant", PhIcons.ChatCircle),
                        NavItem("agents", "Agents", PhIcons.Robot),
                    ),
                    selected = selected, onSelect = { selected = it }, leading = {},
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        rule.mainClock.advanceTimeBy(300)
        val from = bounds("Assistant")
        val to = bounds("Agents")
        assertNear(from.left, pill().left)
        assertNear(from.width, pill().width)
        assertEquals(1, pillCount())
        rule.onNodeWithTag("home-switcher").assertExists()
        rule.onNodeWithText("Agents").performClick()
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(80)
        val at80 = progress(from.left, to.left, pill().left)
        assertTrue("home 80ms $at80", at80 in 0.35f..0.85f)
        rule.mainClock.advanceTimeBy(120)
        val at200 = progress(from.left, to.left, pill().left)
        assertTrue("home 200ms $at200", at200 >= 0.96f)
        rule.mainClock.advanceTimeBy(800)
        assertNear(to.left, pill().left)
        assertNear(to.width, pill().width)
        assertEquals("agents", selected)
    }

    @Test fun fractionFollowsWithoutASpringOfItsOwn() {
        rule.mainClock.autoAdvance = false
        var fraction by mutableFloatStateOf(0f)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = false) {
                GlassSlidingPill(selected = 0, count = 2, color = Color.Black, fraction = fraction) { index ->
                    Box(Modifier.size(if (index == 0) 40.dp else 80.dp, 36.dp).testTag("seg-$index"))
                }
            }
        }
        rule.mainClock.advanceTimeBy(100)
        val first = bounds("seg-0")
        val second = bounds("seg-1")
        assertNear(first.left, pill().left)
        assertNear(first.width, pill().width)
        rule.runOnIdle { fraction = 0.5f }
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(16)
        val mid = pill()
        assertNear(first.left + (second.left - first.left) * 0.5f, mid.left)
        assertNear(first.width + (second.width - first.width) * 0.5f, mid.width)
        rule.mainClock.advanceTimeBy(500)
        assertNear(mid.left, pill().left)
        assertNear(mid.width, pill().width)
    }

    @Test fun thePillSitsOnTheChosenSegmentInRtl() {
        rule.mainClock.autoAdvance = false
        rule.setContent {
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Rtl) {
                PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = true) {
                    GlassSlidingPill(selected = 0, count = 2, color = Color.Black) { index ->
                        Box(Modifier.size(if (index == 0) 50.dp else 90.dp, 36.dp).testTag("seg-$index"))
                    }
                }
            }
        }
        rule.mainClock.advanceTimeBy(100)
        val chosen = bounds("seg-0")
        assertNear(chosen.left, pill().left)
        assertNear(chosen.width, pill().width)
        assertTrue("first segment should sit on the right", chosen.left > bounds("seg-1").left)
    }

    @Test fun briefAndBoardKeepTheirOwnMarks() {
        var selected by mutableIntStateOf(0)
        var style by mutableStateOf(AppStyle.Brief)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = false, reduceMotion = false) {
                JSegmented(listOf(JSegment("Day"), JSegment("Week")), selected, { selected = it })
            }
        }
        for (next in listOf(AppStyle.Brief, AppStyle.Board)) {
            rule.runOnIdle { style = next; selected = 0 }
            rule.waitForIdle()
            assertEquals(0, pillCount())
            rule.onNodeWithText("Week").performClick()
            rule.waitForIdle()
            assertEquals("$next selects", 1, selected)
            assertEquals(0, pillCount())
        }
    }

    private fun pill(): DpRect = rule.onNodeWithTag("sliding-pill", useUnmergedTree = true).getUnclippedBoundsInRoot()

    private fun pillCount(): Int = rule.onAllNodesWithTag("sliding-pill", useUnmergedTree = true).fetchSemanticsNodes().size

    private fun bounds(tagOrText: String): DpRect {
        val tagged = rule.onAllNodesWithTag(tagOrText, useUnmergedTree = true).fetchSemanticsNodes()
        return if (tagged.isNotEmpty()) rule.onNodeWithTag(tagOrText, useUnmergedTree = true).getUnclippedBoundsInRoot()
        else rule.onNodeWithText(tagOrText).getUnclippedBoundsInRoot()
    }

    private fun progress(from: Dp, to: Dp, at: Dp): Float {
        val span = (to - from).value
        return if (span == 0f) 1f else (at - from).value / span
    }

    private fun assertNear(expected: Dp, actual: Dp, tolerance: Dp = 1.dp) {
        assertTrue("expected $expected ± $tolerance, was $actual", abs((actual - expected).value) <= tolerance.value)
    }
}
