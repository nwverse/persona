package persona.core.designsystem

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.tween
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.systemGestures
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.PointerInputScope
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.input.pointer.util.addPointerInputChange
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.max

/**
 * The phone drawer's finger tracking, copied from the iPhone.
 *
 * Release: [AssistantDrawer.settlesOpen] (`apple/Persona/Views/Assistant/AssistantSidebar.swift:141-143`) is
 * [SwipeRelease.goes] (`apple/Persona/Views/Shell/ShellPager.swift:193-196`). A flick faster than 300 pt/s goes that
 * way; otherwise progress plus 0.2 s of velocity, over the width, opens past one half. Velocity is positive toward
 * open. 300 is not scale-invariant, so the threshold is in dp/s and the width in dp.
 *
 * Share: `AssistantSidebar.swift:119-121` is `velocity / way` (the page moves with the finger). The pager's settle
 * (`ShellPager.swift:156-157`) negates that because the strip moves against the finger; the drawer does not.
 * SwiftUI's initial velocity is the share of the remaining distance per second. Compose's is in value units per
 * second, so a 0..1 progress spring takes `share * (target - progress)`.
 *
 * iOS `Theme.Motion.drawer` (`apple/Shared/Design/Theme.swift:795-797`) does not read Reduce Motion. Android still
 * settles at once when [PersonaTheme.reduceMotion] is set; whether the spring should run then is an open question.
 */
internal object DrawerDrag {
    /** `SwipeRelease` flick, in dp per second (the iPhone's 300 pt/s). */
    const val flickDpPerSecond = 300f

    /** How much of the finger's velocity the half-way rule looks ahead, in seconds (`ShellPager.swift:195`). */
    const val projectionSeconds = 0.2f

    /** Below this, the remaining distance is too short to turn into a share (`AssistantSidebar.swift:120`). */
    const val minimumWay = 1f

    fun settlesOpen(progress: Float, velocityDpPerSecond: Float, widthDp: Float): Boolean {
        if (abs(velocityDpPerSecond) > flickDpPerSecond) return velocityDpPerSecond > 0f
        val width = max(widthDp, minimumWay)
        return progress + velocityDpPerSecond * projectionSeconds / width > 0.5f
    }

    /**
     * Share of the remaining distance the finger covers per second. Always ≥ 0; a finger heading the other way is 0.
     * [velocity] and [width] must share a unit (both dp, or both px).
     */
    fun velocityShare(progress: Float, target: Float, velocity: Float, width: Float): Float {
        val way = (target - progress) * max(width, minimumWay)
        return if (abs(way) > minimumWay) max(0f, velocity / way) else 0f
    }

    /** [velocityShare] as Compose progress units per second, signed toward [target]. */
    fun progressPerSecond(progress: Float, target: Float, velocity: Float, width: Float): Float =
        velocityShare(progress, target, velocity, width) * (target - progress)

    /** `AssistantDrawer.drag` (`AssistantSidebar.swift:100-104`): no animation, clamped to 0..1. */
    fun tracked(dragStart: Float, translationPx: Float, widthPx: Float): Float =
        (dragStart + translationPx / max(widthPx, minimumWay)).coerceIn(0f, 1f)

    /**
     * `AssistantDrawer.takes` (`AssistantSidebar.swift:147-149`). Within 45° of horizontal (strictly more horizontal
     * than vertical), and while closed only to the right. [isOpen] is "out at all" (`progress > 0`), not the resting flag.
     */
    fun takes(dx: Float, dy: Float, isOpen: Boolean): Boolean = abs(dx) > abs(dy) && (isOpen || dx > 0f)

    /**
     * A down in the system back-gesture inset is not the drawer's. iOS has no such inset on the Assistant root; it
     * refuses the gesture when a page is pushed (`DrawerPan`, `AssistantSidebar.swift:505-509`). Android's gesture
     * navigation owns both edges even at the root (`docs/design/DESIGN_SYSTEM.md` 4.2: the pan yields to the system).
     * Consuming that inset would swallow Back. A zero inset (three-button navigation) yields nothing.
     */
    fun inSystemBackInset(x: Float, leadingPx: Float, trailingPx: Float, widthPx: Float): Boolean =
        (leadingPx > 0f && x < leadingPx) || (trailingPx > 0f && x > widthPx - trailingPx)

