package persona.feature.assistant.settings

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.runtime.*
import androidx.compose.ui.res.stringResource
import persona.core.designsystem.*
import persona.feature.assistant.R

@Composable
internal fun AssistantSettingsOwner(owner: String) {
    val name = LocalManagerRoster.current?.manager(owner)?.shownName ?: owner
    JGroupItem(GroupPosition.Single) {
        JRow(stringResource(R.string.assistant_settings_target), value = name, chevron = false)
    }
}

/** The manager's future-session default, distinct from the current main conversation's mode. */
@Composable
internal fun AssistantDefaultPermission(mode: String, enabled: Boolean, onPick: (String) -> Unit) {
    var choosing by remember { mutableStateOf(false) }
    val options = listOf(
        "default" to R.string.assistant_settings_permission_default,
        "acceptEdits" to R.string.assistant_settings_permission_edits,
        "plan" to R.string.assistant_settings_permission_plan,
        "auto" to R.string.assistant_settings_permission_auto,
        "bypassPermissions" to R.string.assistant_settings_permission_full,
    )
    val title = stringResource(R.string.assistant_settings_permission)
    Column {
        JSectionHeader(title)
        JGroupItem(GroupPosition.Single) {
            JRow(title, value = options.firstOrNull { it.first == mode }?.let { stringResource(it.second) } ?: mode,
                subtitle = stringResource(R.string.assistant_settings_permission_hint), subtitleLines = 3,
                enabled = enabled, onClick = { choosing = true })
        }
    }
    if (choosing) JSheet(onDismiss = { choosing = false }, title = title) {
        LazyColumn {
            itemsIndexed(options, key = { _, it -> it.first }) { index, option ->
                JGroupItem(GroupPosition.of(index, options.size)) {
                    JRow(stringResource(option.second), leading = { JRadio(mode == option.first, null) },
                        chevron = false, onClick = { choosing = false; onPick(option.first) })
                }
            }
        }
    }
}
