package persona.core.designsystem

import androidx.compose.runtime.staticCompositionLocalOf

/**
 * Hardware actions available to an [ExpandedComposer] inside a large-window chat. Null outside that integration keeps
 * phone, preview and standalone-editor behavior unchanged. [send] is null whenever the ordinary Send button is disabled.
 */
data class ComposerHardwareActions(
    val send: (() -> Unit)? = null,
)

val LocalComposerHardware = staticCompositionLocalOf<ComposerHardwareActions?> { null }
