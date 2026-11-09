package persona.core.designsystem

import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.semantics.toggleableState
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons

enum class MessageSource { Lark, Mail, Other }

/**
 * An inbox item. Items that need a reply ([status] NeedsYou) are cards of their own in glass and strips with
 * an amber holder on the board; the rest are rows of a group. [extra] holds the draft and its actions. [avatar] is the
 * chat's or sender's picture once it has loaded; until then (and without one) the avatar shows the initial.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun MessageItem(
    sender: String,
    title: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    position: GroupPosition = GroupPosition.Single,
    source: MessageSource = MessageSource.Other,
    sourceLabel: String? = null,
    group: String? = null,
    time: String? = null,
    preview: String? = null,
    status: Status? = null,
    unread: Int = 0,
    attachment: String? = null,
    tags: List<String> = emptyList(),
    onLongClick: (() -> Unit)? = null,
    selected: Boolean = false,
    avatar: Painter? = null,
    /** In place of the initial when there is no [avatar]: what the item is (a Feishu document's type). */
    avatarIcon: PhIcon? = null,
    /** A small mark after the title (the translate icon on a translated mail); none draws the title as always. */
    titleIcon: PhIcon? = null,
    titleIconLabel: String? = null,
    /** An event heading above the sender and source conversation. Other message rows keep their layout. */
    heading: String? = null,
    /** In a selection (#2576): picked or not, drawn as a check before the avatar; null outside one. */
    checked: Boolean? = null,
    /** In a selection, a row that can't be picked (an agent's card, a done item): faded. */
    dimmed: Boolean = false,
    extra: (@Composable ColumnScope.() -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val style = PersonaTheme.style
    val prominent = status == Status.NeedsYou
    val badge = when (source) { MessageSource.Lark -> PhIcons.ChatsCircle; MessageSource.Mail -> PhIcons.EnvelopeSimple; MessageSource.Other -> null }
    val badgeColor = if (style == AppStyle.Glass) (if (source == MessageSource.Mail) palette.needsYou.dot else palette.working.dot) else null
    val click = Modifier.selectedRow(selected || checked == true).onSecondaryClick(onLongClick)
        .then(if (checked != null) Modifier.semantics { toggleableState = androidx.compose.ui.state.ToggleableState(checked) } else Modifier)
        .combinedClickable(role = if (checked != null) Role.Checkbox else Role.Button, onClick = onClick, onLongClick = onLongClick)
    val body: @Composable ColumnScope.() -> Unit = {
        Row(verticalAlignment = Alignment.Top) {
            if (checked != null) {
                JCheckMark(androidx.compose.ui.state.ToggleableState(checked), Modifier.padding(top = Tokens.Space.s))
                Spacer(Modifier.width(Tokens.Space.m))
            }
            JAvatar(group ?: sender, size = if (prominent) 42.dp else 38.dp, icon = avatarIcon, badge = badge, badgeColor = badgeColor, image = avatar)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(heading ?: group ?: sender, style = type.headline, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                    sourceLabel?.let { Spacer(Modifier.width(6.dp)); Text(it, style = type.caption, color = palette.ink3, maxLines = 1) }
                    if (unread > 0) { Spacer(Modifier.width(6.dp)); JBadge(unread) }
                    Spacer(Modifier.weight(1f))
                    time?.let { Spacer(Modifier.width(8.dp)); Text(it, style = type.micro, color = palette.ink3, maxLines = 1) }
                }
                if (group != null || heading != null) Text(sender, style = type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink3, maxLines = 1)
                Spacer(Modifier.height(2.dp))
                if (titleIcon == null) Text(title, style = if (prominent) type.bodyStrong else type.body, color = palette.ink, maxLines = 2, overflow = TextOverflow.Ellipsis)
                else Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(title, Modifier.weight(1f, fill = false), style = if (prominent) type.bodyStrong else type.body, color = palette.ink,
                        maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Spacer(Modifier.width(Tokens.Space.xs))
                    JIcon(titleIcon, titleIconLabel, size = Tokens.Space.l, tint = palette.ink3, decorative = titleIconLabel == null)
                }
                preview?.let {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (status == Status.Working) { JSpinner(Modifier.size(12.dp), stroke = 1.6.dp); Spacer(Modifier.width(6.dp)) }
                        Text(it, style = type.callout, color = if (status == Status.Working && style != AppStyle.Brief) palette.working.text else palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    }
                }
                if (attachment != null || tags.isNotEmpty()) {
                    Spacer(Modifier.height(8.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        attachment?.let { JChip(it, icon = PhIcons.Paperclip) }
                        tags.forEach { JTag(it) }
                    }
                }
                extra?.invoke(this)
            }
        }
    }
    val modifier = if (dimmed) modifier.alpha(0.45f) else modifier
    when {
        style == AppStyle.Board -> {
            val shape = RoundedCornerShape(10.dp)
            Column(
                modifier.fillMaxWidth().padding(bottom = if (position.last) 0.dp else 8.dp).clip(shape)
                    .background(palette.surface).holder(status?.let { palette.status(it).dot } ?: palette.idle.dot, Tokens.Space.xxs)
                    .border(1.dp, palette.line, shape).then(click).padding(start = 18.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
                content = body,
            )
        }
        // JCard clips only inside this modifier: the clip here keeps the selection plate within the card's corners.
        prominent && style == AppStyle.Glass -> JCard(
            modifier.padding(bottom = if (position.last) 0.dp else 12.dp).clip(PersonaTheme.shapes.cardShape).selectedRow(selected).onSecondaryClick(onLongClick),
            onClick = onClick, onLongClick = onLongClick, padding = 16.dp, content = body,
        )
        else -> JGroupItem(position, modifier) {
            Column(click.padding(horizontal = if (style == AppStyle.Brief) 0.dp else 14.dp, vertical = 14.dp), content = body)
        }
    }
}

/** A reply Persona drafted: highlighted label and a quoted draft in brief, a tinted box elsewhere. */
@Composable
fun DraftPreview(text: String, modifier: Modifier = Modifier, label: String = stringResource(R.string.designsystem_draft_ready), actions: (@Composable RowScope.() -> Unit)? = null) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    Column(modifier.fillMaxWidth().padding(top = 12.dp)) {
        when (PersonaTheme.style) {
            AppStyle.Brief -> {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    JIcon(PhIcons.Sparkle, null, size = 14.dp, tint = palette.ink, selected = true)
                    Spacer(Modifier.width(2.dp))
                    MarkerText(label, type.micro.copy(fontWeight = FontWeight.Bold))
                }
                Spacer(Modifier.height(8.dp))
                Row(Modifier.height(IntrinsicSize.Min)) {
                    Box(Modifier.width(2.dp).fillMaxHeight().clip(CircleShape).background(palette.line))
                    Spacer(Modifier.width(12.dp))
                    Text(text, style = type.callout, color = palette.ink2, maxLines = 4, overflow = TextOverflow.Ellipsis)
                }
            }
            else -> Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(if (PersonaTheme.style == AppStyle.Board) 6.dp else 18.dp)).background(palette.statusSurface).padding(12.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    JIcon(PhIcons.Sparkle, null, size = 14.dp, tint = palette.accent, selected = true)
                    Spacer(Modifier.width(6.dp))
                    Text(label, style = type.caption.copy(fontWeight = FontWeight.SemiBold), color = palette.accent, maxLines = 1)
                }
                Spacer(Modifier.height(6.dp))
                Text(text, style = type.callout, color = palette.ink, maxLines = 5, overflow = TextOverflow.Ellipsis)
            }
        }
        actions?.let { Spacer(Modifier.height(12.dp)); Row(horizontalArrangement = Arrangement.spacedBy(8.dp), content = it) }
    }
}

