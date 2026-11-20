package persona.feature.files

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.flow.StateFlow
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import persona.core.navigation.ChatRoute
import java.time.Instant

/**
 * Embedded in the chat destination; Files/Git are panes, not navigation entries.
 *
 * [initialPath] opens a path the conversation mentioned (a file label, a tool's path, a diff's file header): relative
 * paths are in [cwd], "~…" goes to the hub as it is. A file opens straight in its preview and back returns to the
 * conversation; a folder (a trailing slash, or the hub answering `is_dir`) opens in the browser as its top.
 * [initialLine] (a message's `path:12`) opens the file's text at that line, marked.
 */
@Composable
fun SessionFilesScreen(
    session: ChatRoute,
    cwd: String,
    api: SessionFiles,
    onBackToChat: () -> Unit,
    onInsertPath: (String) -> Unit,
    initialPane: FilesPane = FilesPane.Files,
    modifier: Modifier = Modifier,
    initialPath: String? = null,
    initialLine: Int? = null,
    /** The hub connection: the pane reads again what failed once it is back online. */
    connection: StateFlow<ConnectionState>? = null,
    machineHome: String? = null,
    machineOs: String? = null,
    onOpenFolderTerminal: ((String) -> Unit)? = null,
) {
    val asked = initialPath?.trim()?.takeIf { it.isNotEmpty() }
    // A path from the conversation that names its machine fully can open before the session's folder is known.
    val base = cwd.ifBlank { asked?.takeIf { it.startsWith("/") || FilesPaths.windowsDrive(it) || FilesPaths.isHome(it) }?.let { start ->
        if (FilesPaths.namesDirectory(start)) FilesPaths.resolve(start, "/") else FilesPaths.parent(FilesPaths.resolve(start, "/")) ?: "/"
    }.orEmpty() }
    key(session, base, api, asked, initialLine, connection) {
        if (base.isBlank()) {
            JListPage(stringResource(R.string.files_title), modifier, onBack = onBackToChat) {
                item { JEmptyState(stringResource(R.string.files_no_folder_title), stringResource(R.string.files_no_folder_message),
                    action = { JButton(stringResource(R.string.files_back_to_chat), onBackToChat) }) }
            }
        } else {
            val scope = rememberCoroutineScope()
            val resources = LocalResources.current
            val location = rememberUpdatedState(machineHome to machineOs)
            val controller = remember { FilesController(api, session.machineId, base, scope, { filesLoadError(resources, it) },
                { resources.getString(if (it) R.string.files_git_pulled else R.string.files_git_pull_current) }, connection, { location.value }) }
            val images = remember { MarkdownImages(api, session.machineId, base) }
            val state by controller.state.collectAsStateWithLifecycle()
            var pane by rememberSaveable { mutableStateOf(if (asked != null) FilesPane.Files else initialPane) }
            LaunchedEffect(controller) { controller.start(asked, initialLine) }
            val back: () -> Unit = { if (!controller.back(pane)) onBackToChat() }
            BackHandler(onBack = back)
            Box(modifier.fillMaxSize()) {
                when (val top = state.selection) {
                    null -> FilesBrowser(state, controller, pane, { pane = it }, onBackToChat, onInsertPath,
                        machineHome, machineOs, onOpenFolderTerminal)
                    is FileSelection.Commit -> CommitPage(top.hash, state, controller, back)
                    is FileSelection.CommitFile -> CommitFilePage(top, state, controller, back)
                    is FileSelection.Compare -> ComparePage(top, state, controller, back)
                    else -> FilePreview(state, controller, images, onInsertPath, back,
                        readOnly = !controller.canWrite((top as? FileSelection.File)?.path ?: state.here))
                }
            }
        }
    }
}

