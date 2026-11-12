package persona.feature.chat

import android.content.res.Resources
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.CancellationException
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.Subagent
import persona.core.contracts.api.ChatRepository
import persona.core.designsystem.HueTintAlpha
import persona.core.designsystem.JSpinner
import persona.core.designsystem.JIcon
import persona.core.designsystem.JTextButton
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.WorkState
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.resolve
import persona.core.designsystem.workRail
import persona.core.designsystem.WorkStep as TimelineStep
import persona.core.domain.lines.LineItem
import persona.core.domain.lines.LineStore
import persona.core.domain.lines.SubagentStatus
import persona.core.domain.lines.Subagents
import persona.core.domain.lines.ToolCall

/**
 * Which subagent cards are open, by their call, while the chat is (iOS `openTools`): a card that joins a grid when the
 * next subagent starts, or scrolls away and back, stays as the reader left it.
 */
@Stable
internal class SubagentFolds {
    private val open = mutableStateMapOf<String, Boolean>()
    fun isOpen(call: String) = open[call] == true
    fun toggle(call: String) { open[call] = !isOpen(call) }
}

internal val LocalSubagentFolds = staticCompositionLocalOf<SubagentFolds?> { null }

/** What a subagent's card says under its title (iOS `SubagentCardText`): what it is doing, then how it went in how many steps. */
internal object SubagentCardText {
    /** "Running the tests… · 7 steps", "Starting…"; "Done · 7 steps · 1 min". */
    fun line(res: Resources, status: SubagentStatus, agent: Subagent?, took: Long?): String {
        val steps = Subagents.steps(agent)?.let { res.getQuantityString(R.plurals.chat_subagent_steps, it, it) }
        return when (status) {
            SubagentStatus.Running -> Subagents.doing(agent)?.let { listOfNotNull(it + "…", steps).joinToString(" · ") }
                ?: res.getString(R.string.chat_subagent_starting)
            else -> listOfNotNull(res.getString(when (status) {
                SubagentStatus.Failed -> R.string.chat_subagent_failed
                SubagentStatus.Stopped -> R.string.chat_subagent_stopped
                else -> R.string.chat_subagent_done
            }), steps, took?.let { Subagents.ran(it).resolve(res) }).joinToString(" · ")
        }
    }

    /** Over a grid (iOS `SubagentGridText.head`): "Running 3 agents" while any runs, "Ran 3 agents · 12 steps" after. */
    fun head(res: Resources, count: Int, running: Int, steps: Int): String {
        if (running > 0) return res.getQuantityString(R.plurals.chat_subagent_grid_running, running, running)
        val ran = res.getQuantityString(R.plurals.chat_subagent_grid_ran, count, count)
        return if (steps > 0) ran + " · " + res.getQuantityString(R.plurals.chat_subagent_grid_steps, steps, steps) else ran
    }
}

/** Its title: what the agent asked it to do (`description`), else what the session calls it. */
private fun subagentTitle(call: ToolCall, agent: Subagent?, fallback: String): String =
    call.use.item.toolInput.inputText("description")
        ?: agent?.description?.takeIf(String::isNotBlank) ?: agent?.type?.takeIf(String::isNotBlank) ?: fallback

private fun kotlinx.serialization.json.JsonElement?.inputText(key: String): String? =
    ((this as? kotlinx.serialization.json.JsonObject)?.get(key) as? kotlinx.serialization.json.JsonPrimitive)
        ?.takeIf { it.isString }?.content?.takeIf(String::isNotBlank)

/** The colour a card's state is drawn in (iOS `SubagentCard.tint`). */
@Composable
private fun SubagentStatus.tint(): Color {
    val palette = PersonaTheme.palette
    return when (this) {
        SubagentStatus.Running -> palette.working.dot
        SubagentStatus.Done -> palette.positive
        SubagentStatus.Failed -> palette.danger.dot
        SubagentStatus.Stopped -> palette.ink2
    }
}

