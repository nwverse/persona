package persona.feature.chat

import persona.core.designsystem.text
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import persona.core.chatlist.ChatListState
import persona.core.chatlist.beginRecordNavigation
import persona.core.chatlist.scrollToRecord
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.TaskIndex
import persona.core.domain.lines.TaskProgressItem
import persona.core.domain.lines.TaskProgressStatus
import persona.core.contracts.Item

private data class TaskReturnPosition(val rowId: String, val offset: Int, val expanded: Set<String>)

/** Where the chat is asked to go: the record of a task on the progress page, or the call a task's notice came from. */
private fun interface RecordDestination { fun resolve(lines: List<ChatLine>): TaskRecordTarget? }

/** The call a notice [notice] asks for. A class, not the id: the same call asked for again is a new request. */
internal class CallJump(val toolUseId: String, val notice: Item)

/** A task's notice asking for the call that started it ("Jump to the step that started it"), taken by [ChatTasks]. */
@Stable
internal class ChatCallJumps {
    var pending by mutableStateOf<CallJump?>(null)
        private set
    fun jump(toolUseId: String, notice: Item) { pending = CallJump(toolUseId, notice) }
    internal fun take(): CallJump? = pending.also { pending = null }
}

/** One chat-header attachment owns the full page and its return affordance. */
@Composable
internal fun ChatTasks(
    index: TaskIndex,
    lines: List<ChatLine>,
    chatList: ChatListState,
    onFreeze: () -> Unit,
    onVisibilityChanged: (Boolean) -> Unit,
    onExpandFold: (Item) -> Unit,
    foldErrors: Map<String, String>,
    /** False while the link card peeks and leaves the conversation four tenths: the bar goes, its page and state stay. */
    showBar: Boolean = true,
    /** Notices' jumps to their calls: the same way there and the same "Back" as the progress page's record link. */
    callJumps: ChatCallJumps? = null,
) {
    var open by rememberSaveable { mutableStateOf(false) }
    var target by remember { mutableStateOf<RecordDestination?>(null) }
    var origin by remember { mutableStateOf<TaskReturnPosition?>(null) }
    var returning by remember { mutableStateOf(false) }
    var notice by remember { mutableStateOf<String?>(null) }
    val resources = LocalResources.current
    val visible = open || target != null || returning
    val visibilityChanged by rememberUpdatedState(onVisibilityChanged)
    LaunchedEffect(visible) { visibilityChanged(visible) }
    DisposableEffect(Unit) { onDispose { visibilityChanged(false) } }

    LaunchedEffect(target, lines, returning, foldErrors) {
        if (returning) {
            val saved = origin ?: return@LaunchedEffect
            if (chatList.position.expanded != saved.expanded) {
                chatList.setExpanded(saved.expanded)
                return@LaunchedEffect
            }
            if (!chatList.scrollToRecord(saved.rowId, saved.offset)) notice = resources.getString(R.string.chat_tasks_origin_gone)
            returning = false
            origin = null
        } else {
            val destination = (target ?: return@LaunchedEffect).resolve(lines)
            when {
                destination?.expandFold != null -> {
                    if (destination.expandFold.id in foldErrors) {
                        notice = resources.getString(R.string.chat_tasks_load_failed)
                        target = null
                    } else onExpandFold(destination.expandFold)
                }
                destination?.rowId != null -> {
                    if (!chatList.scrollToRecord(destination.rowId)) notice = resources.getString(R.string.chat_tasks_record_gone)
                    target = null
                }
                destination?.expandGroup != null -> chatList.setExpanded(chatList.position.expanded + destination.expandGroup)
                else -> { notice = resources.getString(R.string.chat_tasks_record_gone); target = null }
            }
        }
    }
    if (showBar && index.progress.isNotEmpty()) TaskProgressBar(index.progress) { open = true }
    if (origin != null) {
        JTextButton(stringResource(R.string.chat_tasks_return), { target = null; returning = true },
            enabled = !returning, icon = PhIcons.ArrowLeft,
            modifier = Modifier.padding(horizontal = Tokens.Space.m))
    }
    notice?.let { Text(it, Modifier.padding(horizontal = Tokens.Space.m), style = PersonaTheme.type.caption,
        color = PersonaTheme.palette.ink2) }

    fun go(destination: RecordDestination) {
        if (origin == null) {
            val row = chatList.list.layoutInfo.visibleItemsInfo.firstOrNull { it.index == chatList.list.firstVisibleItemIndex }
            (row?.key as? String)?.let { origin = TaskReturnPosition(it,
                chatList.list.firstVisibleItemScrollOffset, chatList.position.expanded) }
        }
        chatList.beginRecordNavigation()
        onFreeze()
        notice = null
        // A jump while "Back" is still on its way keeps the first origin and its "Back".
        returning = false
        target = destination
    }
    val call = callJumps?.pending
    LaunchedEffect(call) {
        val jump = callJumps?.take() ?: return@LaunchedEffect
        go { lines -> callRecordTarget(lines, jump.toolUseId, jump.notice) }
    }

    val closeTasks = { open = false }
    if (open) JFullScreen(closeTasks) {
        TaskProgressPage(index.progress, onBack = closeTasks, onRecord = { task ->
            go { lines -> taskRecordTarget(lines, task.sourceItemId, task.sourceSeq) }
            open = false
        })
    }
}

