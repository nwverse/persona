package persona.feature.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/**
 * Settings › Connectors › Messages (iOS `MessagesConnectorView`): one Mac's Messages, read through the hub. Which Mac and whether it
 * may read (with the numbered steps there and "Check again" when it may not), "Hand to Persona" and deleting what the hub kept
 * (only with it off), and "Conversations": every conversation, read straight from the Mac. Nothing is marked read, nothing is sent.
 */
/** What deleting the hub's Messages data removed, each count in its language's plural form. */
internal fun messagesDeletedText(res: android.content.res.Resources, d: ConnectorDataDeleted): String =
    res.getString(
        R.string.settings_src_messages_deleted,
        res.getQuantityString(R.plurals.settings_src_messages_deleted_chats, d.chats ?: 0, d.chats ?: 0),
        res.getQuantityString(R.plurals.settings_src_messages_deleted_people, d.people ?: 0, d.people ?: 0),
        res.getQuantityString(R.plurals.settings_src_messages_deleted_inbox, d.inbox ?: 0, d.inbox ?: 0),
    )

@Composable
internal fun MessagesConnectorPage(connectors: ConnectorsManagement?, onBack: () -> Unit) {
    var chats by remember { mutableStateOf(false) }
    var thread by remember { mutableStateOf<MessagesChat?>(null) }
    val open = thread
    when {
        open != null -> MessagesThreadPage(connectors, open, onBack = { thread = null })
        chats -> MessagesChatsPage(connectors, onBack = { chats = false }, onOpen = { thread = it })
        else -> MessagesStatusPage(connectors, onBack, onChats = { chats = true })
    }
}

