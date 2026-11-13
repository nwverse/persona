package persona.feature.settings

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.designsystem.*

/** A session can survive its lost system confirmation page and a later APK upgrade. */
@Composable
internal fun AppUpdateRecovery(controller: AppUpdateState) {
    val context = LocalContext.current.applicationContext
    val scope = rememberCoroutineScope()
    val update by controller.state.collectAsState()
    var pending by remember { mutableStateOf(emptySet<Int>()) }
    var confirmed by remember { mutableStateOf<Set<Int>?>(null) }
    var busy by remember { mutableStateOf(false) }
    var message by remember { mutableStateOf<Int?>(null) }
    suspend fun refresh() {
        try {
            pending = AppInstaller.pendingSessions(context)
            if (message == R.string.settings_update_pending_failed) message = null
        }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { message = R.string.settings_update_pending_failed }
    }
    LaunchedEffect(update.installing) { if (!update.installing) refresh() }
    LifecycleEventEffect(Lifecycle.Event.ON_START) { scope.launch { refresh() } }
    if (pending.isNotEmpty() || message != null) JCard {
        Column {
            if (pending.isNotEmpty()) JButton(
                stringResource(R.string.settings_update_pending_cancel),
                { confirmed = pending.toSet() }, Modifier.fillMaxWidth(),
                enabled = !busy && !update.installing, kind = JButtonKind.Secondary,
            )
            message?.let { SettingsCaption(stringResource(it)) }
            if (message == R.string.settings_update_pending_failed) JButton(
                stringResource(R.string.settings_retry_read), { scope.launch { refresh() } },
                enabled = !busy && !update.installing, kind = JButtonKind.Quiet,
            )
        }
    }
    confirmed?.let { ids ->
        JDialog(
            onDismiss = { if (!busy) confirmed = null },
            title = stringResource(R.string.settings_update_pending_cancel),
            text = stringResource(R.string.settings_update_pending_confirm),
            confirmEnabled = !busy && !update.installing,
            confirm = stringResource(R.string.settings_update_pending_cancel) to {
                busy = true
                scope.launch {
                    try {
                        AppInstaller.cancelPending(context, ids)
                        controller.pendingInstallationCancelled()
                        message = R.string.settings_update_pending_cancelled
                        confirmed = null
                        refresh()
                    } catch (cancelled: CancellationException) { throw cancelled }
                    catch (_: Exception) { message = R.string.settings_update_pending_failed; confirmed = null }
                    finally { busy = false }
                }
            },
        )
    }
}
