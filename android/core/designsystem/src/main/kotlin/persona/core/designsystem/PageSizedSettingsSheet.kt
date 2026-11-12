package persona.core.designsystem

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties

/** A centered page above the shell, with the same opaque surface and dismiss behavior as a settings sheet. */
@Composable
fun JPageSizedSettingsSheet(onDismiss: () -> Unit, content: @Composable () -> Unit) {
    val palette = PersonaTheme.palette
    val container = if (PersonaTheme.style == AppStyle.Brief) palette.background else palette.surfaceOpaque
    val close = stringResource(R.string.designsystem_close)
    val duration = if (PersonaTheme.reduceMotion) Tokens.Motion.reduced else PersonaTheme.motion.standard
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        HardwareShortcutHost(Modifier.fillMaxSize()) {
            Box(Modifier.fillMaxSize()) {
                Box(Modifier.fillMaxSize().background(palette.scrim)
                    .clickable(role = Role.Button, onClick = onDismiss).semantics { contentDescription = close })
                BoxWithConstraints(Modifier.fillMaxSize().safeDrawingPadding(), contentAlignment = Alignment.Center) {
                    val bounds = settingsPageBounds(maxWidth, maxHeight)
                    val presented = remember { MutableTransitionState(false).apply { targetState = true } }
                    AnimatedVisibility(visibleState = presented, enter = fadeIn(tween(duration)), exit = fadeOut(tween(duration))) {
                        Box(
                            (if (bounds == null) Modifier.fillMaxSize() else Modifier.size(bounds.width, bounds.height))
                                .testTag("settings-page-card")
                                .clip(PersonaTheme.shapes.panelShape)
                                .background(container)
                                .hardwareShortcutDismiss(onDismiss),
                        ) {
                            CompositionLocalProvider(LocalReadingInset provides Tokens.Space.flush, LocalGlassSource provides null) {
                                content()
                            }
                        }
                    }
                }
            }
        }
    }
}
