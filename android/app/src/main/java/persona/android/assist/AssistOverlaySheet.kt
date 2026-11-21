package persona.android.assist

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.speech.RecognitionListener
import android.speech.RecognizerIntent
import android.speech.SpeechRecognizer
import android.widget.Toast
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import persona.android.PersonaApp
import persona.android.R
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.AppStyle
import persona.core.designsystem.AttachmentState
import persona.core.designsystem.AttachmentThumb
import persona.core.designsystem.JChip
import persona.core.designsystem.JIcon
import persona.core.designsystem.JIconButton
import persona.core.designsystem.JNotice
import persona.core.designsystem.JSpinner
import persona.core.designsystem.JTextButton
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.LocalAppStyleStore
import persona.core.designsystem.LocalPersonaDesign
import persona.core.designsystem.LocalPersonaName
import persona.core.designsystem.LocalPersonaPack
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons
import persona.feature.chat.StreamingReplyMarkdown

/** Wraps the Digital Assistant overlay in Persona's active theme, appearance, feedback, and safe link handler. */
@Composable
internal fun AssistOverlayEnvironment(
    onOpenExternalUri: (String) -> Unit,
    content: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as? PersonaApp
    val currentStyle = app?.appearance?.style?.collectAsState()?.value ?: AppStyle.Default
    val face = app?.appearance?.face?.collectAsState()?.value
    val pack = app?.personas?.current?.collectAsState()?.value
    val openExternal by rememberUpdatedState(onOpenExternalUri)
    val failedMessage = stringResource(R.string.app_error_failed)
    val feedback = remember(context, failedMessage) {
        object : AppFeedback {
            override fun failure(error: Throwable, operation: Boolean): Boolean =
                notice(error.message ?: failedMessage)

            override fun notice(message: String): Boolean {
                Toast.makeText(context.applicationContext, message, Toast.LENGTH_SHORT).show()
                return true
            }
        }
    }
    val uriHandler = remember {
        object : UriHandler {
            override fun openUri(uri: String) {
                val parsed = runCatching { Uri.parse(uri) }.getOrNull() ?: return
                val scheme = parsed.scheme?.lowercase()
                if ((scheme == "https" || scheme == "http") && !parsed.host.isNullOrBlank()) {
                    openExternal(uri)
                }
            }
        }
    }
    PersonaTheme(style = currentStyle) {
        val providers = buildList<androidx.compose.runtime.ProvidedValue<*>> {
            if (app != null) add(LocalAppStyleStore provides app.appearance)
            if (face != null) add(LocalPersonaDesign provides face)
            add(LocalPersonaPack provides pack)
            add(LocalPersonaName provides (pack?.manifest?.name ?: "Persona"))
            add(LocalAppFeedback provides feedback)
            add(LocalUriHandler provides uriHandler)
        }
        CompositionLocalProvider(*providers.toTypedArray(), content = content)
    }
}

/**
 * Gemini / Muse style floating bottom sheet shown over the current app when Persona is invoked
 * as Android's Default Digital Assistant (`ROLE_ASSISTANT`) or via `ACTION_ASSIST`.
 */
