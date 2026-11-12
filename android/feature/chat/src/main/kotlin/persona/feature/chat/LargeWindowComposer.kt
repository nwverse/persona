package persona.feature.chat

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.RowScope
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.res.stringResource
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatComposerService
import persona.core.designsystem.ComposerHardwareActions
import persona.core.designsystem.LocalComposerHardware
import persona.core.designsystem.LocalLargeWindowInput

/**
 * The production chat composer with large-window input integrations. Compact and wide-phone windows call the original
 * composable directly, with no wrapper layout or key handling. Sending remains the caller's [onSend] in every window.
 */
@Composable
fun LargeWindowComposer(
    session: SessionKey,
    state: ChatComposerState,
    working: Boolean,
    sending: Boolean,
    canClear: Boolean,
    service: ChatComposerService?,
    onSend: () -> Unit,
    onCommand: (String) -> Unit,
    onNotice: (String) -> Unit,
    extraContent: Boolean = false,
    enabled: Boolean = true,
    placeholder: String = stringResource(R.string.chat_composer_placeholder),
    leading: (@Composable RowScope.() -> Unit)? = null,
    accessory: (@Composable ColumnScope.() -> Unit)? = null,
    modeHint: String? = null,
    onOverlay: (Boolean) -> Unit = {},
    visible: Boolean = true,
    agentName: String = "Claude",
    canInterrupt: Boolean = true,
    mentions: ComposerMentions? = null,
    onMention: (ChatLinkContext) -> Unit = {},
    onMentionTask: (String) -> Unit = {},
    onMentionOpen: (Boolean) -> Unit = {},
    onModel: (() -> Unit)? = null,
    loop: Boolean = false,
    blockedLabel: String? = null,
    onBlocked: () -> Unit = {},
) {
    val composer: @Composable () -> Unit = {
        ChatComposer(
            session = session, state = state, working = working, sending = sending, canClear = canClear,
            service = service, onSend = onSend, onCommand = onCommand, onNotice = onNotice,
            extraContent = extraContent, enabled = enabled, placeholder = placeholder, leading = leading,
            accessory = accessory, modeHint = modeHint, onOverlay = onOverlay, visible = visible,
            agentName = agentName, canInterrupt = canInterrupt, mentions = mentions, onMention = onMention,
            onMentionTask = onMentionTask, onMentionOpen = onMentionOpen, onModel = onModel, loop = loop,
            blockedLabel = blockedLabel, onBlocked = onBlocked,
        )
    }
    if (!LocalLargeWindowInput.current) {
        composer()
        return
    }

    val currentOnSend by rememberUpdatedState(onSend)
    val sendAction = remember { { currentOnSend() } }
    val hasContent = state.hasContent || extraContent
    val canSend = enabled && state.ready && !sending
    val sendEnabled = canSend && hasContent && blockedLabel == null
    val hardware = remember(sendEnabled) { ComposerHardwareActions(send = if (sendEnabled) sendAction else null) }
    Box(Modifier.onPreviewKeyEvent { event ->
        val action = composerKeyAction(ComposerKeyInput(
            key = when (event.key) {
                Key.Enter, Key.NumPadEnter -> ComposerKey.Enter
                Key.Escape -> ComposerKey.Escape
                else -> ComposerKey.Other
            },
            press = if (event.type == KeyEventType.KeyDown) ComposerKeyPress.Down else ComposerKeyPress.Up,
            repeatCount = event.nativeKeyEvent.repeatCount,
            ctrlPressed = event.isCtrlPressed,
            metaPressed = event.isMetaPressed,
            shiftPressed = event.isShiftPressed,
            composing = state.value.composition != null,
            canSend = canSend,
            hasContent = hasContent,
            blocked = blockedLabel != null,
        ))
        when (action) {
            ComposerKeyAction.Send -> { sendAction(); true }
            ComposerKeyAction.Consume -> true
            ComposerKeyAction.Collapse,
            ComposerKeyAction.PassThrough -> false
        }
    }) {
        CompositionLocalProvider(LocalComposerHardware provides hardware) {
            composer()
        }
    }
}
