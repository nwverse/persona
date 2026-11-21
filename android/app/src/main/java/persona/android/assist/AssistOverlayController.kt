package persona.android.assist

import android.graphics.Bitmap
import android.util.Log
import java.util.UUID
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.OutboxMessage
import persona.feature.chat.ChatLinkContext
import persona.feature.chat.ComposerAttachment

internal const val ASSIST_LOG_TAG = "PersonaAssist"

internal enum class AssistCaptureStatus {
    /** VoiceInteractionSession opened; awaiting async `onHandleScreenshot` callback. */
    Waiting,

    /** Screenshot is staged strictly in memory; NOT written to disk and NOT attached yet. */
    Available,

    /** User manually tapped "Attach current screen"; written to `cacheDir/assist/` for next send. */
    Attached,

    /** Page is `FLAG_SECURE`, system returned null, or capture timed out without a bitmap. */
    Unavailable,

    /** Entry point does not provide screenshots or capture was cleared after a text-only turn. */
    NotSupported,
}

internal enum class AssistHubStatus {
    Connecting,
    Ready,
    Offline,
    Unpaired,
}

internal enum class AssistTurnDelivery {
    Sending,
    Sent,
    Queued,
    Failed,
}

internal data class AssistOverlayTurn(
    val id: String,
    val fromUser: Boolean,
    val text: String,
    val streaming: Boolean = false,
    val hasScreenAttachment: Boolean = false,
    val delivery: AssistTurnDelivery = AssistTurnDelivery.Sent,
)

internal data class AssistHubSnapshot(
    val status: AssistHubStatus = AssistHubStatus.Connecting,
    val mainSession: SessionKey? = null,
    val mainWorking: Boolean = false,
    val chatSnapshot: ChatSnapshot? = null,
    val outbox: List<OutboxMessage> = emptyList(),
)

internal data class AssistOverlayUiState(
    val invocationId: String = "",
    val captureStatus: AssistCaptureStatus = AssistCaptureStatus.Waiting,
    val attachedScreenshot: ComposerAttachment? = null,
    val attachedThumbnail: Bitmap? = null,
    val attachedContext: ChatLinkContext? = null,
    val hasStagedContext: Boolean = false,
    val attachingScreen: Boolean = false,
    val hubStatus: AssistHubStatus = AssistHubStatus.Connecting,
    val mainSession: SessionKey? = null,
    val draftText: String = "",
    val sending: Boolean = false,
    val awaitingReply: Boolean = false,
    val errorMessage: String? = null,
    val turns: List<AssistOverlayTurn> = emptyList(),
) {
    val expanded: Boolean get() = turns.isNotEmpty() || sending
    val canSend: Boolean
        get() = !sending &&
            !attachingScreen &&
            hubStatus == AssistHubStatus.Ready &&
            (draftText.isNotBlank() || attachedScreenshot != null)
}

internal data class SentOverlayTurn(
    val clientMessageId: String,
    val displayText: String,
    val hasScreenAttachment: Boolean,
)

/**
 * Pure state machine and coordinator for Persona's floating Digital Assistant overlay.
 *
 * Privacy and lifecycle guarantees:
 * - Incoming `onHandleScreenshot` bitmaps and filtered `onHandleAssist` context are staged ONLY
 *   in memory until the user explicitly taps "Attach current screen".
 * - Disk writes (`cacheDir/assist/`) happen strictly upon manual user confirmation, and are deleted
 *   immediately upon removal (`×`), successful upload/send, or overlay dismissal.
 * - Sending a text-only message or dismissing the overlay immediately recycles staged bitmaps and
 *   drops staged context so nothing lingers across turns or sessions.
 */
