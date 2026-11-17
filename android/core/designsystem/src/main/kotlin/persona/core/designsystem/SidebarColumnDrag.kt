package persona.core.designsystem

import android.graphics.RenderEffect
import android.graphics.Shader
import android.os.Build
import android.os.Handler
import android.os.Looper
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asComposeRenderEffect
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.layer.GraphicsLayer
import androidx.compose.ui.graphics.layer.drawLayer
import androidx.compose.ui.graphics.rememberGraphicsLayer
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.PointerInputScope
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.zIndex
import kotlin.coroutines.cancellation.CancellationException
import kotlin.math.abs
import kotlin.math.min
import kotlin.math.roundToInt

/**
 * The foldable list column following a finger, copying iOS `SplitColumnControl` (`apple/Persona/Views/SplitColumnControl.swift`)
 * and `Theme.Split` (`apple/Shared/Design/Theme.swift:45-61`). Assistant history and the Agents list only.
 *
 * The line is 1:1 with the finger (`SplitColumnControl.swift:246`). Pictures taken when the drag begins are stretched,
 * so the column does not lay out on each move (`:289-297`, `:321-328`). Release uses the flick at 500 pt/s, otherwise
 * half the width (`:256`), then a 0.28 s bounce-0 spring with the finger's velocity (`:284`) and a 0.2 s fade (`:270-275`).
 * Reduce Motion jumps the line and still fades (`:278-282`).
 */
internal object ColumnDrag {
    /** `gestureRecognizerShouldBegin`: `abs(v.x) > abs(v.y) * 1.5` (`SplitColumnControl.swift:136`, `:145`). */
    const val angle = 1.5f
    /** `Theme.Split.dragBlur` (`Theme.swift:47`). */
    const val dragBlur = 0.8f
    /** `Theme.Split.blurReach` (`Theme.swift:49`). Kept equal to `Tokens.Size.box80`. */
    const val blurReach = 80f
    /** `Theme.Split.fling` (`Theme.swift:51`), in dp each second. */
    const val flingDpPerSec = 500f
    /** `Theme.Split.overDim` (`Theme.swift:61`). */
    const val overDim = 0.2f
    /** `Theme.Split.settle` (`Theme.swift:53`): 0.28 s. */
    const val settleMs = 280
    /** `Theme.Split.fade` (`Theme.swift:55`): 0.2 s. Not `Tokens.Motion.Conceal`. */
    const val fadeMs = 200
    /** `Theme.Split.settle` bounce 0 (`SplitColumnControl.swift:284`). */
    const val settleBounce = 0f
    /** Half of `Theme.Size.tapTarget` (`Theme.swift:581`, `SplitColumnControl.swift:136`). Equal to `Tokens.MinTouch.ios / 2`. */
    const val edge = 22f
    /** `Theme.Split.guide` (`Theme.swift:58`). Equal to `Tokens.Size.bar`. */
    const val guide = 3f
    /**
     * Stand-in for a full `UIBlurEffect.systemUltraThinMaterial`. UIKit does not publish that blur's radius
     * (`SplitColumnControl.swift:229-233`). The fraction ramps with [columnDragBlur]; this is only how many dp that
     * fraction is multiplied by. Not a measured iOS value.
     */
    val fullMaterial = Tokens.Size.iconXS
}

internal enum class ColumnDragZone { Open, Close }

internal enum class DragStep {
    Idle, OpenPage, OpenColumn, ShutBeside, ShutOverColumn, ShutOverPage, Follow, Settle, Fade,
}

/**
 * Open from the leading edge while the column is hidden and the detail is at its root; close from the column's edge.
 * `SplitColumnControl.swift:131-146` and `canGoBack` `:161-169`. A rightward drag on the column edge is not ours
 * (`:144-145`). The angle test is strict (`>`).
 *
 * iOS reads `location.x` from the left of the window and has no layout-direction branch. [startX] is that x.
 */
