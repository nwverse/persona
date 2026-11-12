package persona.feature.chat

import android.view.DragEvent
import androidx.compose.foundation.draganddrop.dragAndDropTarget
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.RowScope
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import androidx.compose.ui.draganddrop.toAndroidDragEvent
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlin.coroutines.coroutineContext
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatComposerService
import persona.core.designsystem.ComposerDropTargetFrame
import persona.core.designsystem.ComposerHardwareActions
import persona.core.designsystem.LocalComposerHardware
import persona.core.designsystem.LocalLargeWindowInput

/**
 * The production chat composer with large-window input integrations. Compact and wide-phone windows call the original
 * composable directly, with no wrapper layout, key handling or drop target. Sending remains the caller's [onSend].
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

    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var dropHover by remember { mutableStateOf(false) }
    var dropImporting by remember { mutableStateOf(false) }
    val canDrop = visible && enabled && service != null && !sending && blockedLabel == null && !dropImporting
    val currentCanDrop by rememberUpdatedState(canDrop)
    val dropFailure = stringResource(R.string.chat_composer_drop_failed)
    val currentDrop by rememberUpdatedState<(DragEvent) -> Boolean> { event ->
        val uris = composerDropUris(event.clipData)
        if (uris.isEmpty()) {
            false
        } else {
            val permissions = context.requestComposerDropPermissions(event)
            dropImporting = true
            scope.launch {
                try {
                    for (uri in uris) {
                        val mediaCount = state.attachments.count { it.media }
                        val result = withContext(NonCancellable + Dispatchers.IO) {
                            importDroppedAttachment(context, uri, mediaCount)
                        }
                        result.attachment?.let { attachment ->
                            state.beginEdit()
                            state.attachments += attachment
                        }
                        result.notice?.let(onNotice)
                        // A cancelled wrapper still owns the completed copy through the draft; stop before the next URI.
                        coroutineContext.ensureActive()
                    }
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (_: Exception) {
                    onNotice(dropFailure)
                } finally {
                    permissions?.release()
                    dropImporting = false
                    dropHover = false
                }
            }
            true
        }
    }
    val dropTarget = remember {
        ComposerDropTarget(
            accepts = { currentCanDrop },
            hovering = { dropHover = it },
            drop = { currentDrop(it) },
        )
    }
    LaunchedEffect(canDrop) { if (!canDrop) dropHover = false }

    // Only copies owned by this draft are deleted, after removal or a send that clears it. Other sessions' drops and
    // edited-photo sources stay; no directory sweep can remove an attachment still in use elsewhere.
    LaunchedEffect(state) {
        var before = state.attachments to state.photoEdits.values.map { it.sourceUri }
        snapshotFlow {
            state.attachments to state.photoEdits.filterKeys { id -> state.attachments.any { it.id == id } }.values.map { it.sourceUri }
        }.collect { now ->
            val gone = leftDrops(before.first, now.first, context.packageName, before.second, now.second)
            before = now
            if (gone.isNotEmpty()) withContext(Dispatchers.IO) { gone.forEach { deleteDrop(context, it) } }
        }
    }

    ComposerDropTargetFrame(
        active = dropHover || dropImporting,
        importing = dropImporting,
        label = stringResource(R.string.chat_composer_drop_target),
        importingLabel = stringResource(R.string.chat_composer_drop_importing),
        modifier = Modifier.onPreviewKeyEvent { event ->
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
        }.dragAndDropTarget(
            shouldStartDragAndDrop = { event ->
                currentCanDrop && composerDropAccepts(event.toAndroidDragEvent().clipDescription)
            },
            target = dropTarget,
        ),
    ) {
        CompositionLocalProvider(LocalComposerHardware provides hardware) {
            composer()
        }
    }
}
