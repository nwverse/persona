package persona.feature.files

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.semantics
import kotlinx.coroutines.CancellationException
import persona.core.contracts.api.SessionFileFeature
import persona.core.contracts.api.SessionPathStat
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

@Composable
internal fun FileDetails(target: FileBrowserTarget, controller: FilesController, machineOs: String?,
    permissionAction: JAction?, onClose: () -> Unit) {
    val resources = LocalResources.current
    val supportsInfo = controller.api.supports(controller.machineId, SessionFileFeature.Info)
    val entry = target.entry
    var stat by remember(target.path) { mutableStateOf<SessionPathStat?>(null) }
    var error by remember(target.path) { mutableStateOf<String?>(null) }
    var retryable by remember(target.path) { mutableStateOf(false) }
    var attempt by remember(target.path) { mutableIntStateOf(0) }
    LaunchedEffect(controller, target.path, supportsInfo, attempt) {
        if (!supportsInfo) return@LaunchedEffect
        error = null
        try { stat = controller.api.fileInfo(controller.machineId, controller.cwd, target.path) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) {
            error = when (editRefusal(failure, null)) {
                EditRefusal.HubTooOld -> resources.getString(R.string.files_info_update_hub)
                is EditRefusal.NodeOutdated -> resources.getString(R.string.files_info_update_node)
                EditRefusal.Gone -> resources.getString(R.string.files_edit_gone)
                else -> { retryable = true; filesLoadError(resources, failure) }
            }
        }
    }
    val detail = stat
    val dir = detail?.let { it.kind == "dir" } ?: entry.dir
    val link = detail?.symlink ?: entry.symlink
    val broken = detail?.linkBroken ?: entry.linkBroken
    val copy = rememberCopy()
    val copied = stringResource(R.string.files_path_copied)
    JSheet(onClose, title = entry.name, scrollable = true) {
        DetailField(stringResource(R.string.files_info_kind), stringResource(when {
            broken -> R.string.files_broken_link
            link && dir -> R.string.files_info_link_folder
            link -> R.string.files_info_link_file
            dir -> R.string.files_folder
            else -> R.string.files_info_file
        }), danger = broken)
        DetailField(stringResource(R.string.files_info_location), FilesPaths.parent(target.path) ?: target.path)
        if (link) {
            (detail?.linkTarget ?: entry.linkTarget)?.let { DetailField(stringResource(R.string.files_info_link_target), it) }
            detail?.linkReal?.takeUnless { broken || it.isEmpty() }?.let { real ->
                DetailField(stringResource(R.string.files_info_real), real)
                JTextButton(stringResource(R.string.files_info_go_real), {
                    onClose()
                    if (dir) controller.open(real) else controller.showInFolder(real)
                }, icon = PersonaIcons.open)
            }
        }
        if (dir) {
            detail?.items?.let { count ->
                val text = if (detail.itemsMore == true)
                    stringResource(R.string.files_info_items_more, LocalizedFormats.number(count, currentLocale))
                else pluralStringResource(R.plurals.files_info_items, count, LocalizedFormats.number(count, currentLocale))
                DetailField(stringResource(R.string.files_info_contents), text)
            }
        } else {
            val bytes = detail?.size ?: entry.size
            val exact = stringResource(R.string.files_info_bytes, LocalizedFormats.number(bytes, currentLocale))
            DetailField(stringResource(R.string.files_info_size), if (bytes < 1024) exact else stringResource(R.string.files_info_size_bytes, fileSize(bytes, currentLocale), exact))
        }
        fullDate(detail?.birthtime, currentLocale)?.let { DetailField(stringResource(R.string.files_info_created), it) }
        fullDate(detail?.mtime ?: entry.mtime, currentLocale)?.let { DetailField(stringResource(R.string.files_info_modified), it) }
        fullDate(detail?.ctime, currentLocale)?.let { DetailField(stringResource(R.string.files_info_changed), it) }
        if (!machineOs.equals("windows", ignoreCase = true)) {
            detail?.owner?.let { DetailField(stringResource(R.string.files_info_owner), it) }
            detail?.group?.let { DetailField(stringResource(R.string.files_info_group), it) }
            detail?.mode?.let { mode ->
                DetailField(stringResource(R.string.files_info_permissions), fileModeText(mode))
                Text(permissionWords(resources, mode), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
                if (!link && permissionAction != null) JTextButton(permissionAction.label, {
                    onClose(); permissionAction.onClick()
                }, enabled = permissionAction.enabled, icon = permissionAction.icon)
            }
        }
        when {
            !supportsInfo -> Notice(stringResource(R.string.files_info_update_hub), padded = false)
            error != null -> {
                Notice(error.orEmpty(), padded = false)
                if (retryable) JTextButton(stringResource(R.string.files_retry), { retryable = false; attempt++ })
            }
            detail == null -> Loading()
            detail.kind == "missing" -> Notice(stringResource(R.string.files_edit_gone), padded = false)
            detail.mode == null && detail.mtime == null -> Notice(stringResource(R.string.files_info_update_node), padded = false)
        }
        JTextButton(stringResource(R.string.files_copy_path), { copy(target.path, copied) }, icon = PersonaIcons.copy)
    }
}

@Composable
private fun DetailField(label: String, value: String, danger: Boolean = false) {
    Row(Modifier.fillMaxWidth().semantics(mergeDescendants = true) {}.padding(vertical = Tokens.Space.s),
        verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        Text(label, Modifier.weight(0.42f), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        Text(value, Modifier.weight(0.58f), style = PersonaTheme.type.body,
            color = if (danger) PersonaTheme.palette.danger.text else PersonaTheme.palette.ink)
    }
}
