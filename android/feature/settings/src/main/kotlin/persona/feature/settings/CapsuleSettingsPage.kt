package persona.feature.settings

import androidx.annotation.StringRes
import androidx.compose.runtime.*
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import persona.core.contracts.api.FloatingCapsuleSettings
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons

internal object CapsuleSettingsText {
    val modes = listOf(
        "off" to R.string.settings_capsule_mode_off,
        "active" to R.string.settings_capsule_mode_active,
        "always" to R.string.settings_capsule_mode_always,
    )
    @StringRes fun labelRes(mode: String): Int? = modes.firstOrNull { it.first == mode }?.second
    @Composable fun label(mode: String): String = labelRes(mode)?.let { stringResource(it) }.orEmpty()
}

@Composable
internal fun CapsuleSettingsPage(settings: FloatingCapsuleSettings?, onBack: () -> Unit) {
    val mode = settings?.mode?.collectAsState()?.value ?: "off"
    val permitted = settings?.permissionGranted?.collectAsState()?.value == true
    val kept = settings?.keeperEnabled?.collectAsState()?.value == true
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { settings?.refreshPermission() }
    JListPage(title = stringResource(R.string.settings_capsule_title), onBack = onBack) {
        item { JNotice(stringResource(R.string.settings_capsule_notice)) }
        item { JSectionHeader(stringResource(R.string.settings_capsule_display_header)) }
        CapsuleSettingsText.modes.forEachIndexed { index, (key, title) ->
            item { JGroupItem(GroupPosition.of(index, CapsuleSettingsText.modes.size)) {
                JRow(stringResource(title), value = if (key == mode) stringResource(R.string.settings_capsule_selected) else null, enabled = settings != null,
                    onClick = { settings?.setMode(key) })
            } }
        }
        item { JSectionHeader(stringResource(R.string.settings_capsule_permission_header)) }
        item { JGroupItem(GroupPosition.of(0, 2)) {
            JRow(stringResource(R.string.settings_capsule_overlay), leadingIcon = PhIcons.SquaresFour,
                value = stringResource(if (permitted) R.string.settings_capsule_allowed else R.string.settings_capsule_not_allowed), onClick = { settings?.requestPermission() })
        } }
        item { JGroupItem(GroupPosition.of(1, 2)) {
            JRow(stringResource(R.string.settings_capsule_keeper), subtitle = stringResource(R.string.settings_capsule_keeper_subtitle),
                leadingIcon = PhIcons.PushPin,
                value = stringResource(if (kept) R.string.settings_capsule_keeper_on else R.string.settings_capsule_keeper_off),
                onClick = { settings?.openKeeperSettings() })
        } }
        if (mode != "off" && !permitted) item {
            JNotice(stringResource(R.string.settings_capsule_needs_permission), actionLabel = stringResource(R.string.settings_capsule_allow), onAction = { settings?.requestPermission() })
        }
        if (mode != "off" && permitted && !kept) item {
            JNotice(stringResource(R.string.settings_capsule_keeper_notice), actionLabel = stringResource(R.string.settings_capsule_keeper_turn_on), onAction = { settings?.openKeeperSettings() })
        }
    }
}
