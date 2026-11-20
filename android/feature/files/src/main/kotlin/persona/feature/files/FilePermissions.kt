package persona.feature.files

import android.content.res.Resources
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.toggleable
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.api.SessionPathStat
import persona.core.designsystem.*

private val roles = intArrayOf(R.string.files_action_owner, R.string.files_action_group, R.string.files_action_everyone)
private val rights = intArrayOf(R.string.files_action_read, R.string.files_action_write, R.string.files_action_run)
private val rightNames = intArrayOf(R.string.files_action_access_0, R.string.files_action_access_1, R.string.files_action_access_2,
    R.string.files_action_access_3, R.string.files_action_access_4, R.string.files_action_access_5, R.string.files_action_access_6,
    R.string.files_action_access_7)

internal fun permissionWords(resources: Resources, mode: Int): String = roles.indices.joinToString(" · ") { i ->
    resources.getString(R.string.files_action_access_for, resources.getString(roles[i]), resources.getString(rightNames[(mode shr (6 - i * 3)) and 7]))
}

internal fun permissionChanges(resources: Resources, from: Int, to: Int): String = roles.indices.mapNotNull { i ->
    val shift = 6 - i * 3
    val old = (from shr shift) and 7
    val new = (to shr shift) and 7
    if (new == old) null else resources.getString(R.string.files_action_access_change, resources.getString(roles[i]),
        resources.getString(rightNames[old]), resources.getString(rightNames[new]))
}.joinToString("\n")

@Composable
internal fun FilePermissions(controller: FilesController, target: FileBrowserTarget, onDismiss: () -> Unit) {
    val resources = LocalResources.current
    val scope = rememberCoroutineScope()
    val feedback = LocalAppFeedback.current
    var stat by remember(target.path) { mutableStateOf<SessionPathStat?>(null) }
    var error by remember(target.path) { mutableStateOf<String?>(null) }
    var attempt by remember(target.path) { mutableIntStateOf(0) }
    var edited by rememberSaveable(target.path) { mutableStateOf<Int?>(null) }
    var asking by rememberSaveable(target.path) { mutableStateOf(false) }
    var saving by remember(target.path) { mutableStateOf(false) }
    LaunchedEffect(controller, target.path, attempt) {
        error = null
        try {
            stat = controller.api.fileInfo(controller.machineId, controller.cwd, target.path)
            if (stat?.mode == null || stat?.symlink == true) error = resources.getString(R.string.files_action_permissions_unknown)
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { error = editRefusalText(resources, editRefusal(failure, target.entry.name)) }
    }
    val original = stat?.mode?.and(511)?.takeIf { stat?.symlink != true }
    val mode = edited ?: original
    if (asking && original != null && mode != null) {
        JDialog(onDismiss = { if (!saving) asking = false }, title = stringResource(R.string.files_action_confirm_mode, target.entry.name),
            text = permissionChanges(resources, original, mode) + "\n\n" + fileModeText(original) + " → " + fileModeText(mode),
            confirmEnabled = !saving, confirm = stringResource(R.string.files_action_change) to {
                if (!saving) {
                    saving = true
                    scope.launch {
                        try {
                            controller.api.chmod(controller.machineId, controller.cwd, target.path, mode)
                            controller.directory()
                            feedback?.notice(resources.getString(R.string.files_action_mode_saved), FeedbackStyle.Success)
                            onDismiss()
                        } catch (cancelled: CancellationException) { throw cancelled }
                        catch (failure: Exception) {
                            asking = false
                            error = editRefusalText(resources, editRefusal(failure, target.entry.name))
                        } finally { saving = false }
                    }
                }
            })
        return
    }
    JSheet(onDismiss, title = stringResource(R.string.files_action_permissions), scrollable = true) {
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(target.entry.name, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            if (original != null && mode != null) {
                PermissionGrid(mode, stat?.owner, stat?.group) { edited = it }
                Text(fileModeText(mode), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink)
                Text(stringResource(R.string.files_action_run_hint), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
                if (mode != original) Text(stringResource(R.string.files_action_before, fileModeText(original)), style = PersonaTheme.type.callout)
            }
            error?.let {
                Text(it, color = PersonaTheme.palette.danger.text, style = PersonaTheme.type.callout)
                if (original == null) JButton(stringResource(R.string.files_retry), { attempt++ }, kind = JButtonKind.Quiet)
            }
            if (original == null && error == null) androidx.compose.material3.CircularProgressIndicator()
            JButton(stringResource(R.string.files_action_review_permissions), { asking = true }, enabled = mode != null && original != null && mode != original)
        }
    }
}

@Composable
internal fun PermissionGrid(mode: Int, owner: String?, group: String?, onChange: (Int) -> Unit) {
    roles.forEachIndexed { row, role ->
        val roleLabel = stringResource(role)
        val name = when (row) { 0 -> owner; 1 -> group; else -> null }
        Text(listOfNotNull(roleLabel, name?.takeIf(String::isNotBlank)).joinToString(" · "), style = PersonaTheme.type.callout,
            color = PersonaTheme.palette.ink)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            rights.forEachIndexed { column, right ->
                val bit = 1 shl (8 - 3 * row - column)
                val checked = mode and bit != 0
                val label = stringResource(right)
                Row(Modifier.heightIn(min = Tokens.MinTouch.android).testTag("file-permission-$row-$column")
                    .semantics { contentDescription = "$roleLabel, $label" }
                    .toggleable(checked, role = Role.Checkbox) { onChange(mode xor bit) },
                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                    JCheckbox(checked, null)
                    Text(label, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink)
                }
            }
        }
    }
}
