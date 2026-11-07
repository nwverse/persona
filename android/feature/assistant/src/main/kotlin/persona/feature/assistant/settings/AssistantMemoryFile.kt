package persona.feature.assistant.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.contracts.Machine
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantManagement
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import persona.feature.assistant.LocalAssistantHost
import persona.feature.assistant.R
import persona.feature.assistant.conversationTime
import persona.feature.assistant.proactive.AssistantProactiveScreen
import kotlinx.coroutines.launch

@Composable
internal fun MemoryFilePart(
    state: AssistantSettingsUiState, name: String?, preview: String?, onBack: () -> Unit,
    onSave: suspend (String, String) -> Boolean, onDelete: suspend (String) -> Unit,
) {
    val scope = rememberCoroutineScope()
    val resources = LocalResources.current
    val indexTitle = stringResource(R.string.assistant_settings_memory_index)
    val entry = state.memory.entries?.firstOrNull { it.file.name == name }
        ?: state.memory.index?.takeIf { it.name == name }?.let { parseMemory(it).copy(title = indexTitle) }
    // The index is Persona's own table of contents: shown, not edited by hand.
    val editable = entry != null && entry.file.name != MEMORY_INDEX
    var editing by remember { mutableStateOf(preview == "memory-edit") }
    var deleting by remember { mutableStateOf(preview == "memory-delete") }
    JListPage(
        title = entry?.title ?: stringResource(R.string.assistant_settings_memory), onBack = onBack,
        actions = {
            if (editable) {
                JIconButton(PhIcons.PencilSimple, stringResource(R.string.assistant_settings_edit), { editing = true }, enabled = !state.busy)
                JIconButton(PhIcons.Trash, stringResource(R.string.assistant_settings_delete), { deleting = true }, enabled = !state.busy, tint = PersonaTheme.palette.danger.text)
            }
        },
    ) {
        if (entry == null) {
            item("missing") { JEmptyState(stringResource(R.string.assistant_settings_memory_missing_title), stringResource(R.string.assistant_settings_memory_missing_text), icon = PhIcons.Brain) }
            return@JListPage
        }
        item("meta") {
            Row(Modifier.padding(top = Tokens.Space.s, bottom = Tokens.Space.s), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
                verticalAlignment = Alignment.CenterVertically) {
                entry.typeLabel(resources)?.let { JTag(it) }
                conversationTime(entry.updated, currentLocale)?.let { Text(stringResource(R.string.assistant_settings_memory_updated, it), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3) }
            }
        }
        entry.description?.let { description ->
            item("description") { Text(description, Modifier.padding(bottom = Tokens.Space.m), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2) }
        }
        item("body") {
            JCard {
                SelectionContainer {
                    Text(entry.body.ifBlank { stringResource(R.string.assistant_settings_memory_blank) }, style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
                }
            }
        }
        item("file") { Caption(entry.file.name) }
    }
    if (editing && entry != null) {
        var content by rememberSaveable(entry.file.name) { mutableStateOf(entry.file.content) }
        JSheet(onDismiss = { if (!state.busy) editing = false }, title = stringResource(R.string.assistant_settings_memory_edit_title, entry.title), dismissible = !state.busy) {
            LazyColumn(Modifier.fillMaxWidth().weight(1f, fill = false), contentPadding = PaddingValues(bottom = Tokens.Space.l),
                verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                item { Caption(stringResource(R.string.assistant_settings_memory_edit_caption)) }
                state.notice?.takeIf { it.error }?.let { notice -> item { JNotice(notice.text.resolve(resources), status = Status.NeedsYou) } }
                item {
                    JTextField(content, { content = it }, Modifier.fillMaxWidth(), label = stringResource(R.string.assistant_settings_memory_content), enabled = !state.busy,
                        minLines = 8, maxLines = Int.MAX_VALUE)
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                JButton(stringResource(R.string.assistant_settings_cancel), { editing = false }, Modifier.weight(1f), kind = JButtonKind.Secondary, enabled = !state.busy)
                JButton(stringResource(R.string.assistant_settings_save), { scope.launch { if (onSave(entry.file.name, content)) editing = false } }, Modifier.weight(1f),
                    enabled = !state.busy && content != entry.file.content, loading = state.busy)
            }
        }
    }
    if (deleting && entry != null) JDialog(
        onDismiss = { deleting = false }, title = stringResource(R.string.assistant_settings_memory_delete_title),
        text = stringResource(R.string.assistant_settings_memory_delete_text, entry.title, MEMORY_INDEX),
        destructive = true, confirmEnabled = !state.busy,
        confirm = stringResource(R.string.assistant_settings_delete) to { deleting = false; scope.launch { onDelete(entry.file.name) }; Unit },
        dismiss = stringResource(R.string.assistant_settings_cancel) to { deleting = false },
    )
}