    /** Why a drag was refused, the words `DrawerPan` records (`AssistantSidebar.swift:484-527`). */
    enum class Decision { Take, Direction, Pushed, SwipeRow, SidewaysScroller, SystemBack }

    /**
     * [onSwipeRow] and [sidewaysRoom] are resolved by the caller (iOS hit-tests them; Android reads whether the child
     * consumed the move). When the drawer is open, a sideways scroller is not checked (`DrawerPan` only does that
     * while closed, line 526).
     */
    fun decision(
        dx: Float,
        dy: Float,
        isOpen: Boolean,
        atRoot: Boolean,
        onSwipeRow: Boolean,
        sidewaysRoom: Boolean,
        inSystemBack: Boolean,
    ): Decision {
        if (inSystemBack) return Decision.SystemBack
        if (!atRoot) return Decision.Pushed
        if (isOpen) {
            if (dx < 0f && abs(dx) > abs(dy) && onSwipeRow) return Decision.SwipeRow
            return if (takes(dx, dy, isOpen = true)) Decision.Take else Decision.Direction
        }
        if (abs(dx) <= abs(dy)) return Decision.Direction
        if (dx <= 0f) return Decision.Direction
        if (sidewaysRoom) return Decision.SidewaysScroller
        return Decision.Take
    }

    /**
     * How far the sidebar sits below its resting place, in px. `applyToUIKit` (`AssistantSidebar.swift:69`) is
     * `(1 - progress) * Theme.Motion.drawerLift`, and `drawerLift` is `Theme.Space.s` (`Theme.swift:801`). At rest
     * open the offset is zero so no layer is added. At rest closed the sidebar is not composed.
     */
    fun liftPx(progress: Float, fullLiftPx: Float): Float =
        if (progress >= 1f) 0f else (1f - progress.coerceAtLeast(0f)) * fullLiftPx

    /**
     * Hairline opacity. `DrawerPageMotion` (`AssistantSidebar.swift:406` and `:421`) strokes only while out, at
     * opacity `progress`. Null hides the line (closed). At 1 the line keeps its own color, unscaled.
     */
    fun hairlineAlpha(progress: Float, lineAlpha: Float): Float? = when {
        progress <= 0f -> null
        progress >= 1f -> lineAlpha
        else -> lineAlpha * progress
    }
}

private class DrawerSettle(val open: Boolean, val velocity: Float, val token: Int)

/**
 * Finger state for [rememberPhoneDrawerDrag]. The pointer scope cannot call [Animatable.stop] (it is a restricted
 * suspend scope), so a drag writes [dragProgress] and a normal coroutine snaps the animatable when the finger lets go.
 */
private class DrawerRuntime(opened: Boolean) {
    val anim = Animatable(if (opened) 1f else 0f)
    var settle by mutableStateOf(DrawerSettle(opened, 0f, 0))
    var dragging by mutableStateOf(false)
    var dragProgress by mutableFloatStateOf(if (opened) 1f else 0f)
    var widthPx = 1f
    var leadingPx = 0f
    var trailingPx = 0f
    var opened = opened
    var carryVelocity = false
    var spec: FiniteAnimationSpec<Float> = tween(0)
    var onOpen: () -> Unit = {}
    var onDismiss: () -> Unit = {}
    var haptics: (Haptic) -> Unit = {}
    var clearText: () -> Unit = {}
    var horizontalConsumedPx = 0f
    lateinit var scope: CoroutineScope

    /**
     * While the finger is down, the page is wherever the finger put it. Once the settle animation has
     * finished, snap to exactly 0 or 1: a spring can stop a hair short, and that hair is enough to lift
     * the sidebar (`AssistantSidebar.swift:69`) and change the resting shadow.
     */
    val shown: Float
        get() {
            val raw = if (dragging) dragProgress else anim.value
            if (dragging || anim.isRunning) return raw
            if (settle.open && raw >= 1f - 1e-3f) return 1f
            if (!settle.open && raw <= 1e-3f) return 0f
            return raw
        }
}

internal class PhoneDrawerDrag(
    private val progressOf: () -> Float,
    val modifier: Modifier,
    private val widthOf: () -> Float,
    private val setWidth: (Float) -> Unit,
) {
    val progress: Float get() = progressOf()

    /** The drawer's width in px, measured by the shell. Tracking divides by it (`AssistantSidebar.swift:103`). */
    var widthPx: Float
        get() = widthOf()
        set(value) { setWidth(value) }
}

