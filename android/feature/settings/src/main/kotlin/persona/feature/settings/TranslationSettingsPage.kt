package persona.feature.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import android.content.res.Resources
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import kotlinx.coroutines.CancellationException
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

/**
 * Settings › Translation (docs/android/android.md "Translation", the iOS TranslationSettingsView, the web's TranslationSettings): on / off, the
 * translation model, the target language, and what gets translated: mail (on), and the Feishu chats picked here (none:
 * Feishu has too many messages to translate them all). Kept on this phone and sent to the hub, which translates new mail
 * ahead of time for it.
 */
@Composable
internal fun TranslationSettingsPage(
    translation: TranslationSettings?,
    models: ModelConfiguration?,
    connectors: ConnectorsManagement?,
    onBack: () -> Unit,
    onModel: () -> Unit,
    /** Opens the chat picker at once (captures). */
    pickingAtStart: Boolean = false,
) {
    BackHandler(onBack = onBack)
    if (translation == null) {
        JListPage(title = stringResource(R.string.settings_translation_title), onBack = onBack) { item { JNotice(stringResource(R.string.settings_translation_demo)) } }
        return
    }
    val prefs by translation.prefs.collectAsState()
    // Feishu comes from the Lark extension: without it on the hub there are no chats to pick (picked ones stay kept).
    val lark = LocalLarkFeatures.current.chats
    val resources = LocalContext.current.resources
    var model by remember { mutableStateOf<String?>(null) }
    var choosingTarget by remember { mutableStateOf(false) }
    var picking by remember { mutableStateOf(pickingAtStart) }
    LaunchedEffect(models) {
        model = try { models?.purpose("translate")?.let { d -> d.purpose.summaryParts(d.providers).joinToString(resources.getString(R.string.settings_list_separator)).ifEmpty { resources.getString(R.string.settings_not_configured) } } }
        catch (error: CancellationException) { throw error } catch (_: Exception) { null }
    }
    JListPage(title = stringResource(R.string.settings_translation_title), onBack = onBack) {
        item("enabled") {
            JGroupItem(GroupPosition.of(0, 2), Modifier.padding(top = Tokens.Space.m)) {
                JSwitchRow(stringResource(R.string.settings_translation_auto), prefs.enabled, { on -> translation.update { it.copy(enabled = on) } },
                    Modifier.testTag("translate-enabled"), leadingIcon = PhIcons.Translate)
            }
        }
        item("model") {
            JGroupItem(GroupPosition.of(1, 2)) {
                JRow(stringResource(R.string.settings_translation_model), leadingIcon = PhIcons.Brain, subtitle = model, subtitleLines = 1, onClick = onModel)
            }
        }
        item("about") {
            SettingsCaption(stringResource(R.string.settings_translation_about),
                Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s))
        }
        if (prefs.enabled) {
            item("target") {
                JGroupItem(GroupPosition.Single) {
                    JRow(stringResource(R.string.settings_translation_target), leadingIcon = PhIcons.GlobeSimple, value = TranslationPrefs.languageName(prefs.target),
                        onClick = { choosingTarget = true })
                }
            }
            item("scope-header") { JSectionHeader(stringResource(R.string.settings_translation_scope)) }
            item("mail") {
                JGroupItem(GroupPosition.of(0, if (lark) 2 else 1)) {
                    JSwitchRow(stringResource(R.string.settings_translation_mail), prefs.mail, { on -> translation.update { it.copy(mail = on) } },
                        Modifier.testTag("translate-mail"), leadingIcon = PhIcons.EnvelopeSimple)
                }
            }
            if (lark) {
                item("lark") {
                    JGroupItem(GroupPosition.of(1, 2)) {
                        JRow(stringResource(R.string.settings_translation_lark), leadingIcon = PhIcons.ChatCircleDots,
                            value = TranslationText.chats(prefs.chats.size), onClick = { picking = true })
                    }
                }
                item("lark-about") {
                    SettingsCaption(stringResource(R.string.settings_translation_lark_about),
                        Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s))
                }
            }
            if (lark && prefs.chats.isNotEmpty()) {
                item("chats-header") { JSectionHeader(stringResource(R.string.settings_translation_lark_chats), count = prefs.chats.size) }
                itemsIndexed(prefs.chats, key = { _, c -> "chat-${c.id}" }) { index, chat ->
                    JGroupItem(GroupPosition.of(index, prefs.chats.size)) {
                        JRow(chat.name, leading = { JAvatar(chat.name, icon = PhIcons.ChatsCircle) },
                            trailing = {
                                JIconButton(PhIcons.X, stringResource(R.string.settings_translation_remove_chat, chat.name),
                                    { translation.update { p -> p.copy(chats = p.chats.filterNot { it.id == chat.id }) } })
                            })
                    }
                }
            }
        }
        item("note") {
            SettingsCaption(stringResource(R.string.settings_translation_note),
                Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.l))
        }
    }
    if (choosingTarget) JActionSheet(
        TranslationPrefs.languages.map { language ->
            JAction(language.name, checked = language.code == prefs.target) {
                translation.update { it.copy(target = language.code) }
                choosingTarget = false
            }
        },
        onDismiss = { choosingTarget = false }, title = stringResource(R.string.settings_translation_target),
    )
    if (picking) TranslationChatPicker(connectors, prefs.chats, onDismiss = { picking = false }) { chats ->
        translation.update { it.copy(chats = chats) }
        picking = false
    }
}

