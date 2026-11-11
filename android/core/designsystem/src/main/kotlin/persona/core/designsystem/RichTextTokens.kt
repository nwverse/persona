package persona.core.designsystem

import androidx.compose.ui.unit.dp

/**
 * Geometry of rich message text (code blocks, list markers, quotes) that the shared scale has no step for. A code
 * block's string and number colours are palette roles ([PersonaPalette.codeString], [PersonaPalette.codeNumber]).
 */
object RichTextTokens {
    /** The outline around a code block. */
    val codeBorder = 1.dp
    /** The copy icon in a code block's header. */
    val codeCopyIcon = 12.dp
    /** The narrowest a list's number or bullet column gets, so items line up. */
    val listMarkerMinWidth = 20.dp
    /** The bar down the leading edge of a quote. */
    val quoteBar = 3.dp
}