@Composable
private fun FilesBrowser(
    state: FilesState,
    controller: FilesController,
    pane: FilesPane,
    onPane: (FilesPane) -> Unit,
    onBackToChat: () -> Unit,
    onInsertPath: (String) -> Unit,
    machineHome: String?,
    machineOs: String?,
    onOpenFolderTerminal: ((String) -> Unit)?,
) {
    val copy = rememberCopy()
    val pathCopied = stringResource(R.string.files_path_copied)
    var hidden by rememberShowHidden()
    var menu by remember { mutableStateOf(false) }
    val here = state.here
    var newMenu by remember(here) { mutableStateOf(false) }
    var find by rememberSaveable(here) { mutableStateOf(false) }
    var info by remember(here) { mutableStateOf<FileBrowserTarget?>(null) }
    var selecting by rememberSaveable(here, pane) { mutableStateOf(false) }
    var selected by rememberSaveable(here, pane) { mutableStateOf(arrayListOf<String>()) }
    var renaming by rememberSaveable { mutableStateOf<String?>(null) }
    var trashing by rememberSaveable { mutableStateOf(arrayListOf<String>()) }
    val report = rememberFileEditReport(controller)
    val readOnly = !controller.canWrite(here)
    val moveStatus by controller.moveStatus.collectAsStateWithLifecycle()
    val edits = controller.editsFiles && !readOnly && moveStatus == null
    val endSelection = { selecting = false; selected = arrayListOf<String>() }
    val actions = rememberFileBrowserActions(controller, here, readOnly, machineOs, hidden, moveStatus, { result ->
        if (result is FileEdit.Moved) endSelection()
        report(result)
    }, onOpenFolderTerminal)
    if (find) {
        FindInFolder(controller, here) { find = false }
        return
    }
    val entries = remember(state.directory, hidden, state.highlightedPath) {
        state.directory?.entries.orEmpty().filter { hidden || !it.name.startsWith('.') || joinRemotePath(here, it.name) == state.highlightedPath }
            .sortedWith(compareByDescending<SessionFileEntry> { it.dir }.thenBy { it.name.lowercase() })
    }
    val selectedPaths = remember(selected) { selected.toSet() }
    val targets = remember(entries, here, selectedPaths) { entries.mapNotNull { entry ->
        val path = joinRemotePath(here, entry.name)
        if (path in selectedPaths) FileBrowserTarget(path, entry) else null
    } }
    val toggle: (String) -> Unit = { path ->
        selected = ArrayList(if (path in selected) selected - path else selected + path)
    }
    BackHandler(enabled = selecting, onBack = endSelection)
    val filePane = pane == FilesPane.Files || state.git?.isRepo == false
    val list = rememberLazyListState()
    val large = PersonaTheme.style != AppStyle.Board
    LaunchedEffect(state.highlightedPath, state.directory) {
        val highlight = state.highlightedPath ?: return@LaunchedEffect
        val index = entries.indexOfFirst { joinRemotePath(here, it.name) == highlight }
        if (index >= 0) list.scrollToItem(index + 3 + (if (large) 1 else 0) + (if (readOnly) 1 else 0) + (if (state.directoryError != null) 1 else 0))
    }
    JListPage(
        title = FilesPaths.name(here), subtitle = here, onBack = onBackToChat, largeTitle = true,
        state = list, scrollUnderBottom = !selecting,
        actions = {
            if (filePane) {
                if (actions.newItems.isNotEmpty() && !readOnly) JIconButton(PersonaIcons.add, stringResource(R.string.files_new_menu), { newMenu = true })
                JIconButton(PersonaIcons.search, stringResource(R.string.files_find), { find = true })
            }
            JIconButton(PersonaIcons.retry, stringResource(R.string.files_refresh), { controller.directory(); controller.refreshGit() },
                enabled = !state.directoryLoading && !state.gitLoading)
        },
        bottomBar = if (moveStatus == FileMove.Pending) ({
            Box(Modifier.semantics { liveRegion = LiveRegionMode.Polite }) { Notice(stringResource(R.string.files_moving)) }
        }) else if (selecting) ({
            val batch = actions.selectedItems(targets).toMutableList()
            if (edits && targets.isNotEmpty() && targets.all { controller.canWrite(it.path) }) batch += JAction(stringResource(R.string.files_trash), PersonaIcons.delete, destructive = true) {
                trashing = ArrayList(targets.map { it.path })
            }
            JBottomBar { Column(Modifier.fillMaxWidth()) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Text(pluralStringResource(R.plurals.files_selected_count, targets.size, targets.size), style = PersonaTheme.type.caption,
                        color = PersonaTheme.palette.ink)
                    JTextButton(stringResource(R.string.files_select_done), endSelection)
                }
                if (batch.isNotEmpty()) Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    batch.forEach { action ->
                        val icon = action.icon
                        if (icon == null) JTextButton(action.label, action.onClick, enabled = action.enabled)
                        else JIconButton(icon, action.label, action.onClick, enabled = action.enabled)
                    }
                }
            } }
        }) else null,
    ) {
        item("panes") {
            val changes = state.git?.files?.size?.takeIf { it > 0 }
            // Git stays offered while its status loads (the chat menu only opens it for repositories); it goes when there is none.
            val chat = JSegment(stringResource(R.string.files_pane_chat))
            val files = JSegment(stringResource(R.string.files_pane_files))
            val options = if (state.git?.isRepo == false) listOf(chat, files)
                else listOf(chat, files, JSegment("Git", count = changes))
            JSegmented(options, if (pane == FilesPane.Files || options.size < 3) 1 else 2,
                { when (it) { 0 -> onBackToChat(); 1 -> onPane(FilesPane.Files); else -> onPane(FilesPane.Git) } },
                Modifier.padding(vertical = Tokens.Space.s))
        }
        if (filePane) {
            item("crumbs") {
                Crumbs(FilesPaths.pathBar(here, machineHome), controller.sessionFolder, controller::open, Modifier.fillMaxWidth())
            }
            item("folder-controls") {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    JIconButton(PersonaIcons.back, stringResource(R.string.files_history_back), controller::previousFolder, enabled = state.backFolders.isNotEmpty())
                    JIconButton(PersonaIcons.forward, stringResource(R.string.files_history_forward), controller::nextFolder, enabled = state.forwardFolders.isNotEmpty())
                    JIconButton(PhIcons.ArrowUp, stringResource(R.string.files_up), controller::up, enabled = state.parent != null)
                    JIconButton(PersonaIcons.home, stringResource(R.string.files_go_home), { controller.open(machineHome ?: "~") })
                    Spacer(Modifier.weight(1f))
                    JIconButton(PersonaIcons.moreMenu, stringResource(R.string.files_options), { menu = true })
                }
            }
            if (readOnly) item("read-only") { Notice(stringResource(R.string.files_location_read_only), padded = false) }
            filesBrowser(state, controller, entries, selecting, selectedPaths, toggle, { path -> selecting = true; selected = arrayListOf(path) },
                onInsertPath, { info = it }, actions.rowItems, rename = if (edits) ({ renaming = it }) else null,
                trash = if (edits) ({ trashing = arrayListOf(it) }) else null)
        } else gitBrowser(state, controller)
    }
    if (menu) JActionSheet(buildList {
        if (entries.isNotEmpty()) add(JAction(stringResource(R.string.files_select_items), PersonaIcons.done) { selecting = true })
        add(JAction(stringResource(R.string.files_browser_info), PersonaIcons.info) { info = FileBrowserTarget(here, SessionFileEntry(FilesPaths.name(here), dir = true)) })
        add(JAction(stringResource(R.string.files_show_hidden), PersonaIcons.view, checked = hidden) { hidden = !hidden })
        state.parent?.let { add(JAction(stringResource(R.string.files_parent_folder), PhIcons.ArrowUp) { controller.up() }) }
        add(JAction(stringResource(R.string.files_copy_folder), PersonaIcons.copy) { copy(here, pathCopied) })
        add(JAction(stringResource(R.string.files_insert_folder), PersonaIcons.insert) { onInsertPath(here) })
        addAll(actions.folderItems)
    }, { menu = false }, title = stringResource(R.string.files_folder_actions))
    if (newMenu) JActionSheet(actions.newItems, { newMenu = false }, title = stringResource(R.string.files_new_menu))
    info?.let { target -> FileDetails(target, controller, machineOs, if (controller.canWrite(target.path)) actions.permissions(target) else null) { info = null } }
    renaming?.let { path -> RenameFileDialog(FilesPaths.name(path), { controller.rename(path, it, report) }, { renaming = null }) }
    if (trashing.isNotEmpty()) {
        val paths = trashing
        val dismiss = { trashing = arrayListOf<String>() }
        val trash = { controller.trash(paths) { result -> if (result is FileEdit.Trashed) endSelection(); report(result) } }
        if (paths.size == 1) TrashFileDialog(FilesPaths.name(paths.first()), trash, dismiss)
        else JDialog(dismiss, title = stringResource(R.string.files_trash_many_title, paths.size), text = stringResource(R.string.files_trash_message),
            confirm = stringResource(R.string.files_trash) to { dismiss(); trash() }, destructive = true)
    }
}