@Composable
private fun MessagesStatusPage(connectors: ConnectorsManagement?, onBack: () -> Unit, onChats: () -> Unit) {
    val scope = rememberCoroutineScope()
    val res = LocalResources.current
    var status by remember { mutableStateOf<MessagesStatus?>(null) }
    var loading by remember { mutableStateOf(true) }
    var saving by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }
    var deleted by remember { mutableStateOf<String?>(null) }
    var confirmingDelete by remember { mutableStateOf(false) }

    fun load() {
        if (connectors == null) { loading = false; return }
        scope.launch {
            loading = true
            try { status = connectors.messagesStatus(); failure = null }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { failure = res.getString(R.string.settings_src_messages_read_failed, e.message.orEmpty()) }
            finally { loading = false }
        }
    }
    fun setAi(on: Boolean) {
        val api = connectors ?: return
        scope.launch {
            saving = true
            try { status = api.setMessagesAi(on); deleted = null; failure = null }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { failure = res.getString(R.string.settings_src_messages_save_failed, e.message.orEmpty()) }
            finally { saving = false }
        }
    }
    fun deleteData() {
        val api = connectors ?: return
        scope.launch {
            saving = true
            try {
                val d = api.deleteMessagesData()
                deleted = messagesDeletedText(res, d)
                failure = null
            } catch (e: CancellationException) { throw e }
            catch (e: Exception) { failure = res.getString(R.string.settings_src_messages_delete_failed, e.message.orEmpty()) }
            finally { saving = false }
        }
    }

    LaunchedEffect(connectors) { load() }
    BackHandler(onBack = onBack)
    val s = status
    JListPage(title = stringResource(R.string.settings_src_messages_title), onBack = onBack, refreshing = loading && s != null, onRefresh = ::load) {
        item("status-header") { JSectionHeader(stringResource(R.string.settings_src_status_header)) }
        item("status-row") {
            JGroupItem(if (s?.status == "ok") GroupPosition.of(0, 2) else GroupPosition.Single) {
                JRow(
                    title = s?.machine?.takeIf { it.isNotBlank() }?.let { stringResource(R.string.settings_src_messages_on_machine, it) }
                        ?: stringResource(R.string.settings_src_messages_title),
                    subtitle = stringResource(messagesStateText(s, loading)), subtitleLines = 2,
                    leadingIcon = PhIcons.ChatTeardropText,
                    trailing = {
                        Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
                            JStatusDot(messagesStatusDot(s))
                            if (loading) JSpinner()
                        }
                    },
                )
            }
        }
        if (s?.status == "ok") item("chats") {
            JGroupItem(GroupPosition.of(1, 2)) {
                JRow(stringResource(R.string.settings_src_messages_chats), leadingIcon = PhIcons.Chats, onClick = onChats)
            }
        }
        ((if (s?.status == "ok") null else s?.error?.takeIf { it.isNotBlank() }) ?: failure)?.let { e ->
            item("error") { JNotice(e, Modifier.padding(top = Tokens.Space.m), status = Status.NeedsYou) }
        }
        val guide = s?.guide.orEmpty()
        if (s?.status == "no_permission" && guide.isNotEmpty()) {
            item("guide-header") {
                JSectionHeader(stringResource(R.string.settings_src_messages_guide_header,
                    s.machine.ifBlank { stringResource(R.string.settings_src_messages_that_mac) }))
            }
            itemsIndexed(guide, key = { i, _ -> "guide-$i" }) { i, step ->
                JGroupItem(GroupPosition.of(i, guide.size + 1)) {
                    JRow {
                        Text("${i + 1}", style = PersonaTheme.type.headline, color = PersonaTheme.palette.accent,
                            modifier = Modifier.align(Alignment.Top).widthIn(min = Tokens.Space.l))
                        Spacer(Modifier.width(Tokens.Space.m))
                        Text(step, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
                    }
                }
            }
            item("recheck") {
                JGroupItem(GroupPosition.of(guide.size, guide.size + 1)) {
                    JRow(stringResource(R.string.settings_src_messages_recheck), leadingIcon = PhIcons.ArrowClockwise,
                        enabled = !loading, chevron = false, onClick = ::load)
                }
            }
        }
        s?.ai?.let { ai ->
            item("ai-header") { JSectionHeader(stringResource(R.string.settings_src_settings_header)) }
            item("ai") {
                JGroupItem(GroupPosition.of(0, 2)) {
                    JSwitchRow(stringResource(R.string.settings_src_messages_ai), checked = ai, onCheckedChange = ::setAi,
                        leadingIcon = PhIcons.Sparkle, enabled = !saving)
                }
            }
            item("delete") {
                JGroupItem(GroupPosition.of(1, 2)) {
                    // Deleting what the hub kept asks for "Hand to Persona" off first: on, the next sync would bring it back.
                    JRow(stringResource(R.string.settings_src_messages_delete), leadingIcon = PhIcons.Trash, destructive = true,
                        enabled = !ai && !saving, chevron = false, onClick = { confirmingDelete = true })
                }
            }
            deleted?.let { d -> item("deleted") { SettingsCaption(d, Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s)) } }
            item("ai-caption") {
                SettingsCaption(stringResource(if (ai) R.string.settings_src_messages_ai_on else R.string.settings_src_messages_ai_off),
                    Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s))
            }
        }
        item("footer") {
            SettingsCaption(stringResource(if (s?.ai == null) R.string.settings_src_messages_footer_private else R.string.settings_src_messages_footer),
                Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s))
        }
    }
    if (confirmingDelete) JDialog(
        onDismiss = { confirmingDelete = false },
        title = stringResource(R.string.settings_src_messages_delete_title),
        text = stringResource(R.string.settings_src_messages_delete_body),
        confirm = stringResource(R.string.settings_src_delete) to { confirmingDelete = false; deleteData() },
        dismiss = stringResource(R.string.settings_src_cancel) to { confirmingDelete = false },
        destructive = true,
    )
}

/** The words under the Mac's name (iOS `stateText`). */
internal fun messagesStateText(status: MessagesStatus?, loading: Boolean): Int = when (status?.status) {
    null -> if (loading) R.string.settings_src_checking else R.string.settings_src_status_unreadable
    "ok" -> R.string.settings_src_messages_state_ok
    "no_permission" -> R.string.settings_src_messages_state_no_permission
    "not_configured" -> R.string.settings_src_messages_state_not_configured
    else -> R.string.settings_src_messages_state_failed
}

