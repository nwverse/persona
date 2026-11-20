package persona.core.designsystem

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.disabled
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import persona.core.designsystem.icons.PersonaIcons

/** The exit callback of this full-screen root; pushes inside it keep their own Back action. */
internal val LocalPresentedClose = staticCompositionLocalOf<(() -> Unit)?> { null }

/** The top-start exit of a presented page, in the primary ink of its own surface. */
@Composable
fun JSheetClose(onClose: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, raised: Boolean = LocalIconsRaised.current) {
    JIconButton(PersonaIcons.close, stringResource(R.string.designsystem_close), onClose, modifier,
        enabled = enabled, raised = raised, tint = PersonaTheme.palette.ink)
}

/** Save, Done or Add on an edit sheet. TalkBack names the operation the tick performs. */
@Composable
fun JSheetConfirm(label: String, onConfirm: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, loading: Boolean = false) {
    if (loading) {
        Box(modifier.size(Tokens.MinTouch.android).semantics {
            contentDescription = label
            role = Role.Button
            disabled()
        }, contentAlignment = Alignment.Center) {
            JSpinner(Modifier.size(Tokens.Size.iconXS))
        }
    } else {
        JIconButton(PersonaIcons.done, label, onConfirm, modifier, enabled = enabled)
    }
}

@Composable
internal fun SheetPageBar(title: String?, onClose: () -> Unit, closeEnabled: Boolean, actions: @Composable RowScope.() -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JSheetClose(onClose, enabled = closeEnabled)
        if (title.isNullOrBlank()) Spacer(Modifier.weight(1f))
        else Text(title, Modifier.weight(1f).semantics { heading() }, style = PersonaTheme.type.title,
            color = PersonaTheme.palette.ink, maxLines = 3, overflow = TextOverflow.Ellipsis)
        actions()
    }
}
