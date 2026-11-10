package persona.core.designsystem

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcons

/** The composer trays are bounded independently of the transcript and the keyboard. */
object AssistantTrayTokens {
    val maximumHeight = 330.dp
    const val maximumHeightFraction = 0.45f
    val markSize = 8.dp
    val entryMinimumHeight = 44.dp
    const val expandedRotation = 180f
}

enum class AssistantTrayTone { Pending, Progress, Agents }

@Composable
fun assistantTrayColor(tone: AssistantTrayTone): Color = when (tone) {
    AssistantTrayTone.Pending -> PersonaTheme.palette.caution
    AssistantTrayTone.Progress -> PersonaTheme.palette.sourceFeishu.dot
    AssistantTrayTone.Agents -> PersonaTheme.palette.positive
}

/** One finite-width control: an empty Pending is quiet but can still show its empty state. */
@Composable
fun AssistantTrayEntry(label: String, count: Int, tone: AssistantTrayTone, expanded: Boolean,
    description: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    val palette = PersonaTheme.palette
    val active = tone != AssistantTrayTone.Pending || count > 0
    val angle by animateFloatAsState(if (expanded) AssistantTrayTokens.expandedRotation else 0f,
        tween(if (PersonaTheme.reduceMotion) 0 else PersonaTheme.motion.quick), label = "tray-arrow")
    val shape = androidx.compose.foundation.shape.RoundedCornerShape(PersonaTheme.shapes.control)
    Row(modifier.heightIn(min = AssistantTrayTokens.entryMinimumHeight).clip(shape)
        .background(if (expanded) palette.surfaceOpaque else palette.surfaceStrong)
        .clickable(enabled = enabled, onClick = onClick).semantics { stateDescription = description }
        .padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.s),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(AssistantTrayTokens.markSize).clip(CircleShape)
            .background(if (active) assistantTrayColor(tone) else palette.ink3))
        Text(label, Modifier.weight(1f), style = PersonaTheme.type.labelSmall,
            color = if (active) palette.ink else palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (count > 0) Text(count.toString(), style = PersonaTheme.type.micro, maxLines = 1,
            color = if (tone == AssistantTrayTone.Pending) palette.cautionText else palette.ink3)
        JIcon(PhIcons.CaretUp, null, size = Tokens.Space.m, tint = palette.ink3, modifier = Modifier.rotate(angle))
    }
}

/** Row status marks share the same deliberately named palette as the tray controls. */
@Composable
fun AssistantTrayMark(tone: AssistantTrayTone?, modifier: Modifier = Modifier) {
    Box(modifier.size(AssistantTrayTokens.markSize).clip(CircleShape)
        .background(tone?.let { assistantTrayColor(it) } ?: PersonaTheme.palette.ink3))
}