internal fun messagesStatusDot(status: MessagesStatus?): Status = when (status?.status) {
    "ok" -> Status.Idle
    null, "not_configured" -> Status.Offline
    else -> Status.NeedsYou
}

/** "Conversations": the Mac's conversations, newest first, a page at a time (iOS `MessagesChatListView`). */
@Composable
private fun MessagesChatsPage(connectors: ConnectorsManagement?, onBack: () -> Unit, onOpen: (MessagesChat) -> Unit) {
    val scope = rememberCoroutineScope()
    var chats by remember { mutableStateOf<List<MessagesChat>>(emptyList()) }
    var loaded by remember { mutableStateOf(false) }
    var loading by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }
    var before by remember { mutableStateOf<String?>(null) }
    var more by remember { mutableStateOf(false) }
    var query by remember { mutableStateOf("") }

    fun load(next: Boolean = false) {
        val api = connectors ?: run { loaded = true; return }
        if (loading || next && (!more || before == null)) return
        scope.launch {
            loading = true
            try {
                val page = api.messagesChats(if (next) before else null)
                chats = if (next) chats + page.chats.filter { c -> chats.none { it.id == c.id } } else page.chats
                more = page.more == true
                before = page.before
                failure = null
            } catch (e: CancellationException) { throw e }
            catch (e: Exception) { failure = e.message }
            finally { loading = false; loaded = true }
        }
    }
    LaunchedEffect(connectors) { load() }
    BackHandler(onBack = onBack)
    val shown = filterMessagesChats(chats, query)
    JListPage(title = stringResource(R.string.settings_src_messages_chats), onBack = onBack, refreshing = loading && loaded, onRefresh = { load() }) {
        item("search") {
            JSearchField(query, { query = it }, stringResource(R.string.settings_src_messages_search),
                Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s))
        }
        failure?.let { f -> item("failure") { JNotice(f, Modifier.padding(vertical = Tokens.Space.s), status = Status.NeedsYou, icon = PhIcons.WarningCircle) } }
        if (!loaded) item("loading") { JLoading() }
        else if (shown.isEmpty() && failure == null) item("empty") {
            JEmptyState(stringResource(if (query.isBlank()) R.string.settings_src_messages_no_chats else R.string.settings_src_messages_no_match),
                stringResource(if (query.isBlank()) R.string.settings_src_messages_no_chats_hint else R.string.settings_src_messages_no_match_hint),
                icon = PhIcons.ChatTeardropText)
        }
        itemsIndexed(shown, key = { _, c -> c.id }) { i, chat ->
            if (chat.id == chats.lastOrNull()?.id) LaunchedEffect(chat.id) { load(next = true) }
            JGroupItem(GroupPosition.of(i, shown.size)) { MessagesChatRow(chat) { onOpen(chat) } }
        }
        if (loading && loaded && chats.isNotEmpty()) item("more") { JLoading() }
    }
}

/** Names and handles match what was typed; an empty query keeps them all. */
internal fun filterMessagesChats(chats: List<MessagesChat>, query: String): List<MessagesChat> {
    val q = query.trim()
    if (q.isEmpty()) return chats
    return chats.filter { c ->
        c.name.contains(q, ignoreCase = true) || c.members.any { it.shown.contains(q, ignoreCase = true) || it.handle.contains(q) }
    }
}

@Composable
private fun MessagesChatRow(chat: MessagesChat, onClick: () -> Unit) {
    val preview = chat.preview.orEmpty().let { if (chat.fromMe == true && it.isNotEmpty()) stringResource(R.string.settings_src_messages_you_prefix, it) else it }
    JRow(
        title = chat.name.ifBlank { chat.members.firstOrNull()?.shown.orEmpty() },
        subtitle = preview.ifBlank { null },
        leading = { JAvatar(chat.name, icon = if (chat.isGroup) PhIcons.Chats else null) },
        value = messagesShortTime(chat.last),
        trailing = (chat.unread ?: 0).takeIf { it > 0 }?.let { n -> { JBadge(n) } },
        onClick = onClick, chevron = false,
    )
}