internal fun columnDragTakeOver(
    drags: Boolean,
    shown: Boolean,
    detailAtRoot: Boolean,
    startX: Float,
    columnWidth: Float,
    edge: Float,
    velocityX: Float,
    velocityY: Float,
): ColumnDragZone? {
    if (!drags) return null
    if (!(abs(velocityX) > abs(velocityY) * ColumnDrag.angle)) return null
    if (!shown) {
        if (!detailAtRoot || startX > edge || velocityX <= 0f) return null
        return ColumnDragZone.Open
    }
    if (velocityX < 0f && abs(startX - columnWidth) <= edge) return ColumnDragZone.Close
    return null
}

/** `min(width, max(0, (opening ? 0 : width) + dx))` (`SplitColumnControl.swift:246`). No rubber band. */
internal fun columnDragLine(opening: Boolean, width: Float, dx: Float): Float {
    val start = if (opening) 0f else width
    return (start + dx).coerceIn(0f, width.coerceAtLeast(0f))
}

/**
 * A fling faster than [ColumnDrag.flingDpPerSec] decides; otherwise past halfway stays open.
 * Exactly the fling threshold uses the half rule (`abs(velocity) > fling`). Exactly half stays closed (`line > width / 2`).
 * `SplitColumnControl.swift:256`.
 */
internal fun columnDragShows(line: Float, width: Float, velocity: Float): Boolean =
    if (abs(velocity) > ColumnDrag.flingDpPerSec) velocity > 0f else line > width / 2f

/**
 * UIKit `initialSpringVelocity` is `distance == 0 ? 0 : velocity / distance` (`SplitColumnControl.swift:284`).
 * Compose's `Animatable` velocity is units per second, which is this share times the distance, the finger velocity.
 */
internal fun columnDragVelocityShare(velocity: Float, distance: Float): Float =
    if (distance == 0f) 0f else velocity / distance

/** `min(1, abs(dx) / Theme.Split.blurReach) * Theme.Split.dragBlur` (`SplitColumnControl.swift:248`). [dx] is in dp. */
internal fun columnDragBlur(dx: Float): Float = min(1f, abs(dx) / ColumnDrag.blurReach) * ColumnDrag.dragBlur

internal fun columnDragBlurRadius(dxDp: Float, fullMaterial: Dp): Dp = fullMaterial * columnDragBlur(dxDp)

/** `Theme.Split.overDim * line / width` (`SplitColumnControl.swift:294`). The over layout only. */
internal fun columnDragOverDim(line: Float, width: Float): Float =
    if (width > 0f) ColumnDrag.overDim * line / width else 0f

/** `min(max(line - guide / 2, 0), window - guide)` (`SplitColumnControl.swift:296`). */
internal fun columnDragGuideX(line: Float, guide: Float, window: Float): Float {
    val limit = window - guide
    if (limit <= 0f) return 0f
    return (line - guide / 2f).coerceIn(0f, limit)
}

/** The finger-release spring for every style. `UIView.animate(springDuration: Theme.Split.settle, bounce: 0)` (`:284`). */
internal fun columnDragSettleSpring() = IosSpring(ColumnDrag.settleMs, ColumnDrag.settleBounce)

/**
 * Blur of the pictures. `RenderEffect` exists on API 31; below that the drag draws no blur (stated, not approximated).
 * A radius under one pixel is skipped: a zero blur radius is not a defined effect.
 */
internal fun columnDragBlurEffect(radiusPx: Float, sdk: Int = Build.VERSION.SDK_INT): androidx.compose.ui.graphics.RenderEffect? {
    if (sdk < Build.VERSION_CODES.S || radiusPx < 1f) return null
    return RenderEffect.createBlurEffect(radiusPx, radiusPx, Shader.TileMode.CLAMP).asComposeRenderEffect()
}

/** The last line the overlay drew, in dp. Tests read it; production does not. */
internal var columnDragDrawnLineDp = Float.NaN

/** The live controller, so a test can move the line without a second gesture path. */
internal var columnDragControllerForTest: ColumnDragController? = null