/** A card's or a grid's mark on the work rail: a spinner while it runs, the subagent icon after, on a wash of its colour. */
@Composable
private fun SubagentMark(running: Boolean, tint: Color, name: String) {
    // The page under the mark hides the rail behind it, as a step's icon does.
    Box(Modifier.size(Tokens.Size.iconXS).clip(CircleShape).background(PersonaTheme.palette.background)) {
        Box(Modifier.size(Tokens.Size.iconXS).background(tint.copy(alpha = HueTintAlpha)), contentAlignment = Alignment.Center) {
            if (running) JSpinner(Modifier.size(Tokens.Space.m), color = tint, stroke = Tokens.Space.xxs)
            else JIcon(toolCategory(name), null, size = Tokens.Space.m, tint = tint, decorative = true)
        }
    }
}

/** Where a step's words start on the rail: its inset, its icon and the gap after it ([TimelineStep] in Glass). */
private val textColumn: Dp get() = Tokens.Space.m + Tokens.Size.iconXS + Tokens.Space.l

/**
 * A subagent the agent launched (an Agent / Task call) as iOS draws it in Glass (#4339 C2, iOS `SubagentCard`): while it
 * runs, a card with its title and what it is doing; opened, what it was asked, what it reported, its latest steps and
 * "View Subagent". Once it reported back it is the tool's own row ([row]), as on iOS.
 */
