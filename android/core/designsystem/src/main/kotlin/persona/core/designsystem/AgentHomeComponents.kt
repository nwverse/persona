package persona.core.designsystem

import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight

@Composable
fun JAgentHomeSurface(repository: Boolean, content: @Composable () -> Unit) {
    val palette = PersonaTheme.palette
    val home = LocalHomeChrome.current != null && PersonaTheme.style == AppStyle.Glass && repository
    CompositionLocalProvider(LocalPalette provides if (home) palette.copy(
        background = if (palette.dark) HomeTokens.repositoryDark else HomeTokens.repositoryLight,
    ) else palette) { content() }
}

/** The iPhone's filters wrap into another line instead of adding a horizontal navigation strip. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun HomeAgentFilters(bays: List<Bay>, selected: Int, onSelect: (Int) -> Unit, modifier: Modifier = Modifier) {
    if (LocalHomeChrome.current == null || PersonaTheme.style != AppStyle.Glass) {
        QuickFilterBar(bays, selected, onSelect, modifier)
        return
    }
    val palette = PersonaTheme.palette
    FlowRow(modifier.padding(horizontal = Tokens.Space.l),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        bays.forEachIndexed { index, bay ->
            val chosen = index == selected
            Row(Modifier.clip(CircleShape)
                .background(if (chosen) palette.needsYou.dot.copy(alpha = HomeTokens.filterSelectedAlpha) else palette.sunken)
                .border(Tokens.Space.xxs / 2, if (chosen) palette.needsYou.dot.copy(alpha = HomeTokens.filterStrokeAlpha) else Color.Transparent, CircleShape)
                .clickable(role = Role.Tab) { onSelect(index) }.semantics { this.selected = chosen }
                .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                bay.status?.let { JStatusDot(it) }
                Text(bay.label, style = PersonaTheme.type.callout.copy(fontWeight = if (chosen) FontWeight.SemiBold else FontWeight.Medium),
                    color = if (chosen) palette.accent else palette.ink, maxLines = 1, softWrap = false)
                if (index != 0) bay.count?.let { Text("$it", style = PersonaTheme.type.callout, color = palette.ink2, maxLines = 1) }
            }
        }
    }
}