internal class ColumnDragController {
    var step by mutableStateOf(DragStep.Idle)
    var layoutShown by mutableStateOf<Boolean?>(null)
    var dxPx by mutableFloatStateOf(0f)
    var blurDxPx by mutableFloatStateOf(0f)
    var opening by mutableStateOf(false)
    var over by mutableStateOf(false)
    var widthPx by mutableFloatStateOf(0f)
    var fingerVelocityPx by mutableFloatStateOf(0f)
    val lineAnim = Animatable(0f)
    val fadeAnim = Animatable(1f)
    var page: GraphicsLayer? = null
    var column: GraphicsLayer? = null
    var pageSeen = false
    var columnSeen = false
    var pageFrozen = false
    var columnFrozen = false
    var finishRequested = false
    var generation = 0

    val snaps: Boolean get() = step != DragStep.Idle
    val covers: Boolean
        get() = step == DragStep.OpenColumn || step == DragStep.ShutOverPage || step == DragStep.Follow ||
            step == DragStep.Settle || step == DragStep.Fade

    fun begin(zone: ColumnDragZone, over: Boolean, widthPx: Float) {
        if (step != DragStep.Idle) return
        generation++
        opening = zone == ColumnDragZone.Open
        this.over = over
        this.widthPx = widthPx
        dxPx = 0f
        blurDxPx = 0f
        fingerVelocityPx = 0f
        pageSeen = false
        columnSeen = false
        pageFrozen = false
        columnFrozen = false
        finishRequested = false
        layoutShown = null
        // Opening records the page first (`:209-216`). Closing over records the column, then the page without it
        // (`:217-223`). Closing beside records both and does not move the layout until release (`:198`, `:257-262`).
        step = when {
            opening -> DragStep.OpenPage
            over -> DragStep.ShutOverColumn
            else -> DragStep.ShutBeside
        }
    }

    fun move(dxPx: Float) {
        if (step == DragStep.Idle || step == DragStep.Settle || step == DragStep.Fade) return
        this.dxPx = dxPx
    }

    /** Finger up. Velocity is screen px each second, positive toward the right, as `pan.velocity.x` (`:176`). */
    fun end(velocityPxPerSec: Float) {
        if (step == DragStep.Idle || step == DragStep.Settle || step == DragStep.Fade) return
        fingerVelocityPx = velocityPxPerSec
        blurDxPx = dxPx
        if (step == DragStep.Follow) step = DragStep.Settle else finishRequested = true
    }

    /**
     * The draw recorded a picture. Advance one snapshot phase.
     * iOS takes both pictures inside `begin` before the gesture returns (`SplitColumnControl.swift:209-223`).
     * Android records in the draw that shows that phase, then advances on the next main-thread turn.
     */
    fun onPicture() {
        when (step) {
            DragStep.OpenPage -> {
                if (!pageSeen || pageFrozen) return
                pageFrozen = true
                columnSeen = false
                // Show the column under the picture, without the button animation (`:211-215`). Parent `shown`
                // waits until release (`SplitNavigation.swift:491-495` ignores settle while the drag is active).
                layoutShown = true
                step = DragStep.OpenColumn
            }
            DragStep.OpenColumn -> {
                if (!columnSeen || columnFrozen) return
                columnFrozen = true
                step = if (finishRequested) DragStep.Settle else DragStep.Follow
            }
            DragStep.ShutBeside -> {
                if (!pageSeen || !columnSeen || pageFrozen) return
                pageFrozen = true
                columnFrozen = true
                step = if (finishRequested) DragStep.Settle else DragStep.Follow
            }
            DragStep.ShutOverColumn -> {
                if (!columnSeen || columnFrozen) return
                columnFrozen = true
                pageFrozen = false
                pageSeen = false
                layoutShown = false
                step = DragStep.ShutOverPage
            }
            DragStep.ShutOverPage -> {
                if (!pageSeen || pageFrozen) return
                pageFrozen = true
                step = if (finishRequested) DragStep.Settle else DragStep.Follow
            }
            else -> Unit
        }
    }

