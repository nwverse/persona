package persona.feature.chat

import android.content.res.Resources
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import persona.core.contracts.CODEX_TAKEOVER_CONTINUE_ONLY
import persona.core.contracts.CodexTakeoverInfo
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentOperations
import persona.core.contracts.api.ChatActions
import persona.core.contracts.api.HubCodedFailure
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.sort.canContinueCodex
import persona.core.domain.sort.canTakeover
import persona.core.hub.HubFailure

/** Transport adapter for explicit Codex takeover and continuation, shared by Chat and Agents surfaces. */
interface CodexTakeoverClient {
    suspend fun takeoverInfo(session: Session): CodexTakeoverInfo
    suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session
    suspend fun startContinuation(session: Session, info: CodexTakeoverInfo): Session

    companion object {
        fun of(actions: ChatActions): CodexTakeoverClient = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session) = actions.codexTakeoverInfo(session)
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean) =
                actions.codexTakeover(session, token, confirmStop)
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo) =
                actions.startCodexContinuation(session, info)
        }

        fun of(operations: AgentOperations): CodexTakeoverClient = object : CodexTakeoverClient {
            override suspend fun takeoverInfo(session: Session) = operations.codexTakeoverInfo(session.key)
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean) =
                operations.codexTakeover(session.key, token, confirmStop)
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo) =
                operations.startCodexContinuation(session.key, info, session.todo?.id)
        }
    }
}

/**
 * Shared state machine for explicit Codex read-only session takeover and continuation (matching iPhone's
 * `CodexTakeoverCoordinator`):
 * 1. GET `/api/v1/machines/{mid}/codex/{sid}/takeover` with immediate loading state (Super Rule 5).
 * 2. `!canResume` or 409 `codex_takeover_continue_only` -> continuation confirmation dialog.
 * 3. `running` -> destructive takeover confirmation dialog (plus an alternative "Continue in a new session instead").
 * 4. Not running and `canResume` -> direct POST without dialog.
 * 5. Any failure dismisses the dialog and reports a retryable toast (retry always starts from a fresh GET).
 */
