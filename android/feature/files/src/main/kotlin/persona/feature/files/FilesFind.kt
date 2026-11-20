package persona.feature.files

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

/** The node walks this folder once. Typing reranks the returned names locally, including their parent folders. */
@Composable
internal fun FindInFolder(controller: FilesController, folder: String, onClose: () -> Unit) {
    val resources = LocalResources.current
    var query by rememberSaveable(folder) { mutableStateOf("") }
    var attempt by remember(folder) { mutableIntStateOf(0) }
    var entries by remember(folder) { mutableStateOf<List<FileNameHit>?>(null) }
    var truncated by remember(folder) { mutableStateOf(false) }
    var error by remember(folder) { mutableStateOf<String?>(null) }
    LaunchedEffect(controller, folder, attempt) {
        error = null
        try {
            val index = controller.api.findFiles(controller.machineId, folder)
            entries = withContext(Dispatchers.Default) { fileNameEntries(index.files) }
            truncated = index.truncated
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { error = filesLoadError(resources, failure) }
    }
    val ranked by produceState<Pair<String, List<FileNameHit>>?>(null, entries, query) {
        val index = entries.orEmpty()
        val text = query
        value = text to withContext(Dispatchers.Default) { findFileNames(index, text) }
    }
    val matches = ranked?.takeIf { it.first == query }?.second
    BackHandler(onBack = onClose)
    JListPage(stringResource(R.string.files_find), subtitle = folder, onBack = onClose, largeTitle = false) {
        item("query") { JSearchField(query, { query = it }, stringResource(R.string.files_find_placeholder)) }
        when {
            error != null -> item { RetryState(stringResource(R.string.files_find_failed), error.orEmpty()) { attempt++ } }
            entries == null -> item { Loading() }
            else -> {
                if (truncated) item { Notice(stringResource(R.string.files_find_truncated), padded = false) }
                if (query.isNotBlank() && matches?.isEmpty() == true) item { Notice(stringResource(R.string.files_find_none), padded = false) }
                items(matches.orEmpty(), key = { it.path }) { hit ->
                    JRow(hit.name, subtitle = hit.folder.ifEmpty { folder }, leadingIcon = if (hit.directory) PersonaIcons.folder else fileIcon(hit.name),
                        onClick = {
                            val path = joinRemotePath(folder, hit.path)
                            onClose()
                            if (hit.directory) controller.open(path) else controller.push(FileSelection.File(path))
                        })
                }
            }
        }
    }
}