@Composable
internal fun AssistOverlaySheet(
    ui: AssistOverlayUiState,
    onDraftChange: (String) -> Unit,
    onDictationHeard: (String) -> Unit,
    onNotice: (String) -> Unit,
    onAttachScreen: () -> Unit,
    onRemoveScreen: () -> Unit,
    onSend: () -> Unit,
    onOpenInPersona: () -> Unit,
    onDismiss: () -> Unit,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val shapes = PersonaTheme.shapes
    val dismissThresholdPx = with(LocalDensity.current) { Tokens.MinTouch.android.toPx() }
    var dragOffsetPx by remember { mutableFloatStateOf(0f) }
    val scrimInteraction = remember { MutableInteractionSource() }
    val cardInteraction = remember { MutableInteractionSource() }
    val closeDescription = stringResource(R.string.app_assist_close)
    val dictation = rememberOverlayDictation(onHeard = onDictationHeard, onError = onNotice)

    BoxWithConstraints(
        Modifier
            .fillMaxSize()
            .testTag("assist-overlay-root"),
    ) {
        val sheetMaxHeight = maxHeight * 0.78f

        // Dimmed backdrop over the underlying app; tapping outside the card dismisses cleanly.
        Box(
            Modifier
                .fillMaxSize()
                .background(palette.scrim)
                .clickable(
                    interactionSource = scrimInteraction,
                    indication = null,
                    role = Role.Button,
                    onClickLabel = closeDescription,
                    onClick = {
                        dictation?.stop()
                        onDismiss()
                    },
                )
                .semantics { contentDescription = closeDescription }
                .testTag("assist-overlay-scrim"),
        )

        Box(
            Modifier
                .align(Alignment.BottomCenter)
                .imePadding(),
        ) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .widthIn(max = Tokens.Size.readable)
                    .heightIn(max = sheetMaxHeight)
                    .graphicsLayer { translationY = dragOffsetPx }
                    .clip(shapes.sheetShape)
                    .background(palette.surfaceOpaque)
                    .border(Tokens.Size.line, palette.line, shapes.sheetShape)
                    .clickable(interactionSource = cardInteraction, indication = null) {}
                    .navigationBarsPadding()
                    .animateContentSize(animationSpec = PersonaTheme.motion.size())
                    .padding(horizontal = Tokens.Space.l, vertical = Tokens.Space.s)
                    .testTag("assist-overlay-card"),
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.s),
            ) {
                // Drag handle + header (swipe down to dismiss).
                Column(
                    Modifier
                        .fillMaxWidth()
                        .pointerInput(dismissThresholdPx) {
                            detectVerticalDragGestures(
                                onDragEnd = {
                                    if (dragOffsetPx >= dismissThresholdPx) {
                                        dragOffsetPx = 0f
                                        dictation?.stop()
                                        onDismiss()
                                    } else {
                                        dragOffsetPx = 0f
                                    }
                                },
                                onDragCancel = { dragOffsetPx = 0f },
                                onVerticalDrag = { _, dragAmount ->
                                    dragOffsetPx = (dragOffsetPx + dragAmount).coerceAtLeast(0f)
                                },
                            )
                        },
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Box(
                        Modifier
                            .padding(top = Tokens.Space.xs, bottom = Tokens.Space.xs)
                            .size(width = Tokens.Size.slimBar, height = Tokens.Size.bar)
                            .clip(CircleShape)
                            .background(palette.line),
                    )
                    Row(
                        Modifier.fillMaxWidth(),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        JIcon(
                            icon = PersonaIcons.persona,
                            contentDescription = null,
                            size = Tokens.Size.iconXS,
                            tint = palette.accent,
                        )
                        Spacer(Modifier.width(Tokens.Space.s))
                        Column(Modifier.weight(1f)) {
                            Text(
                                text = LocalPersonaName.current,
                                style = type.label.copy(fontWeight = FontWeight.SemiBold),
                                color = palette.ink,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            if (ui.hubStatus == AssistHubStatus.Connecting) {
                                Text(
                                    text = stringResource(R.string.app_assist_connecting),
                                    style = type.micro,
                                    color = palette.ink3,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                        JTextButton(
                            text = stringResource(R.string.app_assist_open_in_persona),
                            onClick = {
                                dictation?.stop()
                                onOpenInPersona()
                            },
                            icon = PersonaIcons.openExternal,
                            modifier = Modifier.testTag("assist-open-in-persona"),
                        )
                        JIconButton(
                            icon = PersonaIcons.close,
                            contentDescription = closeDescription,
                            onClick = {
                                dictation?.stop()
                                onDismiss()
                            },
                            modifier = Modifier.testTag("assist-close-button"),
                        )
                    }
                }

                // Hub unpaired / offline banner with direct handoff to Persona App.
                when (ui.hubStatus) {
                    AssistHubStatus.Unpaired -> JNotice(
                        text = stringResource(R.string.app_assist_hub_unpaired_body),
                        title = stringResource(R.string.app_assist_hub_unpaired_title),
                        status = Status.Offline,
                        icon = PersonaIcons.offline,
                        actionLabel = stringResource(R.string.app_open_persona),
                        onAction = onOpenInPersona,
                        modifier = Modifier.testTag("assist-hub-unpaired"),
                    )

                    AssistHubStatus.Offline -> JNotice(
                        text = stringResource(R.string.app_assist_hub_offline_body),
                        title = stringResource(R.string.app_assist_hub_offline_title),
                        status = Status.Offline,
                        icon = PersonaIcons.offline,
                        actionLabel = stringResource(R.string.app_open_persona),
                        onAction = onOpenInPersona,
                        modifier = Modifier.testTag("assist-hub-offline"),
                    )

                    else -> Unit
                }

                ui.errorMessage?.let { error ->
                    JNotice(
                        text = error,
                        status = Status.NeedsYou,
                        icon = PersonaIcons.error,
                        modifier = Modifier.testTag("assist-error-notice"),
                    )
                }

                // Expanded live streaming reply viewport (appears after user sends a question).
                if (ui.expanded) {
                    AssistStreamingTranscript(
                        turns = ui.turns,
                        awaitingReply = ui.awaitingReply,
                        modifier = Modifier
                            .weight(1f, fill = false)
                            .fillMaxWidth()
                            .testTag("assist-transcript"),
                    )
                }

                // Manual screen attachment bar above the input composer (NEVER attached automatically).
                AssistScreenAttachmentRow(
                    ui = ui,
                    onAttachScreen = onAttachScreen,
                    onRemoveScreen = onRemoveScreen,
                )

                // Input row with optional safe dictation and Send action.
                AssistInputBar(
                    draftText = ui.draftText,
                    canSend = ui.canSend,
                    sending = ui.sending,
                    inputEnabled = !ui.sending && ui.hubStatus != AssistHubStatus.Unpaired,
                    dictating = dictation?.listening == true,
                    onDraftChange = onDraftChange,
                    onToggleDictation = dictation?.let { control -> { control.toggle() } },
                    onSend = {
                        dictation?.stop()
                        onSend()
                    },
                )
            }
        }
    }
}

@Composable
private fun AssistStreamingTranscript(
    turns: List<AssistOverlayTurn>,
    awaitingReply: Boolean,
    modifier: Modifier = Modifier,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val listState = rememberLazyListState()
    var followBottom by remember { mutableStateOf(true) }

    LaunchedEffect(listState) {
        snapshotFlow {
            val info = listState.layoutInfo
            val lastVisible = info.visibleItemsInfo.lastOrNull()?.index ?: -1
            val total = info.totalItemsCount
            total == 0 || lastVisible >= total - 2
        }.collect { nearBottom ->
            followBottom = nearBottom
        }
    }

    val lastTurnTextLen = turns.lastOrNull()?.text?.length ?: 0
    LaunchedEffect(turns.size, lastTurnTextLen, awaitingReply) {
        if (followBottom && (turns.isNotEmpty() || awaitingReply)) {
            val target = (turns.size + if (awaitingReply) 1 else 0) - 1
            if (target >= 0) {
                listState.scrollToItem(target)
            }
        }
    }

    LazyColumn(
        state = listState,
        modifier = modifier,
        contentPadding = PaddingValues(vertical = Tokens.Space.xs),
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.m),
    ) {
        items(turns, key = { it.id }) { turn ->
            if (turn.fromUser) {
                Column(
                    Modifier.fillMaxWidth(),
                    horizontalAlignment = Alignment.End,
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                ) {
                    Column(
                        Modifier
                            .clip(PersonaTheme.shapes.cardShape)
                            .background(palette.userBubble)
                            .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
                        verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                    ) {
                        if (turn.hasScreenAttachment) {
                            Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                            ) {
                                JIcon(
                                    icon = PersonaIcons.screen,
                                    contentDescription = null,
                                    size = Tokens.Size.markS,
                                    tint = palette.userBubbleText ?: palette.ink2,
                                )
                                Text(
                                    text = stringResource(R.string.app_assist_screen_attached),
                                    style = type.micro,
                                    color = palette.userBubbleText ?: palette.ink2,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                        if (turn.text.isNotBlank()) {
                            Text(
                                text = turn.text,
                                style = type.body,
                                color = palette.userBubbleText ?: palette.ink,
                            )
                        }
                    }
                    val deliveryLabel = when (turn.delivery) {
                        AssistTurnDelivery.Sending -> stringResource(R.string.app_assist_delivery_sending)
                        AssistTurnDelivery.Queued -> stringResource(R.string.app_assist_delivery_queued)
                        AssistTurnDelivery.Failed -> stringResource(R.string.app_assist_delivery_failed)
                        AssistTurnDelivery.Sent -> null
                    }
                    if (deliveryLabel != null) {
                        Text(
                            text = deliveryLabel,
                            style = type.micro,
                            color = if (turn.delivery == AssistTurnDelivery.Failed) palette.danger.text else palette.ink3,
                            maxLines = 1,
                        )
                    }
                }
            } else {
                Column(
                    Modifier
                        .fillMaxWidth()
                        .padding(vertical = Tokens.Space.xxs),
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                ) {
                    StreamingReplyMarkdown(
                        text = turn.text,
                        id = turn.id,
                        streaming = turn.streaming,
                        bodyStyle = type.body,
                        color = palette.ink,
                        selectable = false,
                    )
                }
            }
        }
        if (awaitingReply) {
            item(key = "assist-awaiting-reply") {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .padding(vertical = Tokens.Space.xs),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
                ) {
                    JSpinner(Modifier.size(Tokens.Size.markS), color = palette.accent)
                    Text(
                        text = stringResource(R.string.app_assist_replying),
                        style = type.caption,
                        color = palette.ink2,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
    }
}

@Composable
private fun AssistScreenAttachmentRow(
    ui: AssistOverlayUiState,
    onAttachScreen: () -> Unit,
    onRemoveScreen: () -> Unit,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    when (ui.captureStatus) {
        AssistCaptureStatus.Waiting -> {
            Row(
                Modifier
                    .fillMaxWidth()
                    .testTag("assist-screen-waiting"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
            ) {
                JSpinner(Modifier.size(Tokens.Size.markS), color = palette.ink3)
                Text(
                    text = stringResource(R.string.app_assist_screen_preparing),
                    style = type.caption,
                    color = palette.ink3,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }

        AssistCaptureStatus.Available -> {
            Row(
                Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
            ) {
                JChip(
                    text = stringResource(R.string.app_assist_attach_screen),
                    icon = PersonaIcons.screen,
                    accent = true,
                    role = Role.Button,
                    onClick = if (ui.attachingScreen || ui.sending) null else onAttachScreen,
                    modifier = Modifier.testTag("assist-attach-screen-chip"),
                )
                if (ui.attachingScreen) {
                    JSpinner(Modifier.size(Tokens.Size.markS), color = palette.accent)
                }
            }
        }

        AssistCaptureStatus.Attached -> {
            val thumb = remember(ui.attachedThumbnail) {
                ui.attachedThumbnail?.takeUnless { it.isRecycled }?.asImageBitmap()
            }
            Row(
                Modifier
                    .fillMaxWidth()
                    .testTag("assist-screen-attached-row"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m),
            ) {
                AttachmentThumb(
                    image = thumb,
                    contentDescription = stringResource(R.string.app_assist_screen_preview),
                    state = if (ui.sending) AttachmentState.Uploading else AttachmentState.Ready,
                    onRemove = if (ui.sending) null else onRemoveScreen,
                )
                Column(
                    Modifier.weight(1f),
                    verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs),
                ) {
                    Text(
                        text = stringResource(R.string.app_assist_screen_attached),
                        style = type.labelSmall.copy(fontWeight = FontWeight.Medium),
                        color = palette.ink,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    ui.attachedContext?.title?.takeIf(String::isNotBlank)?.let { title ->
                        Text(
                            text = title,
                            style = type.caption,
                            color = palette.ink2,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }

        AssistCaptureStatus.Unavailable -> {
            Row(
                Modifier
                    .fillMaxWidth()
                    .testTag("assist-screen-unavailable"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
            ) {
                JIcon(
                    icon = PersonaIcons.privacy,
                    contentDescription = null,
                    size = Tokens.Size.markS,
                    tint = palette.ink3,
                )
                Text(
                    text = stringResource(R.string.app_assist_screen_unavailable),
                    style = type.caption,
                    color = palette.ink3,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }

        AssistCaptureStatus.NotSupported -> Unit
    }
}

@Composable
private fun AssistInputBar(
    draftText: String,
    canSend: Boolean,
    sending: Boolean,
    inputEnabled: Boolean,
    dictating: Boolean,
    onDraftChange: (String) -> Unit,
    onToggleDictation: (() -> Unit)?,
    onSend: () -> Unit,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val shapes = PersonaTheme.shapes
    val placeholder = stringResource(
        if (dictating) R.string.app_assist_dictate_listening else R.string.app_assist_input_placeholder,
    )

    Row(
        Modifier
            .fillMaxWidth()
            .clip(shapes.cardShape)
            .background(palette.sunken)
            .border(Tokens.Size.line, palette.line, shapes.cardShape)
            .padding(start = Tokens.Space.m, end = Tokens.Space.xs, top = Tokens.Space.xs, bottom = Tokens.Space.xs),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
    ) {
        Box(
            Modifier
                .weight(1f)
                .padding(vertical = Tokens.Space.s),
            contentAlignment = Alignment.CenterStart,
        ) {
            if (draftText.isEmpty()) {
                Text(
                    text = placeholder,
                    style = type.body,
                    color = palette.ink3,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            BasicTextField(
                value = draftText,
                onValueChange = onDraftChange,
                enabled = inputEnabled,
                textStyle = type.body.copy(color = palette.ink),
                cursorBrush = SolidColor(palette.accent),
                maxLines = 4,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                keyboardActions = KeyboardActions(onSend = { if (canSend) onSend() }),
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("assist-input-field"),
            )
        }
        if (onToggleDictation != null) {
            JIconButton(
                icon = if (dictating) PersonaIcons.dictating else PersonaIcons.dictate,
                contentDescription = stringResource(
                    if (dictating) R.string.app_assist_dictate_stop else R.string.app_assist_dictate,
                ),
                onClick = onToggleDictation,
                enabled = inputEnabled,
                selected = dictating,
                tint = if (dictating) palette.accent else palette.ink2,
                modifier = Modifier.testTag("assist-dictate-button"),
            )
        }
        Box(
            Modifier
                .size(Tokens.MinTouch.android)
                .clip(CircleShape)
                .background(if (canSend) palette.action else palette.surfaceStrong)
                .clickable(
                    enabled = canSend && !sending,
                    role = Role.Button,
                    onClickLabel = stringResource(R.string.app_send),
                    onClick = onSend,
                )
                .testTag("assist-send-button"),
            contentAlignment = Alignment.Center,
        ) {
            if (sending) {
                JSpinner(Modifier.size(Tokens.Size.iconXS), color = palette.ink2)
            } else {
                JIcon(
                    icon = PersonaIcons.send,
                    contentDescription = stringResource(R.string.app_send),
                    size = Tokens.Size.iconXS,
                    tint = if (canSend) palette.onAction else palette.ink3,
                )
            }
        }
    }
}

/**
 * Overlay-safe voice dictation helper that never requires `LocalActivityResultRegistryOwner`
 * (which is unavailable inside a `VoiceInteractionSession`). Activates only when `RECORD_AUDIO`
 * is already granted and platform speech recognition is available.
 */
private class OverlayDictationHandle(
    private val context: Context,
    private val onDevice: Boolean,
    private val intent: Intent,
    private val onHeard: () -> (String) -> Unit,
    private val onError: () -> (String) -> Unit,
    private val stoppedMessage: String,
) {
    var listening by mutableStateOf(false)
        private set

    private var recognizer: SpeechRecognizer? = null

    fun toggle() {
        if (listening) stop() else start()
    }

    private fun start() {
        stop()
        val speech = runCatching {
            if (onDevice && Build.VERSION.SDK_INT >= 31) SpeechRecognizer.createOnDeviceSpeechRecognizer(context)
            else SpeechRecognizer.createSpeechRecognizer(context)
        }.getOrNull() ?: return
        recognizer = speech
        listening = true
        speech.setRecognitionListener(object : RecognitionListener {
            override fun onResults(results: Bundle?) {
                val words = results?.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION)
                    ?.firstOrNull()?.trim().orEmpty()
                stop()
                if (words.isNotEmpty()) onHeard().invoke(words)
            }

            override fun onError(error: Int) {
                stop()
                if (error != SpeechRecognizer.ERROR_NO_MATCH && error != SpeechRecognizer.ERROR_SPEECH_TIMEOUT) {
                    onError().invoke(stoppedMessage)
                }
            }

            override fun onReadyForSpeech(params: Bundle?) = Unit
            override fun onBeginningOfSpeech() = Unit
            override fun onRmsChanged(rmsdB: Float) = Unit
            override fun onBufferReceived(buffer: ByteArray?) = Unit
            override fun onEndOfSpeech() = Unit
            override fun onPartialResults(partialResults: Bundle?) = Unit
            override fun onEvent(eventType: Int, params: Bundle?) = Unit
        })
        runCatching { speech.startListening(intent) }.onFailure {
            stop()
            onError().invoke(stoppedMessage)
        }
    }

    fun stop() {
        listening = false
        val current = recognizer
        recognizer = null
        if (current != null) {
            runCatching { current.stopListening() }
            runCatching { current.destroy() }
        }
    }
}

@Composable
private fun rememberOverlayDictation(
    onHeard: (String) -> Unit,
    onError: (String) -> Unit,
): OverlayDictationHandle? {
    val context = LocalContext.current
    val resources = LocalResources.current
    val granted = remember(context) {
        ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED
    }
    if (!granted) return null
    val onDevice = remember(context) {
        Build.VERSION.SDK_INT >= 31 && SpeechRecognizer.isOnDeviceRecognitionAvailable(context)
    }
    val available = remember(context, onDevice) {
        onDevice || SpeechRecognizer.isRecognitionAvailable(context)
    }
    if (!available) return null
    val locale = resources.configuration.locales[0]
    val intent = remember(locale) {
        Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
            .putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            .putExtra(RecognizerIntent.EXTRA_LANGUAGE, if (locale.language == "zh") "zh-CN" else locale.toLanguageTag())
            .putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, false)
    }
    val heardRef = rememberUpdatedState(onHeard)
    val errorRef = rememberUpdatedState(onError)
    val stoppedMessage = stringResource(R.string.app_assist_dictate_stopped)
    val handle = remember(context, onDevice, intent, stoppedMessage) {
        OverlayDictationHandle(
            context = context.applicationContext,
            onDevice = onDevice,
            intent = intent,
            onHeard = { heardRef.value },
            onError = { errorRef.value },
            stoppedMessage = stoppedMessage,
        )
    }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(handle, lifecycle) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP || event == Lifecycle.Event.ON_DESTROY) {
                handle.stop()
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            handle.stop()
        }
    }
    return handle
}