/**
 * Progress of the phone drawer, plus the modifier that tracks a finger when [dragEnabled].
 *
 * The spring runs only from [settle], so a finger that takes over mid-flight (`dragBegan`,
 * `AssistantSidebar.swift:92-97`) is not restarted at velocity 0 when the parent flag catches up.
 * Glass passes the share as the spring's initial velocity. Brief, Board and reduced motion pass 0
 * (their tweens ignore it; reduced motion stays the existing instant tween).
 */
@Composable
internal fun rememberPhoneDrawerDrag(
    opened: Boolean,
    onOpen: () -> Unit,
    onDismiss: () -> Unit,
    dragEnabled: Boolean,
): PhoneDrawerDrag {
    val runtime = remember { DrawerRuntime(opened) }
    runtime.scope = rememberCoroutineScope()
    runtime.opened = opened
    runtime.onOpen = onOpen
    runtime.onDismiss = onDismiss
    runtime.carryVelocity = PersonaTheme.style == AppStyle.Glass && !PersonaTheme.reduceMotion
    runtime.spec = drawerSpec(PersonaTheme.style, PersonaTheme.reduceMotion)
    runtime.haptics = rememberHaptics()
    val focus = LocalFocusManager.current
    val keyboard = LocalSoftwareKeyboardController.current
    runtime.clearText = {
        // `dragBegan` resigns the first responder when the drag starts closed (`AssistantSidebar.swift:94-96`).
        focus.clearFocus()
        keyboard?.hide()
    }
    val density = LocalDensity.current
    val direction = LocalLayoutDirection.current
    val gestures = WindowInsets.systemGestures
    runtime.leadingPx = gestures.getLeft(density, direction).toFloat()
    runtime.trailingPx = gestures.getRight(density, direction).toFloat()

    LaunchedEffect(opened) {
        if (!runtime.dragging && runtime.settle.open != opened) {
            runtime.settle = DrawerSettle(opened, 0f, runtime.settle.token + 1)
        }
    }
    LaunchedEffect(runtime.settle.token) {
        val settle = runtime.settle
        val velocity = if (runtime.carryVelocity) settle.velocity else 0f
        val target = if (settle.open) 1f else 0f
        if (runtime.anim.value != target || velocity != 0f) {
            runtime.anim.animateTo(target, runtime.spec, initialVelocity = velocity)
        }
    }

    // A horizontal scroller reports the distance it consumed. A vertical list can still consume the
    // pointer event while it decides, which is not `sidewaysScroller` (`AssistantSidebar.swift:551-563`).
    val nested = remember {
        object : NestedScrollConnection {
            override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset {
                runtime.horizontalConsumedPx += consumed.x
                return Offset.Zero
            }
        }
    }
    val modifier = Modifier.nestedScroll(nested).pointerInput(dragEnabled) {
        if (!dragEnabled) return@pointerInput
        trackDrawer(runtime)
    }
    return PhoneDrawerDrag(
        progressOf = { runtime.shown },
        modifier = modifier,
        widthOf = { runtime.widthPx },
        setWidth = { runtime.widthPx = it },
    )
}