internal class AssistOverlayController(
    private val mainScope: CoroutineScope,
    private val prepareScreenshot: suspend (Bitmap) -> Bitmap?,
    private val createThumbnail: suspend (Bitmap) -> Bitmap?,
    private val saveScreenshot: suspend (Bitmap, String) -> ComposerAttachment?,
    private val deleteScreenshot: suspend (ComposerAttachment) -> Unit,
    private val sendTextToMain: suspend (String, String) -> Unit,
    private val sendWithScreenToMain: suspend (String, ComposerAttachment, ChatLinkContext?, String) -> Unit,
    private val publishDraftForFullApp: (String, ComposerAttachment?, ChatLinkContext?, String) -> Unit,
    private val onWakeHub: () -> Unit,
    private val onLaunchFullApp: (SessionKey?) -> Unit,
    private val onDismiss: () -> Unit,
    private val screenOnlyDisplayLabel: () -> String,
    private val defaultSendError: () -> String,
    private val screenshotWaitTimeoutMillis: Long = SCREENSHOT_WAIT_TIMEOUT_MILLIS,
    private val nextId: () -> String = { UUID.randomUUID().toString() },
    private val log: (String) -> Unit = { message -> Log.i(ASSIST_LOG_TAG, message) },
) {
    private val mutableState = MutableStateFlow(AssistOverlayUiState())
    val state: StateFlow<AssistOverlayUiState> = mutableState.asStateFlow()

    private var invocationOpen = false
    private var invocationId = ""
    private var captureDiscardedForInvocation = false
    private var stagedBitmap: Bitmap? = null
    private var stagedContext: ChatLinkContext? = null
    private var stagedContextFocused = false
    private var handedOffAttachment: ComposerAttachment? = null

    private var hubSnapshot = AssistHubSnapshot()
    private var baselineItemIds: Set<String> = emptySet()
    private val sentTurns = mutableListOf<SentOverlayTurn>()

    private var screenshotWaitJob: Job? = null
    private var stageJob: Job? = null
    private var attachJob: Job? = null
    private var sendJob: Job? = null

    /** Starts a fresh overlay session; idempotent within an active invocation (e.g. `onPrepareShow` -> `onShow`). */
    fun beginSession(supportsScreenshot: Boolean, showFlags: Int = 0) {
        if (invocationOpen) return
        cleanupJobsAndResources(deleteAttached = true)
        invocationOpen = true
        invocationId = nextId()
        captureDiscardedForInvocation = !supportsScreenshot
        handedOffAttachment = null
        baselineItemIds = emptySet()
        sentTurns.clear()
        log("beginSession supportsScreenshot=$supportsScreenshot showFlags=$showFlags")
        onWakeHub()
        mutableState.value = AssistOverlayUiState(
            invocationId = invocationId,
            captureStatus = if (supportsScreenshot) AssistCaptureStatus.Waiting else AssistCaptureStatus.NotSupported,
            hubStatus = hubSnapshot.status,
            mainSession = hubSnapshot.mainSession,
        )
        if (supportsScreenshot && screenshotWaitTimeoutMillis > 0L) {
            val activeId = invocationId
            screenshotWaitJob = mainScope.launch {
                delay(screenshotWaitTimeoutMillis)
                if (invocationOpen && invocationId == activeId &&
                    mutableState.value.captureStatus == AssistCaptureStatus.Waiting
                ) {
                    log("screenshotWaitTimeout status=Unavailable")
                    mutableState.update { it.copy(captureStatus = AssistCaptureStatus.Unavailable) }
                }
            }
        }
    }

    /** Updates live Hub connection, main session, and streaming transcript snapshot. */
    fun onHubUpdate(snapshot: AssistHubSnapshot) {
        hubSnapshot = snapshot
        val projection = projectOverlayTurns(snapshot, sentTurns, baselineItemIds)
        mutableState.update { current ->
            current.copy(
                hubStatus = snapshot.status,
                mainSession = snapshot.mainSession,
                turns = projection.turns,
                awaitingReply = projection.awaitingReply,
            )
        }
    }

    /** Handles `onHandleScreenshot(Bitmap?)`: stages safely in memory ONLY; never writes to disk or attaches. */
    fun onScreenshotReceived(bitmap: Bitmap?) {
        if (!invocationOpen) {
            beginSession(supportsScreenshot = true)
        }
        val activeId = invocationId
        val valid = bitmap != null && !bitmap.isRecycled && bitmap.width > 0 && bitmap.height > 0
        log(
            "onHandleScreenshot received=${bitmap != null} valid=$valid " +
                "size=${bitmap?.width ?: 0}x${bitmap?.height ?: 0} config=${bitmap?.config?.name ?: "none"}",
        )
        screenshotWaitJob?.cancel()
        screenshotWaitJob = null
        if (captureDiscardedForInvocation) return
        if (!valid) {
            if (mutableState.value.captureStatus != AssistCaptureStatus.Attached) {
                mutableState.update { it.copy(captureStatus = AssistCaptureStatus.Unavailable) }
            }
            return
        }
        val incoming = requireNotNull(bitmap)
        stageJob?.cancel()
        stageJob = mainScope.launch {
            val prepared = try {
                prepareScreenshot(incoming)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                null
            }
            if (!invocationOpen || invocationId != activeId || captureDiscardedForInvocation) {
                prepared?.safeRecycle()
                return@launch
            }
            if (prepared == null) {
                log("screenshotPrepareFailed status=Unavailable")
                if (mutableState.value.captureStatus != AssistCaptureStatus.Attached) {
                    mutableState.update { it.copy(captureStatus = AssistCaptureStatus.Unavailable) }
                }
                return@launch
            }
            stagedBitmap?.safeRecycle()
            stagedBitmap = prepared
            log("screenshotStagedInMemory size=${prepared.width}x${prepared.height}")
            if (mutableState.value.captureStatus != AssistCaptureStatus.Attached) {
                mutableState.update { it.copy(captureStatus = AssistCaptureStatus.Available) }
            }
        }
    }

    /** Handles filtered `onHandleAssist` context: staged in memory ONLY until user confirms attaching the screen. */
    fun onAssistContextReceived(context: ChatLinkContext?, isFocused: Boolean) {
        log("onHandleAssist safeContext=${context != null} focused=$isFocused")
        if (context == null) return
        if (!invocationOpen) {
            beginSession(supportsScreenshot = true)
        }
        if (captureDiscardedForInvocation) return
        if (stagedContext == null || isFocused || !stagedContextFocused) {
            stagedContext = context
            stagedContextFocused = isFocused
        }
        mutableState.update { current ->
            current.copy(
                hasStagedContext = stagedContext != null,
                attachedContext = if (current.captureStatus == AssistCaptureStatus.Attached) {
                    current.attachedContext ?: stagedContext
                } else {
                    current.attachedContext
                },
            )
        }
    }

    /** Manual user confirmation: persists the staged screen to private `cacheDir/assist/` and previews it. */
    fun attachCurrentScreen() {
        val current = mutableState.value
        val source = stagedBitmap
        if (!invocationOpen || current.attachingScreen || current.sending ||
            current.captureStatus != AssistCaptureStatus.Available || source == null || source.isRecycled
        ) {
            return
        }
        val activeId = invocationId
        mutableState.update { it.copy(attachingScreen = true, errorMessage = null) }
        attachJob?.cancel()
        attachJob = mainScope.launch {
            val saved = try {
                saveScreenshot(source, activeId)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                null
            }
            val thumb = if (saved != null) {
                try {
                    createThumbnail(source)
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (_: Exception) {
                    null
                }
            } else {
                null
            }
            if (!invocationOpen || invocationId != activeId || captureDiscardedForInvocation) {
                thumb?.safeRecycle()
                if (saved != null) runCatching { deleteScreenshot(saved) }
                return@launch
            }
            if (saved == null) {
                thumb?.safeRecycle()
                log("attachCurrentScreenFailed")
                mutableState.update {
                    it.copy(
                        attachingScreen = false,
                        errorMessage = defaultSendError(),
                    )
                }
                return@launch
            }
            log("attachCurrentScreenConfirmed bytes=${saved.size ?: 0L} withContext=${stagedContext != null}")
            mutableState.update {
                it.copy(
                    captureStatus = AssistCaptureStatus.Attached,
                    attachedScreenshot = saved,
                    attachedThumbnail = thumb,
                    attachedContext = stagedContext,
                    attachingScreen = false,
                    errorMessage = null,
                )
            }
        }
    }

    /** Removes the manually attached screen (and paired context) and deletes its temporary cache file immediately. */
    fun removeAttachedScreen() {
        val current = mutableState.value
        if (current.sending) return
        attachJob?.cancel()
        attachJob = null
        val previous = current.attachedScreenshot
        val canReattach = !captureDiscardedForInvocation && stagedBitmap?.let { !it.isRecycled } == true
        mutableState.update {
            it.copy(
                captureStatus = if (canReattach) AssistCaptureStatus.Available else AssistCaptureStatus.NotSupported,
                attachedScreenshot = null,
                attachedThumbnail = null,
                attachedContext = null,
                attachingScreen = false,
                errorMessage = null,
            )
        }
        if (previous != null) {
            mainScope.launch { runCatching { deleteScreenshot(previous) } }
        }
    }

    fun updateDraftText(value: String) {
        mutableState.update { it.copy(draftText = value, errorMessage = null) }
    }

    fun appendDictation(heard: String) {
        val trimmed = heard.trim()
        if (trimmed.isEmpty()) return
        mutableState.update { current ->
            val updated = persona.feature.chat.withDictation(current.draftText, trimmed)
            current.copy(draftText = updated, errorMessage = null)
        }
    }

    fun showNotice(message: String) {
        mutableState.update { it.copy(errorMessage = message) }
    }

    /** Sends the message directly to the Hub's main conversation and begins streaming the reply in place. */
    fun send() {
        val current = mutableState.value
        if (!invocationOpen || !current.canSend) return
        val prompt = current.draftText.trim()
        val screenshot = current.attachedScreenshot
        val contextCard = current.attachedContext
        val clientId = nextId()
        val activeId = invocationId
        if (sentTurns.isEmpty()) {
            baselineItemIds = hubSnapshot.chatSnapshot?.items.orEmpty().mapTo(HashSet()) { it.id }
        }
        if (screenshot == null) {
            // Text-only send without attaching the screen: immediately release staged capture & context.
            releaseStagedCaptureOnly()
        }
        mutableState.update { it.copy(sending = true, errorMessage = null) }
        sendJob = mainScope.launch {
            try {
                if (screenshot != null) {
                    sendWithScreenToMain(prompt, screenshot, contextCard, clientId)
                } else {
                    sendTextToMain(prompt, clientId)
                }
                if (!invocationOpen || invocationId != activeId) {
                    if (screenshot != null) runCatching { deleteScreenshot(screenshot) }
                    return@launch
                }
                sentTurns += SentOverlayTurn(
                    clientMessageId = clientId,
                    displayText = prompt.ifBlank { screenOnlyDisplayLabel() },
                    hasScreenAttachment = screenshot != null,
                )
                if (screenshot != null) {
                    runCatching { deleteScreenshot(screenshot) }
                    releaseStagedCaptureOnly()
                }
                val projection = projectOverlayTurns(hubSnapshot, sentTurns, baselineItemIds)
                mutableState.update {
                    it.copy(
                        draftText = "",
                        sending = false,
                        errorMessage = null,
                        turns = projection.turns,
                        awaitingReply = projection.awaitingReply,
                    )
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                if (!invocationOpen || invocationId != activeId) return@launch
                mutableState.update {
                    it.copy(
                        sending = false,
                        errorMessage = error.message?.takeIf(String::isNotBlank) ?: defaultSendError(),
                    )
                }
            }
        }
    }

    /**
     * Opens Persona's full `MainActivity` main conversation. Preserves any unsent text or
     * manually-attached screen (never unconfirmed screenshots) via [publishDraftForFullApp],
     * then dismisses the overlay.
     */
    fun openInPersona() {
        val current = mutableState.value
        val unsentText = current.draftText.trim()
        val confirmedShot = current.attachedScreenshot
        val confirmedContext = if (confirmedShot != null) current.attachedContext else null
        if (unsentText.isNotEmpty() || confirmedShot != null) {
            handedOffAttachment = confirmedShot
            publishDraftForFullApp(
                invocationId.ifEmpty { nextId() },
                confirmedShot,
                confirmedContext,
                unsentText,
            )
        }
        stagedBitmap?.safeRecycle()
        stagedBitmap = null
        stagedContext = null
        stagedContextFocused = false
        onLaunchFullApp(current.mainSession)
        onDismiss()
    }

    /** Ends the current overlay session (on hide/dismiss/destroy) and releases all bitmaps & un-sent files. */
    fun endSession() {
        if (!invocationOpen && stagedBitmap == null && mutableState.value.attachedScreenshot == null) return
        invocationOpen = false
        cleanupJobsAndResources(deleteAttached = true)
        baselineItemIds = emptySet()
        sentTurns.clear()
        handedOffAttachment = null
        mutableState.value = AssistOverlayUiState(
            captureStatus = AssistCaptureStatus.NotSupported,
            hubStatus = hubSnapshot.status,
            mainSession = hubSnapshot.mainSession,
        )
    }

    private fun releaseStagedCaptureOnly() {
        captureDiscardedForInvocation = true
        screenshotWaitJob?.cancel()
        screenshotWaitJob = null
        stageJob?.cancel()
        stageJob = null
        attachJob?.cancel()
        attachJob = null
        stagedBitmap?.safeRecycle()
        stagedBitmap = null
        stagedContext = null
        stagedContextFocused = false
        mutableState.update {
            it.copy(
                captureStatus = AssistCaptureStatus.NotSupported,
                attachedScreenshot = null,
                attachedThumbnail = null,
                attachedContext = null,
                hasStagedContext = false,
                attachingScreen = false,
            )
        }
    }

    private fun cleanupJobsAndResources(deleteAttached: Boolean) {
        screenshotWaitJob?.cancel()
        screenshotWaitJob = null
        stageJob?.cancel()
        stageJob = null
        attachJob?.cancel()
        attachJob = null
        sendJob?.cancel()
        sendJob = null

        stagedBitmap?.safeRecycle()
        stagedBitmap = null
        stagedContext = null
        stagedContextFocused = false

        val current = mutableState.value
        val toDelete = current.attachedScreenshot
        if (deleteAttached && toDelete != null && toDelete !== handedOffAttachment) {
            mainScope.launch { runCatching { deleteScreenshot(toDelete) } }
        }
    }

    companion object {
        const val SCREENSHOT_WAIT_TIMEOUT_MILLIS = 4_000L
    }
}

internal data class OverlayTurnsProjection(
    val turns: List<AssistOverlayTurn>,
    val awaitingReply: Boolean,
)

/**
 * Projects only the conversation turns belonging to the current overlay session from the main
 * chat's live [AssistHubSnapshot], preserving real-time streaming state and immediate optimistic
 * user bubbles.
 */
internal fun projectOverlayTurns(
    hub: AssistHubSnapshot,
    sentTurns: List<SentOverlayTurn>,
    baselineItemIds: Set<String>,
): OverlayTurnsProjection {
    if (sentTurns.isEmpty()) return OverlayTurnsProjection(emptyList(), awaitingReply = false)
    val items = hub.chatSnapshot?.items.orEmpty()
    val streamingIds = hub.chatSnapshot?.streamingItemIds.orEmpty()
    val sentById = sentTurns.associateBy { it.clientMessageId }
    val matchedClientIds = mutableSetOf<String>()

    val firstIndex = items.indexOfFirst { item ->
        item.clientMsgId != null && item.clientMsgId in sentById
    }.takeIf { it >= 0 } ?: items.indexOfFirst { item ->
        item.id !in baselineItemIds && item.role == "user" && item.kind == "text"
    }

    val projected = mutableListOf<AssistOverlayTurn>()
    if (firstIndex >= 0) {
        for (i in firstIndex until items.size) {
            val item = items[i]
            if (item.kind != "text") continue
            when (item.role) {
                "user" -> {
                    val raw = item.text.orEmpty()
                    val stripped = stripOverlayWireTags(raw)
                    val matched = item.clientMsgId?.let(sentById::get)
                        ?: sentTurns.firstOrNull { candidate ->
                            candidate.clientMessageId !in matchedClientIds &&
                                (candidate.displayText == stripped.text ||
                                    (stripped.hasAttachedImage && candidate.hasScreenAttachment && stripped.text.isBlank()))
                        }
                    if (matched != null) matchedClientIds += matched.clientMessageId
                    val display = stripped.text.ifBlank { matched?.displayText.orEmpty() }
                    if (display.isNotBlank() || stripped.hasAttachedImage || matched?.hasScreenAttachment == true) {
                        projected += AssistOverlayTurn(
                            id = item.id,
                            fromUser = true,
                            text = display,
                            hasScreenAttachment = stripped.hasAttachedImage || matched?.hasScreenAttachment == true,
                            delivery = AssistTurnDelivery.Sent,
                        )
                    }
                }

                "assistant" -> {
                    val body = item.text.orEmpty()
                    if (body.isNotBlank()) {
                        projected += AssistOverlayTurn(
                            id = item.id,
                            fromUser = false,
                            text = body,
                            streaming = item.id in streamingIds,
                            delivery = AssistTurnDelivery.Sent,
                        )
                    }
                }
            }
        }
    }

    val outboxById = hub.outbox.associateBy { it.clientMessageId }
    for (sent in sentTurns) {
        if (sent.clientMessageId in matchedClientIds) continue
        val outbox = outboxById[sent.clientMessageId]
        val delivery = when (outbox?.state) {
            DeliveryState.Refused, DeliveryState.Rejected -> AssistTurnDelivery.Failed
            DeliveryState.Queued, DeliveryState.WaitingForNetwork -> AssistTurnDelivery.Queued
            DeliveryState.Sent -> AssistTurnDelivery.Sent
            else -> AssistTurnDelivery.Sending
        }
        projected += AssistOverlayTurn(
            id = "pending:${sent.clientMessageId}",
            fromUser = true,
            text = sent.displayText,
            hasScreenAttachment = sent.hasScreenAttachment,
            delivery = delivery,
        )
    }

    val last = projected.lastOrNull()
    val awaitingReply = last != null &&
        last.fromUser &&
        last.delivery != AssistTurnDelivery.Failed &&
        (hub.mainWorking || last.delivery == AssistTurnDelivery.Sending || last.delivery == AssistTurnDelivery.Sent)

    return OverlayTurnsProjection(projected, awaitingReply)
}

internal data class StrippedWireMessage(
    val text: String,
    val hasAttachedImage: Boolean,
)

private val CONTEXT_TAG_REGEX = Regex("<persona-context\\b[^>]*>[\\s\\S]*?</persona-context>\\s*")
private val ATTACHED_IMAGES_REGEX = Regex("<attached_images>[\\s\\S]*?</attached_images>\\s*")

internal fun stripOverlayWireTags(raw: String): StrippedWireMessage {
    val hasImage = ATTACHED_IMAGES_REGEX.containsMatchIn(raw)
    val cleaned = raw
        .replace(CONTEXT_TAG_REGEX, "")
        .replace(ATTACHED_IMAGES_REGEX, "")
        .trim()
    return StrippedWireMessage(text = cleaned, hasAttachedImage = hasImage)
}

private fun Bitmap.safeRecycle() {
    if (!isRecycled) runCatching { recycle() }
}