/** The hub at the top of settings: name, connection state, a few facts. */
@Composable
fun HubCard(
    name: String,
    connected: Boolean,
    statusText: String,
    modifier: Modifier = Modifier,
    facts: List<Pair<String, String>> = emptyList(),
    busy: Boolean = false,
    onClick: (() -> Unit)? = null,
) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val tone = if (connected) palette.done else palette.needsYou
    JCard(modifier, onClick = onClick, status = if (PersonaTheme.style == AppStyle.Board) (if (connected) Status.Unread else Status.NeedsYou) else null) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            when (PersonaTheme.style) {
                AppStyle.Glass -> Box(Modifier.size(52.dp), contentAlignment = Alignment.Center) {
                    Canvas(Modifier.size(52.dp)) {
                        drawCircle(Brush.radialGradient(listOf(tone.dot.copy(alpha = 0.55f), Color.Transparent)))
                        drawCircle(tone.dot, radius = 15.dp.toPx())
                    }
                    JIcon(if (connected) PhIcons.PlugsConnected else PhIcons.WifiSlash, null, size = 18.dp, tint = tone.onDot, selected = true)
                }
                AppStyle.Brief -> Box(Modifier.size(52.dp).clip(CircleShape).background(palette.action), contentAlignment = Alignment.Center) {
                    Text(stringResource(R.string.designsystem_brand_persona).take(1), style = type.title.copy(fontSize = androidx.compose.ui.unit.TextUnit(22f, androidx.compose.ui.unit.TextUnitType.Sp), fontWeight = FontWeight.Bold), color = palette.onAction)
                    Box(Modifier.align(Alignment.BottomEnd).size(14.dp).clip(CircleShape).background(palette.surface).padding(2.dp).clip(CircleShape).background(tone.dot))
                }
                AppStyle.Board -> Unit
            }
            if (PersonaTheme.style != AppStyle.Board) Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Text(name, style = type.title, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (busy) { JSpinner(Modifier.size(12.dp), stroke = 1.6.dp); Spacer(Modifier.width(6.dp)) }
                    else if (PersonaTheme.style == AppStyle.Brief) { Box(Modifier.size(8.dp).clip(CircleShape).background(tone.dot)); Spacer(Modifier.width(6.dp)) }
                    Text(statusText, style = type.callout, color = if (PersonaTheme.style == AppStyle.Glass) tone.text else palette.ink2, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            if (onClick != null) JIcon(PhIcons.CaretRight, null, size = 18.dp, tint = palette.ink3)
        }
        if (facts.isNotEmpty()) {
            Spacer(Modifier.height(14.dp))
            if (PersonaTheme.style == AppStyle.Board) Fields(facts.map { "${it.second} ${it.first}".trim() })
            else Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                facts.forEach { (label, value) ->
                    Column(Modifier.weight(1f)) {
                        Text(value, style = type.label, color = palette.ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(label, style = type.micro, color = palette.ink3, maxLines = 1)
                    }
                }
            }
        }
    }
}

