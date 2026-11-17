package persona.core.designsystem

import androidx.compose.ui.geometry.Rect
import kotlin.math.min

/**
 * Left-edge strip claimed from system gestures at a page root (#9803), so the phone drawer and the
 * foldable list column can open as on the iPhone.
 *
 * iOS: there is no system back-swipe at a root (`SwipeBack.swift:11-16`); `DrawerPan` covers the whole
 * page (`AssistantSidebar.swift:153-154`); the column opens from within half a tap target of the
 * leading edge while the detail cannot go back (`SplitColumnControl.swift:132-137`, `:160-169`).
 * Android's gesture navigation owns both edges even at a root; [Modifier.systemGestureExclusion]
 * reclaims a left strip. The right edge stays with system Back (open in #9803). Three-button
 * navigation has a zero inset, so the drawer excludes nothing there.
 */
internal object RootEdgeExclusion {
    /**
     * Android honours at most 200 dp of exclusion height per edge. A band that tall, centred on the
     * page's vertical middle, covers one-handed reach along the edge (same middle as
     * `PaneDivider.kt:115-118`) without claiming the status bar or the home indicator.
     */
    const val heightDp = 200f

    /**
     * Local rect for `Modifier.systemGestureExclusion`, or null when there is nothing to claim.
     *
     * [stripWidthPx] is the left system-gestures inset for the phone drawer, or at least the column
     * open zone (`Tokens.MinTouch.ios / 2`) for the foldable column. Zero width excludes nothing.
     * [active] is false on a pushed page, while the drawer or column is open, or when the drag is off.
     * [bandHeightPx] must be at most [heightDp] in px; taller requests are clamped to the layout.
     */
    fun rect(
        active: Boolean,
        stripWidthPx: Float,
        layoutWidthPx: Float,
        layoutHeightPx: Float,
        bandHeightPx: Float,
    ): Rect? {
        if (!active || stripWidthPx <= 0f || layoutWidthPx <= 0f || layoutHeightPx <= 0f || bandHeightPx <= 0f) {
            return null
        }
        val height = min(bandHeightPx, layoutHeightPx)
        val top = ((layoutHeightPx - height) / 2f).coerceAtLeast(0f)
        val width = min(stripWidthPx, layoutWidthPx)
        if (width <= 0f || height <= 0f) return null
        return Rect(left = 0f, top = top, right = width, bottom = top + height)
    }
}
