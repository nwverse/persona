package persona.feature.files

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.viewinterop.AndroidView
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

private enum class EditorDecision { Discard, Changed, Overwrite }

@Composable
internal fun FileEditor(document: FileEditorDocument, readOnly: Boolean, close: (reload: Boolean) -> Unit) {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val resources = LocalResources.current
    val keyboard = LocalSoftwareKeyboardController.current
    val feedback = LocalAppFeedback.current
    val input = remember(document) { FileCodeInput(context, document.opened.body, FilesPaths.extension(document.file.name)) }
    var dirty by remember(document) { mutableStateOf(false) }
    var undoable by remember(document) { mutableStateOf(false) }
    var saving by remember(document) { mutableStateOf(false) }
    var decision by remember(document) { mutableStateOf<EditorDecision?>(null) }
    var search by remember(document) { mutableStateOf(false) }
    var error by remember(document) { mutableStateOf<String?>(null) }
    val palette = PersonaTheme.palette
    val font = with(LocalDensity.current) { PersonaTheme.type.code.fontSize.toPx() }
    val inset = with(LocalDensity.current) { Tokens.Space.s.roundToPx() }
    SideEffect {
        input.changed = { changed, undo -> dirty = changed; undoable = undo; error = null }
        input.tooLarge = { error = resources.getString(R.string.files_editor_write_limit) }
    }

    fun exit() {
        if (saving) return
        if (dirty) decision = EditorDecision.Discard else close(false)
    }
    fun save(copy: Boolean = false, force: Boolean = false) {
        if (saving || readOnly) return
        decision = null
        keyboard?.hide()
        val edited = input.text.toString()
        saving = true
        error = null
        scope.launch {
            try {
                val message = if (copy) {
                    val name = document.saveCopy(edited)
                    if (name == null) { error = resources.getString(R.string.files_editor_copy_full); return@launch }
                    resources.getString(R.string.files_editor_saved_copy, name)
                } else {
                    document.save(edited, force)
                    resources.getString(R.string.files_editor_saved)
                }
                feedback?.notice(message, FeedbackStyle.Success)
                close(true)
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) {
                if (!copy && fileEditorConflict(failure, "file_changed")) decision = EditorDecision.Changed
                else error = resources.getString(R.string.files_editor_save_failed,
                    editRefusalText(resources, editRefusal(failure, document.file.name)))
            } finally { saving = false }
        }
    }

    BackHandler { exit() }
    FilePage(document.file.name, subtitle = stringResource(if (dirty) R.string.files_editor_edited else R.string.files_editor_editing),
        onBack = ::exit, titleBusy = saving, scrollEdges = false, actions = {
            JIconButton(PersonaIcons.undo, stringResource(R.string.files_undo), input::undoOnce, enabled = undoable && !saving && !readOnly)
            JIconButton(PersonaIcons.search, stringResource(R.string.files_editor_find_replace), { search = !search }, selected = search)
            JIconButton(PersonaIcons.done, stringResource(R.string.files_editor_save), { save() },
                Modifier.testTag("file-editor-save"), enabled = dirty && !saving && !readOnly)
        }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).imePadding()) {
            if (search) EditorFind(input, !saving && !readOnly)
            if (readOnly) Notice(stringResource(R.string.files_edit_read_only))
            error?.let { Notice(it) }
            AndroidView(factory = { input }, modifier = Modifier.fillMaxWidth().weight(1f), update = {
                it.appearance(palette, font, inset)
                it.isEnabled = !saving && !readOnly
                it.contentDescription = resources.getString(R.string.files_editor_field)
            })
        }
    }
    val dismiss = { decision = null }
    when (decision) {
        EditorDecision.Changed -> JActionSheet(listOf(
            JAction(stringResource(R.string.files_editor_copy)) { save(copy = true) },
            JAction(stringResource(R.string.files_editor_discard_mine)) { decision = null; close(true) },
            JAction(stringResource(R.string.files_editor_overwrite_anyway), destructive = true) { decision = EditorDecision.Overwrite },
            JAction(stringResource(R.string.files_editor_keep), onClick = dismiss),
        ), dismiss, title = stringResource(R.string.files_editor_changed_title, document.file.name),
            preview = stringResource(R.string.files_editor_changed_detail))
        EditorDecision.Overwrite -> JDialog(onDismiss = dismiss, title = stringResource(R.string.files_editor_overwrite_title),
            text = stringResource(R.string.files_editor_overwrite_detail),
            confirm = stringResource(R.string.files_editor_overwrite) to { save(force = true) },
            dismiss = stringResource(R.string.files_editor_cancel) to dismiss, destructive = true)
        EditorDecision.Discard -> JDialog(onDismiss = dismiss, title = stringResource(R.string.files_editor_discard_title),
            text = stringResource(R.string.files_editor_discard_detail),
            confirm = stringResource(R.string.files_editor_discard) to { decision = null; close(false) },
            dismiss = stringResource(R.string.files_editor_keep) to dismiss, destructive = true)
        null -> Unit
    }
}

@Composable
private fun EditorFind(input: FileCodeInput, writable: Boolean) {
    var query by remember { mutableStateOf("") }
    var replacement by remember { mutableStateOf("") }
    var missed by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth().padding(horizontal = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        JTextField(query, { query = it; missed = false }, Modifier.fillMaxWidth().testTag("editor-query"),
            label = stringResource(R.string.files_editor_find), singleLine = true, autofill = false)
        JTextField(replacement, { replacement = it }, Modifier.fillMaxWidth().testTag("editor-replacement"),
            label = stringResource(R.string.files_editor_replace), singleLine = true, autofill = false)
        FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            JButton(stringResource(R.string.files_editor_next), { missed = !input.findNext(query) },
                kind = JButtonKind.Quiet, size = JButtonSize.Small, enabled = query.isNotEmpty())
            JButton(stringResource(R.string.files_editor_replace), { missed = !input.replaceMatch(query, replacement, false) },
                kind = JButtonKind.Quiet, size = JButtonSize.Small, enabled = query.isNotEmpty() && writable)
            JButton(stringResource(R.string.files_editor_replace_all), { missed = !input.replaceMatch(query, replacement, true) },
                kind = JButtonKind.Quiet, size = JButtonSize.Small, enabled = query.isNotEmpty() && writable)
        }
        if (missed) Text(stringResource(R.string.files_editor_no_matches), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
    }
}
