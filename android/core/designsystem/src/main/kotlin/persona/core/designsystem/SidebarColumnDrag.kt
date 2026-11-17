package persona.core.designsystem

import android.graphics.RenderEffect
import android.graphics.Shader
import android.os.Build
import android.os.Handler
import android.os.Looper
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
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
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asComposeRenderEffect
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.layer.GraphicsLayer
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
import kotlinx.coroutines.runBlocking

/**
 * The foldable list column following a finger, copying iOS `SplitColumnControl` (`apple/Persona/Views/SplitColumnControl.swift`)
 * and `Theme.Split` (`apple/Shared/Design/Theme.swift:45-61`). Assistant history and the Agents list only.
 *
 * The line is 1:1 with the finger (`SplitColumnControl.swift:254`). Still pictures, taken after the layout they show
 * has been drawn (`snapshotView(afterScreenUpdates: true)`, `:216-231`), are stretched, so the column does not lay out
 * on each move (`:300-306`, `:330-337`). Release uses the flick at 500 pt/s, otherwise
 * half the width (`:264`), then a 0.28 s bounce-0 spring with the finger's velocity (`:293`) and a 0.2 s fade (`:277-286`).
 * Reduce Motion jumps the line and still fades (`:287-291`).
 */
internal object ColumnDrag {
    /** `gestureRecognizerShouldBegin`: `abs(v.x) > abs(v.y) * 1.5` (`SplitColumnControl.swift:137`, `:145`). */
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
    /** `Theme.Split.settle` bounce 0 (`SplitColumnControl.swift:293`). */
    const val settleBounce = 0f
    /** Half of `Theme.Size.tapTarget` (`Theme.swift:585`, `SplitColumnControl.swift:137`). Equal to `Tokens.MinTouch.ios / 2`. */
    const val edge = 22f
    /** `Theme.Split.guide` (`Theme.swift:58`). Equal to `Tokens.Size.bar`. */
    const val guide = 3f
    /**
     * Stand-in for a full `UIBlurEffect.systemUltraThinMaterial`. UIKit does not publish that blur's radius
     * (`SplitColumnControl.swift:238`). The fraction ramps with [columnDragBlur]; this is only how many dp that
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
 * `SplitColumnControl.swift:131-146` and `canGoBack` `:160-169`. A rightward drag on the column edge is not ours
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

/** `min(width, max(0, (opening ? 0 : width) + dx))` (`SplitColumnControl.swift:254`). No rubber band. */
internal fun columnDragLine(opening: Boolean, width: Float, dx: Float): Float {
    val start = if (opening) 0f else width
    return (start + dx).coerceIn(0f, width.coerceAtLeast(0f))
}

/**
 * A fling faster than [ColumnDrag.flingDpPerSec] decides; otherwise past halfway stays open.
 * Exactly the fling threshold uses the half rule (`abs(velocity) > fling`). Exactly half stays closed (`line > width / 2`).
 * `SplitColumnControl.swift:264`.
 */
internal fun columnDragShows(line: Float, width: Float, velocity: Float): Boolean =
    if (abs(velocity) > ColumnDrag.flingDpPerSec) velocity > 0f else line > width / 2f

/**
 * UIKit `initialSpringVelocity` is `distance == 0 ? 0 : velocity / distance` (`SplitColumnControl.swift:293`).
 * Compose's `Animatable` velocity is units per second, which is this share times the distance, the finger velocity.
 */
internal fun columnDragVelocityShare(velocity: Float, distance: Float): Float =
    if (distance == 0f) 0f else velocity / distance

/** `min(1, abs(dx) / Theme.Split.blurReach) * Theme.Split.dragBlur` (`SplitColumnControl.swift:256`). [dx] is in dp. */
internal fun columnDragBlur(dx: Float): Float = min(1f, abs(dx) / ColumnDrag.blurReach) * ColumnDrag.dragBlur

internal fun columnDragBlurRadius(dxDp: Float, fullMaterial: Dp): Dp = fullMaterial * columnDragBlur(dxDp)

/** `Theme.Split.overDim * line / width` (`SplitColumnControl.swift:303`). The over layout only. */
internal fun columnDragOverDim(line: Float, width: Float): Float =
    if (width > 0f) ColumnDrag.overDim * line / width else 0f

/** `min(max(line - guide / 2, 0), window - guide)` (`SplitColumnControl.swift:306`). */
internal fun columnDragGuideX(line: Float, guide: Float, window: Float): Float {
    val limit = window - guide
    if (limit <= 0f) return 0f
    return (line - guide / 2f).coerceIn(0f, limit)
}

/** The finger-release spring for every style. `UIView.animate(springDuration: Theme.Split.settle, bounce: 0)` (`:293`). */
internal fun columnDragSettleSpring() = IosSpring(ColumnDrag.settleMs, ColumnDrag.settleBounce)

/**
 * Blur of the pictures. `RenderEffect` exists on API 31; below that the drag draws no blur (stated, not approximated).
 * A radius under one pixel is skipped: a zero blur radius is not a defined effect.
 */
internal fun columnDragBlurEffect(radiusPx: Float, sdk: Int = Build.VERSION.SDK_INT): androidx.compose.ui.graphics.RenderEffect? {
    if (sdk < Build.VERSION_CODES.S || radiusPx < 1f) return null
    // `sdk` is what the unit test passes. Lint only trusts `SDK_INT` around `createBlurEffect` (API 31, minSdk 30).
    return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        RenderEffect.createBlurEffect(radiusPx, radiusPx, Shader.TileMode.CLAMP).asComposeRenderEffect()
    } else {
        null
    }
}

