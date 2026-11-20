package persona.feature.files

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.pluralStringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.contracts.api.SessionFileFeature
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.contracts.api.SessionMoveConflictPolicy
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.R as DesignR

internal data class MoveSource(val path: String, val directory: Boolean) {
    fun contains(folder: String): Boolean =
        directory && FilesPaths.isWithin(FilesPaths.normalize(folder), FilesPaths.normalize(path))

    fun canMoveInto(folder: String): Boolean =
        folder.isNotBlank() && FilesPaths.normalize(folder) != FilesPaths.parent(FilesPaths.normalize(path)) && !contains(folder)
}

internal sealed interface FileMove {
    data object Pending : FileMove
    data class Conflict(val sources: List<MoveSource>, val directory: String, val name: String) : FileMove
}

/** Browse folders independently of the Files view behind the sheet. */
@Composable
internal fun MoveDestinationSheet(
    sources: List<MoveSource>,
    controller: FilesController,
    hidden: Boolean,
    copying: Boolean,
    onDismiss: () -> Unit,
    onMove: (String) -> Unit,
) {
    val scope = rememberCoroutineScope()
    val browser = remember(controller, sources) { controller.destinationBrowser(scope) }
    val state by browser.state.collectAsStateWithLifecycle()
    LaunchedEffect(browser) { browser.directory(FilesPaths.parent(sources.first().path) ?: controller.cwd) }
    val listing = state.directory
    val here = state.here
    val folders = remember(listing, hidden) {
        listing?.entries.orEmpty().filter { it.dir && !it.linkBroken && (hidden || !it.name.startsWith('.')) }.sortedBy { it.name.lowercase() }
    }
    val ready = listing != null && !state.directoryLoading && state.directoryError == null
    val resources = LocalResources.current
    var creating by rememberSaveable(here) { mutableStateOf(false) }
    var createError by remember(here) { mutableStateOf<String?>(null) }
    val nested = sources.any { it.contains(here) }
    val writable = controller.canWrite(here)
    val canMoveHere = ready && writable && !nested && (copying || sources.all { it.canMoveInto(here) })
    val title = when {
        copying -> stringResource(R.string.files_action_copy_to)
        sources.size == 1 -> stringResource(R.string.files_move_title, FilesPaths.name(sources.single().path))
        else -> pluralStringResource(R.plurals.files_move_selection, sources.size, sources.size)
    }
    if (creating) {
        NewFileName(folder = true, onDismiss = { creating = false }) { name ->
            creating = false
            scope.launch {
                try { browser.directory(controller.api.mkdir(controller.machineId, controller.cwd, here, name)) }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { createError = editRefusalText(resources, editRefusal(failure, name)) }
            }
        }
        return
    }
    JSheet(onDismiss, modifier = Modifier.testTag("files-move-destination"),
        title = title) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(here, Modifier.weight(1f), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2,
                maxLines = 1, overflow = TextOverflow.MiddleEllipsis)
            JIconButton(PersonaIcons.queueUp, stringResource(R.string.files_up), browser::up, enabled = state.parent != null)
        }
        if (ready && writable && !nested && controller.api.supports(controller.machineId, SessionFileFeature.Mkdir)) {
            JTextButton(stringResource(R.string.files_action_new_folder), { creating = true }, icon = PersonaIcons.folder)
        }
        createError?.let { Notice(it, padded = false) }
        key(here) {
            LazyColumn(Modifier.fillMaxWidth().weight(1f, fill = false)) {
                val error = state.directoryError
                when {
                    error != null -> item {
                        RetryState(stringResource(R.string.files_folder_failed), error) { browser.directory() }
                    }
                    !ready -> item { Loading() }
                    else -> {
                        if (folders.isEmpty() && listing?.truncated != true) {
                            item { Notice(stringResource(R.string.files_move_no_folders), padded = false) }
                        }
                        itemsIndexed(folders, key = { _, folder -> folder.name }) { index, folder ->
                            val path = joinRemotePath(here, folder.name)
                            val blocked = sources.any { it.contains(path) }
                            JGroupItem(GroupPosition.of(index, folders.size)) {
                                JRow(folder.name, leadingIcon = PersonaIcons.folder, onClick = { browser.directory(path) },
                                    enabled = !blocked, chevron = !blocked)
                            }
                        }
                        if (listing?.truncated == true) item {
                            Notice(stringResource(R.string.files_too_many, LocalizedFormats.number(MAX_LISTED, currentLocale)), padded = false)
                        }
                    }
                }
            }
        }
        if (ready && !canMoveHere) {
            val reason = when {
                !writable -> R.string.files_location_read_only
                nested -> R.string.files_move_inside_itself
                else -> R.string.files_move_same_folder
            }
            Notice(stringResource(reason), padded = false)
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JButton(stringResource(DesignR.string.designsystem_cancel), onDismiss, Modifier.weight(1f), kind = JButtonKind.Secondary)
            JButton(stringResource(if (copying) R.string.files_action_copy_here else R.string.files_move_here), { onMove(here) }, Modifier.weight(1f),
                enabled = canMoveHere)
        }
    }
}

@Composable
internal fun MoveConflictDialog(conflict: FileMove.Conflict, answer: (SessionMoveConflictPolicy?) -> Unit) {
    val cancel = { answer(null) }
    val explanation = stringResource(R.string.files_move_conflict_message)
    val message = if (conflict.sources.size == 1) explanation else explanation + "\n\n" +
        stringResource(R.string.files_action_bulk_conflict, conflict.sources.size)
    JDialog(cancel, title = stringResource(R.string.files_edit_exists, conflict.name),
        text = message, dismiss = null) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            JButton(stringResource(R.string.files_move_keep_both), { answer(SessionMoveConflictPolicy.KeepBoth) }, Modifier.fillMaxWidth())
            JButton(stringResource(R.string.files_move_replace), { answer(SessionMoveConflictPolicy.Replace) }, Modifier.fillMaxWidth(),
                kind = JButtonKind.Destructive)
            JButton(stringResource(DesignR.string.designsystem_cancel), cancel, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
        }
    }
}