private suspend fun PointerInputScope.trackDrawer(runtime: DrawerRuntime) {
    val slop = viewConfiguration.touchSlop
    awaitEachGesture {
        val down = awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
        // One finger (`DrawerPan` maximumNumberOfTouches, `AssistantSidebar.swift:642`).
        if (DrawerDrag.inSystemBackInset(down.position.x, runtime.leadingPx, runtime.trailingPx, size.width.toFloat())) {
            return@awaitEachGesture
        }
        val pointer = down.id
        val origin = down.position
        val tracker = VelocityTracker()
        // `addPointerInputChange` keeps the move samples and skips the finger-up, matching the tracker's
        // own contract. Adding the up (same place, later time) makes the flick look like it stopped.
        tracker.addPointerInputChange(down)
        var took = false
        var settled = false
        var dragStart = 0f
        try {
            while (true) {
                val event = awaitPointerEvent(PointerEventPass.Initial)
                val change = event.changes.firstOrNull { it.id == pointer }
                if (change == null || !change.pressed) {
                    if (took) {
                        // Set before the settle coroutine: a parent update must not also take the cancel path below.
                        settled = true
                        releaseDrawer(runtime, tracker, dragStart, density)
                        change?.consume()
                    }
                    break
                }
                val dx = change.position.x - origin.x
                val dy = change.position.y - origin.y
                tracker.addPointerInputChange(change)
                if (!took) {
                    if (hypot(dx, dy) < slop) continue
                    val open = runtime.shown > 0f
                    // Let the child see this move (Final) before deciding, except a rightward drag while open:
                    // iOS checks `sidewaysScroller` only while closed (`AssistantSidebar.swift:526`) and `onSwipeRow`
                    // only for a leftward drag on a row (`:500`). A vertical move has to reach a scroll view.
                    // A horizontal scroller consumes overscroll even at its start, so a rightward drag there yields;
                    // iOS takes that drag when the scroller has no room toward its start (`:551-563`).
                    var onSwipeRow = false
                    var sidewaysRoom = false
                    val takeImmediately = open && dx > 0f && abs(dx) > abs(dy)
                    if (!takeImmediately) {
                        runtime.horizontalConsumedPx = 0f
                        val seen = awaitPointerEvent(PointerEventPass.Final).changes.firstOrNull { it.id == pointer }
                        val horizontal = abs(runtime.horizontalConsumedPx) > 0.5f
                        if (open && dx < 0f && abs(dx) > abs(dy) && seen?.isConsumed == true) onSwipeRow = true
                        else if (!open && dx > 0f && abs(dx) > abs(dy) && horizontal) sidewaysRoom = true
                    }
                    val choice = DrawerDrag.decision(
                        dx, dy, isOpen = open, atRoot = true,
                        onSwipeRow = onSwipeRow, sidewaysRoom = sidewaysRoom, inSystemBack = false,
                    )
                    if (choice != DrawerDrag.Decision.Take) break
                    took = true
                    // Take over wherever the page is, including mid-spring (`dragBegan`, `:92-93`).
                    dragStart = runtime.shown
                    runtime.dragProgress = DrawerDrag.tracked(dragStart, dx, runtime.widthPx)
                    runtime.dragging = true
                    runtime.scope.launch { runtime.anim.stop() }
                    if (dragStart == 0f) runtime.clearText()
                    change.consume()
                } else {
                    change.consume()
                    runtime.dragProgress = DrawerDrag.tracked(dragStart, dx, runtime.widthPx)
                }
            }
        } finally {
            if (took && !settled) {
                // Cancelled or failed: close, with no leftover speed (`AssistantSidebar.swift:676-680`).
                val progress = runtime.dragProgress
                runtime.scope.launch {
                    runtime.anim.stop()
                    runtime.anim.snapTo(progress)
                    runtime.dragging = false
                    runtime.settle = DrawerSettle(false, 0f, runtime.settle.token + 1)
                    if (runtime.opened) runtime.onDismiss()
                }
            }
        }
    }
}

/**
 * `letGo` (`AssistantSidebar.swift:107-115`). [density] is px per dp, so the flick threshold stays 300 dp/s.
 * The share uses dp for both the velocity and the width. A drag that started closed and settles open plays
 * `Haptics.tap` (line 110); the button's open does not.
 */
private fun releaseDrawer(runtime: DrawerRuntime, tracker: VelocityTracker, dragStart: Float, density: Float) {
    val pxPerDp = max(density, DrawerDrag.minimumWay)
    val widthDp = max(runtime.widthPx, DrawerDrag.minimumWay) / pxPerDp
    val velocityDp = tracker.calculateVelocity().x / pxPerDp
    val progress = runtime.dragProgress
    val opens = DrawerDrag.settlesOpen(progress, velocityDp, widthDp)
    val target = if (opens) 1f else 0f
    val velocity = DrawerDrag.progressPerSecond(progress, target, velocityDp, widthDp)
    if (dragStart == 0f && opens) runtime.haptics(Haptic.Tap)
    runtime.scope.launch {
        runtime.anim.stop()
        runtime.anim.snapTo(progress)
        runtime.dragging = false
        runtime.settle = DrawerSettle(opens, velocity, runtime.settle.token + 1)
        if (opens && !runtime.opened) runtime.onOpen()
        else if (!opens && runtime.opened) runtime.onDismiss()
    }
}
