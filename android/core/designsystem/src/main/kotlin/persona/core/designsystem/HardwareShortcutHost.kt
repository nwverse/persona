package persona.core.designsystem

import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type

internal val LocalHardwareShortcutController = staticCompositionLocalOf<HardwareShortcutController?> { null }

private fun HardwareShortcutController.handle(event: KeyEvent): Boolean {
    val shortcut = when {
        event.key == Key.Escape -> HardwareShortcut.Escape
        event.key == Key.Slash && event.isCtrlPressed -> HardwareShortcut.Help
        else -> return false
    }
    return dispatch(shortcut, if (event.type == KeyEventType.KeyDown) HardwareKeyPhase.Down else HardwareKeyPhase.Up,
        repeated = event.nativeKeyEvent.repeatCount > 0)
}

/**
 * Large windows own their keyboard help and transient stack. The key listener runs after focused children, so a
 * terminal, an IME, the expanded composer and pointer menus keep the keys they consume. Compact windows add no node.
 */
@Composable
fun HardwareShortcutHost(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    if (!LocalLargeWindowInput.current) {
        content()
        return
    }
    val controller = remember { HardwareShortcutController() }
    val focus = remember { FocusRequester() }
    CompositionLocalProvider(LocalHardwareShortcutController provides controller) {
        LaunchedEffect(controller) { runCatching { focus.requestFocus() } }
        Box(modifier.onKeyEvent(controller::handle).focusRequester(focus).focusable()) {
            content()
            if (controller.helpVisible) KeyboardShortcutsSheet(onDismiss = controller::closeHelp)
        }
    }
}

/** Register one dismissible surface and handle only keys its focused children did not consume. */
fun Modifier.hardwareShortcutDismiss(onDismiss: () -> Unit): Modifier = composed {
    val controller = LocalHardwareShortcutController.current ?: return@composed this
    val currentDismiss by rememberUpdatedState(onDismiss)
    val token = remember { Any() }
    val focus = remember { FocusRequester() }
    DisposableEffect(controller, token) {
        val registration = controller.register(token) { currentDismiss() }
        onDispose { controller.unregister(registration) }
    }
    LaunchedEffect(controller, token) { runCatching { focus.requestFocus() } }
    onKeyEvent(controller::handle).focusRequester(focus).focusable()
}
