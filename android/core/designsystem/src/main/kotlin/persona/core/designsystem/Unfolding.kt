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
 * What opens in a chat, as iOS `.unfolding` (`ChatPin.swift`).
 *
 * A composable [Modifier], not a wrapper: the opened block already has a place in the tree, and a `graphicsLayer`
 * alpha does not change the size it measures. The row is its full height on the first frame and only fades in
 * ([Tokens.Motion.Reveal], easeOut 0.28 s). What closes leaves the composition at once, its space with it (#3005);
 * there is no exit transition. [rememberOpenEdge] is true only on the composition where `open` flips from closed to
 * open, so a row that was already open, or that a lazy list composed again, does not fade again.
 *
 * [Modifier.concealing] is the menu and inbox fade ([Tokens.Motion.Conceal]): the space stays while the alpha goes.
 * A chat does not conceal what it closes. [expandSpec] / [Modifier.expanding] is [Tokens.Motion.Expand] (0.4 s, no
 * bounce), the in-place grow iOS uses for a pinned shelf: an [IosSpring] on Glass, a tween on Brief and Board.
 * A zero duration is a tween, never a spring. Reduced motion makes all three instant.
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
