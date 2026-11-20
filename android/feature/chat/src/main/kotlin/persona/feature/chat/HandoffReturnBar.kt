package persona.feature.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.minimumInteractiveComponentSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import persona.core.designsystem.JIcon
import persona.core.designsystem.JIconButton
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons
import persona.core.navigation.RouteSource

/** The return hint belongs to this navigation entry. Hiding it leaves system Back available. */
@Composable
internal fun HandoffReturnBar(from: RouteSource.Handoff, onBack: () -> Unit) {
    var dismissed by rememberSaveable(from) { mutableStateOf(false) }
    if (dismissed) return
    val palette = PersonaTheme.palette
    Row(Modifier.fillMaxWidth().background(palette.surfaceOpaque).padding(horizontal = Tokens.Space.s),
        verticalAlignment = Alignment.CenterVertically) {
        Row(Modifier.weight(1f).clickable(role = Role.Button, onClick = onBack).minimumInteractiveComponentSize()
            .padding(horizontal = Tokens.Space.s, vertical = Tokens.Space.xs),
            horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
            JIcon(PersonaIcons.back, decorative = true, size = Tokens.Size.markS, tint = palette.accent)
            Text(stringResource(R.string.chat_handoff_from, from.title.ifBlank { stringResource(R.string.chat_screen_untitled) }),
                style = PersonaTheme.type.labelSmall, color = palette.accent)
        }
        JIconButton(PersonaIcons.close, stringResource(R.string.chat_handoff_hide_return), { dismissed = true },
            modifier = Modifier.minimumInteractiveComponentSize(), raised = false, tint = palette.ink2)
    }
}
