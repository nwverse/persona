package persona.feature.chat

import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalResources
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.LocalHubFeatures
import persona.core.domain.lines.ChildSessionCards
import persona.core.domain.lines.LineItem
import persona.core.domain.lines.SubagentStatus
import persona.core.domain.lines.ToolCall
import persona.core.domain.sort.childSessions
import java.time.Duration

internal data class ChatChildren(val rows: Map<String, Session>, val repository: ChatRepository)
internal val LocalChatChildren = staticCompositionLocalOf<ChatChildren?> { null }

data class ChildSessionHome(val key: SessionKey, val childSessionId: String? = null)

internal fun childSessionHome(key: SessionKey, row: Session?): ChildSessionHome {
    val parent = row?.takeIf { it.key == key }?.foldedUnder?.takeIf { it.isNotBlank() && it != key.sessionId }
    return if (parent == null) ChildSessionHome(key) else ChildSessionHome(SessionKey(key.machineId, parent), key.sessionId)
}

/** Resolve before creating the draft editor and file/terminal actions: all of the page belongs to the parent. */
@Composable
fun rememberChildSessionHome(key: SessionKey, rows: List<Session>, actions: ChatActions?, features: Collection<String>): ChildSessionHome {
    val listed = rows.firstOrNull { it.key == key }
    var lookedUp by remember(key) { mutableStateOf<Session?>(null) }
    LaunchedEffect(key, listed != null, actions, features) {
        if (listed == null && "sessions_lookup" in features) {
            try { lookedUp = actions?.completeRow(Session(key.machineId, key.sessionId)) }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { /* Keep the direct link until metadata is available. */ }
        }
    }
    return childSessionHome(key, listed ?: lookedUp)
}

/** Bounded hubs keep children outside the root window; a failed refresh keeps the last confirmed cards. */
@Composable
internal fun rememberChatChildren(key: SessionKey, parent: Session?, rows: List<Session>, chrome: ChatChromeBindings,
    repository: ChatRepository): ChatChildren {
    val features = chrome.connection?.lastHello?.features ?: LocalHubFeatures.current
    var held by remember(key) { mutableStateOf(emptyList<Session>()) }
    val local = remember(rows, key) { childSessions(rows, key) }
    val missing = (parent?.children?.total ?: 0) > local.size
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(key, parent?.children, missing, features, chrome.actions, chrome.covered, lifecycle) {
        if (!missing) { held = emptyList(); return@LaunchedEffect }
        if (parent == null || "sessions_hierarchy" !in features || chrome.covered) return@LaunchedEffect
        lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            while (true) {
                try { held = chrome.actions?.childRows(parent) ?: held }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) { /* Preserve the last confirmed child rows. */ }
                delay(CHILD_REFRESH_MILLIS)
            }
        }
    }
    val children = remember(held, rows, key) { childSessions((rows + held).distinctBy { it.key }, key) }
    return ChatChildren(children.associateBy { it.sessionId }, repository)
}

private const val CHILD_REFRESH_MILLIS = 5_000L

/** The existing task card, with its child's own metadata and a transcript read only while open. */
@Composable
internal fun ChildSessionCard(sessionId: String, assistant: Boolean) {
    val children = LocalChatChildren.current ?: return
    val row = children.rows[sessionId] ?: return
    val callId = ChildSessionCards.call(sessionId)
    val folds = LocalSubagentFolds.current
    val opened = folds?.isOpen(callId) == true
    val source = remember(row.key, opened, children.repository) {
        if (opened) children.repository.observe(row.key) else flowOf(ChatSnapshot())
    }
    val snapshot by source.collectAsStateWithLifecycle(ChatSnapshot())
    val items = snapshot.items
    val result = items.lastOrNull { it.role == "assistant" && it.kind == "text" && !it.text.isNullOrBlank() }
    val prompt = items.firstOrNull { it.role == "user" && it.kind == "text" }?.text
    val lastTool = items.lastOrNull { it.kind == "tool_use" }?.toolName
    val title = persona.core.domain.lines.Subagents.compact(row.title, 96)
        .ifEmpty { LocalResources.current.getString(R.string.chat_subagent_untitled) }
    val status = ChildSessionCards.status(row)
    val agent = Subagent(sessionId, sessionId = sessionId, description = title, toolUseId = callId,
        running = status == SubagentStatus.Running, startedAt = row.startedAt, updatedAt = row.updatedAt,
        lastText = row.lastText, model = row.model, lastTool = lastTool?.let { WorkflowTool(name = it) })
    val use = Item(callId, role = "assistant", kind = "tool_use", toolName = "Agent", toolUseId = callId,
        toolInput = buildJsonObject { put("description", title); put("prompt", prompt.orEmpty()) })
    val words = result?.let { it.copy(kind = "tool_result") } ?: row.lastText?.let {
        Item("$callId-result", role = "user", kind = "tool_result", text = it)
    }
    val call = ToolCall(LineItem(use), words?.let(::LineItem), emptyList())
    val res = LocalResources.current
    val start = parseTime(row.startedAt)
    val end = parseTime(row.updatedAt)
    val took = if (status != SubagentStatus.Running && start != null && end != null && end >= start)
        workDuration(res, Duration.between(start, end).seconds.coerceAtLeast(1)) else null
    val effort = row.effort?.takeIf { it in setOf("minimal", "low", "medium", "high", "xhigh", "max", "ultra") }
        ?.let { SessionModes.effortLabel(it, res) }
    val model = listOfNotNull(persona.core.domain.modelShortName(row.model).ifEmpty { null }, effort).joinToString(" · ").ifEmpty { null }
    val harness = if (row.isClaude) res.getString(R.string.chat_subagent_harness_claude) else row.agentLabel
    val meta = SubagentCardText.Meta(if (assistant) null else harness, model, null, null, took)
    val actions = remember(row.key, children.repository) {
        ChatRowActions(row.key, fullText = { children.repository.fullText(row.key, it.id) }, openPath = null,
            photos = null, pictures = { emptyList() }, openImages = { _, _ -> }, menu = null, retryLineId = null, onRetry = {})
    }
    CompositionLocalProvider(LocalChatSubagents provides ChatSubagents(emptyMap(), null, mapOf(callId to agent)),
        LocalChatRowActions provides actions) {
        SubagentCard(call, live = status == SubagentStatus.Running, assistant = assistant, statusOverride = status, metaOverride = meta)
    }
}
