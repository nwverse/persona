package persona.core.designsystem

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/** Application chrome shared with the compact iPhone shell, independent of platform system bars. */
internal object HomeTokens {
    const val drawerFraction = 0.82f
    val drawerMaximum = 380.dp
    val drawerPageRadius = 55.dp
    val drawerShadow = 24.dp
    const val drawerShadowAlpha = 0.18f
    val drawerLight = Color(0xFFF9F9F9)
    val drawerDark = Color(0xFF171717)
    val drawerRowHeight = 52.dp
    val settingsGlyph = 54.dp
    val repositoryLight = Color(0xFFF5F5F5)
    val repositoryDark = Color(0xFF2A2A2A)
    const val filterSelectedAlpha = 0.15f
    const val filterStrokeAlpha = 0.35f
    val composerRadius = 32.dp
    val composerShadow = 12.dp
    val composerStroke = 0.5.dp
    const val composerShadowAlpha = 0.08f
    val composerLight = Color.White
    val composerDark = Color(0xFF1F1F21)
    val composerStrokeLight = Color(0xFFE3E3E3)
    val composerStrokeDark = Color(0xFF3A3A3C)
    val homeFaceSize = 46.dp
    val headerHeight = 70.dp
    val segmentWidth = 72.dp
    val segmentHeight = 36.dp
    val segmentLight = Color(0xFFE6E6E6)
    val segmentDark = Color(0xFF3A3A3C)
    const val shellTextStep = 19f / 17f
    const val shellScaleMaximum = 1.6f
}