/** The last line the overlay drew, in dp. Tests read it; production does not. */
internal var columnDragDrawnLineDp = Float.NaN

/** True after the overlay has drawn this frame. Tests read it; production does not. */
internal var columnDragOverlayDrawn = false

/** The live controller, so a test can move the line without a second gesture path. */
internal var columnDragControllerForTest: ColumnDragController? = null

/**
 * Replaces [stillImage] in tests. Null in production.
 *
 * minSdk is 30, so `GraphicsLayer.toImageBitmap()` already uses the API 28 hardware snapshot and keeps a child
 * `RenderEffect`. Robolectric forces the software snapshot, which drops that effect. The replacement lives in test
 * source, because the hardware path reflects into an internal Compose method that R8 does not keep.
 */
internal var columnDragSnapshotForTest: (suspend (GraphicsLayer) -> ImageBitmap)? = null

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
    /** Scratch layers. The overlay draws [pagePicture] and [columnPicture], never these. */
    var pageLayer: GraphicsLayer? = null
    var columnLayer: GraphicsLayer? = null
    var pagePicture: ImageBitmap? = null
    var columnPicture: ImageBitmap? = null
    var pageSeen = false
    var columnSeen = false
    var pageFrozen = false
    var columnFrozen = false
    var finishRequested = false
    var generation = 0
    /** The overlay must draw before the layout under it changes (`SplitColumnControl.swift:204` before `:217`). */
    private var pendingLayout = false

    val snaps: Boolean get() = step != DragStep.Idle

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
        pagePicture = null
        columnPicture = null
        finishRequested = false
        pendingLayout = false
        columnDragOverlayDrawn = false
        layoutShown = null
        // Opening records the page first (`:206`), then the column after the column is shown (`:216-224`).
        // Closing over records the column, then the page without it (`:225-231`).
        // Closing beside records both and does not move the layout until release (`:199`; layout under the pictures is `:266-271`).
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
     * iOS takes both pictures inside `begin` before the gesture returns (`SplitColumnControl.swift:206-231`).
     * Android records in the draw that shows that phase, then advances on the next main-thread turn.
     */
    fun onPicture() {
        when (step) {
            DragStep.OpenPage -> {
                if (!pageSeen || pageFrozen) return
                pageFrozen = true
                columnSeen = false
                // The overlay draws this picture first (`:204-216`). Showing the column waits until that draw
                // (`:217-223`). Parent `shown` waits until release (`SplitNavigation.swift:491-495`).
                pendingLayout = true
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
                pagePicture = null
                // Cover with the picture of the column still up, then hide it (`:204-216`, then `:225-231`).
                pendingLayout = true
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

    /** The covering still is on screen. The layout under it may change now (`:217`, `:225`). */
    fun onCoverDrawn() {
        if (!pendingLayout) return
        pendingLayout = false
        val gen = generation
        Handler(Looper.getMainLooper()).post {
            if (generation != gen) return@post
            when (step) {
                DragStep.OpenColumn -> layoutShown = true
                DragStep.ShutOverPage -> layoutShown = false
                else -> Unit
            }
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
    controller.pageLayer = page
    controller.columnLayer = column
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
                // `dragging` is already false before this settle (`:272-273`), so the beside choice can be remembered.
                onSettle(shows)
                controller.layoutShown = shows
                val target = if (shows) widthPx else 0f
                controller.lineAnim.snapTo(current)
                if (reduceMotion) {
                    // The line jumps. The fade below still runs (`:287-291`).
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
                // `UIView.animate(withDuration: Theme.Split.fade)` (`:278`). The curve is the system ease; Compose's
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

/**
 * True once this frame's layout is the one the picture must show.
 * `animateFloatAsState` applies `snap()` on a later frame than the write, so the first frame after
 * `layoutShown` changes still has the old [progress]. iOS calls `layoutIfNeeded` and then
 * `snapshotView(afterScreenUpdates: true)` (`SplitColumnControl.swift:216-231`).
 */
/** The column in place (`:217-224`). Beside close keeps both where they already are. */
private fun columnPictureReady(step: DragStep, progress: Float): Boolean = when (step) {
    DragStep.OpenColumn, DragStep.ShutOverColumn, DragStep.ShutBeside -> progress >= 0.999f
    else -> false
}

/** The page as it is (`:206`), and again after the column is hidden (`:225-231`). */
private fun pagePictureReady(step: DragStep, progress: Float): Boolean = when (step) {
    DragStep.OpenPage, DragStep.ShutOverPage -> progress <= 0.001f
    DragStep.ShutBeside -> progress >= 0.999f
    else -> false
}

/**
 * [progress] is the value this frame composed with, so the draw matches it.
 * The bitmap is pixels (`GraphicsLayer.toImageBitmap`). A `GraphicsLayer` kept as the picture would still
 * point at child layers, which move or leave when the column does (`snapshotView` is a still image).
 */
internal fun ColumnDragController.recordModifier(progress: Float): Modifier {
    val pageToRecord = pageLayer?.takeIf { !pageFrozen && pagePictureReady(step, progress) }
    val columnToRecord = columnLayer?.takeIf { !columnFrozen && columnPictureReady(step, progress) }
    if (pageToRecord == null && columnToRecord == null) return Modifier
    return Modifier.drawWithContent {
        var recorded = false
        if (pageToRecord != null) {
            pageToRecord.record { this@drawWithContent.drawContent() }
            stillImage(pageToRecord)?.let {
                pagePicture = it
                pageSeen = true
                recorded = true
            }
        }
        if (columnToRecord != null) {
            columnToRecord.record { this@drawWithContent.drawContent() }
            stillImage(columnToRecord)?.let {
                columnPicture = it
                columnSeen = true
                recorded = true
            }
        }
        drawContent()
        // The bitmap already holds pixels. The phase change (show or hide the column) waits until after this draw.
        if (recorded) postPictureAdvance()
    }
}

/**
 * Rasterize while the recorded children still exist.
 *
 * Public `toImageBitmap` (`SplitColumnControl.swift:191` is the same rendered frame). A test may replace it via
 * [columnDragSnapshotForTest].
 */
private fun stillImage(layer: GraphicsLayer): ImageBitmap? {
    val size = layer.size
    if (size.width < 1 || size.height < 1) return null
    val shot = columnDragSnapshotForTest
    return runBlocking { if (shot != null) shot(layer) else layer.toImageBitmap() }
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
    // The over/beside choice is the column's overlay flag, frozen when the finger is accepted (`:199`).
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
            // `gestureRecognizerShouldBegin` returns false while a drag exists (`drag == nil`, `:131`).
            canBegin = { step == DragStep.Idle },
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
    canBegin: () -> Boolean,
) {
    val slop = viewConfiguration.touchSlop
    awaitEachGesture {
        val down = awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
        // Refuse before taking the touch (`:131`). Consuming here would swallow a scroll or a row swipe.
        if (!canBegin()) return@awaitEachGesture
        val tracker = VelocityTracker()
        tracker.addPosition(down.uptimeMillis, down.position)
        val origin = down.position
        var accepted = false
        try {
            while (true) {
                val event = awaitPointerEvent(PointerEventPass.Initial)
                // Extra fingers stay down. A pan does not end for them (`moved` has no pointer-count branch, `:171-179`).
                val change = event.changes.firstOrNull { it.id == down.id } ?: break
                if (!change.pressed) {
                    if (accepted) {
                        // `onCancelPointerInput` rebuilds the up with the change already consumed. A finger-up is not.
                        // `.cancelled` passes velocity 0 (`:178`); `.ended` passes `pan.velocity.x` (`:176`).
                        val cancelled = change.isConsumed
                        onEnd(if (cancelled) 0f else tracker.calculateVelocity().x)
                    }
                    return@awaitEachGesture
                }
                tracker.addPosition(change.uptimeMillis, change.position)
                val dx = change.position.x - origin.x
                val dy = change.position.y - origin.y
                if (!accepted) {
                    if (dx * dx + dy * dy <= slop * slop) continue
                    if (!canBegin()) return@awaitEachGesture
                    val tracked = tracker.calculateVelocity()
                    // The platform tracker reports 0 until it has a speed. The movement since the down is that
                    // direction, which is what the angle test compares (`SplitColumnControl.swift:133`).
                    val velocityX = if (tracked.x == 0f && tracked.y == 0f) change.position.x - origin.x else tracked.x
                    val velocityY = if (tracked.x == 0f && tracked.y == 0f) change.position.y - origin.y else tracked.y
                    val zone = columnDragTakeOver(
                        drags = true,
                        shown = shown(),
                        detailAtRoot = detailAtRoot(),
                        startX = origin.x,
                        columnWidth = columnWidthPx(),
                        edge = edgePx(),
                        velocityX = velocityX,
                        velocityY = velocityY,
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
internal fun ColumnDragOverlay(
    controller: ColumnDragController,
    page: ImageBitmap?,
    column: ImageBitmap?,
    step: DragStep,
) {
    // Pictures are written in draw, so they are parameters: a skipped read of the controller would keep the
    // overlay down after the stills exist (`SplitColumnControl` draws them for the whole drag, `:206-245`).
    // One still is enough to cover the screen before the layout under it changes (`:204-216`).
    val interactive = page != null && column != null &&
        (step == DragStep.Follow || step == DragStep.Settle || step == DragStep.Fade)
    val cover = when {
        interactive -> null
        step == DragStep.OpenColumn -> page
        step == DragStep.ShutOverPage -> column
        else -> null
    }
    if (!interactive && cover == null) {
        columnDragOverlayDrawn = false
        return
    }
    val density = androidx.compose.ui.platform.LocalDensity.current
    val linePx = when (step) {
        DragStep.Settle, DragStep.Fade -> controller.lineAnim.value
        else -> columnDragLine(controller.opening, controller.widthPx, controller.dxPx)
    }
    val blurPx = if (step == DragStep.Settle || step == DragStep.Fade) controller.blurDxPx else controller.dxPx
    val blurDp = blurPx / density.density
    val radiusPx = with(density) { columnDragBlurRadius(blurDp, ColumnDrag.fullMaterial).toPx() }
    val dim = if (controller.over) {
        // `UIColor.black` (`SplitColumnControl.swift:210`) at `Theme.Split.overDim * line / width` (`:303`).
        // `mediaBackdrop` is black in every style, so this file adds no color literal.
        PersonaTheme.palette.mediaBackdrop.copy(alpha = columnDragOverDim(linePx, controller.widthPx))
    } else Color.Unspecified
    val alpha = if (step == DragStep.Fade) controller.fadeAnim.value else 1f
    val guidePx = with(density) { Tokens.Size.bar.toPx() }
    androidx.compose.runtime.SideEffect { columnDragDrawnLineDp = linePx / density.density }
    if (cover != null) {
        val still = cover
        // The screen as it was, over whatever changes under it (`:204-216`). No line yet: the finger's frame is the
        // one that has both pictures (`:247-248`).
        Canvas(Modifier.fillMaxSize().zIndex(2f)) {
            drawImage(still)
            columnDragOverlayDrawn = true
            controller.onCoverDrawn()
        }
        return
    }
    // Above the column's own zIndex while it is over the page. iOS draws page, dim, column, blur, then the guide (`:206-245`).
    BoxWithConstraints(Modifier.fillMaxSize().zIndex(2f).clipToBounds().graphicsLayer { this.alpha = alpha }) {
        val windowPx = with(density) { maxWidth.toPx() }
        androidx.compose.foundation.Canvas(Modifier.fillMaxSize().graphicsLayer {
            renderEffect = columnDragBlurEffect(radiusPx)
        }) {
            drawColumnDragScene(page!!, column!!, linePx, controller.widthPx, beside = !controller.over,
                opening = controller.opening, dim = dim)
            columnDragOverlayDrawn = true
        }
        val guideX = columnDragGuideX(linePx, guidePx, windowPx)
        // The guide is a sibling of the blurred canvas, so the blur does not cover it (`:242-245` adds it after the blur).
        Box(Modifier.offset { IntOffset(guideX.roundToInt(), 0) }.width(Tokens.Size.bar).fillMaxHeight()
            .background(PersonaTheme.palette.accent))
    }
}

/** `SplitColumnControl.draw` (`:330-337`): source start maps to dest start, clipped to the strip. */
internal fun androidx.compose.ui.graphics.drawscope.DrawScope.drawColumnDragScene(
    page: ImageBitmap,
    column: ImageBitmap,
    linePx: Float,
    columnWidthPx: Float,
    beside: Boolean,
    opening: Boolean,
    dim: Color,
) {
    val window = size.width
    // Opening, and any over drag, takes the page from the whole window (`:206`). Closing beside takes it from the column edge.
    val pageStart = if (opening || !beside) 0f else columnWidthPx
    val destStart = if (beside) linePx else 0f
    drawStrip(page, pageStart, window, destStart, window)
    if (!beside && dim.alpha > 0f) drawRect(dim)
    drawStrip(column, 0f, columnWidthPx, 0f, linePx)
}

private fun androidx.compose.ui.graphics.drawscope.DrawScope.drawStrip(
    image: ImageBitmap,
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
            scale(scale, 1f, pivot = Offset.Zero) { drawImage(image) }
        }
    }
}
