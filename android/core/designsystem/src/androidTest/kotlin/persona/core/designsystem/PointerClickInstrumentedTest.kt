package persona.core.designsystem

import android.os.SystemClock
import android.view.InputDevice
import android.view.MotionEvent
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** Real mouse-button injection; adb hover or console events are not acceptance evidence for this path. */
@RunWith(AndroidJUnit4::class)
class PointerClickInstrumentedTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun secondaryButtonAnchorsTheMenuAtWindowEdges() {
        var anchor by mutableStateOf<Offset?>(null)
        var targetBounds by mutableStateOf(Rect.Zero)
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                Box(Modifier.fillMaxSize().onGloballyPositioned { targetBounds = it.boundsInWindow() }
                    .onSecondaryClickAt { anchor = it }.testTag("target")) {
                    anchor?.let {
                        JPointerMenu(it, { anchor = null }, (1..20).map { index -> JAction("Action $index") {} },
                            Modifier.testTag("pointer-menu"))
                    }
                }
            }
        }
        val margin = 24 * InstrumentationRegistry.getInstrumentation().targetContext.resources.displayMetrics.density
        val points = listOf(
            Offset(targetBounds.right - margin, targetBounds.top + margin),
            Offset(targetBounds.left + margin, targetBounds.bottom - margin),
            Offset(targetBounds.left + margin, targetBounds.top + margin),
            Offset(targetBounds.right - margin, targetBounds.bottom - margin),
        )
        for ((index, point) in points.withIndex()) {
            secondaryClick(point)
            rule.waitForIdle()
            val actual = requireNotNull(anchor)
            assertEquals(point.x, actual.x, 1f)
            assertEquals(point.y, actual.y, 1f)
            val root = rule.onNodeWithTag("target").getUnclippedBoundsInRoot()
            val menu = rule.onNodeWithTag("pointer-menu").getUnclippedBoundsInRoot()
            assertTrue(menu.left >= root.left && menu.top >= root.top)
            assertTrue(menu.right <= root.right && menu.bottom <= root.bottom)
            val density = InstrumentationRegistry.getInstrumentation().targetContext.resources.displayMetrics.density
            val anchorX = root.left.value + (actual.x - targetBounds.left) / density
            val anchorY = root.top.value + (actual.y - targetBounds.top) / density
            if (index == 0 || index == 3) assertTrue("right edge flips left", menu.right.value <= anchorX + 2f)
            if (index == 1 || index == 3) assertTrue("bottom edge flips up", menu.bottom.value <= anchorY + 2f)
            if (index == 2) {
                assertTrue("top-left grows right", menu.left.value >= anchorX - 2f)
                assertTrue("top-left grows down", menu.top.value >= anchorY - 2f)
            }
            rule.runOnUiThread { anchor = null }
            rule.waitForIdle()
        }
    }

    private fun secondaryClick(windowPoint: Offset) {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val location = IntArray(2)
        rule.runOnUiThread { rule.activity.window.decorView.getLocationOnScreen(location) }
        val x = location[0] + windowPoint.x
        val y = location[1] + windowPoint.y
        val downTime = SystemClock.uptimeMillis()
        val down = MotionEvent.obtain(downTime, downTime, MotionEvent.ACTION_DOWN, x, y, 0).apply {
            source = InputDevice.SOURCE_MOUSE
            buttonState = MotionEvent.BUTTON_SECONDARY
        }
        val up = MotionEvent.obtain(downTime, downTime + 10, MotionEvent.ACTION_UP, x, y, 0).apply {
            source = InputDevice.SOURCE_MOUSE
        }
        try {
            instrumentation.sendPointerSync(down)
            instrumentation.sendPointerSync(up)
            instrumentation.waitForIdleSync()
        } finally {
            down.recycle()
            up.recycle()
        }
    }
}
