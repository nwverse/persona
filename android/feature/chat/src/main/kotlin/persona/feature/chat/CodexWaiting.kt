package persona.feature.chat

import android.content.res.Resources
import androidx.compose.runtime.*
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.withTimeoutOrNull
import persona.core.contracts.AgentState
import persona.core.contracts.Session
import persona.core.contracts.SessionsListEvent
import persona.core.contracts.api.ChatActions
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection

/** Follow only the same computer's pane, never a new thread inferred from its folder. */
internal fun codexThreadOf(rows: List<Session>, waiting: Session): Session? {
    val pane = waiting.paneId?.takeIf(String::isNotBlank) ?: return null
    if (!waiting.awaitingFirstMessage) return null
    return rows.firstOrNull {
        it.machineId == waiting.machineId && it.paneId == pane && it.sessionId != waiting.sessionId &&
            !it.awaitingFirstMessage && it.agent == "codex" && it.agentState != AgentState.Offline
    }
}

/** The bounded list may exclude the new thread. Look outside its filter only while this waiting page is visible. */
@Composable
internal fun FollowWaitingCodex(
    waiting: Session?, listed: Boolean, actions: ChatActions?, connection: HubConnection?,
    onThread: suspend (Session) -> Unit,
) {
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val found by rememberUpdatedState(onThread)
    LaunchedEffect(waiting?.key, listed, actions, connection, lifecycle) {
        if (waiting?.awaitingFirstMessage != true || listed || actions == null) return@LaunchedEffect
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            suspend fun check() {
                try { withTimeoutOrNull(15_000) { actions.codexThread(waiting) }?.let { found(it) } }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) { /* A later list hint or reconnect checks again; input is kept. */ }
            }
            check()
            connection?.let { hub ->
                merge(hub.events.filterIsInstance<SessionsListEvent>().map { Unit },
                    hub.state.filter { it == ConnectionState.Online }.map { Unit })
                    .conflate().collect { check() }
            }
        }
    }
}

/** One empty-state note; it gives way to the first message and to view-only/error states. */
internal fun emptyChatHint(res: Resources, session: Session?, computer: String?, readOnly: Boolean): String = when {
    readOnly -> res.getString(R.string.chat_screen_empty_read_only)
    session?.awaitingFirstMessage == true -> res.getString(R.string.chat_codex_waiting,
        computer?.takeIf(String::isNotBlank) ?: res.getString(R.string.chat_codex_computer))
    else -> res.getString(R.string.chat_screen_empty_hint)
}