/** One conversation, oldest at the top; older pages come in from the top (iOS `MessagesThreadView`). */
@Composable
private fun MessagesThreadPage(connectors: ConnectorsManagement?, chat: MessagesChat, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    var page by remember { mutableStateOf<MessagesPage?>(null) }
    var messages by remember { mutableStateOf<List<MessagesMessage>>(emptyList()) }
    var loading by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<String?>(null) }

    fun load(older: Boolean = false) {
        val api = connectors ?: return
        if (loading) return
        scope.launch {
            loading = true
            try {
                val next = api.messagesThread(chat.id, if (older) page?.before else null)
                messages = if (older) next.messages.filter { m -> messages.none { it.id == m.id } } + messages else next.messages
                page = next
                failure = null
            } catch (e: CancellationException) { throw e }
            catch (e: Exception) { failure = e.message }
            finally { loading = false }
        }
    }
    LaunchedEffect(connectors, chat.id) { load() }
    BackHandler(onBack = onBack)
    val shownChat = page?.chat ?: chat
    val lines = remember(messages, shownChat) { messagesLines(messages, shownChat.isGroup, shownChat.services.size > 1) }
    JListPage(title = shownChat.name.ifBlank { chat.name }, onBack = onBack) {
        if (page?.more == true) item("older") {
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                JTextButton(stringResource(R.string.settings_src_messages_older), { load(older = true) }, enabled = !loading, icon = PhIcons.Clock)
            }
        }
        failure?.let { f -> item("failure") { JNotice(f, Modifier.padding(vertical = Tokens.Space.s), status = Status.NeedsYou, icon = PhIcons.WarningCircle) } }
        if (page == null && failure == null) item("loading") { JLoading() }
        else if (messages.isEmpty() && failure == null) item("empty") {
            JEmptyState(stringResource(R.string.settings_src_messages_no_messages), stringResource(R.string.settings_src_messages_no_messages_hint),
                icon = PhIcons.ChatTeardropText)
        }
        itemsIndexed(lines, key = { _, l -> l.message.id }) { _, line -> MessagesLineView(line) }
    }
}

/** A message with what goes above it: the time after a pause, who wrote it in a group, the service when it changes. */
internal data class MessagesLine(val message: MessagesMessage, val time: Instant? = null, val sender: String? = null, val service: String? = null)

private val messagesPause: Duration = Duration.ofHours(1)

internal fun messagesLines(messages: List<MessagesMessage>, group: Boolean, services: Boolean, zone: ZoneId = ZoneId.systemDefault()): List<MessagesLine> {
    var previous: MessagesMessage? = null
    var service: String? = null
    return messages.map { m ->
        val at = parseInstant(m.at)
        val prevAt = previous?.let { parseInstant(it.at) }
        val continued = at != null && prevAt != null && Duration.between(prevAt, at) < messagesPause &&
            at.atZone(zone).toLocalDate() == prevAt.atZone(zone).toLocalDate()
        val p = previous
        val sender = if (!group || m.isMine || m.isEvent) null
            else if (!continued || p == null || p.isMine || p.isEvent || p.sender?.handle != m.sender?.handle) m.sender?.shown else null
        val svc = m.service?.takeIf { services && !m.isEvent && it.isNotEmpty() && it != service }?.also { service = it }
        previous = m
        MessagesLine(m, time = at.takeIf { !continued }, sender = sender, service = svc)
    }
}

