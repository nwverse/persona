package persona.feature.chat

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.contracts.BriefBody
import persona.core.contracts.BriefDone
import persona.core.contracts.BriefFailed
import persona.core.contracts.BriefProgress
import persona.core.contracts.BriefRow
import persona.core.contracts.BriefTake
import persona.core.contracts.FeedItem
import persona.core.contracts.brief
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JCard
import persona.core.designsystem.JIcon
import persona.core.designsystem.LocalizedFormats
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.currentLocale
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.tasks.AssistantLines

/** A routine's fixed facts in the real assistant feed, shared by all three design-system styles. */
@Composable
internal fun BriefCard(item: FeedItem, host: AssistantTaskHost) {
    val brief = item.brief ?: return
    JCard(Modifier.fillMaxWidth().testTag("brief-card")) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(brief.title.ifBlank { stringResource(R.string.brief_default_title) },
                style = PersonaTheme.type.label, color = PersonaTheme.palette.ink2, maxLines = 1)
            BriefWaiting(brief, host)
            HorizontalDivider(color = PersonaTheme.palette.line)
            BriefPast(brief, host)
            HorizontalDivider(color = PersonaTheme.palette.line)
            BriefAccept(brief, host)
            if (brief.later.isNotEmpty() || brief.laterTotal > 0) {
                HorizontalDivider(color = PersonaTheme.palette.line)
                BriefLater(brief, host)
            }
            if (brief.progressTotal > 0) {
                HorizontalDivider(color = PersonaTheme.palette.line)
                BriefProgress(brief, host)
            }
        }
    }
}

@Composable
private fun BriefHead(icon: PhIcon, status: Status?, title: String) {
    Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
        JIcon(icon, decorative = true, size = Tokens.Space.l,
            tint = status?.let { PersonaTheme.palette.status(it).text } ?: PersonaTheme.palette.accent)
        Text(title, style = PersonaTheme.type.label, color = PersonaTheme.palette.ink2)
    }
}

@Composable
private fun BriefNote(text: String) {
    Text(text, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
}

@Composable
private fun BriefMore(n: Int) {
    if (n > 0) BriefNote(stringResource(R.string.brief_more, n))
}

@Composable
private fun BriefOpen(tag: String, onClick: () -> Unit) {
    JButton(stringResource(R.string.brief_open), onClick, Modifier.testTag(tag), kind = JButtonKind.Secondary, size = JButtonSize.Small)
}

@Composable
private fun briefMeta(row: BriefRow): String {
    val minutes = (row.waitedS.coerceAtLeast(60) + 59) / 60
    val waited = when {
        minutes < 60 -> stringResource(R.string.brief_waited_minutes, minutes)
        minutes < 1_440 -> {
            val base = stringResource(R.string.brief_waited_hours, minutes / 60)
            if (minutes % 60 == 0L) base else "$base ${minutes % 60}m"
        }
        else -> {
            val base = stringResource(R.string.brief_waited_days, minutes / 1_440)
            if (minutes % 1_440 == 0L) base else "$base ${(minutes % 1_440) / 60}h"
        }
    }
    return buildList {
        add(waited)
        if (row.nudged) add(stringResource(R.string.brief_nudged))
        row.from?.takeIf(String::isNotBlank)?.let(::add)
    }.joinToString(" · ")
}

@Composable
private fun BriefWaiting(brief: BriefBody, host: AssistantTaskHost) {
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        if (brief.waitingUnknown) {
            BriefHead(PhIcons.Hand, Status.NeedsYou, stringResource(R.string.brief_waiting_unknown))
            BriefNote(stringResource(R.string.brief_waiting_unknown_note))
        } else {
            BriefHead(PhIcons.Hand, Status.NeedsYou, stringResource(R.string.brief_waiting, brief.waiting.size))
            if (brief.waiting.isEmpty()) BriefNote(stringResource(R.string.brief_nothing))
            brief.waiting.forEach { row ->
                val chat = row.chat(waiting = true, pending = host.pending)
                Row(Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                        Text(row.title, style = PersonaTheme.type.bodyStrong, color = PersonaTheme.palette.ink)
                        Text(briefMeta(row), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                    }
                    if (chat != null) BriefOpen("brief-open-${row.id}") { host.onOpen(chat) }
                }
            }
        }
    }
}

@Composable
private fun BriefMarkLine(icon: PhIcon, status: Status, id: String, title: String, detail: String?, open: (() -> Unit)?) {
    Row(Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JIcon(icon, decorative = true, size = Tokens.Space.m, tint = PersonaTheme.palette.status(status).text)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
            Text(title, style = PersonaTheme.type.bodyStrong, color = PersonaTheme.palette.ink)
            detail?.takeIf(String::isNotBlank)?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
        }
        if (open != null) BriefOpen("brief-open-$id", open)
    }
}

@Composable
private fun briefTime(at: String?): String? = AssistantLines.time(at)?.let {
    LocalizedFormats.format(it, LocalizedFormats.TIME, currentLocale)
}

