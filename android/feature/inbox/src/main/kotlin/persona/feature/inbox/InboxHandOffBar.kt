package persona.feature.inbox

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.designsystem.ComposerFrame
import persona.core.designsystem.ComposerTextField
import persona.core.designsystem.FeedbackAction
import persona.core.designsystem.FeedbackNotice
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.Haptic
import persona.core.designsystem.JAction
import persona.core.designsystem.JChip
import persona.core.designsystem.JIcon
import persona.core.designsystem.JMenu
import persona.core.designsystem.JSwitchRow
import persona.core.designsystem.JTextButton
import persona.core.designsystem.JToastHost
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.SendButton
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PhIcons
import persona.core.designsystem.rememberHaptics
import persona.core.designsystem.rememberToastState
import persona.core.designsystem.tapped

/** Production uses the pairing's store; lightweight fakes and previews can keep a model in the page scope. */
@Composable
internal fun rememberInboxHandOffBarModel(repository: InboxRepository, itemId: String): InboxHandOffBarModel? {
    val shared = (repository as? PinnedInboxRepository)?.base ?: repository
    if (shared is InboxExperienceRepository) return remember(shared, itemId) { shared.handOffBar(itemId) }
    if (shared is InboxHandOffBarOwner) {
        val bars by shared.handOffBars.collectAsStateWithLifecycle()
        val current = bars ?: return null
        return remember(current, shared, itemId) { current.get(itemId, shared) }
    }
    val scope = rememberCoroutineScope()
    val resources = LocalResources.current
    return remember(repository, itemId, scope, resources) { InboxHandOffBarModel(itemId, repository, scope, resources) }
}

/** The existing reply sheet supports Lark, mail, and connectors that explicitly accept replies. */
internal fun inboxSupportsDraftReply(record: InboxRecord): Boolean =
    record.isLark || record.isMail || record.connector?.canReply == true

/**
 * The event page's persistent hand-off bar. Its toast is local to this bar, never a second global feedback host.
 * The model owns the five-second wait; this composable only observes it and opens a requested destination.
 */