@Composable
private fun MessagesLineView(line: MessagesLine) {
    val m = line.message
    val palette = PersonaTheme.palette
    val service = line.service?.let { if (it == "SMS") stringResource(R.string.settings_src_messages_sms) else it }
    Column(Modifier.fillMaxWidth()) {
        val caption = listOfNotNull(line.time?.let { messagesSeparator(it) }, service).joinToString(" · ")
        if (caption.isNotEmpty()) Text(caption, style = PersonaTheme.type.caption, color = palette.ink3, textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth().padding(top = Tokens.Space.m, bottom = Tokens.Space.xs))
        if (m.isEvent || m.unsent == true) {
            val text = if (m.isEvent) m.text.orEmpty() else if (m.isMine) stringResource(R.string.settings_src_messages_you_unsent)
                else stringResource(R.string.settings_src_messages_unsent, m.sender?.shown ?: stringResource(R.string.settings_src_messages_them))
            Text(text, style = PersonaTheme.type.caption, color = palette.ink3, textAlign = TextAlign.Center,
                modifier = Modifier.fillMaxWidth().padding(vertical = Tokens.Space.xs))
            return@Column
        }
        val body: @Composable ColumnScope.() -> Unit = {
            m.replyTo?.takeIf { it.isNotEmpty() }?.let {
                Text(it, style = PersonaTheme.type.caption, color = palette.ink3, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            m.attachments.orEmpty().forEach { a ->
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    JIcon(when (a.kind) { "image" -> PhIcons.Image; "video" -> PhIcons.Play; "audio" -> PhIcons.Waveform; else -> PhIcons.Paperclip }, null)
                    Text(messagesAttachmentTitle(a), style = PersonaTheme.type.callout, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            if (m.undecoded == true) Text(stringResource(R.string.settings_src_messages_undecoded), style = PersonaTheme.type.body.copy(fontStyle = FontStyle.Italic), color = palette.ink2)
            else m.text?.takeIf { it.isNotEmpty() }?.let { Text(it, style = PersonaTheme.type.body) }
        }
        val footer: (@Composable () -> Unit)? = if (m.edited == true) ({
            Text(stringResource(R.string.settings_src_messages_edited), style = PersonaTheme.type.micro, color = palette.ink3)
        }) else null
        if (m.isMine) UserBubbleFrame(footer = footer, content = body)
        else IncomingBubbleFrame(author = line.sender, footer = footer, content = body)
    }
}

@Composable
private fun messagesAttachmentTitle(a: MessagesAttachment): String {
    val name = a.name?.takeIf { it.isNotBlank() }
    return when (a.kind) {
        "image" -> name?.let { stringResource(R.string.settings_src_messages_image_named, it) } ?: stringResource(R.string.settings_src_messages_image)
        "video" -> name?.let { stringResource(R.string.settings_src_messages_video_named, it) } ?: stringResource(R.string.settings_src_messages_video)
        "audio" -> stringResource(R.string.settings_src_messages_audio)
        else -> name ?: stringResource(R.string.settings_src_messages_file)
    }
}

private fun parseInstant(text: String): Instant? = runCatching { Instant.parse(text) }.getOrNull()
    ?: runCatching { java.time.OffsetDateTime.parse(text).toInstant() }.getOrNull()

@Composable
private fun messagesSeparator(at: Instant): String {
    val locale = currentLocale
    val zone = ZoneId.systemDefault()
    val day = at.atZone(zone).toLocalDate()
    val today = java.time.LocalDate.now(zone)
    val time = LocalizedFormats.format(at, LocalizedFormats.TIME, locale, zone)
    return when (day) {
        today -> stringResource(R.string.settings_src_messages_today, time)
        today.minusDays(1) -> stringResource(R.string.settings_src_messages_yesterday, time)
        else -> messagesDayTime(at, locale, zone)
    }
}

/** A separator's date and time before yesterday, in the app's language: Nov 5, 2026, 2:30 PM in English. */
internal fun messagesDayTime(at: Instant, locale: java.util.Locale, zone: ZoneId = ZoneId.systemDefault()): String =
    LocalizedFormats.format(at, LocalizedFormats.DATE_TIME, locale, zone)

@Composable
private fun messagesShortTime(text: String): String? = messagesShortTime(text, currentLocale)

/** A chat row's last time in the app's language: 14:30 today, the month and day this year, else the full date. */
internal fun messagesShortTime(text: String, locale: java.util.Locale, zone: ZoneId = ZoneId.systemDefault(),
                               today: java.time.LocalDate = java.time.LocalDate.now(zone)): String? {
    val at = parseInstant(text) ?: return null
    val day = at.atZone(zone).toLocalDate()
    val skeleton = when {
        day == today -> LocalizedFormats.TIME
        day.year == today.year -> LocalizedFormats.MONTH_DAY
        else -> LocalizedFormats.DATE
    }
    return LocalizedFormats.format(at, skeleton, locale, zone)
}