@Composable
internal fun SubagentCard(call: ToolCall, modifier: Modifier = Modifier, row: @Composable () -> Unit) {
    val links = LocalChatSubagents.current
    val id = call.use.item.toolUseId
    val agent = id?.let { links?.agents?.get(it) }
    if (Subagents.drawsAsRow(call, agent)) return Box(modifier) { row() }
    val res = LocalResources.current
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val status = Subagents.status(call, agent)
    val folds = LocalSubagentFolds.current
    var openHere by rememberSaveable(call.id) { mutableStateOf(false) }
    val open = folds?.isOpen(call.id) ?: openHere
    val title = subagentTitle(call, agent, stringResource(R.string.chat_tool_subagent))
    val line = SubagentCardText.line(res, status, agent, Subagents.took(call, agent))
    Column(modifier.fillMaxWidth().animateContentSize(PersonaTheme.motion.size()).semantics { testTag = "chat.subagent-card" }) {
        val state = stringResource(if (open) R.string.chat_tasks_expanded else R.string.chat_tasks_collapsed)
        Row(Modifier.fillMaxWidth()
            .clickable(role = Role.Button) {
                if (folds != null) folds.toggle(call.id) else openHere = !openHere
            }
            .semantics(mergeDescendants = true) { stateDescription = state }
            .padding(horizontal = Tokens.Space.m, vertical = Tokens.Space.s), verticalAlignment = Alignment.Top) {
            SubagentMark(status == SubagentStatus.Running, status.tint(), call.name)
            Spacer(Modifier.width(Tokens.Space.l))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                Text(title, style = type.callout.copy(fontWeight = FontWeight.SemiBold), color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(line, style = type.micro, color = if (status == SubagentStatus.Failed) palette.danger.text else palette.ink2,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Spacer(Modifier.width(Tokens.Space.xs))
            JIcon(PersonaIcons.disclosure, null, Modifier.padding(top = Tokens.Space.xxs).rotate(if (open) 90f else 0f),
                size = Tokens.Space.m, tint = palette.ink3)
        }
        // Its reports and steps are rows with their own inset: their icons line up with the brief's words.
        if (open) Column(Modifier.fillMaxWidth().padding(start = textColumn - Tokens.Space.m, end = Tokens.Space.m, bottom = Tokens.Space.m),
            verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            call.use.item.toolInput.inputText("prompt")?.let {
                Text(it, Modifier.padding(start = Tokens.Space.m), style = type.caption, color = palette.ink2, maxLines = 6, overflow = TextOverflow.Ellipsis)
            }
            // What it reported so far, each one line until opened (iOS `FoldedReport`).
            Subagents.reports(call).forEach { report ->
                if (report.task != null) TaskNoteLine(report)
                else report.text?.let { Text(it, style = type.caption, color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis) }
            }
            val key = id?.let { links?.keys?.get(it) }
            val repository = links?.repository
            if (key != null && repository != null) SubagentRecentSteps(key, repository, status == SubagentStatus.Running)
            val view = links?.open
            if (key != null && view != null) JTextButton(stringResource(R.string.chat_tool_open_subagent), { view(key) }, icon = PersonaIcons.openExternal)
        }
    }
}

/**
 * A subagent's latest steps in its open card (iOS `SubagentRecentSteps`): its own conversation, followed while shown;
 * the newest short fold the hub sent is opened for them.
 */
@Composable
private fun SubagentRecentSteps(key: SessionKey, repository: ChatRepository, running: Boolean) {
    val snapshot by remember(repository, key) { repository.observe(key) }.collectAsStateWithLifecycle(null)
    var opened by remember(key) { mutableStateOf<Pair<String, List<Item>>?>(null) }
    val items = remember(snapshot, opened) {
        val lines = snapshot?.items.orEmpty().map { it.asLineItem() }
        opened?.let { (fold, page) -> LineStore().apply { replace(lines); unfold(fold, page.map { it.asLineItem() }) }.items } ?: lines
    }
    val fold = remember(items) { Subagents.foldToOpen(items) }
    LaunchedEffect(fold?.id) {
        val f = fold ?: return@LaunchedEffect
        val through = f.fold?.throughSeq ?: return@LaunchedEffect
        try { opened = f.id to repository.expandFold(key, f.seq, through) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { /* Its latest steps stay what the page holds. */ }
    }
    val calls = remember(items) { Subagents.recent(items) }
    if (calls.isEmpty()) {
        if (snapshot?.loading != false) JSpinner(Modifier.size(Tokens.Space.m), stroke = Tokens.Space.xxs)
        return
    }
    val res = LocalResources.current
    Column(Modifier.fillMaxWidth().semantics { testTag = "chat.subagent-steps" }) {
        calls.forEach { step ->
            val presentation = remember(step.use.item, res) { toolPresentation(res, step.use.item) }
            TimelineStep(toolCategory(step.name), presentation.title, detail = presentation.detail, mono = presentation.mono, singleLine = true,
                state = when {
                    step.failed -> WorkState.Failed
                    step.result == null && running -> WorkState.Running
                    else -> WorkState.Done
                })
        }
    }
}

/**
 * Subagents launched one after another (in parallel) as one line of an open work line (iOS `SubagentGrid`, the web's
 * `AgentGrid`): "Running 3 agents" while any runs, "Ran 3 agents · 12 steps in all" after; their cards in the order they
 * were launched, two side by side where there is room (a tablet), one under the other on a phone. [last]: the work line's
 * last step, where the rail ends.
 */
@Composable
internal fun SubagentGrid(calls: List<ToolCall>, last: Boolean, card: @Composable (ToolCall) -> Unit) {
    val res = LocalResources.current
    val agents = LocalChatSubagents.current?.agents.orEmpty()
    val running = Subagents.running(calls, agents)
    val head = SubagentCardText.head(res, calls.size, running, Subagents.steps(calls, agents))
    Column(Modifier.fillMaxWidth().semantics { testTag = "chat.subagent-grid" }) {
        Row(Modifier.fillMaxWidth().workRail(false).semantics(mergeDescendants = true) {}
            .padding(start = Tokens.Space.m, end = Tokens.Space.m, top = Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
            SubagentMark(running > 0, if (running > 0) PersonaTheme.palette.working.dot else PersonaTheme.palette.ink2, calls.first().name)
            Spacer(Modifier.width(Tokens.Space.l))
            Text(head, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        BoxWithConstraints(Modifier.fillMaxWidth()) {
            val columns = (maxWidth / Tokens.Size.subagentColumn).toInt().coerceIn(1, calls.size)
            val rows = calls.chunked(columns)
            Column(Modifier.fillMaxWidth()) {
                rows.forEachIndexed { n, cells ->
                    Row(Modifier.fillMaxWidth().workRail(last && n == rows.lastIndex), verticalAlignment = Alignment.Top) {
                        cells.forEach { Box(Modifier.weight(1f)) { card(it) } }
                        repeat(columns - cells.size) { Spacer(Modifier.weight(1f)) }
                    }
                }
            }
        }
    }
}
