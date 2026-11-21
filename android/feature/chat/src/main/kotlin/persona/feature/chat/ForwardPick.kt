package persona.feature.chat

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.text.style.TextAlign
import persona.core.contracts.Item
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcon
import persona.core.domain.lines.ChatTurn
import persona.core.domain.lines.WorkItem

internal enum class PickAction { Forward, Copy, Export }

/** The transcript's own turns include intermediate narration and consecutive unanswered questions. */
internal fun pickQuestions(turns: List<ChatTurn>): Map<String, List<MessageTarget>> = buildMap {
    val waiting = mutableListOf<MessageTarget>()
    for (turn in turns) {
        for (row in turn.users) {
            val line = row.item ?: continue
            val item = line.item
            val raw = item.text.orEmpty()
            if (item.role != "user" || line.injected != null || item.from != null || item.source != null || isLoopDraft(raw)) continue
            val text = messageModel(raw, user = true).plain
            if (text.isNotBlank()) waiting += MessageTarget(item.id, text, true, raw = raw, time = item.ts, cut = item.id.takeIf { item.fullSize != null })
        }
        val replies = (turn.process + listOfNotNull(turn.answer)).flatMap { row ->
            if (row.type == "reply") listOfNotNull(row.item?.id) else row.steps.filterIsInstance<WorkItem.Narration>().map { it.item.id }
        }
        if (replies.isNotEmpty() && waiting.isNotEmpty()) {
            val questions = waiting.toList()
            replies.forEach { put(it, questions) }
            waiting.clear()
        }
    }
}

/** One selection and timeline order shared by forwarding, clipboard and picture export. */
@Stable
internal class ForwardPick {
    var selected by mutableStateOf<Map<String, MessageTarget>?>(null)
        private set
    var sheet by mutableStateOf<List<MessageTarget>?>(null)
        private set
    var action by mutableStateOf(PickAction.Forward)
        private set
    var refused by mutableStateOf(false)
        private set
    var include by mutableStateOf(true)
        private set
    var readQuestions: () -> Map<String, List<MessageTarget>> = { emptyMap() }
    var readInclude: () -> Boolean = { true }
    var saveInclude: (Boolean) -> Unit = {}
    private var questions by mutableStateOf<Map<String, List<MessageTarget>>>(emptyMap())

    private fun brought(picks: Map<String, MessageTarget>) =
        picks.keys.flatMap { questions[it].orEmpty() }.associateBy { it.id } - picks.keys

    private val additional by derivedStateOf { selected?.let(::brought).orEmpty() }
    fun isBrought(id: String) = include && id in additional
    val offersQuestions get() = additional.isNotEmpty()

    fun open(target: MessageTarget) { action = PickAction.Forward; sheet = listOf(target) }
    fun dismiss() { sheet = null }

    fun start() { sheet?.singleOrNull()?.let(::start) }
    fun start(first: MessageTarget) {
        questions = readQuestions()
        val picks = mapOf(first.id to first)
        selected = picks
        val wanted = readInclude()
        refused = wanted && picks.size + brought(picks).size > ForwardMessage.MOST
        include = wanted && !refused
        dismiss()
    }

    fun setQuestions(on: Boolean) {
        val picks = selected ?: return
        questions = readQuestions()
        refused = on && picks.size + brought(picks).size > ForwardMessage.MOST
        if (!refused) {
            include = on
            saveInclude(on)
        }
    }

    fun toggle(target: MessageTarget) {
        val current = selected ?: return
        questions = readQuestions()
        val removing = target.id in current
        val next = if (removing) current - target.id else current + (target.id to target)
        refused = !removing && next.size + (if (include) brought(next).size else 0) > ForwardMessage.MOST
        if (!refused) selected = next
    }

    fun choose(timeline: List<Item>, operation: PickAction = PickAction.Forward) {
        val current = selected ?: return
        questions = readQuestions()
        val going = if (include) brought(current) + current else current
        val inOrder = timeline.sortedWith(chatItemOrder).mapNotNull { going[it.id] }.distinctBy { it.id }
        refused = inOrder.size > ForwardMessage.MOST
        if (inOrder.isNotEmpty() && !refused) {
            action = operation
            sheet = inOrder
        }
    }