/** The folders from the breadcrumb's top down to the one shown, as chips; it follows you to the end as you go in. */
@Composable
private fun Crumbs(levels: List<Pair<String, String>>, sessionFolder: String, onPick: (String) -> Unit, modifier: Modifier) {
    val list = rememberLazyListState()
    LaunchedEffect(levels.size, levels.lastOrNull()) { if (levels.isNotEmpty()) list.animateScrollToItem(levels.lastIndex) }
    LazyRow(modifier, list, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        itemsIndexed(levels, key = { _, (_, path) -> path }) { index, (name, path) ->
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                if (index > 0) JIcon(PersonaIcons.forward, null, size = Tokens.Space.m, tint = PersonaTheme.palette.ink3)
                val current = index == levels.lastIndex
                val session = FilesPaths.displayPath(sessionFolder).trimEnd('/')
                val title = if (session.equals(path.trimEnd('/'), ignoreCase = FilesPaths.windowsDrive(path))) stringResource(R.string.files_session_segment, name) else name
                JChip(title, selected = current, onClick = { if (!current) onPick(path) })
            }
        }
    }
}

/** [rename] and [trash] (of an entry's full path): offered in each row's menu when the hub can (`file_edit`). */
private fun LazyListScope.filesBrowser(state: FilesState, controller: FilesController, entries: List<SessionFileEntry>,
    selecting: Boolean, selected: Set<String>, toggle: (String) -> Unit, startSelection: (String) -> Unit, insert: (String) -> Unit,
    info: (FileBrowserTarget) -> Unit, rowActions: (FileBrowserTarget) -> List<JAction>,
    rename: ((String) -> Unit)? = null, trash: ((String) -> Unit)? = null) {
    val listing = state.directory
    when {
        listing == null && state.directoryError != null -> item { RetryState(stringResource(R.string.files_folder_failed), state.directoryError) { controller.directory() } }
        listing == null -> item { Loading() }
        else -> {
            if (state.directoryError != null) item("stale") { Notice(stringResource(R.string.files_refresh_failed, state.directoryError), padded = false) }
            if (entries.isEmpty()) item {
                if (listing.entries.isEmpty()) JEmptyState(stringResource(R.string.files_empty_folder), stringResource(R.string.files_empty_folder_message))
                else JEmptyState(stringResource(R.string.files_only_hidden), stringResource(R.string.files_only_hidden_message))
            }
            val now = Instant.now()
            itemsIndexed(entries, key = { _, it -> "entry:${it.name}" }, contentType = { _, _ -> "entry" }) { index, entry ->
                val copyText = rememberCopy()
                val pathCopied = stringResource(R.string.files_path_copied)
                val path = joinRemotePath(listing.path.ifEmpty { state.here }, entry.name)
                val target = FileBrowserTarget(path, entry)
                JGroupItem(GroupPosition.of(index, entries.size)) {
                    // Pictures, documents and videos show the machine's thumbnail in place of the icon (iOS FilesPane, #1370).
                    val thumbnail = if (!entry.dir && !entry.linkBroken && FileThumbnails.wanted(entry.name, entry.size)) controller.thumbnail(path, entry.size, entry.mtime) else null
                    FileRow(entry, gitChange(state.git, path, entry.dir), now, selecting, path in selected,
                        highlighted = state.highlightedPath == path, thumbnail = thumbnail,
                        open = {
                            when {
                                selecting -> toggle(path)
                                entry.linkBroken -> info(target)
                                entry.dir -> controller.open(path)
                                else -> controller.push(FileSelection.File(path))
                            }
                        }, info = { info(target) }, select = { startSelection(path) }, rowActions = { rowActions(target) },
                        copy = { copyText(path, pathCopied) }, insert = { insert(path) },
                        rename = rename?.takeIf { controller.canWrite(path) }?.let { { it(path) } },
                        trash = trash?.takeIf { controller.canWrite(path) }?.let { { it(path) } })
                }
            }
            if (listing.truncated) item { Notice(stringResource(R.string.files_too_many, LocalizedFormats.number(MAX_LISTED, currentLocale)), padded = false) }
        }
    }
}