// ------------------------------------------------------------------ app navigation

/**
 * A top-level destination. [badge] counts things waiting for the user; [badgeText] replaces it ("Demo"); [dot] is a
 * mark without a number (extensions' badges that are words, not counts).
 */
@Immutable
data class NavItem(val key: String, val label: String, val icon: PhIcon, val badge: Int = 0, val badgeText: String? = null, val dot: Boolean = false)

/** A count's small dot: where there is no number to show. */
@Composable
private fun NavDot(modifier: Modifier) {
    Box(modifier.size(8.dp).clip(CircleShape).background(PersonaTheme.palette.needsYou.dot))
}

/**
 * The app's top-level navigation in the active style: a floating glass dock, a quiet paper bar, or a board bar with
 * a rule over the selected tab. Every destination is an icon with its label. It handles the navigation bar inset.
 */
@Composable
fun AppNavigation(items: List<NavItem>, selected: String?, onSelect: (String) -> Unit, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    when (PersonaTheme.style) {
        AppStyle.Glass -> Box(modifier.fillMaxWidth().navigationBarsPadding().padding(start = 16.dp, end = 16.dp, bottom = 10.dp), contentAlignment = Alignment.Center) {
            Row(
                Modifier.fillMaxWidth().height(DockHeight).floatingSurface(RoundedCornerShape(36.dp)).padding(horizontal = 6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                items.forEach { DockItem(it, it.key == selected, Modifier.weight(1f), onSelect) }
            }
        }
        AppStyle.Brief -> Column(modifier.fillMaxWidth().background(palette.background)) {
            Box(Modifier.fillMaxWidth().height(1.dp).background(palette.line))
            Row(Modifier.fillMaxWidth().height(64.dp).padding(horizontal = 12.dp)) {
                items.forEach { item ->
                    val on = item.key == selected
                    Column(
                        Modifier.weight(1f).fillMaxHeight().clickable(role = Role.Tab) { onSelect(item.key) }.semantics { this.selected = on },
                        horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center,
                    ) {
                        Box {
                            JIcon(item.icon, null, size = 24.dp, tint = if (on) palette.ink else palette.ink3, selected = on)
                            when {
                                item.badgeText != null -> JTag(item.badgeText, Modifier.align(Alignment.TopEnd).offset(16.dp, (-6).dp))
                                item.badge > 0 || item.dot -> Box(Modifier.align(Alignment.TopEnd).offset(4.dp, (-2).dp).size(9.dp).clip(CircleShape).background(palette.marker).border(1.5.dp, palette.ink, CircleShape))
                            }
                        }
                        Spacer(Modifier.height(4.dp))
                        Text(item.label, style = type.micro.copy(fontWeight = if (on) FontWeight.Bold else FontWeight.Medium), color = if (on) palette.ink else palette.ink3, maxLines = 1)
                    }
                }
            }
            Spacer(Modifier.navigationBarsPadding())
        }
        // The board's grey runs under the bar too (a rule on top, no white plate): the tickets are the only cards.
        AppStyle.Board -> Column(modifier.fillMaxWidth().background(palette.background).drawBehind { drawLine(palette.line, Offset(0f, 0f), Offset(size.width, 0f), 1.dp.toPx()) }) {
            Row(Modifier.fillMaxWidth().height(60.dp)) {
                items.forEach { item ->
                    val on = item.key == selected
                    Box(Modifier.weight(1f).fillMaxHeight().clickable(role = Role.Tab) { onSelect(item.key) }.semantics { this.selected = on }) {
                        if (on) Box(Modifier.align(Alignment.TopCenter).size(28.dp, 3.dp).background(palette.ink))
                        Column(Modifier.align(Alignment.Center), horizontalAlignment = Alignment.CenterHorizontally) {
                            Box {
                                JIcon(item.icon, null, size = 22.dp, tint = if (on) palette.ink else palette.ink3, selected = on)
                                when {
                                    item.badgeText != null -> JTag(item.badgeText, Modifier.align(Alignment.TopEnd).offset(16.dp, (-6).dp))
                                    item.badge > 0 -> JBadge(item.badge, Modifier.align(Alignment.TopEnd).offset(12.dp, (-7).dp))
                                    item.dot -> NavDot(Modifier.align(Alignment.TopEnd).offset(3.dp, (-1).dp))
                                }
                            }
                            Spacer(Modifier.height(3.dp))
                            Text(item.label, style = type.caption.copy(fontSize = androidx.compose.ui.unit.TextUnit(12f, androidx.compose.ui.unit.TextUnitType.Sp), fontWeight = if (on) FontWeight.Bold else FontWeight.Medium), color = if (on) palette.ink else palette.ink3, maxLines = 1)
                        }
                    }
                }
            }
            Spacer(Modifier.navigationBarsPadding())
        }
    }
}

/**
 * The same destinations down the left edge in expanded windows, straight on the window's one background (no panel,
 * no rule beside it): a soft plate under the selected item in glass and brief, a bar beside it on the board.
 * Labels stay visible.
 */
@Composable
fun AppNavigationRail(items: List<NavItem>, selected: String?, onSelect: (String) -> Unit, modifier: Modifier = Modifier,
                      extra: List<NavItem> = emptyList()) {
    val style = PersonaTheme.style
    // [extra]: the extensions' own pages (nav_page, #2951) under the app's four, after a short rule; they scroll when
    // there are more than the rail has room for
    val column: @Composable (Modifier) -> Unit = { inner ->
        Column(inner.then(if (extra.isEmpty()) Modifier else Modifier.verticalScroll(rememberScrollState())),
            verticalArrangement = Arrangement.spacedBy(6.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            items.forEach { RailItem(it, it.key == selected, onSelect) }
            if (extra.isNotEmpty()) {
                Box(Modifier.padding(vertical = Tokens.Space.xs).width(Tokens.Space.xxl).height(Tokens.Space.xxs / 2).background(PersonaTheme.palette.line))
                extra.forEach { RailItem(it, it.key == selected, onSelect) }
            }
        }
    }
    // No panel of its own: the items sit on the window's one background, like everything beside them.
    when (style) {
        AppStyle.Glass -> Box(modifier.fillMaxHeight().statusBarsPadding().navigationBarsPadding().padding(start = 8.dp), contentAlignment = Alignment.Center) {
            column(Modifier.width(84.dp))
        }
        else -> column(modifier.width(92.dp).fillMaxHeight().statusBarsPadding().padding(top = if (style == AppStyle.Brief) 28.dp else 20.dp))
    }
}

@Composable
private fun RailItem(item: NavItem, on: Boolean, onSelect: (String) -> Unit) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = RoundedCornerShape(if (style == AppStyle.Board) 8.dp else 20.dp)
    Box(
        Modifier.size(72.dp, 64.dp).clip(shape)
            .then(if (on && style != AppStyle.Board) Modifier.background(if (style == AppStyle.Glass) palette.surface else palette.ink.copy(alpha = 0.07f)) else Modifier)
            .clickable(role = Role.Tab) { onSelect(item.key) }.semantics { selected = on },
        contentAlignment = Alignment.Center,
    ) {
        if (on && style == AppStyle.Board) Box(Modifier.align(Alignment.CenterStart).size(3.dp, 28.dp).background(palette.ink))
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Box {
                JIcon(item.icon, null, size = 24.dp, tint = if (on) palette.ink else palette.ink2, selected = on)
                when {
                    item.badgeText != null -> JTag(item.badgeText, Modifier.align(Alignment.TopEnd).offset(16.dp, (-6).dp))
                    item.badge > 0 -> JBadge(item.badge, Modifier.align(Alignment.TopEnd).offset(12.dp, (-7).dp))
                    item.dot -> NavDot(Modifier.align(Alignment.TopEnd).offset(3.dp, (-1).dp))
                }
            }
            Spacer(Modifier.height(4.dp))
            Text(item.label, style = PersonaTheme.type.caption.copy(fontWeight = if (on) FontWeight.Bold else FontWeight.Medium), color = if (on) palette.ink else palette.ink2, maxLines = 1)
        }
    }
}