    /** Run [onPicture] after this draw returns, so the layer already holds pixels. */
    fun postPictureAdvance() {
        val gen = generation
        Handler(Looper.getMainLooper()).post {
            if (generation != gen) return@post
            onPicture()
        }
    }
}

@Composable
internal fun rememberColumnDragController(
    width: Dp,
    overlay: Boolean,
    shown: Boolean,
    detailAtRoot: Boolean,
    onSettle: (Boolean) -> Unit,
): ColumnDragController {
    val controller = remember { ColumnDragController() }
    val page = rememberGraphicsLayer()
    val column = rememberGraphicsLayer()
    controller.page = page
    controller.column = column
    val reduceMotion = PersonaTheme.reduceMotion
    val settle = rememberUpdatedState(onSettle)
    DisposableEffect(controller) {
        columnDragControllerForTest = controller
        onDispose { if (columnDragControllerForTest === controller) columnDragControllerForTest = null }
    }
    ColumnDragRuntime(controller, width, reduceMotion) { settle.value(it) }
    return controller
}

@Composable
private fun ColumnDragRuntime(
    controller: ColumnDragController,
    width: Dp,
    reduceMotion: Boolean,
    onSettle: (Boolean) -> Unit,
) {
    val density = androidx.compose.ui.platform.LocalDensity.current
    // Pictures advance from the draw (`onPicture`). Only the release spring and the fade wait on frames.
    androidx.compose.runtime.LaunchedEffect(controller.step) {
        val gen = controller.generation
        fun live() = controller.generation == gen && controller.step != DragStep.Idle
        when (controller.step) {
            DragStep.Settle -> {
                val widthPx = with(density) { width.toPx() }.takeIf { controller.widthPx <= 0f } ?: controller.widthPx
                val current = columnDragLine(controller.opening, widthPx, controller.dxPx)
                val widthDp = widthPx / density.density
                val lineDp = current / density.density
                val velocityDp = controller.fingerVelocityPx / density.density
                val shows = columnDragShows(lineDp, widthDp, velocityDp)
                // `dragging` is already false before this settle (`:264-265`), so the beside choice can be remembered.
                onSettle(shows)
                controller.layoutShown = shows
                val target = if (shows) widthPx else 0f
                controller.lineAnim.snapTo(current)
                if (reduceMotion) {
                    // The line jumps. The fade below still runs (`:278-282`).
                    controller.lineAnim.snapTo(target)
                } else {
                    val initial = if (target == current) 0f else controller.fingerVelocityPx
                    controller.lineAnim.animateTo(target, columnDragSettleSpring().spec(), initialVelocity = initial)
                }
                if (!live()) return@LaunchedEffect
                controller.step = DragStep.Fade
            }
            DragStep.Fade -> {
                controller.fadeAnim.snapTo(1f)
                // `UIView.animate(withDuration: Theme.Split.fade)` (`:270`). The curve is the system ease; Compose's
                // default tween is not asserted to be that curve.
                controller.fadeAnim.animateTo(0f, tween(ColumnDrag.fadeMs))
                if (!live()) return@LaunchedEffect
                controller.layoutShown = null
                controller.pageFrozen = true
                controller.columnFrozen = true
                controller.step = DragStep.Idle
            }
            else -> Unit
        }
    }
}

internal fun ColumnDragController.recordModifier(): Modifier {
    val pageLayer = page
    val columnLayer = column
    val recordPage = !pageFrozen && pageLayer != null &&
        (step == DragStep.OpenPage || step == DragStep.ShutBeside || step == DragStep.ShutOverPage)
    val recordColumn = !columnFrozen && columnLayer != null &&
        (step == DragStep.OpenColumn || step == DragStep.ShutBeside || step == DragStep.ShutOverColumn)
    val pageToRecord = if (recordPage) pageLayer else null
    val columnToRecord = if (recordColumn) columnLayer else null
    if (pageToRecord == null && columnToRecord == null) return Modifier
    return Modifier.drawWithContent {
        var recorded = false
        if (pageToRecord != null) {
            pageToRecord.record { this@drawWithContent.drawContent() }
            pageSeen = true
            recorded = true
        }
        if (columnToRecord != null) {
            columnToRecord.record { this@drawWithContent.drawContent() }
            columnSeen = true
            recorded = true
        }
        drawContent()
        // After this draw, so the layer holds the pixels iOS snapshots in `begin` (`:209-223`).
        if (recorded) postPictureAdvance()
    }
}