@Composable
internal fun InboxHandOffBar(
    repository: InboxRepository,
    model: InboxHandOffBarModel,
    enabled: Boolean,
    onDraftReply: (() -> Unit)?,
    onOpen: (HandOffDestination) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by model.state.collectAsStateWithLifecycle()
    val phase = state.phase
    val memory by repository.handOffMemory.collectAsStateWithLifecycle(initialValue = null)
    val resources = LocalResources.current
    val haptics = rememberHaptics()
    val toast = rememberToastState()
    val focus = LocalFocusManager.current
    val currentOpen by rememberUpdatedState(onOpen)
    var savedTarget by rememberSaveable(model.itemId) { mutableStateOf(state.target?.key) }
    LaunchedEffect(model.itemId) { model.restoreTarget(savedTarget) }
    LaunchedEffect(state.target?.key) { savedTarget = state.target?.key }
    LaunchedEffect(memory) { memory?.let(model::preselect) }
    LaunchedEffect(enabled, phase) { if (!enabled && phase is HandOffBarPhase.Queued) model.undo() }

    fun openSent() { model.openSent()?.let(currentOpen) }
    val notice = handOffNotice(phase, enabled, model, haptics, ::openSent)
    LaunchedEffect(phase) {
        if (notice != null) toast.show(notice) else toast.current?.let(toast::dismiss)
        val sent = phase as? HandOffBarPhase.Sent
        if (sent?.openRequested == true) {
            openSent()
            model.openHandled()
        }
    }

    if (!enabled && phase is HandOffBarPhase.Idle) return
    Column(modifier.fillMaxWidth().imePadding()) {
        Box(Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.page), contentAlignment = Alignment.TopCenter) {
            if (toast.current == null) {
                if (phase is HandOffBarPhase.Idle) {
                    HandOffChips(model, onDraftReply, haptics)
                } else {
                    HandOffStatusRow(notice)
                }
            }
            JToastHost(toast, Modifier.testTag("inbox-handoff-toast"))
        }
        if (state.lastGone) {
            Row(
                Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.page, vertical = Tokens.Space.xs),
                horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                JIcon(PhIcons.Info, null, size = Tokens.Space.m, tint = PersonaTheme.palette.cautionText, decorative = true)
                Text(
                    stringResource(R.string.inbox_handoff_last_gone),
                    style = PersonaTheme.type.caption,
                    color = PersonaTheme.palette.cautionText,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (enabled) {
            ComposerFrame(
                leading = {
                    HandOffTargetMenu(
                        model = model,
                        state = state,
                        enabled = phase is HandOffBarPhase.Idle || phase is HandOffBarPhase.Sent,
                        markDoneEnabled = phase !is HandOffBarPhase.Queued && phase !is HandOffBarPhase.Sending,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                },
                trailing = {
                    SendButton(
                        onClick = {
                            focus.clearFocus()
                            model.queue(state.input, typed = true)
                        },
                        modifier = Modifier.testTag("inbox-handoff-send"),
                        enabled = state.input.isNotBlank() && phase !is HandOffBarPhase.Sending,
                        loading = phase is HandOffBarPhase.Sending,
                    )
                },
                field = {
                    ComposerTextField(
                        state.input,
                        model::updateInput,
                        placeholder = stringResource(R.string.inbox_handoff_hint),
                        modifier = Modifier.testTag("inbox-handoff-field"),
                        enabled = phase !is HandOffBarPhase.Sending,
                        maxLines = 1,
                    )
                },
            )
        }
    }
}

@Composable
private fun HandOffChips(
    model: InboxHandOffBarModel,
    onDraftReply: (() -> Unit)?,
    haptics: (Haptic) -> Unit,
) {
    val resources = LocalResources.current
    val asks = handoffPresets(resources)
    val chips = listOf(
        HandOffChip(R.string.inbox_handoff_chip_summary, PhIcons.Sparkle, asks[0], "summary"),
        HandOffChip(R.string.inbox_handoff_chip_background, PhIcons.MagnifyingGlass, asks[1], "background"),
        HandOffChip(R.string.inbox_handoff_chip_handle, PhIcons.Robot, asks[2], "handle"),
    )
    Row(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
    ) {
        chips.forEachIndexed { index, chip ->
            JChip(
                stringResource(chip.label),
                modifier = Modifier.testTag("inbox-handoff-chip-${chip.tag}"),
                icon = chip.icon,
                role = Role.Button,
                onClick = haptics.tapped { model.queue(chip.ask, typed = false) },
            )
            if (index == 0 && onDraftReply != null) {
                JChip(
                    stringResource(R.string.inbox_draft_reply),
                    modifier = Modifier.testTag("inbox-handoff-chip-draft"),
                    icon = PhIcons.NotePencil,
                    role = Role.Button,
                    onClick = haptics.tapped(onDraftReply),
                )
            }
        }
    }
}

private data class HandOffChip(val label: Int, val icon: persona.core.designsystem.icons.PhIcon, val ask: String, val tag: String)

@Composable
private fun HandOffTargetMenu(
    model: InboxHandOffBarModel,
    state: HandOffBarState,
    enabled: Boolean,
    markDoneEnabled: Boolean,
    modifier: Modifier = Modifier,
) {
    var expanded by remember { mutableStateOf(false) }
    var more by remember { mutableStateOf(false) }
    var targets by remember { mutableStateOf(emptyList<HandOffTarget>()) }
    val haptics = rememberHaptics()
    val main = stringResource(R.string.inbox_handoff_main_chat)
    val last = stringResource(R.string.inbox_handoff_last_time)
    val name = state.target?.title ?: main
    Box(modifier) {
        JChip(
            if (state.lastTime) "$name ($last)" else name,
            modifier = Modifier.testTag("inbox-handoff-target"),
            icon = if (state.target == null) PhIcons.Sparkle else PhIcons.Robot,
            trailingIcon = PhIcons.CaretDown,
            muted = !enabled,
            role = Role.Button,
            onClick = if (enabled) haptics.tapped {
                targets = model.liveTargets()
                more = false
                expanded = true
            } else null,
        )
        val agents = stringResource(R.string.inbox_handoff_agents)
        val actions = if (more) {
            targets.drop(INBOX_HAND_OFF_MENU_TARGETS).mapIndexed { index, target ->
                JAction(target.title, PhIcons.Robot, checked = target == state.target, subtitle = target.machine,
                    section = agents.takeIf { index == 0 }, onClick = haptics.tapped { model.selectTarget(target) })
            }
        } else {
            buildList {
                add(JAction(main, PhIcons.Sparkle, checked = state.target == null,
                    onClick = haptics.tapped { model.selectTarget(null) }))
                targets.take(INBOX_HAND_OFF_MENU_TARGETS).forEachIndexed { index, target ->
                    add(JAction(target.title, PhIcons.Robot, checked = target == state.target, subtitle = target.machine,
                        section = agents.takeIf { index == 0 }, onClick = haptics.tapped { model.selectTarget(target) }))
                }
                if (targets.size > INBOX_HAND_OFF_MENU_TARGETS) {
                    add(JAction(stringResource(R.string.inbox_handoff_more), onClick = {
                        more = true
                        expanded = true
                    }))
                }
            }
        }
        JMenu(
            expanded = expanded,
            onDismiss = { expanded = false; more = false },
            actions = actions,
            content = {
                JSwitchRow(
                    stringResource(R.string.inbox_handoff_mark_done),
                    state.markDone,
                    model::setMarkDone,
                    enabled = markDoneEnabled,
                )
            },
        )
    }
}

@Composable
private fun handOffNotice(
    phase: HandOffBarPhase,
    enabled: Boolean,
    model: InboxHandOffBarModel,
    haptics: (Haptic) -> Unit,
    openSent: () -> Unit,
): FeedbackNotice? {
    val resources = LocalResources.current
    fun name(target: HandOffTarget?) = target?.title ?: resources.getString(R.string.inbox_handoff_main_chat)
    val view = stringResource(R.string.inbox_handoff_view)
    return when (phase) {
        HandOffBarPhase.Idle -> null
        is HandOffBarPhase.Queued -> {
            val operation = phase.operation
            val message = resources.getString(
                if (operation.markDone) R.string.inbox_handoff_queued_done else R.string.inbox_handoff_queued,
                name(operation.target),
            )
            FeedbackNotice(
                message,
                action = FeedbackAction(view) { haptics(Haptic.Tap); model.viewQueued() },
                secondary = FeedbackAction(stringResource(R.string.inbox_undo)) { haptics(Haptic.Tap); model.undo() },
            )
        }
        is HandOffBarPhase.Sending -> FeedbackNotice(resources.getString(R.string.inbox_handoff_sending_to, name(phase.operation.target)))
        is HandOffBarPhase.Sent -> {
            val operation = phase.operation
            if (phase.doneError != null) {
                FeedbackNotice(
                    resources.getString(R.string.inbox_handoff_done_failed),
                    action = FeedbackAction(view, openSent),
                    style = FeedbackStyle.Error,
                )
            } else {
                FeedbackNotice(
                    resources.getString(if (operation.markDone) R.string.inbox_handoff_sent_done else R.string.inbox_handoff_sent_to, name(operation.target)),
                    action = FeedbackAction(view, openSent),
                    style = FeedbackStyle.Success,
                )
            }
        }
        is HandOffBarPhase.Failed -> FeedbackNotice(
            resources.getString(R.string.inbox_handoff_failed, failureReason(phase.error).resolve(resources)),
            action = if (enabled) FeedbackAction(stringResource(R.string.inbox_retry)) { haptics(Haptic.Tap); model.retry() } else null,
            style = FeedbackStyle.Error,
        )
    }
}

/** Toast expiry never removes the user's way to View or explicitly Retry. */
@Composable
private fun HandOffStatusRow(notice: FeedbackNotice?) {
    notice ?: return
    Row(
        Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
    ) {
        Text(
            notice.message,
            Modifier.weight(1f),
            style = PersonaTheme.type.callout,
            color = if (notice.style == FeedbackStyle.Error) PersonaTheme.palette.danger.text else PersonaTheme.palette.ink2,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        notice.secondary?.let { JTextButton(it.label, it.run) }
        notice.action?.let { JTextButton(it.label, it.run) }
    }
}
