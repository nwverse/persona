package persona.core.designsystem

import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.snap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInParent
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.hideFromAccessibility
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.LayoutDirection
import kotlin.math.roundToInt

/**
 * One pill behind the chosen segment, Glass only.
 *
 * A tap moves the pill's frame on [PersonaTheme.motion.enter], the iPhone's `Theme.Motion.state`
 * (`apple/Shared/Design/Theme.swift` line 726, `Animation.snappy(duration: 0.25)`), through
 * `Tokens.Motion.State`. That is the shell switch's pill (`apple/Persona/DesignSystem/ShellComponents.swift`
 * `SwitchMotion`: the Duo tab bar line 325, across the top line 336) and the `matchedGeometryEffect` id
 * `"pill"` (lines 206 and 221, only when the switch is outside a shell). A segmented picker on iOS is the
 * system's sliding thumb (`Picker` `.segmented`, for example `apple/Persona/Views/Session/ReviewPage.swift`);
 * #9586 maps that tap onto the same state spring. The thumb's own UIKit curve is not in this repo.
 *
 * [fraction], when the caller has a pager position, places the pill there with no spring of its own.
 * iOS `PagerPill` (`ShellComponents.swift` 243–245) reads `shell.pager.position` only while
 * `shellPillFollowsStrip` is set (`ShellView.swift` 226 and 259), and `ShellPager.drag` writes that
 * position with animations off (`ShellPager.swift` 81–83). Equal segments then match
 * `offset = position * (width / n)`. The Duo tab bar does not use `PagerPill` (line 124); it marks the
 * chosen item in place. Android's home switch has no strip, so it passes no fraction.
 *
 * System animations off put the pill on the choice at once, as the Duo tab bar does
 * (`SwitchMotion` line 325, `reduceMotion ? nil`). The horizontal switch does not read Reduce Motion
 * (line 336); Android still stops, with the rest of its motion.
 */
@Composable
internal fun GlassSlidingPill(
    selected: Int,
    count: Int,
    color: Color,
    modifier: Modifier = Modifier,
    fraction: Float? = null,
    segment: @Composable (Int) -> Unit,
) {
    val reduce = PersonaTheme.reduceMotion
    val glass = PersonaTheme.style == AppStyle.Glass
    var primed by remember { mutableStateOf(false) }
    val spans = remember { mutableStateMapOf<Int, SegmentSpan>() }
    if (spans.keys.any { it !in 0 until count }) {
        spans.keys.filter { it !in 0 until count }.forEach { spans.remove(it) }
    }
    val target = pillTarget(spans, count, fraction, selected)
    // The first frame the segments have a place snaps there. After that a tap springs; a fraction
    // (a finger on iOS's strip) and Reduce Motion stay on the target.
    val moves = primed && fraction == null && !reduce && glass && target != null
    if (target != null && !primed) SideEffect { primed = true }
    // The spring starts only once a frame exists, so its first value is that frame. Starting it at 0
    // would glide in from the leading edge on the first draw.
    val place = if (target != null) PillPlace(target, moves) else null
    // One layout, absolute left edges. A Row would mirror in RTL and a Box would mirror a shorter pill
    // again (`Alignment.TopStart`), so the pill left the segment. iOS keeps the pill on the segment when
    // the control mirrors (the system segmented thumb, and `ShellSwitch`'s pill).
    Layout(
        modifier = modifier,
        content = {
            val drawn = place != null && place.width > 0 && place.height > 0
            Box(
                Modifier.clip(CircleShape).background(color).then(
                    if (drawn) Modifier.testTag("sliding-pill").semantics { hideFromAccessibility() } else Modifier,
                ),
            )
            repeat(count) { index ->
                Box(Modifier.onGloballyPositioned { coords ->
                    val at = coords.positionInParent()
                    val span = SegmentSpan(at.x.roundToInt(), coords.size.width, coords.size.height)
                    if (spans[index] != span) spans[index] = span
                }) { segment(index) }
            }
        },
    ) { measurables, constraints ->
        val loose = constraints.copy(minWidth = 0, minHeight = 0)
        val segmentPlaceables = measurables.drop(1).map { it.measure(loose) }
        val width = segmentPlaceables.sumOf { it.width }
        val height = segmentPlaceables.maxOfOrNull { it.height } ?: 0
        val pillPlaceable = measurables.first().measure(
            Constraints.fixed((place?.width ?: 0).coerceAtLeast(0), (place?.height ?: 0).coerceAtLeast(0)),
        )
        layout(width, height) {
            val rtl = layoutDirection == LayoutDirection.Rtl
            var cursor = 0
            segmentPlaceables.forEach { child ->
                val left = if (rtl) width - cursor - child.width else cursor
                child.place(left, 0)
                cursor += child.width
            }
            pillPlaceable.place(place?.x ?: 0, 0)
        }
    }
}

/** Drawn frame. The animation composable is absent until [target] exists, so it is born on that frame. */
@Composable
private fun PillPlace(target: PillFrame, moves: Boolean): PillFrame {
    val spec: FiniteAnimationSpec<Float> = if (moves) PersonaTheme.motion.enter() else snap()
    val animX by animateFloatAsState(target.x.toFloat(), spec, label = "pill-x")
    val animW by animateFloatAsState(target.width.toFloat(), spec, label = "pill-w")
    return if (moves) PillFrame(animX.roundToInt(), animW.roundToInt().coerceAtLeast(0), target.height) else target
}

private data class SegmentSpan(val x: Int, val width: Int, val height: Int)

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

private fun pillTarget(
    spans: Map<Int, SegmentSpan>,
    count: Int,
    fraction: Float?,
    selected: Int,
): PillFrame? {
    if (count <= 0 || (0 until count).any { it !in spans }) return null
    val starts = List(count) { spans.getValue(it).x }
    val widths = List(count) { spans.getValue(it).width }
    val height = (0 until count).maxOf { spans.getValue(it).height }
    return pillFrame(fraction ?: selected.toFloat(), starts, widths, height)
}

