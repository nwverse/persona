package persona.core.designsystem

import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.IntSize

/**
 * What opens in a chat, as iOS `.unfolding` (`ChatPin.swift:707`).
 *
 * A composable [Modifier], not a wrapper: the opened block already has a place in the tree, and a `graphicsLayer`
 * alpha does not change the size it measures. The row is its full height on the first frame and only fades in
 * (`Theme.Motion.reveal`, easeOut 0.28 s, `Theme.swift:751`; [Tokens.Motion.Reveal]). What closes leaves the
 * composition at once, its space with it (`toggleInPlace` sets `disablesAnimations`, `ChatPin.swift:686-688`, #3005).
 * Several iOS sites also write `.transition(.opacity.combined(with: .move(edge: .top)))` on that block. The insertion
 * runs inside that disabled transaction, so this modifier does not slide. [rememberOpenEdge] is true only on the
 * composition where `open` flips from closed to open, matching `Unfolding.revealing` (`ChatPin.swift:684-692`): a row
 * that was already open, or that a lazy list composed again, does not fade again. Reduced motion draws it at once
 * (`ChatPin.swift:721-722`). iPhone and regular width (iPad, iPhone Duo) use the same modifier; no size class skips it.
 *
 * [Modifier.concealing] copies `Theme.Motion.conceal` (`Theme.swift:752`, [Tokens.Motion.Conceal]). iOS uses it for a
 * menu (`MessageMenu.swift:97`, `:117`) and an inbox event (`InboxEventViews.swift:843`), not for a chat close, and no
 * chat calls it. [expandSpec] / [Modifier.expanding] copies `Theme.Motion.expand` (`Theme.swift:736`,
 * `Animation.smooth(duration: 0.4)`, no bounce, [Tokens.Motion.Expand]). Glass maps that spring the same way it maps
 * `Theme.Motion.layout` (also `Animation.smooth`); Brief and Board keep a tween. iOS calls it for the Lark pinned
 * shelf (`LarkPinnedShelf.swift:22`, `LarkConversationsView.swift:186`). Android has no such shelf, so nothing calls
 * [expanding]. A zero duration is a tween, never a spring. Reduced motion makes all three instant.
 */
@Stable
class OpenEdge {
    private var previous: Boolean? = null

    /**
     * True only on the composition where [open] changes from closed to open and motion is allowed.
     * The previous value updates after this composition, so a second pass in the same frame still sees the edge.
     */
    @Composable
    fun revealing(open: Boolean): Boolean {
        val reduce = PersonaTheme.reduceMotion
        val show = previous == false && open && !reduce
        SideEffect { previous = open }
        return show
    }
}

/** One [OpenEdge] for this composition. [key] starts a fresh edge when the row's identity changes. */
@Composable
fun rememberOpenEdge(key: Any? = null): OpenEdge = remember(key) { OpenEdge() }

/**
 * Fade this node in over the space it already takes, when [revealing]. Otherwise draw it as it is.
 * Put it on a node that enters the composition when the row opens.
 */
@Composable
fun Modifier.unfolding(revealing: Boolean): Modifier {
    val reduce = PersonaTheme.reduceMotion
    val fade = remember { revealing && !reduce }
    val alpha = remember { Animatable(if (fade) 0f else 1f) }
    LaunchedEffect(fade, reduce) {
        if (!fade || reduce) alpha.snapTo(1f)
        else alpha.animateTo(
            1f,
            tween(Tokens.Motion.Reveal.duration, easing = ComposerMotion.curve(Tokens.Motion.Reveal.curve)),
        )
    }
    if (!fade || reduce || alpha.value >= 1f) return this
    return graphicsLayer { this.alpha = alpha.value }
}

/**
 * Fade this node away with [Tokens.Motion.Conceal] while it still occupies its space.
 * [leaving] false draws it solid. Not for something a chat closes: that leaves at once.
 */
@Composable
fun Modifier.concealing(leaving: Boolean): Modifier {
    val reduce = PersonaTheme.reduceMotion
    // Reduced motion draws the end state in this composition. A zero-length tween still waits a frame.
    val alpha = animateFloatAsState(
        targetValue = if (leaving && !reduce) 0f else 1f,
        animationSpec = tween(Tokens.Motion.Conceal.duration, easing = ComposerMotion.curve(Tokens.Motion.Conceal.curve)),
        label = "conceal",
    )
    if (reduce) return if (leaving) graphicsLayer { this.alpha = 0f } else this
    if (!leaving && alpha.value >= 1f) return this
    return graphicsLayer { this.alpha = alpha.value }
}

/** How a block grows in place. Glass uses the iOS spring; Brief and Board keep a tween. Instant when reduced. */
@Composable
fun expandSpec(): FiniteAnimationSpec<IntSize> = expandAnimation(PersonaTheme.reduceMotion, PersonaTheme.style == AppStyle.Glass)

/**
 * [expandSpec] as a content-size animation. Chat rows do not use it; the pinned-shelf motion is the token.
 * Reduced motion skips the animation, so the new size is the one measured this frame.
 */
@Composable
fun Modifier.expanding(): Modifier {
    val reduce = PersonaTheme.reduceMotion
    val spec = expandSpec()
    if (reduce) return this
    return animateContentSize(spec)
}

private fun <T> expandAnimation(reduce: Boolean, glass: Boolean): FiniteAnimationSpec<T> {
    val duration = Tokens.Motion.Expand.duration
    if (reduce || duration <= 0 || !glass) {
        return tween(if (reduce || duration <= 0) Tokens.Motion.reduced else duration, easing = FastOutSlowInEasing)
    }
    return IosSpring(duration, Tokens.Motion.Expand.bounce).spec()
}
