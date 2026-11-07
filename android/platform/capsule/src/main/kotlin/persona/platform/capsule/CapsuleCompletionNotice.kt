package persona.platform.capsule

import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import persona.core.designsystem.CapsuleTokens
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaFace
import persona.core.designsystem.PersonaState
import persona.core.designsystem.PersonaTheme

/** A single-line non-focusable presentation, separate from the manual card and its chat subscription. */
@Composable
fun CapsuleCompletionNotice(completion: CapsuleCompletion, onOpen: (CapsuleCompletion) -> Unit) {
    val palette = PersonaTheme.palette
    val feedback = LocalAppFeedback.current
    val shape = RoundedCornerShape(CapsuleTokens.compactRadius)
    val description = stringResource(if (completion.kind == CapsuleNoticeKind.Completed) R.string.capsule_done_description else R.string.capsule_message_description, completion.title)
    Row(Modifier.size(CapsuleTokens.completionWidth, CapsuleTokens.orb).clip(shape).background(palette.surfaceOpaque)
        .border(CapsuleTokens.border, palette.line, shape)
        .clickable {
            try { onOpen(completion) }
            catch (error: Exception) { feedback?.failure(error) }
        }
        .testTag("capsule.completion")
        .semantics { contentDescription = description }
        .padding(horizontal = CapsuleTokens.gap, vertical = CapsuleTokens.smallGap),
        horizontalArrangement = Arrangement.spacedBy(CapsuleTokens.gap), verticalAlignment = Alignment.CenterVertically) {
        PersonaFace(PersonaState.Idle, size = CapsuleTokens.face)
        Text(completion.title, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis,
            style = PersonaTheme.type.label, color = palette.ink)
        Text(stringResource(if (completion.kind == CapsuleNoticeKind.Completed) R.string.capsule_done_suffix else R.string.capsule_message_suffix), maxLines = 1, style = PersonaTheme.type.caption,
            color = if (completion.kind == CapsuleNoticeKind.Completed) palette.done.text else palette.needsYou.text)
    }
}
