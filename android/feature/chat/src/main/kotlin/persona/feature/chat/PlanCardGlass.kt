package persona.feature.chat

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import persona.core.contracts.PlanTask
import persona.core.contracts.TaskPlan
import persona.core.designsystem.JButton
import persona.core.designsystem.JButtonKind
import persona.core.designsystem.JButtonSize
import persona.core.designsystem.JCard
import persona.core.designsystem.JChip
import persona.core.designsystem.JIcon
import persona.core.designsystem.JNumberDisc
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PersonaIcons

// iOS's plan card on Glass (`PlanCard` in Views/Assistant/Tasks/TaskViews.swift, #4339 C13): what Persona draws in its
// conversation for a plan nobody started yet. Brief and Board keep `PlanCard`'s own card, and their plans behind the trays.

/**
 * An open plan as iOS's `PlanCard.open`: what it does ("Start like this", or "Split into N tasks, started like this") with the plan's
 * mark, each task as a numbered row (a number only when there are several) with where it would run, "Start these directly"
 * (the next ones like these start without asking), then **Start**, **Edit** and **Don't start**. While the hub starts it, the
 * choices wait; once it came back started the conversation draws the dispatched line in its place, and **Don't start** leaves
 * "Not started · N tasks".
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun GlassPlanCard(
    plan: TaskPlan, host: AssistantTaskHost, directly: Boolean, onDirectly: (Boolean) -> Unit, busy: Boolean, onEdit: () -> Unit,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    JCard(Modifier.testTag("plan-card-${plan.id}"), padding = Tokens.Space.l) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                JIcon(PersonaIcons.taskPlan, decorative = true, size = Tokens.Size.markXS, tint = palette.ink2)
                Text(if (plan.tasks.size > 1) pluralStringResource(R.plurals.chat_tasks_plan_split, plan.tasks.size, plan.tasks.size)
                    else stringResource(R.string.chat_tasks_plan_single), Modifier.testTag("plan-heading"),
                    style = type.caption.copy(fontWeight = FontWeight.SemiBold), color = palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            plan.tasks.forEachIndexed { n, task ->
                if (n > 0) HorizontalDivider(color = palette.line)
                GlassPlanTask(n + 1, task, numbered = plan.tasks.size > 1, host)
            }
            StartDirectly(directly, enabled = !busy, onDirectly)
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JButton(if (plan.tasks.size > 1) pluralStringResource(R.plurals.chat_tasks_plan_start_count, plan.tasks.size, plan.tasks.size)
                    else stringResource(R.string.chat_tasks_plan_start),
                    { host.tasks.startPlan(plan, directly, host.onError, manager = host.manager) },
                    Modifier.weight(1f).testTag("plan-start-${plan.id}"), size = JButtonSize.Medium, loading = busy)
                JButton(stringResource(R.string.chat_plan_edit), onEdit, Modifier.testTag("plan-edit-${plan.id}"),
                    kind = JButtonKind.Secondary, size = JButtonSize.Medium, enabled = !busy)
                JButton(stringResource(R.string.chat_tasks_plan_dismiss), { host.tasks.dismissPlan(plan, host.onError, host.manager) },
                    Modifier.testTag("plan-dismiss-${plan.id}"), kind = JButtonKind.Secondary, size = JButtonSize.Medium, enabled = !busy)
            }
        }
    }
}

/**
 * One task of an open plan (iOS `PlanTaskRow`): its title, where it would run (the computer, the project, the model or launcher
 * and, when one is asked for, that it only reads or the permission it starts with), each a chip that wraps, and why there.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun GlassPlanTask(number: Int, task: PlanTask, numbered: Boolean, host: AssistantTaskHost) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            if (numbered) JNumberDisc(number)
            Text(task.title, Modifier.weight(1f, fill = false), style = type.bodyStrong, color = palette.ink)
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            JChip(host.machineName(task.machine), icon = PersonaIcons.laptop)
            JChip(task.project, icon = PersonaIcons.folder)
            JChip(task.model?.takeIf(String::isNotBlank) ?: task.launcher?.takeIf(String::isNotBlank) ?: stringResource(R.string.chat_tasks_default_model),
                icon = PersonaIcons.cpu)
            // iOS: a read-only task says so, in the accent; else the permission it starts with, when one is set.
            if (task.readonly == true) JChip(stringResource(R.string.chat_tasks_read_only), icon = PersonaIcons.view, accent = true)
            else task.permission?.takeIf(String::isNotEmpty)?.let { JChip(permissionLabel(it)) }
        }
        task.why?.takeIf(String::isNotBlank)?.let { Text(it, style = type.caption, color = palette.ink2) }
    }
}

/**
 * "Start these directly" (iOS's label button): a ring that becomes a check in the accent once chosen, with the words in the
 * accent too, one line. Such tasks (the same computer and folder, read or edit alike) then start without asking again.
 */
@Composable
private fun StartDirectly(on: Boolean, enabled: Boolean, onChange: (Boolean) -> Unit) {
    val palette = PersonaTheme.palette
    val tint = if (on) palette.accent else palette.ink2
    Row(Modifier.toggleable(value = on, enabled = enabled, role = Role.Checkbox, onValueChange = onChange).testTag("plan-directly"),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        if (on) JIcon(PersonaIcons.successFilled, decorative = true, size = Tokens.Size.markXS, tint = tint, selected = true)
        else Box(Modifier.size(Tokens.Size.markXS).border(Tokens.Size.line, tint, CircleShape))
        Text(stringResource(R.string.chat_tasks_plan_remember), style = PersonaTheme.type.caption, color = tint, maxLines = 1,
            overflow = TextOverflow.Ellipsis)
    }
}

/** A plan that was not started, as iOS's `settled`: its mark (a cross) and "Not started · N tasks", one line. */
@Composable
internal fun GlassPlanNotStarted(count: Int) {
    val palette = PersonaTheme.palette
    Row(Modifier.testTag("plan-not-started"), verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        JIcon(PersonaIcons.close, decorative = true, size = Tokens.Size.markXS, tint = palette.ink2)
        Text(pluralStringResource(R.plurals.chat_tasks_plan_dismissed, count, count), style = PersonaTheme.type.callout,
            color = palette.ink2, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}
