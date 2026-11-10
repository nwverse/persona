package persona.feature.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.res.stringResource
import persona.core.contracts.Todo
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JNotice
import persona.core.designsystem.Status

/** A receipt outside details opens the task; inside those details it opens the task's Agent. */
@Composable
internal fun TaskReceiptLook(todo: Todo, host: AssistantTaskHost) {
    val unavailable = stringResource(R.string.chat_task_detail_agent_gone)
    var missing by remember(todo) { mutableStateOf(false) }
    JButton(stringResource(R.string.chat_tasks_look), {
        if (host.sheet.value != TaskSheet.Task(todo.id)) {
            openTask(host, todo)
        } else {
            // The durable binding remains navigable when the Agent is outside the session list or offline.
            val target = host.tasks.lead(todo, host.sessions)?.key
            if (target == null) {
                missing = true
            } else {
                host.sheet.value = null
                host.onOpen(target)
            }
        }
    }, kind = JButtonKind.Secondary, size = JButtonSize.Small)
    if (missing) JNotice(unavailable, status = Status.NeedsYou)
}
