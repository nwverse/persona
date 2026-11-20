package persona.feature.chat

import android.content.Context
import android.content.SharedPreferences
import android.os.SystemClock
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.*
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONArray
import persona.core.contracts.AgentState
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.sort.shownWorker

/** The app mounts the normal chat with the selected member's own paired services and draft. */
class TeamChatHost(val scope: String, val requested: SessionKey?, val member: @Composable (Session, () -> Unit) -> Unit)

internal data class TeamControl(val open: Boolean, val attention: Boolean, val toggle: () -> Unit)

private const val GoneMillis = 4_000L
private const val RecordLimit = 50

internal fun revealTeam(count: Int, previous: Int?, wide: Boolean, closed: Boolean, asked: Boolean): Boolean =
    count > 0 && (asked || wide && (previous == 0 || previous == null && !closed))

/** Keep the last 50 explicit decisions, including the pairing in the lead key. */
private class TeamRecords(private val preferences: SharedPreferences) {
    private fun entries(): List<String> {
        val records = runCatching { JSONArray(preferences.getString("leads", "[]")) }.getOrNull() ?: return emptyList()
        return (0 until records.length()).map { records.optString(it) }
    }
    fun closed(lead: String) = "0$lead" in entries()
    fun save(lead: String, open: Boolean) {
        val next = entries().filterNot { it.drop(1) == lead } + ((if (open) "1" else "0") + lead)
        preferences.edit().putString("leads", JSONArray(next.takeLast(RecordLimit)).toString()).apply()
    }
}

/** Members picked in this process, per lead as on Web; an unrelated pairing may reuse both ids. */
private val lastWorkers = LinkedHashMap<String, String>()
private fun picked(lead: String, worker: String) {
    lastWorkers.remove(lead)
    lastWorkers[lead] = worker
    if (lastWorkers.size > RecordLimit) lastWorkers.remove(lastWorkers.keys.first())
}

@Composable
internal fun workerTitle(worker: Session): String =
    listOf(worker.title, worker.agentName, worker.folderName).firstOrNull {
        !it.isNullOrBlank() && it != "Claude Code" && it != worker.sessionId && it != worker.paneId
    } ?: stringResource(R.string.chat_team_worker)

@Composable
internal fun TeamChatLayout(key: SessionKey, parent: Session?, workers: List<Session>, host: TeamChatHost,
    covered: Boolean, requestId: String?, lead: @Composable (TeamControl?, SessionKey?, Boolean) -> Unit) {
    val context = LocalContext.current.applicationContext
    val records = remember(context) { TeamRecords(context.getSharedPreferences("persona.team.panel", Context.MODE_PRIVATE)) }
    val leadId = remember(host.scope, key) { JSONArray(listOf(host.scope, key.machineId, key.sessionId)).toString() }
    val wide = LocalWindowLayout.current.expanded
    var open by rememberSaveable(leadId) { mutableStateOf(false) }
    var chosen by rememberSaveable(leadId) { mutableStateOf(lastWorkers[leadId]) }
    var requested by rememberSaveable(leadId, host.requested?.path, requestId) { mutableStateOf<String?>(null) }
    var previous by rememberSaveable(leadId) { mutableStateOf<Int?>(null) }
    var arrived by remember(leadId) { mutableLongStateOf(SystemClock.elapsedRealtime()) }
    var handled by rememberSaveable(leadId, host.requested?.path, requestId) { mutableStateOf(false) }
    val count = parent?.children?.workers ?: 0
    val granted = host.requested?.takeIf { it.machineId == key.machineId && !handled }
    LaunchedEffect(leadId, parent != null, count, host.requested, requestId) {
        if (parent == null) return@LaunchedEffect
        val now = SystemClock.elapsedRealtime()
        if (previous == null) arrived = now
        if (count == 0 && (previous ?: 0) > 0) {
            delay(GoneMillis)
            open = false
            previous = 0
            arrived = 0
            return@LaunchedEffect
        }
        // A summary can arrive before its rollup. An initial zero is not yet a new team starting.
        val before = previous.takeUnless { it == 0 && now - arrived < GoneMillis }
        if (revealTeam(count, before, wide, records.closed(leadId), granted != null)) {
            open = true
            records.save(leadId, true)
            granted?.let { requested = it.sessionId; handled = true }
        }
        previous = count
    }
    fun toggle() { open = !open; records.save(leadId, open) }
    val shown = shownWorker(workers, requested, chosen)
    LaunchedEffect(shown?.key, requested) {
        if (shown != null && shown.sessionId == requested) {
            chosen = shown.sessionId
            picked(leadId, shown.sessionId)
            requested = null
        }
    }
    val panelVisible = open && !covered
    val control = if (count > 0) TeamControl(open, !open && workers.any { it.agentState == AgentState.NeedsYou }, ::toggle) else null
    val renderLead by rememberUpdatedState(lead)
    val leadPage = remember(leadId) {
        movableContentOf { button: TeamControl?, worker: SessionKey?, hidden: Boolean -> renderLead(button, worker, hidden) }
    }
    if (!panelVisible) {
        leadPage(control, null, false)
        return
    }
    val panel: @Composable () -> Unit = {
        CompositionLocalProvider(LocalSplitDetail provides null, LocalHomeChrome provides null, LocalRootLeading provides null) {
            TeamPanel(workers, shown, { requested = null; chosen = it.sessionId; picked(leadId, it.sessionId) }, ::toggle, host.member)
        }
    }
    BackHandler { toggle() }
    Box(Modifier.fillMaxSize()) {
        Row(Modifier.fillMaxSize()) {
            Box(Modifier.weight(1f)) { leadPage(control, shown?.key, !wide) }
            if (wide) {
                Spacer(Modifier.fillMaxHeight().width(Tokens.Size.line).background(PersonaTheme.palette.line))
                Box(Modifier.weight(1f)) { panel() }
            }
        }
        if (!wide) panel()
    }
}

