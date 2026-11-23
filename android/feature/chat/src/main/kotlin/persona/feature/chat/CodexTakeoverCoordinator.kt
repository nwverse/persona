package persona.feature.chat

import android.content.Context
import android.content.res.Resources
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import persona.core.contracts.CODEX_TAKEOVER_CONTINUE_ONLY
import persona.core.contracts.CodexTakeoverInfo
import persona.core.contracts.GroupKeys
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentOperations
import persona.core.contracts.api.AgentStartResult
import persona.core.contracts.api.ChatActions
import persona.core.contracts.api.HubCodedFailure
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.sort.canContinueCodex
import persona.core.domain.sort.canTakeover
import persona.core.hub.HubFailure
import java.util.UUID

/** Transport adapter for explicit Codex takeover and continuation, shared by Chat and Agents surfaces. */
interface CodexTakeoverClient {
    /** Pairing scope of the hub that owns [session], used to isolate persisted continuation receipts per pairing. */
    fun receiptScope(session: Session): String = ""
    suspend fun takeoverInfo(session: Session): CodexTakeoverInfo
    suspend fun takeover(session: Session, token: String, confirmStop: Boolean): Session
    suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String): AgentStartResult
    suspend fun continuationOperation(session: Session, operationId: String): AgentStartResult

    companion object {
        fun of(actions: ChatActions, hubScope: String = ""): CodexTakeoverClient = object : CodexTakeoverClient {
            override fun receiptScope(session: Session) = hubScope
            override suspend fun takeoverInfo(session: Session) = actions.codexTakeoverInfo(session)
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean) =
                actions.codexTakeover(session, token, confirmStop)
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String) =
                actions.startCodexContinuation(session, info, operationId)
            override suspend fun continuationOperation(session: Session, operationId: String) =
                actions.codexContinuationOperation(session, operationId)
        }

        fun of(operations: AgentOperations): CodexTakeoverClient = object : CodexTakeoverClient {
            override fun receiptScope(session: Session) = operations.scopeFor(session.key)
            override suspend fun takeoverInfo(session: Session) = operations.codexTakeoverInfo(session.key)
            override suspend fun takeover(session: Session, token: String, confirmStop: Boolean) =
                operations.codexTakeover(session.key, token, confirmStop)
            override suspend fun startContinuation(session: Session, info: CodexTakeoverInfo, operationId: String) =
                operations.startCodexContinuation(session.key, info, operationId, session.todo?.id)
            override suspend fun continuationOperation(session: Session, operationId: String) =
                operations.codexContinuationOperation(session.key, operationId)
        }
    }
}

/**
 * Persists unconfirmed continuation `operation_id` receipts scoped by hub pairing and canonical session key so closing
 * a preview, switching entry points (Agents / Space / Chat), or restarting the app always reconciles the same
 * `operation_id` via `GET /api/v1/ops/{id}` instead of starting a duplicate Codex agent
 * (`docs/protocol/protocol.md`, "Idempotent operations").
 */
interface ContinuationReceiptStore {
    suspend fun get(scope: String, key: SessionKey): String?
    suspend fun put(scope: String, key: SessionKey, operationId: String)
    suspend fun remove(scope: String, key: SessionKey)
}

internal fun continuationReceiptKey(scope: String, key: SessionKey): String =
    "$scope|${GroupKeys.raw(key.machineId)}|${key.sessionId}"

class InMemoryContinuationReceiptStore(
    private val map: MutableMap<String, String> = mutableMapOf(),
) : ContinuationReceiptStore {
    private val lock = Mutex()
    override suspend fun get(scope: String, key: SessionKey): String? = lock.withLock { map[continuationReceiptKey(scope, key)] }
    override suspend fun put(scope: String, key: SessionKey, operationId: String) = lock.withLock {
        map[continuationReceiptKey(scope, key)] = operationId
    }
    override suspend fun remove(scope: String, key: SessionKey) = lock.withLock {
        val _ = map.remove(continuationReceiptKey(scope, key))
    }
}

/**
 * Shared process-wide disk-backed receipt store (`SharedPreferences` with synchronous IO commit before HTTP send).
 * Every Chat, Agents, Space, and history-preview coordinator reads and writes the same pairing-scoped receipts.
 */
