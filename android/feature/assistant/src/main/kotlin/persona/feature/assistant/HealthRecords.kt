package persona.feature.assistant

import android.content.Context
import android.net.Uri
import androidx.navigationevent.DirectNavigationEventInput
import androidx.navigationevent.NavigationEventDispatcher
import androidx.navigationevent.NavigationEventDispatcherOwner
import androidx.navigationevent.compose.LocalNavigationEventDispatcherOwner
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import persona.core.domain.ComposerMessage
import persona.feature.assistant.settings.*
import persona.feature.chat.selectAttachment
import persona.feature.chat.uploadAttachment

internal enum class HealthSection(val title: Int) {
    Reports(R.string.health_checkups), Trends(R.string.health_trends), Workouts(R.string.health_workouts), Memory(R.string.health_memory)
}

internal fun LazyListScope.healthSections(onOpen: (HealthSection) -> Unit) {
    itemsIndexed(HealthSection.entries, key = { _, section -> section.name }) { i, section ->
        val icon = when (section) {
            HealthSection.Reports -> PersonaIcons.document
            HealthSection.Trends -> PersonaIcons.healthTrends
            HealthSection.Workouts -> PersonaIcons.health
            HealthSection.Memory -> PersonaIcons.memory
        }
        JGroupItem(GroupPosition.of(i, HealthSection.entries.size)) {
            JRow(stringResource(section.title), leadingIcon = icon, onClick = { onOpen(section) })
        }
    }
}

internal sealed interface HealthRead<out T> {
    data object Loading : HealthRead<Nothing>
    data class Failed(val problem: HealthReadProblem? = null) : HealthRead<Nothing>
    data class Ready<T>(val value: T) : HealthRead<T>
}

/** Android sends system Back to the sheet window. Let its current detail or preview pop before the whole sheet. */
@Composable
internal fun HealthSheet(onClose: () -> Unit, content: @Composable () -> Unit) {
    val close by rememberUpdatedState(onClose)
    val input = remember { DirectNavigationEventInput() }
    val owner = remember {
        object : NavigationEventDispatcherOwner {
            override val navigationEventDispatcher = NavigationEventDispatcher(onBackCompletedFallback = { close() })
        }
    }
    DisposableEffect(owner) {
        owner.navigationEventDispatcher.addInput(input)
        onDispose { owner.navigationEventDispatcher.dispose() }
    }
    JPageSizedSettingsSheet(onDismiss = input::backCompleted) {
        CompositionLocalProvider(LocalNavigationEventDispatcherOwner provides owner, content = content)
    }
}

@Composable
internal fun <T> healthRead(vararg keys: Any?, load: suspend () -> T): HealthRead<T> = key(keys.toList()) {
    val read by rememberUpdatedState(load)
    var result by remember { mutableStateOf<HealthRead<T>>(HealthRead.Loading) }
    LaunchedEffect(Unit) {
        result = try { HealthRead.Ready(read()) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (error: HealthReadException) { HealthRead.Failed(error.problem) }
        catch (error: Exception) {
            val failure = hubCodedFailure(error)
            HealthRead.Failed(when {
                failure?.status in setOf(401, 403) -> HealthReadProblem.HubDenied
                failure?.status == 404 || failure?.code == "node_outdated" -> HealthReadProblem.HubOutdated
                else -> null
            })
        }
    }
    result
}

@Composable
internal fun HealthFailure(problem: HealthReadProblem?, onRetry: () -> Unit, onAccess: (() -> Unit)? = null) {
    val words = when (problem) {
        HealthReadProblem.Disabled -> R.string.health_source_disabled
        HealthReadProblem.Permission -> R.string.health_source_permission
        HealthReadProblem.Unavailable -> R.string.health_source_unavailable
        HealthReadProblem.HubDenied -> R.string.health_denied
        HealthReadProblem.HubOutdated -> R.string.health_outdated
        null -> R.string.health_read_failed
    }
    val access = onAccess.takeIf { problem != null }
    JNotice(stringResource(words), actionLabel = stringResource(if (access != null) R.string.health_manage_access else R.string.assistant_settings_retry),
        onAction = access ?: onRetry)
}

@Composable
internal fun HealthReportsPage(
    owner: HealthSpace, upload: ChatComposerService?, onOpenChat: (SessionKey) -> Unit,
    preview: (@Composable (machine: String, path: String, close: () -> Unit) -> Unit)?,
    onBack: () -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var revision by remember { mutableIntStateOf(0) }
    val reports = healthRead(owner, revision) { owner.reports() }
    var selected by remember(owner) { mutableStateOf<CheckupReport?>(null) }
    var file by remember(owner) { mutableStateOf<String?>(null) }
    var deleting by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }
    var pickerOwner by remember { mutableStateOf<HealthSpace?>(null) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        val picked = pickerOwner
        pickerOwner = null
        if (picked === owner && upload != null && uris.isNotEmpty()) scope.launch {
            busy = true
            failed = false
            try {
                val chat = uploadReports(context, owner, upload, uris)
                currentCoroutineContext().ensureActive()
                onOpenChat(chat)
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { failed = true }
            finally { busy = false; revision++ }
        }
    }
    val opened = file
    if (opened != null && preview != null) {
        BackHandler { file = null }
        preview(owner.machineId, opened) { file = null }
        return
    }
    val report = selected
    val back: () -> Unit = { if (report != null) { selected = null; failed = false } else onBack() }
    BackHandler(report != null, back)
    JListPage(report?.title ?: stringResource(R.string.health_checkups), onBack = back,
        onRefresh = if (report == null) ({ revision++ }) else null) {
        if (failed) item { JNotice(stringResource(R.string.health_action_failed), status = Status.NeedsYou) }
        if (report != null) {
            report.recordPath?.takeIf(String::isNotBlank)?.let { path -> item {
                JGroupItem(GroupPosition.Single) { JRow(stringResource(R.string.health_summary), leadingIcon = PersonaIcons.document,
                    enabled = preview != null, onClick = { file = path }) }
            } }
            report.path?.takeIf(String::isNotBlank)?.let { path -> item {
                JGroupItem(GroupPosition.Single) { JRow(stringResource(R.string.health_original), leadingIcon = PersonaIcons.openDocument,
                    enabled = preview != null, onClick = { file = path }) }
            } }
            item { JTextButton(stringResource(R.string.assistant_settings_delete), { deleting = true }, enabled = !busy) }
        } else {
            if (upload != null) item("upload") {
                JButton(stringResource(R.string.health_upload), {
                    pickerOwner = owner
                    picker.launch(arrayOf("application/pdf", "image/*"))
                }, Modifier.fillMaxWidth().padding(vertical = Tokens.Space.s), enabled = !busy, loading = busy,
                    icon = PersonaIcons.add)
            }
            when (reports) {
                HealthRead.Loading -> item { JLoading() }
                is HealthRead.Failed -> item { HealthFailure(reports.problem, { revision++ }) }
                is HealthRead.Ready -> {
                    if (reports.value.isEmpty()) item { JEmptyState(stringResource(R.string.health_no_checkups), stringResource(R.string.health_checkups_empty_note), icon = PersonaIcons.document) }
                    itemsIndexed(reports.value, key = { _, it -> it.name }) { i, entry ->
                        JGroupItem(GroupPosition.of(i, reports.value.size)) {
                            JRow(entry.title, subtitle = stringResource(if (entry.recordPath.isNullOrBlank()) R.string.health_no_summary else R.string.health_has_summary),
                                leadingIcon = PersonaIcons.document, onClick = { selected = entry; failed = false })
                        }
                    }
                }
            }
        }
    }
    if (deleting && report != null) JDialog(onDismiss = { deleting = false }, title = stringResource(R.string.health_delete_report),
        text = stringResource(R.string.health_delete_explanation), destructive = true, confirmEnabled = !busy,
        confirm = stringResource(R.string.assistant_settings_delete) to {
            deleting = false
            scope.launch {
                busy = true
                failed = false
                try { owner.deleteReport(report.name); selected = null; revision++ }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) { failed = true }
                finally { busy = false }
            }
            Unit
        })
}

