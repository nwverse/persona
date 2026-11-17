package persona.core.designsystem

import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.snap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.hideFromAccessibility
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.LayoutDirection
import kotlin.math.roundToInt

/**
 * One pill behind the chosen segment, Glass only.
 *
 * A tap moves the pill on [PersonaTheme.motion.enter], the iPhone's `Theme.Motion.state`
 * (`apple/Shared/Design/Theme.swift` line 726, `Animation.snappy(duration: 0.25)`), through
 * `Tokens.Motion.State`. That is the shell switch's pill (`apple/Persona/DesignSystem/ShellComponents.swift`
 * `SwitchMotion`: the Duo tab bar line 325, across the top line 336) and the `matchedGeometryEffect` id
 * `"pill"` (lines 206 and 221, only when the switch is outside a shell). A segmented picker on iOS is the
 * system's sliding thumb (`Picker` `.segmented`, for example `apple/Persona/Views/Session/ReviewPage.swift`);
 * #9586 maps that tap onto the same state spring. The thumb's own UIKit curve is not in this repo.
 *
 * The animated value is the selected index. It is born on that index, so the first frame already draws the
 * pill, the way the switch is already on the chosen segment before a tap (`SwitchMotion` animates `chosen`
 * only after it changes). [pillFrame] then reads the segment placeables in the same measure pass. System
 * animations off put the pill on the choice at once, as the Duo tab bar does (`SwitchMotion` line 325,
 * `reduceMotion ? nil`). The horizontal switch does not read Reduce Motion (line 336); Android still stops,
 * with the rest of its motion.
 */
/** Measurement copies of the home switch are not the switch the user sees, so they carry no pill tag. */
internal val LocalSlidingPillMeasurement = staticCompositionLocalOf { false }

@Composable
internal fun GlassSlidingPill(
    selected: Int,
    count: Int,
    color: Color,
    modifier: Modifier = Modifier,
    segment: @Composable (Int) -> Unit,
) {
    val reduce = PersonaTheme.reduceMotion
    val glass = PersonaTheme.style == AppStyle.Glass
    // Born on the selected index (the animation's initial value is the target). A later change springs
    // on Glass. Reduce Motion uses the index itself, in this composition, so the pill does not wait a frame.
    val spec: FiniteAnimationSpec<Float> = if (!reduce && glass) PersonaTheme.motion.enter() else snap()
    val sprung by animateFloatAsState(selected.toFloat(), spec, label = "pill-index")
    val animatedIndex = if (!reduce && glass) sprung else selected.toFloat()
    val tag = if (LocalSlidingPillMeasurement.current) Modifier
        else Modifier.testTag("sliding-pill").semantics { hideFromAccessibility() }
    // One layout, absolute left edges. A Row would mirror in RTL and a Box would mirror a shorter pill
    // again (`Alignment.TopStart`), so the pill left the segment. iOS keeps the pill on the segment when
    // the control mirrors (the system segmented thumb, and `ShellSwitch`'s pill).
    Layout(
        modifier = modifier,
        content = {
            Box(Modifier.clip(CircleShape).background(color).then(tag))
            repeat(count) { index -> Box { segment(index) } }
        },
    ) { measurables, constraints ->
        val loose = constraints.copy(minWidth = 0, minHeight = 0)
        val segmentPlaceables = measurables.drop(1).map { it.measure(loose) }
        val width = segmentPlaceables.sumOf { it.width }
        val height = segmentPlaceables.maxOfOrNull { it.height } ?: 0
        val rtl = layoutDirection == LayoutDirection.Rtl
        val starts = IntArray(segmentPlaceables.size)
        val widths = IntArray(segmentPlaceables.size)
        var cursor = 0
        segmentPlaceables.forEachIndexed { i, child ->
            starts[i] = if (rtl) width - cursor - child.width else cursor
            widths[i] = child.width
            cursor += child.width
        }
        val frame = pillFrame(animatedIndex, starts.asList(), widths.asList(), height)
        val pillPlaceable = measurables.first().measure(
            Constraints.fixed(frame.width.coerceAtLeast(0), frame.height.coerceAtLeast(0)),
        )
        layout(width, height) {
            // Behind the labels. The last place() draws on top; the highlight used to be each segment's
            // own background, with the text inside it.
            pillPlaceable.place(frame.x, 0)
            segmentPlaceables.forEachIndexed { i, child -> child.place(starts[i], 0) }
        }
    }
}

/** The pill's place at [index]: 0 on the first segment, [n - 1] on the last. Values between lerp. */
internal data class PillFrame(val x: Int, val width: Int, val height: Int)

internal fun pillFrame(index: Float, starts: List<Int>, widths: List<Int>, height: Int): PillFrame {
    val n = minOf(starts.size, widths.size)
    if (n == 0) return PillFrame(0, 0, height)
    val last = n - 1
    val clamped = index.coerceIn(0f, last.toFloat())
    if (last == 0) return PillFrame(starts[0], widths[0].coerceAtLeast(0), height)
    val i = clamped.toInt().coerceAtMost(last - 1)
    val t = clamped - i
    val x = starts[i] + (starts[i + 1] - starts[i]) * t
    val w = widths[i] + (widths[i + 1] - widths[i]) * t
    return PillFrame(x.roundToInt(), w.roundToInt().coerceAtLeast(0), height)
}