/**
 * One entry: its kind, the name (a hidden one quieter), size and when it changed, the Git change, ⋯. Its menu (⋯ or a long
 * press) inserts or copies the path and offers the available file changes (iOS `FilesPane.entryRow`).
 */
@Composable
private fun FileRow(entry: SessionFileEntry, change: SessionGitFile?, now: Instant, selecting: Boolean, checked: Boolean, highlighted: Boolean,
    open: () -> Unit, info: () -> Unit, select: () -> Unit, rowActions: () -> List<JAction>, copy: () -> Unit, insert: () -> Unit,
    thumbnail: Pair<String, suspend (Int) -> ByteArray?>? = null, rename: (() -> Unit)? = null, trash: (() -> Unit)? = null) {
    var menu by remember { mutableStateOf(false) }
    val palette = PersonaTheme.palette
    val resources = LocalResources.current
    val meta = listOfNotNull(if (entry.dir) stringResource(R.string.files_folder) else fileSize(entry.size, currentLocale),
        fileDate(resources, entry.mtime, now)).joinToString("  ")
    val checkedText = if (selecting) stringResource(if (checked) R.string.files_item_selected else R.string.files_item_not_selected) else null
    JRow(modifier = if (checkedText != null) Modifier.semantics { stateDescription = checkedText } else Modifier,
        onClick = open, onLongClick = { menu = true }, selected = if (selecting) checked else highlighted,
        role = if (selecting) Role.Checkbox else Role.Button, trailing = {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (change != null) {
                if (entry.dir) JTag(stringResource(R.string.files_changed), status = if (change.conflicted) Status.NeedsYou else Status.Unknown) else GitLetter(change)
            }
            if (!selecting) JIconButton(PersonaIcons.moreMenu, stringResource(R.string.files_entry_actions, entry.name), { menu = true })
        }
    }) {
        if (selecting) {
            JCheckbox(checked, null)
            Spacer(Modifier.width(Tokens.Space.s))
        }
        Box {
            if (thumbnail != null) FileThumbnailIcon(thumbnail.first, fileIcon(entry.name), thumbnail.second)
            else JIcon(if (entry.dir) PhIcons.FolderSimple else fileIcon(entry.name), null, tint = palette.ink2, decorative = true)
            if (entry.symlink) JIcon(PersonaIcons.link, null, Modifier.align(Alignment.BottomEnd),
                size = Tokens.Space.m, tint = if (entry.linkBroken) palette.danger.text else palette.accent, decorative = true)
        }
        Spacer(Modifier.width(Tokens.Space.l))
        Column(Modifier.weight(1f)) {
            Text(entry.name, style = PersonaTheme.type.body, color = if (entry.name.startsWith('.')) palette.ink2 else palette.ink,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(meta, style = PersonaTheme.type.caption.copy(fontWeight = FontWeight.Normal), color = palette.ink2,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (entry.symlink) {
                val linkLabel = stringResource(if (entry.linkBroken) R.string.files_broken_link else R.string.files_symlink)
                val target = entry.linkTarget
                Text(if (target == null) linkLabel else stringResource(R.string.files_link_to, linkLabel, target),
                    style = PersonaTheme.type.caption, color = if (entry.linkBroken) palette.danger.text else palette.ink2,
                    maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
    if (menu) JActionSheet(buildList {
        add(JAction(stringResource(R.string.files_browser_info), PersonaIcons.info, onClick = info))
        if (!selecting) add(JAction(stringResource(R.string.files_select_item), PersonaIcons.done, onClick = select))
        add(JAction(stringResource(R.string.files_insert_path), PersonaIcons.insert, onClick = insert))
        add(JAction(stringResource(R.string.files_copy_path), PersonaIcons.copy, onClick = copy))
        rename?.let { add(JAction(stringResource(R.string.files_rename), PersonaIcons.edit, separatorBefore = true, onClick = it)) }
        addAll(rowActions())
        trash?.let { add(JAction(stringResource(R.string.files_trash), PersonaIcons.delete, destructive = true, separatorBefore = rename == null, onClick = it)) }
    }, { menu = false }, entry.name)
}

/** The icon of a file by its kind: documents, images, code and config, anything else. */
internal fun fileIcon(name: String): PhIcon = when (FilesPaths.extension(name)) {
    "md", "markdown", "mdx", "txt", "rtf", "pdf" -> PhIcons.FileText
    "png", "jpg", "jpeg", "gif", "webp", "heic", "bmp", "svg" -> PhIcons.Image
    "kt", "kts", "java", "go", "rs", "swift", "ts", "tsx", "js", "jsx", "py", "rb", "c", "h", "cpp", "m", "sh", "zsh",
    "json", "yaml", "yml", "toml", "xml", "html", "css", "sql", "gradle", "plist" -> PhIcons.Code
    else -> PhIcons.File
}

@Composable internal fun OneLine(text: String) { Text(text, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1, softWrap = false, overflow = TextOverflow.Ellipsis) }

/** A line of explanation. [padded]: on its own the page's side margin; inside a list page that already has it, none. */
@Composable internal fun Notice(text: String, padded: Boolean = true) {
    Text(text, Modifier.padding(horizontal = if (padded) pagePadding else Tokens.Space.xs, vertical = Tokens.Space.s),
        style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
}

@Composable internal fun Loading() { JLoading() }

@Composable internal fun RetryState(title: String, message: String, retry: () -> Unit) {
    JEmptyState(title, message, action = { JButton(stringResource(R.string.files_retry), retry, kind = JButtonKind.Secondary) })
}

/** The most entries a folder listing brings (the node cuts it there and says it's truncated). */
internal const val MAX_LISTED = 2000
