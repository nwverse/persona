package persona.feature.agents

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.InlineTextContent
import androidx.compose.foundation.text.appendInlineContent
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.Placeholder
import androidx.compose.ui.text.PlaceholderVerticalAlign
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import persona.core.contracts.Machine
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

/**
 * New Agent's "Recent" section (#2134): up to three setups that started an Agent, newest first. A tap fills the form and
 * never starts; an offline machine's card is dimmed and can't be picked; a long press removes one or all. Not shown in a
 * space's form, whose machine and folder are fixed, nor while a start waits to be confirmed.
 */
internal fun LazyListScope.recentSetupItems(state: NewAgentState, machines: List<Machine>, model: NewAgentViewModel) {
    if (state.space != null || state.pending != null) return
    val cards = RecentSetups.cards(state.recentSetups, machines)
    if (cards.isEmpty()) return
    item("recent-setups-title") { JSectionHeader(stringResource(R.string.agents_new_recent)) }
    itemsIndexed(cards, key = { _, card -> "recent-setup:${card.index}" }) { index, card ->
        RecentSetupCard(card, machines.firstOrNull { it.id == card.setup.machine }, GroupPosition.of(index, cards.size),
            picked = state.pickedSetup?.same(card.setup) == true, enabled = !state.locked,
            onUse = { model.useRecent(card.setup) }, onRemove = { model.removeRecent(card.setup) }, onClear = model::clearRecent)
    }
}

@Composable
private fun RecentSetupCard(
    card: RecentCard, machine: Machine?, position: GroupPosition, picked: Boolean, enabled: Boolean,
    onUse: () -> Unit, onRemove: () -> Unit, onClear: () -> Unit,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    var menu by remember { mutableStateOf(false) }
    val setup = card.setup
    val separator = stringResource(R.string.agents_new_recent_separator)
    val folder = recentFolder(setup.cwd, machine?.home)
    val machineName = machine?.displayName ?: setup.machine
    val title = buildAnnotatedString {
        withStyle(SpanStyle(fontWeight = type.bodyStrong.fontWeight)) { append(folder) }
        append(separator)
        withStyle(SpanStyle(color = if (card.online) palette.ink2 else palette.ink3)) { append(machineName) }
    }
    val details = recentDetails(setup, separator)
    val caption = type.caption.copy(fontWeight = FontWeight.Normal)
    val iconSize = with(LocalDensity.current) { caption.fontSize.toDp() }
    val warning = mapOf(WARNING to InlineTextContent(Placeholder(caption.fontSize, caption.fontSize, PlaceholderVerticalAlign.TextCenter)) {
        JIcon(PhIcons.Warning, null, size = iconSize, tint = palette.cautionText)
    })
    JGroupItem(position) {
        // An offline card keeps its long press (to remove it), but a tap picks nothing and shows no press.
        val offlinePress = if (card.online || !enabled) Modifier else Modifier.onLongPress(card.index, stringResource(R.string.agents_new_recent_remove)) { menu = true }
        JRow(
            offlinePress, onClick = if (card.online) onUse else null, onLongClick = if (card.online) ({ menu = true }) else null,
            enabled = enabled, selected = picked, chevron = false,
            trailing = when {
                !card.online -> ({ JTag(stringResource(R.string.agents_new_offline), status = Status.Offline) })
                picked -> ({ JIcon(PhIcons.CheckCircle, null, selected = true, tint = palette.accent) })
                else -> null
            },
        ) {
            JIcon(PhIcons.FolderSimple, null, tint = if (card.online) palette.ink2 else palette.ink3, decorative = true)
            Spacer(Modifier.width(Tokens.Space.l))
            Column(Modifier.weight(1f)) {
                Text(title, style = type.body, color = if (card.online) palette.ink else palette.ink3, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(details, style = caption, color = if (card.online) palette.ink2 else palette.ink3, maxLines = 2,
                    overflow = TextOverflow.Ellipsis, inlineContent = warning)
            }
        }
    }
    if (menu) JActionSheet(
        listOf(
            JAction(stringResource(R.string.agents_new_recent_remove), icon = PhIcons.X, onClick = onRemove),
            JAction(stringResource(R.string.agents_new_recent_clear), icon = PhIcons.Trash, destructive = true, onClick = onClear),
        ),
        onDismiss = { menu = false }, title = title.text,
    )
}

private const val WARNING = "warning"

/** Line 2: the launcher (or following the configuration), the model when it isn't the default, then Claude's mode and effort. */
@Composable
private fun recentDetails(setup: RecentSetup, separator: String): AnnotatedString {
    val palette = PersonaTheme.palette
    val launcher = setup.launcher.ifEmpty { stringResource(R.string.agents_new_follow_config) }
    val mode = setup.mode.takeIf { it.isNotEmpty() }?.let { id -> permissionModes.firstOrNull { it.first == id }?.second?.let { stringResource(it) } ?: id }
    val effort = setup.effort.takeIf { it.isNotEmpty() }?.let { id -> effortLevels.firstOrNull { it.first == id }?.second?.let { stringResource(it) } ?: id }
    return buildAnnotatedString {
        append(launcher)
        if (setup.model.isNotEmpty()) { append(separator); append(setup.model) }
        if (mode != null) {
            append(separator)
            // Allow Everything is remembered as any other mode, and says so in the form's caution colour.
            if (setup.mode == BYPASS_MODE) withStyle(SpanStyle(color = palette.cautionText)) {
                appendInlineContent(WARNING, "!")
                append(' ')
                append(mode)
            } else append(mode)
        }
        if (effort != null) { append(separator); append(effort) }
    }
}

/** A card's folder: its last path component, `~` for the machine's home. */
internal fun recentFolder(cwd: String, home: String?): String {
    val path = cwd.trimEnd('/')
    if (path.isEmpty()) return cwd
    if (path == "~" || (home != null && path == home.trimEnd('/'))) return "~"
    return path.substringAfterLast('/')
}

internal const val BYPASS_MODE = "bypassPermissions"