class PersistentContinuationReceiptStore(
    context: Context,
    private val prefsName: String = PREFS_NAME,
    private val ioDispatcher: kotlinx.coroutines.CoroutineDispatcher = Dispatchers.IO,
) : ContinuationReceiptStore {
    private val appContext = context.applicationContext
    private val lock = Mutex()
    private val prefs by lazy { appContext.getSharedPreferences(prefsName, Context.MODE_PRIVATE) }

    override suspend fun get(scope: String, key: SessionKey): String? = lock.withLock {
        withContext(ioDispatcher) {
            prefs.getString(continuationReceiptKey(scope, key), null)?.takeIf(String::isNotBlank)
        }
    }

    override suspend fun put(scope: String, key: SessionKey, operationId: String) = lock.withLock {
        withContext(ioDispatcher) {
            check(prefs.edit().putString(continuationReceiptKey(scope, key), operationId).commit()) {
                "Failed to persist continuation operation receipt"
            }
        }
    }

    override suspend fun remove(scope: String, key: SessionKey) = lock.withLock {
        withContext(ioDispatcher) {
            val _ = prefs.edit().remove(continuationReceiptKey(scope, key)).commit()
        }
    }

    companion object {
        private const val PREFS_NAME = "persona_codex_takeover_receipts"
        @Volatile private var shared: PersistentContinuationReceiptStore? = null

        fun of(context: Context): PersistentContinuationReceiptStore =
            shared ?: synchronized(this) {
                shared ?: PersistentContinuationReceiptStore(context.applicationContext).also { shared = it }
            }
    }
}

/**
 * Shared state machine for explicit Codex read-only session takeover and continuation (matching iPhone's
 * `CodexTakeoverCoordinator` plus pairing-scoped persistent continuation receipt reconciliation):
 * 1. Immediate loading state before network work (Super Rule 5).
 * 2. Take over: GET -> `!canResume` or 409 `codex_takeover_continue_only` -> continuation confirmation dialog;
 *    `running` -> destructive takeover confirmation dialog (plus independent "Continue in a new session instead");
 *    not running -> direct POST without dialog.
 * 3. Continuation: persists a single `operation_id` to [receiptStore] before POST `/api/v1/sessions` and reconciles
 *    lost/pending/unknown outcomes via `GET /api/v1/ops/{id}` across all entry points and restarts.
 */
