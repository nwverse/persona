@file:Suppress("INVISIBLE_REFERENCE", "INVISIBLE_MEMBER")

package persona.core.designsystem

import android.view.MotionEvent
import androidx.compose.animation.core.Animatable
import androidx.compose.foundation.AbstractClickableNode
import androidx.compose.foundation.gestures.DragGestureNode
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.PointerEvent
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.PointerInputChange
import androidx.compose.ui.input.pointer.PointerInputScope
import androidx.compose.ui.input.pointer.SuspendingPointerInputModifierNode
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.input.pointer.util.addPointerInputChange
import androidx.compose.ui.node.DelegatingNode
import androidx.compose.ui.node.LayoutNode
import androidx.compose.ui.node.ModifierNodeElement
import androidx.compose.ui.node.PointerInputModifierNode
import androidx.compose.ui.node.requireLayoutCoordinates
import androidx.compose.ui.node.requireLayoutNode
import androidx.compose.ui.platform.InspectorInfo
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import kotlin.coroutines.cancellation.CancellationException
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.max
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

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
 * iOS `Theme.Motion.drawer` (`apple/Shared/Design/Theme.swift:799-801`) does not read Reduce Motion. Android still
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
    enum class Decision { Take, Direction, Pushed, SwipeRow, SidewaysScroller, SystemBack, Child }

    /**
     * [onSwipeRow], [sidewaysRoom] and [childOwns] are resolved by the caller. iOS hit-tests the scroller and the
     * swipe row (`AssistantSidebar.swift:500`, `:551-563`). A sideways scroller is checked only while closed (line
     * 526), and only when it has room toward its start (line 558). [childOwns] is a move some other recognizer has
     * already taken (a slider, a selection, a chart, a link card). Scroll views wait instead
     * (`shouldBeRequiredToFailBy`, lines 540-544), so a list consuming a horizontal move is not one of those.
     */
    fun decision(
        dx: Float,
        dy: Float,
        isOpen: Boolean,
        atRoot: Boolean,
        onSwipeRow: Boolean,
        sidewaysRoom: Boolean,
        inSystemBack: Boolean,
        childOwns: Boolean = false,
    ): Decision {
        if (inSystemBack) return Decision.SystemBack
        if (!atRoot) return Decision.Pushed
        if (isOpen) {
            if (dx < 0f && abs(dx) > abs(dy) && onSwipeRow) return Decision.SwipeRow
            if (childOwns && abs(dx) > abs(dy)) return Decision.Child
            return if (takes(dx, dy, isOpen = true)) Decision.Take else Decision.Direction
        }
        if (abs(dx) <= abs(dy)) return Decision.Direction
        if (dx <= 0f) return Decision.Direction
        if (childOwns) return Decision.Child
        if (sidewaysRoom) return Decision.SidewaysScroller
        return Decision.Take
    }

    /**
     * How far the sidebar sits below its resting place, in px. `applyToUIKit` (`AssistantSidebar.swift:69`) is
     * `(1 - progress) * Theme.Motion.drawerLift`, and `drawerLift` is `Theme.Space.s` (`Theme.swift:805`). At rest
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

    /**
     * True from the moment a finger takes the page until that settle animation finishes. The shell reads progress
     * in draw lambdas while this is set, so the drag does not recompose the drawer every frame.
     */
    var live by mutableStateOf(false)
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
    var fingerDown = false

    /** An external open or close arrived while the finger was down. That target wins over the release. */
    var superseded = false
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
    private val liveOf: () -> Boolean,
    val modifier: Modifier,
    private val widthOf: () -> Float,
    private val setWidth: (Float) -> Unit,
) {
    val progress: Float get() = progressOf()

    /** A finger is tracking, or the settle animation from that tracking is still running. */
    val live: Boolean get() = liveOf()

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

    // Back, or any other write of [opened], ends a drag in progress and wins (`AssistantSidebar.swift` has no
    // second writer mid-pan; Android's BackHandler does). Skipping this while the finger is down let the release
    // open the drawer again.
    LaunchedEffect(opened) {
        if (runtime.fingerDown) {
            runtime.superseded = true
            if (runtime.dragging) {
                val progress = runtime.dragProgress
                runtime.anim.stop()
                runtime.anim.snapTo(progress)
                runtime.dragging = false
            }
        }
        if (runtime.settle.open != opened) {
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
        // `anim.stop()` cancels this effect, so a finger taking over does not clear [DrawerRuntime.live] here.
        runtime.live = false
    }

    val modifier = Modifier.then(DrawerDragElement(runtime, dragEnabled))
    return PhoneDrawerDrag(
        progressOf = { runtime.shown },
        liveOf = { runtime.live },
        modifier = modifier,
        widthOf = { runtime.widthPx },
        setWidth = { runtime.widthPx = it },
    )
}

/**
 * What is under the finger, from the hit node upward, the way `sidewaysScroller` walks
 * (`AssistantSidebar.swift:551-563`).
 *
 * [Waiting] is a scroller that should let the drawer go first: a vertical list, or a horizontal one with no room
 * toward the start (line 558). [Sideways] is a horizontal scroller that still has that room, and only while the
 * drawer is closed and the finger pulls toward the start (line 526). [Child] is some other drag (a slider, a
 * selection, a chart, a link card, a swipe row). A tap target is none of these.
 */
private enum class UnderFinger { None, Waiting, Sideways, Child }

/** [interop] is an Android view (a link card's WebView). It handles the touch outside Compose's consumption flag. */
private data class FingerTarget(val under: UnderFinger, val interop: Boolean)

private class DrawerDragNode(private val runtime: DrawerRuntime, enabled: Boolean) : DelegatingNode() {
    private var enabled = enabled
    private val pointer = delegate(SuspendingPointerInputModifierNode {
        if (!this@DrawerDragNode.enabled) return@SuspendingPointerInputModifierNode
        trackDrawer(runtime, this@DrawerDragNode)
    })

    fun setEnabled(value: Boolean) {
        if (enabled == value) return
        enabled = value
        // Restarting the pointer job is a configuration change, not a finger cancel (`PointerInputResetException`).
        pointer.resetPointerInputHandler()
    }

    fun underFinger(at: Offset, open: Boolean, dx: Float): FingerTarget {
        if (!isAttached) return FingerTarget(UnderFinger.None, interop = false)
        // Deeper nodes win: a slider inside a list is the slider. A scroller with no room does not stop the walk,
        // so an outer one that still has room keeps the drag (`AssistantSidebar.swift:556-562`).
        var found = UnderFinger.None
        var interop = false
        fun walk(node: LayoutNode) {
            if (found == UnderFinger.Child || found == UnderFinger.Sideways) return
            if (!covers(node, at)) return
            node.forEachChild { walk(it) }
            if (found == UnderFinger.Child || found == UnderFinger.Sideways) return
            when (val kind = classify(node, open, dx)) {
                UnderFinger.Child -> {
                    found = UnderFinger.Child
                    interop = DrawerDragHit.hasAndroidView(node)
                }
                UnderFinger.Sideways -> found = UnderFinger.Sideways
                UnderFinger.Waiting -> if (found == UnderFinger.None) found = UnderFinger.Waiting
                UnderFinger.None -> {}
            }
        }
        requireLayoutNode().forEachChild { walk(it) }
        return FingerTarget(found, interop)
    }

    /**
     * Scroll axes are the offset (`ScrollState.value`), read here so the decision does not wait for the scroller's
     * drag coroutine. Room toward the start is that offset past 1px (`AssistantSidebar.swift:558`).
     */
    private fun classify(node: LayoutNode, open: Boolean, dx: Float): UnderFinger {
        // A link card is an Android view. Kotlin cannot see getInteropView; DrawerDragHit calls it.
        if (DrawerDragHit.hasAndroidView(node)) return UnderFinger.Child
        val config = node.semanticsConfiguration
        if (config != null) {
            val horizontal = SemanticsProperties.HorizontalScrollAxisRange
            if (config.contains(horizontal)) {
                val axis = config[horizontal]
                val value = axis.value()
                val atStart = if (axis.reverseScrolling) value >= axis.maxValue() - 1f else value <= 1f
                return if (!open && dx > 0f && !atStart) UnderFinger.Sideways else UnderFinger.Waiting
            }
            if (config.contains(SemanticsProperties.VerticalScrollAxisRange)) return UnderFinger.Waiting
            if (config.contains(SemanticsProperties.ProgressBarRangeInfo)) return UnderFinger.Child
            if (config.contains(SemanticsProperties.TextSelectionRange)) return UnderFinger.Child
        }
        return if (claimsDrag(node)) UnderFinger.Child else UnderFinger.None
    }

    /** A drag that is not the scroller's own. A click consumes nothing past a tap, so it is not one. */
    private fun claimsDrag(node: LayoutNode): Boolean {
        var current: Modifier.Node? = DrawerDragHit.head(node)
        while (current != null) {
            if (current is PointerInputModifierNode && current !is DragGestureNode && current !is AbstractClickableNode) {
                return true
            }
            if (current is DragGestureNode) return true
            current = DrawerDragHit.next(current)
        }
        return false
    }

    private fun covers(node: LayoutNode, at: Offset): Boolean {
        val child = node.coordinates
        if (!child.isAttached) return false
        val host = requireLayoutCoordinates()
        val origin = host.localPositionOf(child, Offset.Zero)
        val size = child.size
        return at.x >= origin.x && at.y >= origin.y && at.x < origin.x + size.width && at.y < origin.y + size.height
    }
}

private data class DrawerDragElement(val runtime: DrawerRuntime, val enabled: Boolean) : ModifierNodeElement<DrawerDragNode>() {
    override fun create() = DrawerDragNode(runtime, enabled)
    override fun update(node: DrawerDragNode) { node.setEnabled(enabled) }
    override fun InspectorInfo.inspectableProperties() { name = "phoneDrawerDrag" }
}

private suspend fun PointerInputScope.trackDrawer(runtime: DrawerRuntime, hit: DrawerDragNode) {
    val slop = viewConfiguration.touchSlop
    awaitEachGesture {
        // The previous gesture's external close must not block the next finger.
        runtime.superseded = false
        val down = awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
        // One finger (`DrawerPan` maximumNumberOfTouches, `AssistantSidebar.swift:642`).
        if (DrawerDrag.inSystemBackInset(down.position.x, runtime.leadingPx, runtime.trailingPx, size.width.toFloat())) {
            return@awaitEachGesture
        }
        runtime.fingerDown = true
        val pointer = down.id
        val origin = down.position
        val tracker = VelocityTracker()
        // `addPointerInputChange` keeps the move samples and skips the finger-up, matching the tracker's
        // own contract. Adding the up (same place, later time) makes the flick look like it stopped.
        tracker.addPointerInputChange(down)
        var took = false
        var settled = false
        var tornDown = false
        var dragStart = 0f
        try {
            while (true) {
                val event = awaitPointerEvent(PointerEventPass.Initial)
                if (runtime.superseded) {
                    // Back (or another writer) already chose. Do not let the finger's release overwrite it.
                    settled = true
                    break
                }
                val change = event.changes.firstOrNull { it.id == pointer }
                if (change == null || !change.pressed) {
                    if (took) {
                        if (pointerCancelled(event, change)) {
                            // A synthetic up after cancel must not settle open with the finger's speed
                            // (`AssistantSidebar.swift:676-680` calls `close()`, velocity 0).
                        } else {
                            settled = true
                            releaseDrawer(runtime, tracker, dragStart, density)
                            change?.consume()
                        }
                    }
                    break
                }
                val dx = change.position.x - origin.x
                val dy = change.position.y - origin.y
                tracker.addPointerInputChange(change)
                if (!took) {
                    if (hypot(dx, dy) < slop) continue
                    val open = runtime.shown > 0f
                    val horizontal = abs(dx) > abs(dy)
                    // `sidewaysScroller` (`AssistantSidebar.swift:551-563`). A vertical list consumes a horizontal
                    // move without owning it; iOS makes that list wait (`shouldBeRequiredToFailBy`, lines 540-544),
                    // so claim the move before its Main pass. A slider, selection, chart or link card is not a
                    // scroll view: if it consumed the move, it keeps it.
                    val target = hit.underFinger(change.position, open, dx)
                    val under = target.under
                    val sidewaysRoom = under == UnderFinger.Sideways
                    val claimScroller = under == UnderFinger.Waiting && horizontal && (open || dx > 0f)
                    if (claimScroller) change.consume()
                    val seen = awaitPointerEvent(PointerEventPass.Final).changes.firstOrNull { it.id == pointer }
                    if (runtime.superseded) {
                        settled = true
                        break
                    }
                    val childConsumed = seen?.isConsumed == true && !claimScroller
                    // A link card's view owns the touch in the Android view system, which does not set
                    // [PointerInputChange.isConsumed]. A slider or selection does, and only then keeps the drag.
                    val childKeeps = horizontal && under == UnderFinger.Child && (childConsumed || target.interop)
                    val onSwipeRow = childKeeps && open && dx < 0f
                    val childOwns = childKeeps && !onSwipeRow
                    val choice = DrawerDrag.decision(
                        dx, dy, isOpen = open, atRoot = true,
                        onSwipeRow = onSwipeRow, sidewaysRoom = sidewaysRoom, inSystemBack = false,
                        childOwns = childOwns,
                    )
                    if (choice != DrawerDrag.Decision.Take) break
                    took = true
                    // Take over wherever the page is, including mid-spring (`dragBegan`, `:92-93`).
                    dragStart = runtime.shown
                    runtime.live = true
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
        } catch (cancelled: CancellationException) {
            // The pointer job was reset (unfold, a pushed page, another tab). Not a finger cancel.
            tornDown = true
            throw cancelled
        } finally {
            runtime.fingerDown = false
            if (took && !settled && !runtime.superseded) {
                if (tornDown) {
                    // The pointer job was reset because the gesture left (unfold, a pushed page, another tab).
                    // That is not a finger cancel: do not call onDismiss, or the open drawer never becomes the column.
                    parkDrawer(runtime, runtime.opened, notify = false)
                } else {
                    // Cancelled or failed: close, with no leftover speed (`AssistantSidebar.swift:676-680`).
                    parkDrawer(runtime, open = false, notify = true)
                }
            }
        }
    }
}

/**
 * A finger-up from the platform carries [MotionEvent.ACTION_UP]. A cancel is delivered as a synthetic up with no
 * motion event (`onCancelPointerInput`), same time and place, so it must not be treated as a release.
 */
private fun pointerCancelled(event: PointerEvent, change: PointerInputChange?): Boolean {
    if (change == null) return true
    if (change.pressed) return false
    val motion = event.motionEvent ?: return change.previousPressed
    return motion.actionMasked == MotionEvent.ACTION_CANCEL
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
        if (runtime.superseded) return@launch
        runtime.anim.stop()
        if (runtime.superseded) return@launch
        runtime.anim.snapTo(progress)
        if (runtime.superseded) return@launch
        runtime.dragging = false
        runtime.settle = DrawerSettle(opens, velocity, runtime.settle.token + 1)
        if (runtime.superseded) return@launch
        if (opens && !runtime.opened) runtime.onOpen()
        else if (!opens && runtime.opened) runtime.onDismiss()
    }
}

/**
 * Leave the finger, with no speed (`drawer.close()`, `AssistantSidebar.swift:89` and `:676-680`).
 * [notify] tells the shell when this close is the finger's cancel. A torn-down gesture does not: the flag already
 * says whether the drawer is open, and dismissing it races the unfold that turns it into the sidebar column.
 */
private fun parkDrawer(runtime: DrawerRuntime, open: Boolean, notify: Boolean) {
    val progress = runtime.dragProgress
    val wasOpen = runtime.opened
    runtime.scope.launch {
        if (runtime.superseded) return@launch
        runtime.anim.stop()
        if (runtime.superseded) return@launch
        runtime.anim.snapTo(progress)
        if (runtime.superseded) return@launch
        runtime.dragging = false
        runtime.settle = DrawerSettle(open, 0f, runtime.settle.token + 1)
        if (notify && wasOpen && !open) runtime.onDismiss()
    }
}