@Composable
internal fun ColumnDragController.gestureModifier(
    width: Dp,
    shown: Boolean,
    detailAtRoot: Boolean,
    overlay: Boolean,
): Modifier {
    val shownState = rememberUpdatedState(shown)
    val rootState = rememberUpdatedState(detailAtRoot)
    val widthState = rememberUpdatedState(width)
    // The over/beside choice is the column's overlay flag, frozen when the finger is accepted (`:191-193`).
    val overState = rememberUpdatedState(overlay)
    // `pointerInput`'s key stays put so a phase change does not drop the finger (`SplitColumnControl` keeps one pan).
    return Modifier.pointerInput(this) {
        detectColumnDrag(
            shown = { shownState.value },
            detailAtRoot = { rootState.value },
            columnWidthPx = { widthState.value.toPx() },
            edgePx = { (Tokens.MinTouch.ios / 2).toPx() },
            onBegin = { zone -> begin(zone, overState.value, widthState.value.toPx()) },
            onMove = ::move,
            onEnd = ::end,
        )
    }
}

/**
 * One finger, on the initial pass, so a scroll or a row swipe still sees the touch until this drag takes it.
 * The system back edge is not made to wait (`SplitColumnControl.swift:156`): when the detail can go back,
 * [columnDragTakeOver] declines and nothing is consumed.
 *
 * iOS measures x from the left (`:136`). This does too. A right-to-left window is not specified there.
 */
private suspend fun PointerInputScope.detectColumnDrag(
    shown: () -> Boolean,
    detailAtRoot: () -> Boolean,
    columnWidthPx: () -> Float,
    edgePx: () -> Float,
    onBegin: (ColumnDragZone) -> Unit,
    onMove: (Float) -> Unit,
    onEnd: (Float) -> Unit,
) {
    val slop = viewConfiguration.touchSlop
    awaitEachGesture {
        val down = awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
        val tracker = VelocityTracker()
        tracker.addPosition(down.uptimeMillis, down.position)
        val origin = down.position
        var accepted = false
        try {
            while (true) {
                val event = awaitPointerEvent(PointerEventPass.Initial)
                val change = event.changes.firstOrNull { it.id == down.id } ?: break
                if (event.changes.any { it.id != down.id && it.pressed }) {
                    if (accepted) onEnd(0f)
                    return@awaitEachGesture
                }
                if (!change.pressed) {
                    if (accepted) onEnd(tracker.calculateVelocity().x)
                    return@awaitEachGesture
                }
                tracker.addPosition(change.uptimeMillis, change.position)
                val dx = change.position.x - origin.x
                val dy = change.position.y - origin.y
                if (!accepted) {
                    if (dx * dx + dy * dy <= slop * slop) continue
                    val velocity = tracker.calculateVelocity()
                    val zone = columnDragTakeOver(
                        drags = true,
                        shown = shown(),
                        detailAtRoot = detailAtRoot(),
                        startX = origin.x,
                        columnWidth = columnWidthPx(),
                        edge = edgePx(),
                        velocityX = velocity.x,
                        velocityY = velocity.y,
                    ) ?: return@awaitEachGesture
                    accepted = true
                    onBegin(zone)
                }
                change.consume()
                onMove(change.position.x - origin.x)
            }
        } catch (cancelled: CancellationException) {
            if (accepted) onEnd(0f)
            throw cancelled
        }
    }
}

