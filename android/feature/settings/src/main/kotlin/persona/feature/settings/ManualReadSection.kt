package persona.feature.settings

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.api.HubPreferences
import persona.core.contracts.api.ReadSettingsState
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.JGroupItem
import persona.core.designsystem.JNotice
import persona.core.designsystem.JSwitchRow
import persona.core.designsystem.Status
import persona.core.designsystem.Tokens
import persona.core.designsystem.failureReasonText
import persona.core.designsystem.rowPadding

/** What the switch row says under it: why it cannot be changed, else what it does (iOS Settings › Agent, web Settings › Hub › Unread). */
internal fun manualReadFooter(state: ReadSettingsState): Int = when {
    !state.supported -> R.string.settings_manual_read_old_hub
    state.known && !state.canWrite -> R.string.settings_manual_read_readonly
    else -> R.string.settings_manual_read_footer
}

/** It can be flipped once the hub answered that it has the switch and this device may write it. */
internal fun manualReadEditable(state: ReadSettingsState): Boolean = state.known && state.supported && state.canWrite

/**
 * Settings › Agents › Manual mark as read (#7356): the selected hub's switch (GET/PUT /api/v1/read-settings), off by default.
 * On, opening an Agent or the capsule showing its reply leaves it Unread; only Mark as read clears it.
 */
@Composable
internal fun ManualReadSection(preferences: HubPreferences) {
    val state by preferences.readSettings.collectAsState(ReadSettingsState())
    val scope = rememberCoroutineScope()
    val resources = LocalResources.current
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(preferences) { runCatching { preferences.refresh() }.exceptionOrNull()?.let { if (it is CancellationException) throw it } }
    Column(Modifier.padding(top = Tokens.Space.section)) {
        JGroupItem(GroupPosition.Single) {
            JSwitchRow(stringResource(R.string.settings_manual_read), state.manual, { on ->
                scope.launch {
                    error = try { preferences.setManualRead(on); null }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { resources.getString(R.string.settings_manual_read_failed, failureReasonText(failure).resolve(resources)) }
                }
            }, enabled = manualReadEditable(state))
        }
        SettingsCaption(stringResource(manualReadFooter(state)), Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
        error?.let { JNotice(it, Modifier.padding(top = Tokens.Space.s), status = Status.NeedsYou) }
    }
}
