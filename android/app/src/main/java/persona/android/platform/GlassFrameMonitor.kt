package persona.android.platform

import android.app.Activity
import android.os.Handler
import android.os.HandlerThread
import android.util.Log
import android.view.FrameMetrics
import android.view.Window
import persona.core.designsystem.GlassLevel
import persona.core.designsystem.GlassThrottle

/**
 * Lowers the glass style one step (refraction → blur → opaque) when drawing it keeps missing frames.
 * Frames only arrive while something moves, so the window is mostly scrolling and animation. Severe sustained jank
 * lowers after 120 frames; the looser 240-frame threshold keeps a few slow frames while a page opens from costing the effect.
 */
object GlassFrameMonitor {
    private const val WINDOW = 240
    private const val EARLY_WINDOW = 120
    private const val EARLY_JANK_SHARE = 0.60
    private const val JANK_SHARE = 0.30

    internal fun shouldLower(frames: Int, janky: Int): Boolean {
        if (frames < EARLY_WINDOW) return false
        val share = janky.toDouble() / frames
        return share > EARLY_JANK_SHARE || (frames >= WINDOW && share > JANK_SHARE)
    }

    fun attach(activity: Activity, enabled: () -> Boolean) {
        val thread = HandlerThread("glass-frames").apply { start() }
        val budget = 1_000_000_000L / (activity.display?.refreshRate?.toLong()?.coerceAtLeast(30) ?: 60L)
        var frames = 0
        var janky = 0
        val listener = Window.OnFrameMetricsAvailableListener { _, metrics, _ ->
            if (!enabled() || GlassThrottle.ceiling.value == GlassLevel.Solid) return@OnFrameMetricsAvailableListener
            if (metrics.getMetric(FrameMetrics.FIRST_DRAW_FRAME) == 1L) return@OnFrameMetricsAvailableListener
            frames++
            if (metrics.getMetric(FrameMetrics.TOTAL_DURATION) > budget * 3 / 2) janky++
            val lower = shouldLower(frames, janky)
            if (lower) {
                activity.runOnUiThread { GlassThrottle.lower() }
                Log.i("PersonaGlass", "glass lowered after $janky slow frames of $frames")
            }
            if (lower || frames >= WINDOW) {
                frames = 0
                janky = 0
            }
        }
        activity.window.addOnFrameMetricsAvailableListener(listener, Handler(thread.looper))
    }
}