@Composable
internal fun ColumnDragOverlay(controller: ColumnDragController) {
    if (!controller.covers) return
    val page = controller.page ?: return
    val column = controller.column ?: return
    val density = androidx.compose.ui.platform.LocalDensity.current
    val linePx = when (controller.step) {
        DragStep.Settle, DragStep.Fade -> controller.lineAnim.value
        else -> columnDragLine(controller.opening, controller.widthPx, controller.dxPx)
    }
    val blurPx = if (controller.step == DragStep.Settle || controller.step == DragStep.Fade) controller.blurDxPx else controller.dxPx
    val blurDp = blurPx / density.density
    val radiusPx = with(density) { columnDragBlurRadius(blurDp, ColumnDrag.fullMaterial).toPx() }
    val dim = if (controller.over) {
        // `UIColor.black` (`SplitColumnControl.swift:201`) at `Theme.Split.overDim * line / width` (`:294`).
        // `mediaBackdrop` is black in every style, so this file adds no color literal.
        PersonaTheme.palette.mediaBackdrop.copy(alpha = columnDragOverDim(linePx, controller.widthPx))
    } else Color.Unspecified
    val alpha = if (controller.step == DragStep.Fade) controller.fadeAnim.value else 1f
    val guidePx = with(density) { Tokens.Size.bar.toPx() }
    androidx.compose.runtime.SideEffect { columnDragDrawnLineDp = linePx / density.density }
    // Above the column's own zIndex while it is over the page. iOS draws page, dim, column, blur, then the guide (`:198-237`).
    BoxWithConstraints(Modifier.fillMaxSize().zIndex(2f).clipToBounds().graphicsLayer { this.alpha = alpha }) {
        val windowPx = with(density) { maxWidth.toPx() }
        androidx.compose.foundation.Canvas(Modifier.fillMaxSize().graphicsLayer {
            renderEffect = columnDragBlurEffect(radiusPx)
        }) {
            drawColumnDragScene(page, column, linePx, controller.widthPx, beside = !controller.over,
                opening = controller.opening, dim = dim)
        }
        val guideX = columnDragGuideX(linePx, guidePx, windowPx)
        // The guide is a sibling of the blurred canvas, so the blur does not cover it (`:233-237` adds it after the blur).
        Box(Modifier.offset { IntOffset(guideX.roundToInt(), 0) }.width(Tokens.Size.bar).fillMaxHeight()
            .background(PersonaTheme.palette.accent))
    }
}

/** `SplitColumnControl.draw` (`:321-328`): source start maps to dest start, clipped to the strip. */
internal fun androidx.compose.ui.graphics.drawscope.DrawScope.drawColumnDragScene(
    page: GraphicsLayer,
    column: GraphicsLayer,
    linePx: Float,
    columnWidthPx: Float,
    beside: Boolean,
    opening: Boolean,
    dim: Color,
) {
    val window = size.width
    // Opening, and any over drag, takes the page from the whole window (`:198`). Closing beside takes it from the column edge.
    val pageStart = if (opening || !beside) 0f else columnWidthPx
    val destStart = if (beside) linePx else 0f
    drawStrip(page, pageStart, window, destStart, window)
    if (!beside && dim.alpha > 0f) drawRect(dim)
    drawStrip(column, 0f, columnWidthPx, 0f, linePx)
}

private fun androidx.compose.ui.graphics.drawscope.DrawScope.drawStrip(
    layer: GraphicsLayer,
    sourceStart: Float,
    sourceEnd: Float,
    destStart: Float,
    destEnd: Float,
) {
    val source = sourceEnd - sourceStart
    val span = destEnd - destStart
    if (source <= 0f || span <= 0f) return
    val scale = (span / source).coerceAtLeast(0.001f)
    clipRect(destStart, 0f, destEnd, size.height) {
        translate(destStart - sourceStart * scale, 0f) {
            scale(scale, 1f, pivot = Offset.Zero) { drawLayer(layer) }
        }
    }
}