/** Height of the glass dock itself; the shell adds the bottom margin and the navigation bar. */
val DockHeight: Dp = 72.dp

@Composable
private fun DockItem(item: NavItem, on: Boolean, modifier: Modifier, onSelect: (String) -> Unit) {
    val palette = PersonaTheme.palette
    Column(
        modifier.fillMaxHeight().clip(CircleShape).clickable(role = Role.Tab) { onSelect(item.key) }.semantics { selected = on },
        horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center,
    ) {
        Box {
            JIcon(item.icon, null, size = 24.dp, tint = if (on) palette.ink else palette.ink2, selected = on)
            when {
                item.badgeText != null -> JTag(item.badgeText, Modifier.align(Alignment.TopEnd).offset(16.dp, (-6).dp))
                item.badge > 0 || item.dot -> NavDot(Modifier.align(Alignment.TopEnd).offset(3.dp, (-1).dp))
            }
        }
        Spacer(Modifier.height(3.dp))
        Text(item.label, style = PersonaTheme.type.caption.copy(fontSize = androidx.compose.ui.unit.TextUnit(12f, androidx.compose.ui.unit.TextUnitType.Sp), fontWeight = if (on) FontWeight.Bold else FontWeight.Medium), color = if (on) palette.ink else palette.ink2, maxLines = 1)
    }
}