/**
 * Picks the Feishu chats to translate: the picked ones first (so one no longer recent can still be unpicked), then the
 * pinned and recent ones, searchable by name.
 */
@Composable
private fun TranslationChatPicker(
    connectors: ConnectorsManagement?,
    selected: List<TranslateChat>,
    onDismiss: () -> Unit,
    onSave: (List<TranslateChat>) -> Unit,
) {
    var picked by remember { mutableStateOf(selected) }
    var choices by remember { mutableStateOf<List<LarkChatChoice>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var query by remember { mutableStateOf("") }
    val resources = LocalContext.current.resources
    LaunchedEffect(connectors) {
        choices = try { connectors?.larkChatChoices().orEmpty() }
        catch (e: CancellationException) { throw e }
        catch (e: Exception) { error = e.message?.takeIf(String::isNotBlank) ?: resources.getString(R.string.settings_translation_lark_read_failed); emptyList() }
    }
    // Scrolls as a whole: the pinned and recent chats are at most a few dozen.
    JSheet(onDismiss, title = stringResource(R.string.settings_translation_picker_title), scrollable = true,
        pageActions = {
            JSheetConfirm(stringResource(R.string.settings_done), { onSave(picked) }, Modifier.testTag("translate-chats-done"))
        }) {
        TranslationChatPickerContent(selected, choices, error, query, { query = it }, picked) { chat ->
            picked = if (picked.any { it.id == chat.id }) picked.filterNot { it.id == chat.id } else picked + chat
        }
    }
}

/** The picker's search field and rows (inside its sheet; drawn on its own for captures). */
@Composable
internal fun TranslationChatPickerContent(
    selected: List<TranslateChat>,
    choices: List<LarkChatChoice>?,
    error: String?,
    query: String,
    onQuery: (String) -> Unit,
    picked: List<TranslateChat>,
    onToggle: (TranslateChat) -> Unit,
) {
    val rows = remember(selected, choices, query) { TranslationText.pickerRows(selected, choices.orEmpty(), query) }
    Column(Modifier.fillMaxWidth()) {
        JSearchField(query, onQuery, stringResource(R.string.settings_translation_search))
        error?.let { JNotice(it, Modifier.padding(top = Tokens.Space.s), status = Status.NeedsYou) }
        Column(Modifier.fillMaxWidth().padding(top = Tokens.Space.s)) {
            when {
                choices == null && selected.isEmpty() -> JLoading(text = stringResource(R.string.settings_translation_loading))
                rows.isEmpty() -> JEmptyState(stringResource(if (query.isBlank()) R.string.settings_translation_no_chats else R.string.settings_translation_no_match), stringResource(R.string.settings_translation_no_chats_detail),
                    icon = PhIcons.ChatCircleDots)
                else -> rows.forEachIndexed { index, chat ->
                    key(chat.id) {
                        val on = picked.any { it.id == chat.id }
                        val choice = choices?.firstOrNull { it.id == chat.id }
                        val pickedText = stringResource(R.string.settings_translation_picked)
                        val unpickedText = stringResource(R.string.settings_translation_unpicked)
                        JGroupItem(GroupPosition.of(index, rows.size)) {
                            JRow(
                                Modifier.semantics { stateDescription = if (on) pickedText else unpickedText },
                                trailing = { JCheckbox(on, null) },
                                onClick = { onToggle(chat) },
                                role = Role.Checkbox,
                            ) {
                                JAvatar(chat.name,
                                    icon = if (choice?.isGroup == false) null else PhIcons.ChatsCircle)
                                Spacer(Modifier.width(Tokens.Space.m))
                                Text(chat.name, Modifier.weight(1f), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink,
                                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
            // The picked ones show at once; the hub's list is still on its way.
            if (choices == null && selected.isNotEmpty()) JLoading(text = stringResource(R.string.settings_translation_loading))
        }
    }
}

/** Captures (`debug.translateChats 1`): the page opens with its chat picker up, once. */
object TranslationPreview {
    @Volatile var picking: Boolean = false
    fun take(): Boolean = picking.also { picking = false }
}

internal object TranslationText {
    fun chats(resources: Resources, count: Int): String = if (count == 0) resources.getString(R.string.settings_translation_chats_none)
        else resources.getQuantityString(R.plurals.settings_translation_chats, count, count)

    @Composable fun chats(count: Int): String = if (count == 0) stringResource(R.string.settings_translation_chats_none)
        else pluralStringResource(R.plurals.settings_translation_chats, count, count)

    /** The picked chats first (under the names kept for them), then the others offered, filtered by name. */
    fun pickerRows(selected: List<TranslateChat>, choices: List<LarkChatChoice>, query: String): List<TranslateChat> {
        val all = (selected + choices.map { TranslateChat(it.id, it.name) }).distinctBy { it.id }
        val q = query.trim().lowercase()
        return if (q.isEmpty()) all else all.filter { it.name.lowercase().contains(q) }
    }
}