    fun end() {
        dismiss()
        selected = null
        questions = emptyMap()
        refused = false
    }
}

/** While selecting, the row owns the tap and TalkBack action, including over links and buttons inside the message. */
@Composable
internal fun PickableMessageColumn(target: MessageTarget?, modifier: Modifier, verticalArrangement: Arrangement.Vertical = Arrangement.Top,
    content: @Composable ColumnScope.() -> Unit) {
    val pick = LocalChatRowActions.current?.menu?.forwardPick
    val selected = pick?.selected
    if (pick == null || selected == null || target == null || target.pending != null || target.text.isBlank()) {
        Column(modifier, verticalArrangement = verticalArrangement, content = content)
        return
    }
    val checked = target.id in selected
    val brought = pick.isBrought(target.id)
    val included = stringResource(R.string.chat_pick_included)
    Box(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth().heightIn(min = Tokens.MinTouch.android).clearAndSetSemantics {}, verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JCheckMark(when { checked -> ToggleableState.On; brought -> ToggleableState.Indeterminate; else -> ToggleableState.Off })
            Column(Modifier.weight(1f).then(modifier), verticalArrangement = verticalArrangement, content = content)
        }
        Box(Modifier.matchParentSize().testTag("forward-pick-" + target.id)
            .toggleable(checked, role = Role.Checkbox, onValueChange = { pick.toggle(target) })
            .semantics { contentDescription = target.text.take(200); if (brought) stateDescription = included })
    }
}

@Composable
internal fun ForwardPickFooter(pick: ForwardPick, timeline: List<Item>, modifier: Modifier = Modifier) {
    val count = pick.selected?.size ?: return
    BackHandler(enabled = pick.sheet == null, onBack = pick::end)
    Column(modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s)) {
        if (pick.refused) Text(stringResource(R.string.chat_forward_select_limit, ForwardMessage.MOST),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween,
            itemVerticalAlignment = Alignment.CenterVertically) {
            JTextButton(stringResource(R.string.chat_dialog_cancel), pick::end)
            Text(stringResource(R.string.chat_forward_selected, count), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.ink2, textAlign = TextAlign.Center)
            if (pick.offersQuestions) Row(Modifier.testTag("pick-questions").heightIn(min = Tokens.MinTouch.android)
                .toggleable(pick.include, role = Role.Switch, onValueChange = pick::setQuestions),
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                Text(stringResource(R.string.chat_pick_questions), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink)
                JSwitch(pick.include, null)
            }
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            val actionModifier = Modifier.weight(1f)
            if (LocalChatForwarding.current != null) PickButton(stringResource(R.string.chat_menu_forward), PersonaIcons.forwardMessage,
                actionModifier.testTag("forward-picked"), count > 0) { pick.choose(timeline) }
            PickButton(stringResource(R.string.chat_menu_copy), PersonaIcons.copy, actionModifier.testTag("copy-picked"), count > 0) {
                pick.choose(timeline, PickAction.Copy)
            }
            PickButton(stringResource(R.string.chat_pick_export), PersonaIcons.photo, actionModifier.testTag("export-picked"), count > 0) {
                pick.choose(timeline, PickAction.Export)
            }
        }
    }
}

@Composable
private fun PickButton(label: String, icon: PhIcon, modifier: Modifier, enabled: Boolean, click: () -> Unit) {
    val color = if (enabled) PersonaTheme.palette.ink else PersonaTheme.palette.ink3
    Column(modifier.heightIn(min = Tokens.MinTouch.android).clickable(enabled = enabled, role = Role.Button, onClick = click)
        .padding(vertical = Tokens.Space.xs), horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center) {
        JIcon(icon, null, size = Tokens.Size.iconXS, tint = color)
        Text(label, style = PersonaTheme.type.caption, color = color, textAlign = TextAlign.Center)
    }
}
