package persona.core.designsystem

import androidx.compose.foundation.layout.RowScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.AlignmentLine
import androidx.compose.ui.layout.FirstBaseline

/**
 * [modifier] aligned in its Row by the first baseline of its text, as `alignBy(FirstBaseline)`, but a node whose
 * baseline cannot be read at that moment is placed by the Row's own alignment instead of ending the process.
 *
 * The app's pages are laid out under a lookahead pass, and while a window changes size a child can be asked for its
 * baseline when its lookahead state is not there yet: Compose then throws a NullPointerException from inside the
 * measure pass (`LookaheadDelegate.getAlignmentLinesOwner`), which took the process down with a plan card open in an
 * Agents chat on a tablet, and does the same on the current Compose release. The next layout pass reads the baseline
 * as usual, so only that one frame places the child at the Row's default instead of on the baseline.
 */
fun RowScope.firstBaselineAligned(modifier: Modifier = Modifier): Modifier =
    modifier.alignBy { measured -> readBaseline { measured[FirstBaseline] } }

/** The baseline [read] returns, or [AlignmentLine.Unspecified] (the Row's own alignment) when the framework cannot give it yet. */
internal fun readBaseline(read: () -> Int): Int = try {
    read()
} catch (_: NullPointerException) {
    AlignmentLine.Unspecified
}