class CodexTakeoverCoordinator(
    private val res: Resources,
    private val client: CodexTakeoverClient?,
    private val scope: CoroutineScope,
    private val onAdopted: suspend (source: Session, adopted: Session) -> Unit,
    private val onFailure: (message: String, actionLabel: String, action: () -> Unit) -> Unit,
    private val onSuccessToast: (String) -> Unit,
    private val receiptStore: ContinuationReceiptStore = InMemoryContinuationReceiptStore(),
    private val newOperationId: () -> String = { UUID.randomUUID().toString() },
) {
    sealed interface Pending {
        val session: Session
        val info: CodexTakeoverInfo

        data class Takeover(override val session: Session, override val info: CodexTakeoverInfo) : Pending
        data class Continuation(override val session: Session, override val info: CodexTakeoverInfo) : Pending
    }

    enum class ActiveAction { Takeover, Continue, CheckContinuation }

    var pending by mutableStateOf<Pending?>(null)
        private set

    /** True while GET, direct POST, or receipt reconciliation outside a dialog is running. */
    var busy by mutableStateOf(false)
        private set

    /** Which non-dialog operation is running, for visible progress copy in Agents / Space screens. */
    var activeAction by mutableStateOf<ActiveAction?>(null)
        private set

    /** Which dialog button initiated the in-flight confirmation POST; keeps the dialog open with targeted loading. */
    var confirmingAction by mutableStateOf<ActiveAction?>(null)
        private set

    val confirming: Boolean get() = confirmingAction != null
    val confirmingTakeover: Boolean get() = confirmingAction == ActiveAction.Takeover
    val confirmingContinue: Boolean get() = confirmingAction == ActiveAction.Continue || confirmingAction == ActiveAction.CheckContinuation

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
        activeAction = ActiveAction.Takeover
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
                activeAction = null
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
        activeAction = ActiveAction.Continue
        activeSessionKey = session.key
        scope.launch {
            val receiptScope = api.receiptScope(session)
            try {
                val existingOpId = receiptStore.get(receiptScope, session.key)
                if (existingOpId != null) {
                    activeAction = ActiveAction.CheckContinuation
                    val result = api.continuationOperation(session, existingOpId)
                    handleContinuationResult(receiptScope, session, result)
                    return@launch
                }
                val info = api.takeoverInfo(session)
                if (!info.canResume) {
                    pending = Pending.Continuation(session, info)
                } else {
                    executeContinuation(api, session, info)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                reportContinuationFailure(receiptScope, session, error)
            } finally {
                busy = false
                activeAction = null
                if (pending == null) activeSessionKey = null
            }
        }
    }

    /** Reconcile an unconfirmed continuation start against `GET /api/v1/ops/{id}` without starting a second session. */
    fun checkContinuation(session: Session) {
        if (inFlight) return
        val api = client ?: run {
            reportFailure(UnsupportedOperationException("Codex continuation unavailable")) { checkContinuation(session) }
            return
        }
        busy = true
        activeAction = ActiveAction.CheckContinuation
        activeSessionKey = session.key
        scope.launch {
            val receiptScope = api.receiptScope(session)
            try {
                val opId = receiptStore.get(receiptScope, session.key)
                if (opId == null) {
                    if (!session.canContinueCodex) return@launch
                    activeAction = ActiveAction.Continue
                    val info = api.takeoverInfo(session)
                    if (!info.canResume) pending = Pending.Continuation(session, info)
                    else executeContinuation(api, session, info)
                } else {
                    val result = api.continuationOperation(session, opId)
                    handleContinuationResult(receiptScope, session, result)
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                val message = operationFeedback(error).text.resolve(res)
                onFailure(message, res.getString(R.string.chat_takeover_check_status)) { checkContinuation(session) }
            } finally {
                busy = false
                activeAction = null
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
        confirmingAction = when (current) {
            is Pending.Takeover -> ActiveAction.Takeover
            is Pending.Continuation -> ActiveAction.Continue
        }
        activeSessionKey = current.session.key
        scope.launch {
            val receiptScope = api.receiptScope(current.session)
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
                when (current) {
                    is Pending.Takeover -> reportFailure(error) { takeOver(current.session) }
                    is Pending.Continuation -> reportContinuationFailure(receiptScope, current.session, error)
                }
            } finally {
                confirmingAction = null
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
        confirmingAction = ActiveAction.Continue
        activeSessionKey = current.session.key
        scope.launch {
            val receiptScope = api.receiptScope(current.session)
            try {
                executeContinuation(api, current.session, current.info)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Exception) {
                pending = null
                activeSessionKey = null
                reportContinuationFailure(receiptScope, current.session, error)
            } finally {
                confirmingAction = null
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
        val receiptScope = api.receiptScope(session)
        val existingOpId = receiptStore.get(receiptScope, session.key)
        val result = if (existingOpId != null) {
            if (confirmingAction == ActiveAction.Continue) confirmingAction = ActiveAction.CheckContinuation
            api.continuationOperation(session, existingOpId)
        } else {
            val opId = newOperationId()
            // Persist durably before sending so any lost response or restart reconciles against /ops/{opId}.
            receiptStore.put(receiptScope, session.key, opId)
            api.startContinuation(session, info, opId)
        }
        handleContinuationResult(receiptScope, session, result)
    }

    private suspend fun handleContinuationResult(receiptScope: String, session: Session, result: AgentStartResult) {
        when (result) {
            is AgentStartResult.Created -> {
                receiptStore.remove(receiptScope, session.key)
                pending = null
                activeSessionKey = null
                onAdopted(session, result.session)
                onSuccessToast(res.getString(R.string.chat_takeover_continued))
            }
            is AgentStartResult.Rejected -> {
                receiptStore.remove(receiptScope, session.key)
                pending = null
                activeSessionKey = null
                val message = result.message.resolve(res)
                val retryLabel = res.getString(persona.core.designsystem.R.string.designsystem_retry)
                onFailure(message, retryLabel) { continueInNewSession(session) }
            }
            AgentStartResult.Pending -> {
                pending = null
                activeSessionKey = null
                onFailure(
                    res.getString(R.string.chat_takeover_continue_pending),
                    res.getString(R.string.chat_takeover_check_status),
                ) { checkContinuation(session) }
            }
            AgentStartResult.Unknown -> {
                pending = null
                activeSessionKey = null
                onFailure(
                    res.getString(R.string.chat_takeover_continue_unknown),
                    res.getString(R.string.chat_takeover_check_status),
                ) { checkContinuation(session) }
            }
        }
    }

    private suspend fun reportContinuationFailure(receiptScope: String, session: Session, error: Throwable) {
        val message = operationFeedback(error).text.resolve(res)
        if (receiptStore.get(receiptScope, session.key) != null) {
            onFailure(message, res.getString(R.string.chat_takeover_check_status)) { checkContinuation(session) }
        } else {
            onFailure(message, res.getString(persona.core.designsystem.R.string.designsystem_retry)) { continueInNewSession(session) }
        }
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
    /** Optional feedback target for success toasts (e.g. outer window when a full-screen preview closes on completion). */
    successFeedback: AppFeedback? = null,
    receiptStore: ContinuationReceiptStore? = null,
): CodexTakeoverCoordinator {
    val context = LocalContext.current
    val res = LocalResources.current
    val scope = rememberCoroutineScope()
    val feedback = LocalAppFeedback.current
    val store = remember(context, receiptStore) { receiptStore ?: PersistentContinuationReceiptStore.of(context) }
    val openSessionState = rememberUpdatedState(onOpenSession)
    val completedState = rememberUpdatedState(onCompleted)
    val feedbackState = rememberUpdatedState(feedback)
    val successFeedbackState = rememberUpdatedState(successFeedback ?: feedback)
    val fallbackToastState = rememberUpdatedState(fallbackToast)
    val fallbackNoticeState = rememberUpdatedState(onFallbackNotice)
    return remember(res, client, sessions, store) {
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
                try { sessions.refresh() }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) { }
            },
            onFailure = { message, actionLabel, retry ->
                val action = FeedbackAction(actionLabel, retry)
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
                val shown = successFeedbackState.value?.notice(message, FeedbackStyle.Success) == true
                if (!shown) {
                    val localToast = fallbackToastState.value
                    if (localToast != null) {
                        scope.launch { localToast.show(FeedbackNotice(message, style = FeedbackStyle.Success)) }
                    } else {
                        fallbackNoticeState.value?.invoke(message)
                    }
                }
            },
            receiptStore = store,
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
            confirmLoading = coordinator.confirmingTakeover,
            dismissEnabled = !coordinator.confirming,
        ) {
            JTextButton(
                text = stringResource(R.string.chat_takeover_continue_instead),
                onClick = coordinator::continueFromPending,
                enabled = !coordinator.confirming,
                loading = coordinator.confirmingContinue,
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
                confirmLoading = coordinator.confirmingContinue,
                dismissEnabled = !coordinator.confirming,
            )
        }
        null -> Unit
    }
}

/**
 * Visible non-blocking progress pill shown on list pages (Agents / Space) after a long-press menu closes while
 * takeover, continuation, or receipt reconciliation runs (Super Rule 5).
 */
@Composable
fun CodexTakeoverBusyPill(
    coordinator: CodexTakeoverCoordinator,
    sessions: List<Session>,
    modifier: Modifier = Modifier,
) {
    if (!coordinator.busy || coordinator.pending != null) return
    val actionLabel = stringResource(
        when (coordinator.activeAction) {
            CodexTakeoverCoordinator.ActiveAction.Continue -> R.string.chat_takeover_continuing
            CodexTakeoverCoordinator.ActiveAction.CheckContinuation -> R.string.chat_takeover_checking
            CodexTakeoverCoordinator.ActiveAction.Takeover, null -> R.string.chat_takeover_progress
        },
    )
    val title = coordinator.activeSessionKey?.let { key -> sessions.firstOrNull { it.key == key }?.displayTitle }
    val label = if (!title.isNullOrBlank()) "$actionLabel · $title" else actionLabel
    val palette = PersonaTheme.palette
    val shape = PersonaTheme.shapes.panelShape
    Row(
        modifier
            .testTag("codex-takeover-progress")
            .clip(shape)
            .background(palette.surfaceOpaque)
            .border(Tokens.Size.line, palette.line, shape)
            .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
    ) {
        JSpinner(Modifier.size(Tokens.Size.iconXS), color = palette.accent)
        Text(
            label,
            style = PersonaTheme.type.caption,
            color = palette.ink,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
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