@Composable
private fun BriefPast(brief: BriefBody, host: AssistantTaskHost) {
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        BriefHead(PhIcons.Clock, Status.Working,
            stringResource(R.string.brief_past, brief.hours, brief.doneTotal, brief.failedTotal))
        if (brief.done.isEmpty() && brief.failed.isEmpty()) BriefNote(stringResource(R.string.brief_nothing))
        brief.failed.forEach { row: BriefFailed ->
            BriefMarkLine(PhIcons.XCircle, Status.NeedsYou, row.id, row.title,
                listOfNotNull(briefTime(row.at), row.why?.takeIf(String::isNotBlank)).joinToString(" · ").takeIf(String::isNotBlank),
                if (row.kind == "task") ({ host.sheet.value = TaskSheet.Task(row.id) }) else null)
        }
        brief.done.forEach { row: BriefDone ->
            BriefMarkLine(PhIcons.CheckCircle, Status.Unread, row.id, row.title, briefTime(row.at)) {
                host.sheet.value = TaskSheet.Task(row.id)
            }
        }
        BriefMore(brief.pastMore)
    }
}

@Composable
private fun BriefAccept(brief: BriefBody, host: AssistantTaskHost) {
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        BriefHead(PhIcons.CheckCircle, Status.Unread, stringResource(R.string.brief_accept, brief.accept.size))
        if (brief.accept.isEmpty()) BriefNote(stringResource(R.string.brief_nothing))
        brief.accept.forEach { row -> BriefAcceptRow(row, host) }
    }
}

@Composable
private fun BriefAcceptRow(row: BriefRow, host: AssistantTaskHost) {
    val busy by host.tasks.busy.collectAsStateWithLifecycle()
    var outcome by rememberSaveable(row.id, row.askedAt) { mutableStateOf<String?>(null) }
    val askedAt = row.askedAt.orEmpty()
    val sending = "accept:${row.id}" in busy
    Column(Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
        Text(row.title, style = PersonaTheme.type.bodyStrong, color = PersonaTheme.palette.ink)
        row.verdict?.takeIf(String::isNotBlank)?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink) }
        row.detail?.takeIf(String::isNotBlank)?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
        row.recommend?.takeIf(String::isNotBlank)?.let {
            Text(stringResource(R.string.chat_tray_recommended, it), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        }
        Text(briefMeta(row), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        if (outcome != null) {
            Text(outcome.orEmpty(), Modifier.testTag("brief-outcome-${row.id}"), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            BriefOpen("brief-open-${row.id}") { host.sheet.value = TaskSheet.Task(row.id) }
            if (outcome == null && row.isDecision) {
                val finished = stringResource(R.string.chat_tray_done)
                JButton(stringResource(R.string.brief_complete_directly), {
                    host.tasks.finishBrief(row.id, askedAt, host.onError) { outcome = finished }
                }, Modifier.testTag("brief-finish-${row.id}"), enabled = askedAt.isNotBlank() && !sending,
                    kind = JButtonKind.Secondary, size = JButtonSize.Small, loading = sending)
            }
            val take = row.take
            if (outcome == null && take != null) {
                val label = stringResource(when (take) {
                    BriefTake.Accept -> R.string.brief_accept_action
                    BriefTake.GotIt -> R.string.brief_got_it
                    BriefTake.Recommendation -> R.string.brief_take_recommendation
                })
                val taken = stringResource(when (take) {
                    BriefTake.Accept -> R.string.brief_accepted
                    BriefTake.GotIt -> R.string.brief_noted
                    BriefTake.Recommendation -> R.string.brief_recommendation_taken
                })
                JButton(label, { host.tasks.acceptBrief(row.id, askedAt, host.onError) { outcome = taken } },
                    Modifier.testTag("brief-take-${row.id}"), enabled = askedAt.isNotBlank() && !sending,
                    kind = JButtonKind.Primary, size = JButtonSize.Small, loading = sending)
            }
        }
    }
}

@Composable
private fun BriefLater(brief: BriefBody, host: AssistantTaskHost) {
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        BriefHead(PhIcons.Question, Status.Idle, stringResource(R.string.brief_later, brief.laterTotal))
        brief.later.forEach { row -> BriefLaterRow(row, host) }
        BriefMore(brief.laterMore)
    }
}

@Composable
private fun BriefLaterRow(row: BriefRow, host: AssistantTaskHost) {
    val busy by host.tasks.busy.collectAsStateWithLifecycle()
    var handled by rememberSaveable(row.id) { mutableStateOf(false) }
    val chat = row.chat(waiting = false, pending = emptyList())
    Column(Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
        Text(row.title, style = PersonaTheme.type.bodyStrong, color = PersonaTheme.palette.ink)
        Text(briefMeta(row), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        if (handled) Text(stringResource(R.string.brief_marked_handled), Modifier.testTag("brief-outcome-${row.id}"),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            if (chat != null) BriefOpen("brief-open-${row.id}") { host.onOpen(chat) }
            if (!handled) JButton(stringResource(R.string.brief_handled), {
                host.tasks.markBriefHandled(row.id, host.onError) { handled = true }
            }, Modifier.testTag("brief-handled-${row.id}"), enabled = "later:${row.id}" !in busy,
                kind = JButtonKind.Secondary, size = JButtonSize.Small, loading = "later:${row.id}" in busy)
        }
    }
}

@Composable
private fun BriefProgress(brief: BriefBody, host: AssistantTaskHost) {
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        BriefHead(PhIcons.GearSix, Status.Working, stringResource(R.string.brief_progress, brief.progressTotal))
        brief.progress.forEach { row: BriefProgress ->
            Row(Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                    Text(row.title, style = PersonaTheme.type.bodyStrong, color = PersonaTheme.palette.ink)
                    row.stage?.takeIf(String::isNotBlank)?.let { Text(it, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
                }
                BriefOpen("brief-open-${row.id}") { host.sheet.value = TaskSheet.Task(row.id) }
            }
        }
        BriefMore(brief.progressMore)
    }
}