@Composable
internal fun TeamButton(control: TeamControl) {
    val label = stringResource(R.string.chat_team)
    val description = if (control.attention) stringResource(R.string.chat_team_needs_you) else label
    JIconButton(PersonaIcons.subagent, description, control.toggle, Modifier.testTag("chat-team-button"),
        badge = control.attention, selected = control.open)
}

@Composable
private fun TeamPanel(workers: List<Session>, shown: Session?, pick: (Session) -> Unit, close: () -> Unit,
    member: @Composable (Session, () -> Unit) -> Unit) {
    val palette = PersonaTheme.palette
    Column(Modifier.fillMaxSize().background(palette.background).statusBarsPadding().testTag("chat-team-panel").onPreviewKeyEvent {
        if (it.type == KeyEventType.KeyDown && it.key == Key.Escape) { close(); true } else false
    }) {
        Row(Modifier.fillMaxWidth().padding(start = Tokens.Space.m, end = Tokens.Space.s),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JIcon(PersonaIcons.subagent, null)
            Column(Modifier.weight(1f).padding(vertical = Tokens.Space.s)) {
                Text(stringResource(R.string.chat_team), style = PersonaTheme.type.headline, color = palette.ink)
                if (workers.isNotEmpty()) Text(stringResource(R.string.chat_team_summary, workers.size,
                    workers.count { it.agentState == AgentState.Working }), style = PersonaTheme.type.caption, color = palette.ink2)
            }
            JIconButton(PersonaIcons.close, stringResource(R.string.chat_team_close), close, Modifier.testTag("chat-team-close"))
        }
        if (workers.isNotEmpty()) WorkerStrip(workers, shown, pick)
        JDivider()
        if (shown == null) Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text(stringResource(R.string.chat_team_waiting), color = palette.ink2, style = PersonaTheme.type.body)
        } else key(shown.key) { member(shown, close) }
    }
}

@Composable
private fun WorkerStrip(workers: List<Session>, shown: Session?, pick: (Session) -> Unit) {
    val list = rememberLazyListState()
    val scope = rememberCoroutineScope()
    val focus = remember { mutableStateMapOf<SessionKey, FocusRequester>() }
    var focusAfterScroll by remember { mutableStateOf<SessionKey?>(null) }
    val index = workers.indexOfFirst { it.key == shown?.key }
    LaunchedEffect(shown?.key, index, focusAfterScroll) {
        if (index < 0) return@LaunchedEffect
        list.animateScrollToItem(index)
        if (focusAfterScroll == shown?.key) {
            focus[shown?.key]?.requestFocus()
            focusAfterScroll = null
        }
    }
    val stripLabel = stringResource(R.string.chat_team_workers)
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        if (list.canScrollBackward) JIconButton(PersonaIcons.back, stringResource(R.string.chat_team_earlier),
            { scope.launch { list.animateScrollToItem((list.firstVisibleItemIndex - 1).coerceAtLeast(0)) } })
        LazyRow(Modifier.weight(1f).selectableGroup().semantics { contentDescription = stripLabel }, state = list,
            contentPadding = PaddingValues(horizontal = Tokens.Space.s), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            itemsIndexed(workers, key = { _, row -> row.key.path }) { at, row ->
                val title = workerTitle(row)
                val status = Status.entries.firstOrNull { it.name == row.agentState.name } ?: Status.Unknown
                val spoken = stringResource(R.string.chat_team_member, title, status.localizedLabel)
                val requester = remember(row.key) { FocusRequester() }
                DisposableEffect(row.key) { focus[row.key] = requester; onDispose { focus.remove(row.key) } }
                JChip(title, Modifier.widthIn(max = ReadingWidth / 3).focusRequester(requester)
                    .semantics { contentDescription = spoken; selected = shown?.key == row.key }.onPreviewKeyEvent {
                        val next = when (it.key) { Key.DirectionLeft -> at - 1; Key.DirectionRight -> at + 1
                            Key.MoveHome -> 0; Key.MoveEnd -> workers.lastIndex; else -> return@onPreviewKeyEvent false }
                        if (it.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                        val target = workers[next.coerceIn(workers.indices)]
                        focusAfterScroll = target.key
                        pick(target)
                        true
                    }, selected = shown?.key == row.key, onClick = { pick(row) }, status = status, role = Role.Tab)
            }
        }
        if (list.canScrollForward) JIconButton(PersonaIcons.forward, stringResource(R.string.chat_team_more),
            { scope.launch { list.animateScrollToItem((list.firstVisibleItemIndex + 1).coerceAtMost(workers.lastIndex)) } })
    }
}