@Composable
private fun TaskProgressBar(tasks: List<TaskProgressItem>, onClick: () -> Unit) {
    val completed = tasks.count { it.status == TaskProgressStatus.Completed }
    val active = tasks.count { it.status == TaskProgressStatus.InProgress }
    Row(Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m), verticalAlignment = Alignment.CenterVertically) {
        JChip(stringResource(R.string.chat_tasks_bar, completed, tasks.size, active), Modifier.weight(1f),
            onClick = onClick, icon = PhIcons.ListChecks)
        JIconButton(PhIcons.CaretRight, stringResource(R.string.chat_tasks_view_all), onClick)
    }
}

@Composable
internal fun TaskProgressPage(tasks: List<TaskProgressItem>, onBack: () -> Unit, onRecord: (TaskProgressItem) -> Unit) {
    JListPage(title = stringResource(R.string.chat_tasks_title), onBack = onBack) {
        item {
            val completed = tasks.count { it.status == TaskProgressStatus.Completed }
            Text(stringResource(R.string.chat_tasks_done_count, completed, tasks.size), Modifier.padding(vertical = Tokens.Space.m),
                style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
        }
        if (tasks.isEmpty()) item { JEmptyState(stringResource(R.string.chat_tasks_empty_title), stringResource(R.string.chat_tasks_empty_text)) }
        itemsIndexed(tasks, key = { _, task -> task.id }) { index, task ->
            JGroupItem(GroupPosition.of(index, tasks.size)) { TaskProgressRow(task) { onRecord(task) } }
        }
        item {
            Text(stringResource(R.string.chat_tasks_note), Modifier.padding(vertical = Tokens.Space.m),
                style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        }
    }
}

@Composable
private fun TaskProgressRow(task: TaskProgressItem, onRecord: () -> Unit) {
    val status = when (task.status) {
        TaskProgressStatus.Completed -> Status.Unread
        TaskProgressStatus.InProgress -> Status.Working
        TaskProgressStatus.Pending -> Status.Idle
        TaskProgressStatus.Unknown -> Status.Unknown
    }
    val label = stringResource(when (task.status) {
        TaskProgressStatus.Pending -> R.string.chat_tasks_pending
        TaskProgressStatus.InProgress -> R.string.chat_tasks_in_progress
        TaskProgressStatus.Completed -> R.string.chat_tasks_completed
        TaskProgressStatus.Unknown -> R.string.chat_tasks_unknown
    })
    JRow(task.subject.text(), subtitle = task.description?.takeIf(String::isNotBlank),
        leading = { JStatusMark(status) }, value = label, subtitleLines = Int.MAX_VALUE)
    JTextButton(stringResource(R.string.chat_tasks_view_record), onRecord, Modifier.padding(horizontal = Tokens.Space.s))
}
