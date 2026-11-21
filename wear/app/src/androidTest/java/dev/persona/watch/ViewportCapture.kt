package dev.persona.watch

import android.app.Activity
import android.app.Instrumentation
import android.graphics.Bitmap
import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.widget.ScrollView

/** Native screenshots only. Refreshes remain enabled; replaced views cause a bounded retry. */
internal class ViewportCapture(
    private val instrumentation: Instrumentation,
    private val currentActivity: () -> Activity,
    check: () -> Unit,
    private val save: (String, Bitmap) -> Unit,
) {
    private val guard = check

    private data class Position(
        val activity: Activity,
        val root: View,
        val scroll: ScrollView?,
        val extent: Int,
        val step: Int,
        val y: Int,
    )

    fun capture(name: String) {
        var requested = 0
        for (index in 0 until MAX_VIEWPORTS) {
            val position = frame("$name-$index") { _, _ -> requested }
            if (position.y >= position.extent) return
            requested = minOf(position.extent, position.y + position.step)
        }
        error("fixture_capture_page_too_long")
    }

    /** Center an actual chart or row; never move or redraw its contents for the screenshot. */
    fun captureCentered(name: String, predicate: (View) -> Boolean) {
        frame(name) { root, scroll ->
            val target = checkNotNull(find(root, predicate)) { "fixture_capture_target_missing" }
            if (scroll == null) 0 else {
                val targetLocation = IntArray(2)
                val scrollLocation = IntArray(2)
                target.getLocationInWindow(targetLocation)
                scroll.getLocationInWindow(scrollLocation)
                scroll.scrollY + targetLocation[1] - scrollLocation[1] + (target.height - scroll.height) / 2
            }
        }
    }

    private fun frame(name: String, requested: (View, ScrollView?) -> Int): Position {
        repeat(MAX_ATTEMPTS) {
            guard()
            val position = onMain {
                val current = positionOrNull(requested)
                current?.let { it.scroll?.scrollTo(0, it.y) }
                current
            }
            SystemClock.sleep(SETTLE_MS)
            guard()
            if (position == null || !current(position, requested)) return@repeat
            check(instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString() ==
                instrumentation.targetContext.packageName) { "fixture_screenshot_window" }
            val bitmap = checkNotNull(instrumentation.uiAutomation.takeScreenshot()) { "fixture_screenshot_failed" }
            try {
                guard()
                check(instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString() ==
                    instrumentation.targetContext.packageName) { "fixture_screenshot_window" }
                // A foreground poll can rebuild the hierarchy while takeScreenshot is in flight.
                if (!current(position, requested)) return@repeat
                save(name, bitmap)
                return position
            } finally {
                bitmap.recycle()
            }
        }
        error("fixture_capture_unstable_viewport")
    }

    private fun position(requested: (View, ScrollView?) -> Int): Position {
        val activity = currentActivity()
        check(!activity.isDestroyed && !activity.isFinishing) { "fixture_capture_activity" }
        val root = activity.window.decorView
        val scroll = find(root) { it is ScrollView } as? ScrollView
        val extent = scroll?.let {
            ((it.getChildAt(0)?.height ?: 0) - it.height + it.paddingTop + it.paddingBottom).coerceAtLeast(0)
        } ?: 0
        // Half-height steps leave a central, readable copy of wide text on a round display.
        val step = ((scroll?.height ?: root.height) / 2).coerceAtLeast(1)
        return Position(activity, root, scroll, extent, step, requested(root, scroll).coerceIn(0, extent))
    }

    private fun current(expected: Position, requested: (View, ScrollView?) -> Int): Boolean = onMain {
        val actual = positionOrNull(requested) ?: return@onMain false
        actual.activity === expected.activity && actual.root === expected.root && actual.root.isAttachedToWindow &&
            actual.scroll === expected.scroll && actual.extent == expected.extent && actual.step == expected.step &&
            actual.y == expected.y && (actual.scroll?.scrollY ?: 0) == expected.y &&
            (actual.scroll?.height ?: actual.root.height) > 0 &&
            !(actual.scroll ?: actual.root).isLayoutRequested
    }

    private fun positionOrNull(requested: (View, ScrollView?) -> Int): Position? = try {
        position(requested)
    } catch (_: IllegalStateException) {
        // A replaced activity/layout may briefly lack the requested chart or row.
        null
    }

    private fun <T> onMain(action: () -> T): T {
        var result: Result<T>? = null
        instrumentation.runOnMainSync { result = runCatching(action) }
        return checkNotNull(result).getOrThrow()
    }

    private fun find(view: View, predicate: (View) -> Boolean): View? {
        if (predicate(view)) return view
        if (view is ViewGroup) for (index in 0 until view.childCount) {
            find(view.getChildAt(index), predicate)?.let { return it }
        }
        return null
    }

    private companion object {
        const val MAX_VIEWPORTS = 20
        const val MAX_ATTEMPTS = 5
        const val SETTLE_MS = 250L
    }
}
