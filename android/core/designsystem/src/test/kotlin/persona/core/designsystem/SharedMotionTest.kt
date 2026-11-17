package persona.core.designsystem

import androidx.compose.animation.core.SpringSpec
import androidx.compose.animation.core.TweenSpec
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Shared motions from `spec/tokens/motion.json` as Compose specs (#9897), matching iOS `Theme.Motion`
 * (`apple/Shared/Design/Theme.swift`).
 */
class SharedMotionTest {
    @Test fun expandIsTheIosSmoothSpring() {
        assertEquals(400, Tokens.Motion.Expand.duration)
        assertEquals(0f, Tokens.Motion.Expand.bounce)
        val spring = SharedMotion.expand<Float>() as SpringSpec
        assertEquals(IosSpring(400, 0f).dampingRatio, spring.dampingRatio, 1e-6f)
        assertEquals(IosSpring(400, 0f).stiffness, spring.stiffness, 0.01f)
    }

    @Test fun revealAndConcealAreTheIosEasedTweens() {
        val reveal = SharedMotion.reveal<Float>() as TweenSpec
        assertEquals(Tokens.Motion.Reveal.duration, reveal.durationMillis)
        val conceal = SharedMotion.conceal<Float>() as TweenSpec
        assertEquals(Tokens.Motion.Conceal.duration, conceal.durationMillis)
    }

    @Test fun drawerAndSpringBackMatchTokens() {
        assertEquals(320, Tokens.Motion.Drawer.duration)
        assertEquals(0f, Tokens.Motion.Drawer.bounce)
        assertEquals(300, Tokens.Motion.SpringBack.duration)
        assertEquals(0.25f, Tokens.Motion.SpringBack.bounce)
        assertTrue(SharedMotion.drawer<Float>() is SpringSpec)
        assertTrue(SharedMotion.springBack<Float>() is SpringSpec)
    }

    @Test fun keyboardIsTheIosMassSpring() {
        assertEquals(555.027f, Tokens.Motion.Keyboard.stiffness)
        assertEquals(47.118f, Tokens.Motion.Keyboard.damping)
        val spring = IosMassSpring(
            stiffness = Tokens.Motion.Keyboard.stiffness,
            damping = Tokens.Motion.Keyboard.damping,
        )
        // mass 1: damping / (2√stiffness) ≈ 1 (critically damped).
        assertEquals(1f, spring.dampingRatio, 0.001f)
        val spec = SharedMotion.keyboard<Float>() as SpringSpec
        assertEquals(spring.dampingRatio, spec.dampingRatio, 1e-6f)
        assertEquals(spring.stiffness, spec.stiffness, 1e-3f)
    }
}
