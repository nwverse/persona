package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.shadows.ShadowLooper
import java.io.File
import kotlin.math.roundToInt

/**
 * Start, finger at half the column, and the frame after release. Glass, light and dark.
 * Upright 673dp is over; 951dp-wide landscape is beside (`columnPlacement`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35])
class SidebarColumnDragShotsTest {
    @get:Rule val rule = createComposeRule()

    @Config(sdk = [35], qualifiers = "en-w673dp-h951dp-mdpi")
    @Test fun overFoldable() = frames(over = true, prefix = "over")

    @Config(sdk = [35], qualifiers = "en-w951dp-h673dp-mdpi")
    @Test fun besideFoldable() = frames(over = false, prefix = "beside")

    @Config(sdk = [35], qualifiers = "en-w673dp-h951dp-mdpi")
    @Test fun restingOverMatches() = resting(overlay = true)

    @Config(sdk = [35], qualifiers = "en-w951dp-h673dp-mdpi")
    @Test fun restingBesideMatches() = resting(overlay = false)

    private fun frames(over: Boolean, prefix: String) {
        var dark by mutableStateOf(false)
        val accent = IntArray(1)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = dark, reduceMotion = false) {
                val color = PersonaTheme.palette.accent
                SideEffect { accent[0] = color.toArgb() }
                Box(Modifier.fillMaxSize().testTag("shot")) {
                    JSidebarColumn(sidebar = { Box(Modifier.fillMaxSize().background(PersonaTheme.palette.positive)) },
                        width = 320.dp, pageStart = 320.dp, shown = false, overlay = over, drags = true) {
                        Box(Modifier.fillMaxSize().background(Color(0xFF88AACC))) {
                            Box(Modifier.align(Alignment.CenterEnd).width(32.dp).fillMaxHeight().background(Color(0xFFE25B4A)))
                        }
                    }
                }
            }
        }
        val out = File("build/column-drag-shots").also { it.mkdirs() }
        println("column-drag-shots ${out.absolutePath}")
        val widthPx = with(rule.density) { 320.dp.toPx() }
        for (isDark in listOf(false, true)) {
            val mode = if (isDark) "dark" else "light"
            rule.mainClock.autoAdvance = true
            rule.runOnIdle { dark = isDark }
            rule.waitForIdle()
            val start = save(out, "$prefix-$mode-start")
            val controller = columnDragControllerForTest ?: error("missing controller")
            assertEquals(DragStep.Idle, controller.step)
            rule.runOnIdle { controller.begin(ColumnDragZone.Open, over = over, widthPx = widthPx) }
            var i = 0
            while (i < 8 && controller.step != DragStep.Follow) {
                rule.onNodeWithTag("shot").captureToImage()
                rule.waitForIdle()
                ShadowLooper.idleMainLooper()
                i++
            }
            assertEquals("$prefix $mode stuck at ${controller.step} page=${controller.pageSeen} col=${controller.columnSeen}",
                DragStep.Follow, controller.step)
            rule.runOnIdle { controller.move(widthPx / 2f) }
            rule.waitForIdle()
            val mid = save(out, "$prefix-$mode-mid")
            // The guide is 3px centered on the line (`Theme.Split.guide`). Sample its middle, not the antialiased edge.
            val center = (widthPx / 2f).roundToInt().coerceIn(1, mid.width - 2)
            assertEquals("$prefix $mode guide", accent[0], mid.getPixel(center, mid.height / 2))
            val farX = mid.width - 2
            val y = mid.height / 2
            if (over) {
                assertTrue("$prefix $mode dim", luminance(mid.getPixel(farX, y)) < luminance(start.getPixel(farX, y)) - 4)
            } else {
                assertEquals("$prefix $mode page edge", start.getPixel(farX, y), mid.getPixel(farX, y))
            }
            // Hold the clock on the fade's first frame: the spring has finished, the overlay is still up (`:278`).
            rule.mainClock.autoAdvance = false
            rule.runOnIdle {
                controller.end(2000f)
                Snapshot.sendApplyNotifications()
            }
            var frames = 0
            while (frames < 80 && controller.step != DragStep.Fade) {
                rule.mainClock.advanceTimeByFrame()
                ShadowLooper.idleMainLooper()
                frames++
            }
            assertEquals("$prefix $mode did not release (${controller.step})", DragStep.Fade, controller.step)
            save(out, "$prefix-$mode-release")
            var fade = 0
            while (fade < 40 && controller.step != DragStep.Idle) {
                rule.mainClock.advanceTimeByFrame()
                ShadowLooper.idleMainLooper()
                fade++
            }
            assertEquals(DragStep.Idle, controller.step)
        }
    }

    private fun resting(overlay: Boolean) {
        var dark by mutableStateOf(false)
        var shown by mutableStateOf(false)
        var drags by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.fillMaxSize().testTag("shot").background(PersonaTheme.palette.background)) {
                    JSidebarColumn(sidebar = { Box(Modifier.fillMaxSize().background(PersonaTheme.palette.positive)) },
                        width = 320.dp, pageStart = 320.dp, shown = shown, overlay = overlay, drags = drags) {
                        Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background))
                    }
                }
            }
        }
        for (isDark in listOf(false, true)) for (isShown in listOf(false, true)) {
            dark = isDark
            shown = isShown
            drags = false
            val off = capture()
            drags = true
            val on = capture()
            assertSame(off, on, "overlay=$overlay shown=$isShown dark=$isDark")
        }
    }

    private fun capture(): Bitmap {
        rule.waitForIdle()
        return rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
    }

    private fun save(out: File, name: String): Bitmap {
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        println("wrote ${File(out, "$name.png").absolutePath} ${bitmap.width}x${bitmap.height}")
        return bitmap
    }

    private fun assertSame(a: Bitmap, b: Bitmap, label: String) {
        assertEquals("$label width", a.width, b.width)
        assertEquals("$label height", a.height, b.height)
        var diff = 0
        for (y in 0 until a.height) for (x in 0 until a.width) if (a.getPixel(x, y) != b.getPixel(x, y)) diff++
        assertEquals(label, 0, diff)
    }

    private fun luminance(pixel: Int): Int {
        val r = (pixel shr 16) and 0xFF
        val g = (pixel shr 8) and 0xFF
        val b = pixel and 0xFF
        return (r * 3 + g * 6 + b) / 10
    }
}