class CodexTakeoverCoordinator(
    private val res: Resources,
    private val client: CodexTakeoverClient?,
    private val scope: CoroutineScope,
    private val onAdopted: suspend (source: Session, adopted: Session) -> Unit,
    private val onFailure: (message: String, retryLabel: String, retry: () -> Unit) -> Unit,
    private val onSuccessToast: (String) -> Unit,
) {
    sealed interface Pending {
        val session: Session
        val info: CodexTakeoverInfo

        data class Takeover(override val session: Session, override val info: CodexTakeoverInfo) : Pending
        data class Continuation(override val session: Session, override val info: CodexTakeoverInfo) : Pending
    }

    var pending by mutableStateOf<Pending?>(null)
        private set

    /** True while GET or an unconfirmed direct POST is running. */
    var busy by mutableStateOf(false)
        private set

    /** True while a dialog confirmation POST is running; the dialog stays open with confirm loading. */
    var confirming by mutableStateOf(false)
        private set

    /** Session currently being acted on, so list rows and chat bars can scope their loading indicator. */
    var activeSessionKey by mutableStateOf<SessionKey?>(null)
        private set

    val inFlight: Boolean get() = busy || confirming

    fun isBusyFor(session: Session): Boolean = inFlight && activeSessionKey == session.key

    fun takeOver(session: Session) {
        if (inFlight || !session.canContinueCodex) return
        val api = client ?: run {
            reportFailure(UnsupportedOperationException("Codex takeover unavailable")) { takeOver(session) }
            return
        }
        busy = true
        activeSessionKey = session.key
        scope.launch {
            try {
                val info = api.takeoverInfo(session)
                when {
                    !info.canResume -> pending = Pending.Continuation(session, info)
                    info.running -> pending = Pending.Takeover(session, info)
                    else -> executeTakeover(api, session, info, confirmStop = false)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                reportFailure(error) { takeOver(session) }
            } finally {
                busy = false
                if (pending == null) activeSessionKey = null
            }
        }
    }

    fun continueInNewSession(session: Session) {
        if (inFlight || !session.canContinueCodex) return
        val api = client ?: run {
            reportFailure(UnsupportedOperationException("Codex continuation unavailable")) { continueInNewSession(session) }
            return
        }
        busy = true
        activeSessionKey = session.key
        scope.launch {
            try {
                val info = api.takeoverInfo(session)
                if (!info.canResume) {
                    pending = Pending.Continuation(session, info)
                } else {
                    executeContinuation(api, session, info)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                reportFailure(error) { continueInNewSession(session) }
            } finally {
                busy = false
                if (pending == null) activeSessionKey = null
            }
        }
    }

    fun confirm() {
        if (confirming) return
        val current = pending ?: return
        val api = client ?: run {
            pending = null
            activeSessionKey = null
            reportFailure(UnsupportedOperationException("Codex takeover unavailable")) {
                when (current) {
                    is Pending.Takeover -> takeOver(current.session)
                    is Pending.Continuation -> continueInNewSession(current.session)
                }
            }
            return
        }
        confirming = true
        activeSessionKey = current.session.key
        scope.launch {
            try {
                when (current) {
                    is Pending.Takeover -> executeTakeover(api, current.session, current.info, confirmStop = true)
                    is Pending.Continuation -> executeContinuation(api, current.session, current.info)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                pending = null
                activeSessionKey = null
                reportFailure(error) {
                    when (current) {
                        is Pending.Takeover -> takeOver(current.session)
                        is Pending.Continuation -> continueInNewSession(current.session)
                    }
                }
            } finally {
                confirming = false
                if (pending == null) activeSessionKey = null
            }
        }
    }

    /** Switch directly from the running-takeover dialog to starting an independent continuation session. */
    fun continueFromPending() {
        if (confirming) return
        val current = pending ?: return
        val api = client ?: run {
            pending = null
            activeSessionKey = null
            reportFailure(UnsupportedOperationException("Codex continuation unavailable")) { continueInNewSession(current.session) }
            return
        }
        confirming = true
        activeSessionKey = current.session.key
        scope.launch {
            try {
                executeContinuation(api, current.session, current.info)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                pending = null
                activeSessionKey = null
                reportFailure(error) { continueInNewSession(current.session) }
            } finally {
                confirming = false
                if (pending == null) activeSessionKey = null
            }
        }
    }

    fun dismiss() {
        if (confirming) return
        pending = null
        activeSessionKey = null
    }

    private suspend fun executeTakeover(
        api: CodexTakeoverClient,
        session: Session,
        info: CodexTakeoverInfo,
        confirmStop: Boolean,
    ) {
        val adopted = try {
            api.takeover(session, info.token, confirmStop)
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Exception) {
            val continuation = continuationOnlyInfo(error, info)
            if (continuation != null) {
                pending = Pending.Continuation(session, continuation)
                return
            }
            throw error
        }
        pending = null
        activeSessionKey = null
        onAdopted(session, adopted)
        onSuccessToast(res.getString(R.string.chat_takeover_done))
    }

    private suspend fun executeContinuation(
        api: CodexTakeoverClient,
        session: Session,
        info: CodexTakeoverInfo,
    ) {
        val created = api.startContinuation(session, info)
        pending = null
        activeSessionKey = null
        onAdopted(session, created)
        onSuccessToast(res.getString(R.string.chat_takeover_done))
    }

    private fun reportFailure(error: Throwable, retry: () -> Unit) {
        val message = operationFeedback(error).text.resolve(res)
        val retryLabel = res.getString(persona.core.designsystem.R.string.designsystem_retry)
        onFailure(message, retryLabel, retry)
    }

    companion object {
        const val REASON_ENCRYPTED_CONTEXT = "encrypted_context"
        const val REASON_UNAVAILABLE = "unavailable"

        fun continuationOnlyInfo(error: Throwable, original: CodexTakeoverInfo): CodexTakeoverInfo? {
            val code = (error as? HubCodedFailure)?.code ?: HubFailure.from(error).code
            if (code != CODEX_TAKEOVER_CONTINUE_ONLY) return null
            return original.copy(canResume = false, reason = REASON_ENCRYPTED_CONTEXT)
        }
    }
}

@Composable
fun rememberCodexTakeoverCoordinator(
    client: CodexTakeoverClient?,
    sessions: SessionsRepository,
    onOpenSession: ((SessionKey) -> Unit)?,
    fallbackToast: JToastState? = null,
    onFallbackNotice: ((String) -> Unit)? = null,
    onCompleted: ((source: Session, adopted: Session) -> Unit)? = null,
): CodexTakeoverCoordinator {
    val res = LocalResources.current
    val scope = rememberCoroutineScope()
    val feedback = LocalAppFeedback.current
    val openSessionState = rememberUpdatedState(onOpenSession)
    val completedState = rememberUpdatedState(onCompleted)
    val feedbackState = rememberUpdatedState(feedback)
    val fallbackToastState = rememberUpdatedState(fallbackToast)
    val fallbackNoticeState = rememberUpdatedState(onFallbackNotice)
    return remember(res, client, sessions) {
        CodexTakeoverCoordinator(
            res = res,
            client = client,
            scope = scope,
            onAdopted = { source, adopted ->
                sessions.adopt(adopted)
                completedState.value?.invoke(source, adopted)
                if (adopted.key != source.key) {
                    openSessionState.value?.invoke(adopted.key)
                }
                runCatching { sessions.refresh() }
            },
            onFailure = { message, retryLabel, retry ->
                val action = FeedbackAction(retryLabel, retry)
                val shown = feedbackState.value?.notice(message, action, FeedbackStyle.Error) == true
                if (!shown) {
                    val localToast = fallbackToastState.value
                    if (localToast != null) {
                        scope.launch {
                            localToast.show(FeedbackNotice(message, action = action, style = FeedbackStyle.Error))
                        }
                    } else {
                        fallbackNoticeState.value?.invoke(message)
                    }
                }
            },
            onSuccessToast = { message ->
                val shown = feedbackState.value?.notice(message, FeedbackStyle.Success) == true
                if (!shown) {
                    val localToast = fallbackToastState.value
                    if (localToast != null) {
                        scope.launch { localToast.show(FeedbackNotice(message, style = FeedbackStyle.Success)) }
                    } else {
                        fallbackNoticeState.value?.invoke(message)
                    }
                }
            },
        )
    }
}

/** Shared takeover / continuation confirmation dialogs for Chat and Agents screens. */
@Composable
fun CodexTakeoverDialogs(coordinator: CodexTakeoverCoordinator) {
    when (val pending = coordinator.pending) {
        is CodexTakeoverCoordinator.Pending.Takeover -> JDialog(
            onDismiss = coordinator::dismiss,
            title = stringResource(R.string.chat_takeover_confirm_title),
            text = stringResource(R.string.chat_takeover_running_confirm),
            confirm = stringResource(R.string.chat_takeover_action) to coordinator::confirm,
            dismiss = stringResource(persona.core.designsystem.R.string.designsystem_cancel) to coordinator::dismiss,
            destructive = true,
            confirmEnabled = !coordinator.confirming,
            confirmLoading = coordinator.confirming,
        ) {
            JTextButton(
                text = stringResource(R.string.chat_takeover_continue_instead),
                onClick = coordinator::continueFromPending,
                enabled = !coordinator.confirming,
            )
        }
        is CodexTakeoverCoordinator.Pending.Continuation -> {
            val reasonText = stringResource(
                if (pending.info.reason == CodexTakeoverCoordinator.REASON_ENCRYPTED_CONTEXT) {
                    R.string.chat_takeover_reason_encrypted_context
                } else {
                    R.string.chat_takeover_reason_unavailable
                },
            )
            JDialog(
                onDismiss = coordinator::dismiss,
                title = stringResource(R.string.chat_takeover_continue_title),
                text = reasonText,
                confirm = stringResource(R.string.chat_takeover_continue_long) to coordinator::confirm,
                dismiss = stringResource(persona.core.designsystem.R.string.designsystem_cancel) to coordinator::dismiss,
                confirmEnabled = !coordinator.confirming,
                confirmLoading = coordinator.confirming,
            )
        }
        null -> Unit
    }
}

/**
 * Shared menu actions for Chat header `⋯` and Agents row long-press menus: same order, labels and coordinator.
 * "Take over" appears only when `canResume` is true; "Continue in a new session" appears whenever takeover is offered.
 */
@Composable
fun codexTakeoverMenuActions(
    session: Session,
    coordinator: CodexTakeoverCoordinator,
    enabled: Boolean,
    separatorBefore: Boolean = false,
): List<JAction> {
    if (!session.canContinueCodex) return emptyList()
    val actionEnabled = enabled && !coordinator.inFlight
    return buildList {
        if (session.canTakeover) {
            add(
                JAction(
                    label = stringResource(R.string.chat_takeover_action),
                    icon = PersonaIcons.terminal,
                    enabled = actionEnabled,
                    separatorBefore = separatorBefore,
                    onClick = { coordinator.takeOver(session) },
                ),
            )
        }
        add(
            JAction(
                label = stringResource(R.string.chat_takeover_continue_long),
                icon = PersonaIcons.compose,
                enabled = actionEnabled,
                separatorBefore = separatorBefore && !session.canTakeover,
                onClick = { coordinator.continueInNewSession(session) },
            ),
        )
    }
}