/** A clinical image is a report original, so it takes the same private file route as a PDF. */
internal suspend fun uploadReports(context: Context, owner: HealthSpace, upload: ChatComposerService, uris: List<Uri>): SessionKey {
    val originals = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
        uris.map { selectAttachment(context, it, image = false, keepForDraft = false) }
    }
    require(originals.all { it.error == null })
    val machine = SessionKey(owner.machineId, PendingMainConversation.sessionId)
    val paths = originals.map { attachment ->
        ComposerMessage.quotePath(uploadAttachment(context, upload, machine, attachment, destination = AssistantSpace.HEALTH))
    }
    return owner.startReportChat(context.getString(R.string.health_report_prompt, paths.joinToString(" ")))
}

@Composable
internal fun HealthMemoryPage(owner: HealthSpace, onBack: () -> Unit) {
    var revision by remember { mutableIntStateOf(0) }
    var selected by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    var notice by remember { mutableStateOf<SettingsNotice?>(null) }
    var deleteFailed by remember { mutableStateOf(false) }
    val read = healthRead(owner, revision) { memoryEntries(owner.memory()) }
    val memory = (read as? HealthRead.Ready)?.value
    val name = selected
    if (name != null && memory != null) {
        BackHandler { selected = null }
        val state = AssistantSettingsUiState(busy = busy, notice = notice, memory = MemoryState(memory.first, memory.second))
        suspend fun change(operation: suspend () -> Unit): Boolean {
            busy = true
            notice = null
            return try { operation(); revision++; true }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { notice = SettingsNotice(SettingsText(R.string.health_action_failed), true); false }
            finally { busy = false }
        }
        MemoryFilePart(state, name, null, onBack = { selected = null },
            onSave = { n, text -> change { owner.saveMemory(n, text) } },
            onDelete = { n -> if (change { owner.deleteMemory(n) }) selected = null else deleteFailed = true })
        if (deleteFailed) JDialog(onDismiss = { deleteFailed = false }, title = stringResource(R.string.health_action_failed),
            confirm = stringResource(android.R.string.ok) to { deleteFailed = false })
        return
    }
    JListPage(stringResource(R.string.health_memory), onBack = onBack, onRefresh = { revision++ }) {
        item { Text(stringResource(R.string.health_memory_note), Modifier.padding(vertical = Tokens.Space.m),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2) }
        when (read) {
            HealthRead.Loading -> item { JLoading() }
            is HealthRead.Failed -> item { HealthFailure(read.problem, { revision++ }) }
            is HealthRead.Ready -> {
                val (entries, index) = read.value
                if (entries.isEmpty()) item { JEmptyState(stringResource(R.string.assistant_settings_memory_empty_title), stringResource(R.string.assistant_settings_memory_empty_text), icon = PersonaIcons.memory) }
                itemsIndexed(entries, key = { _, it -> it.file.name }) { i, entry ->
                    JGroupItem(GroupPosition.of(i, entries.size)) {
                        JRow(entry.title, subtitle = entry.description, leadingIcon = PersonaIcons.memory, onClick = { notice = null; selected = entry.file.name })
                    }
                }
                if (index != null && entries.isNotEmpty()) item {
                    JGroupItem(GroupPosition.Single) { JRow(stringResource(R.string.assistant_settings_memory_index), onClick = { selected = index.name }) }
                }
            }
        }
    }
}
