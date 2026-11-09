package persona.core.designsystem

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

internal val LocalFlatPageSurface = staticCompositionLocalOf { false }

/** A settings navigation stack presented above its originating home, with iPhone-style grouped surfaces. */
@Composable
fun JSettingsSheet(onDismiss: () -> Unit, content: @Composable () -> Unit) {
    val base = PersonaTheme.palette
    val grouped = if (PersonaTheme.style != AppStyle.Glass) base else base.copy(
        background = if (base.dark) base.background else base.surfaceOpaque,
        surface = if (base.dark) base.surfaceOpaque else base.background,
        surfaceOpaque = if (base.dark) base.surfaceOpaque else base.background,
    )
    JSheet(onDismiss, Modifier.fillMaxSize(), contentPadding = 0.dp, bottomPadding = 0.dp, showDragHandle = false) {
        CompositionLocalProvider(LocalPalette provides grouped, LocalFlatPageSurface provides true,
            LocalHomeChrome provides null, LocalRootBack provides null, LocalRootLeading provides null) {
            Box(Modifier.fillMaxSize()) { content() }
        }
    }
}
