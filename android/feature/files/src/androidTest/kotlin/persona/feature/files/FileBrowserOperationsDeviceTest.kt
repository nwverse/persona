package persona.feature.files

import android.app.Activity
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.content.res.Configuration
import android.graphics.Bitmap
import android.net.Uri
import android.provider.MediaStore
import androidx.activity.compose.LocalActivityResultRegistryOwner
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.ActivityResultRegistryOwner
import androidx.activity.result.contract.ActivityResultContract
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.core.app.ActivityOptionsCompat
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CompletableDeferred
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import persona.core.navigation.ChatRoute
import java.io.File
import java.util.Locale
import java.util.concurrent.CopyOnWriteArrayList

private class ActionComputer : SessionFiles by EditorTestMachine() {
    val root = "/home/reader/project"
    val calls = CopyOnWriteArrayList<String>()
    val created = CopyOnWriteArrayList<String>()
    val bytes = "the complete file\n".toByteArray()
    var slowZip: CompletableDeferred<Unit>? = null
    var granted = 420
    var supportsWrites = true
    var moveConflict = false
    override fun supports(machineId: String, feature: SessionFileFeature) = supportsWrites || feature == SessionFileFeature.Download
    override fun editsFiles(machineId: String) = supportsWrites
    override suspend fun directory(machineId: String, cwd: String, path: String): SessionDirectory {
        val entries = if (path == root) listOf(SessionFileEntry("Notes", dir = true), SessionFileEntry("draft.md"),
            SessionFileEntry("photo.png"), SessionFileEntry("link", symlink = true)) + created.map { SessionFileEntry(it, dir = it == "New folder") }
            else when (path) {
                "/" -> listOf(SessionFileEntry("home", dir = true))
                "/home" -> listOf(SessionFileEntry("reader", dir = true))
                "/home/reader" -> listOf(SessionFileEntry("project", dir = true))
                else -> listOf(SessionFileEntry("child"))
            }
        return SessionDirectory(path, entries)
    }
    override suspend fun file(machineId: String, cwd: String, path: String) = SessionFileContent(path, FilesPaths.name(path),
        text = null, mime = "text/plain; charset=utf-8", revision = "r1", size = 0)
    override suspend fun filePreview(machineId: String, cwd: String, path: String) = file(machineId, cwd, path)
    override suspend fun fileInfo(machineId: String, cwd: String, path: String) =
        SessionPathStat(path, "file", mode = granted, owner = "reader", group = "team")
    override suspend fun mkdir(machineId: String, cwd: String, dir: String, name: String): String {
        calls += "mkdir $dir/$name"
        created += name
        return "$dir/$name"
    }
    override suspend fun writeText(machineId: String, cwd: String, path: String, text: String, create: Boolean, revision: String?, sha256: String?, force: Boolean): SessionWrittenFile {
        calls += "write $path $create $force"
        created += FilesPaths.name(path)
        return SessionWrittenFile(path, text.length.toLong(), "r1")
    }
    override suspend fun copyFiles(machineId: String, cwd: String, paths: List<String>, dir: String, conflict: SessionFileConflict?): List<SessionFileChange> {
        calls += "copy $paths $dir $conflict"
        if (conflict == null) throw HubFailure(HubError.Server, status = 409, code = "exists", body = """{"name":"draft.md"}""")
        return paths.map { SessionFileChange(it, joinRemotePath(dir, "draft 2.md")) }
    }
    override suspend fun move(machineId: String, cwd: String, paths: List<String>, directory: String, conflict: SessionMoveConflictPolicy?): List<SessionMovedFile> {
        calls += "move $paths $directory $conflict"
        if (moveConflict && conflict == null) throw HubFailure(HubError.Server, status = 409, code = "exists", body = """{"name":"draft.md"}""")
        return paths.map { SessionMovedFile(it, joinRemotePath(directory, FilesPaths.name(it))) }
    }
    override suspend fun zipFiles(machineId: String, cwd: String, paths: List<String>, name: String, dir: String?): SessionZippedFile {
        calls += "zip $paths $name $dir"
        try { slowZip?.await() } catch (cancelled: kotlinx.coroutines.CancellationException) { calls += "zip cancelled"; throw cancelled }
        return SessionZippedFile(joinRemotePath(dir ?: "/tmp/zip", name), name, bytes.size.toLong(), paths.size)
    }
    override suspend fun download(machineId: String, cwd: String, path: String, into: File): Long {
        calls += "download $path"
        into.writeBytes(bytes)
        return bytes.size.toLong()
    }
    override suspend fun uploadFile(machineId: String, cwd: String, dir: String, name: String, file: File, conflict: SessionFileConflict?): SessionUploadedFile {
        calls += "upload $dir/$name " + file.readText()
        return SessionUploadedFile("$dir/$name", name, file.length())
    }
    override suspend fun chmod(machineId: String, cwd: String, path: String, mode: Int): Int {
        calls += "chmod $path $mode"
        granted = mode
        return mode
    }
}

class FileBrowserOperationsDeviceTest {
    @get:Rule val rule = createAndroidComposeRule<FileEditorTestActivity>()
    private val machine = ActionComputer()
    private val launched = CopyOnWriteArrayList<Intent>()
    private val notices = CopyOnWriteArrayList<String>()
    private val instrumentation get() = InstrumentationRegistry.getInstrumentation()
    private val ctx get() = rule.activity
    private val root = machine.root
    private val registry = object : ActivityResultRegistryOwner {
        var key = 0
        override val activityResultRegistry = object : ActivityResultRegistry() {
            override fun <I, O> onLaunch(requestCode: Int, contract: ActivityResultContract<I, O>, input: I, options: ActivityOptionsCompat?) {
                key = requestCode
                launched += contract.createIntent(ctx, input)
            }
        }
    }
    private val feedback = object : AppFeedback {
        override fun failure(error: Throwable, operation: Boolean) = false
        override fun notice(message: String): Boolean { notices += message; return true }
    }

    private fun show(cwd: String = root, os: String = "linux") {
        val args = InstrumentationRegistry.getArguments()
        val style = AppStyle.entries.firstOrNull { it.key == args.getString("style") } ?: AppStyle.Brief
        rule.setContent {
            CompositionLocalProvider(LocalActivityResultRegistryOwner provides registry, LocalAppFeedback provides feedback) {
                PersonaTheme(darkTheme = args.getString("dark") == "true", style = style, reduceMotion = true) {
                    SessionFilesScreen(ChatRoute("fixture", "conversation"), cwd, machine, {}, {}, machineHome = "/home/reader",
                        machineOs = os, onOpenFolderTerminal = { machine.calls += "terminal $it" })
                }
            }
        }
    }
    private fun menu(name: String) { rule.onNodeWithContentDescription("Actions for $name").performClick() }
    private fun tap(label: String) {
        val node = rule.onAllNodes(hasText(label) and hasClickAction()).onLast()
        // A tall action sheet scrolls independently of the browser.
        runCatching { node.performScrollTo() }
        node.performClick()
    }
    private fun awaitCall(value: String) = rule.waitUntil(6_000) { value in machine.calls }
    private fun shot(name: String) {
        val args = InstrumentationRegistry.getArguments()
        val suffix = args.getString("capture") ?: return
        rule.waitForIdle()
        val automation = instrumentation.uiAutomation
        // A native dialog window can still be fading in after Compose has finished laying out its content.
        automation.waitForIdle(550, 4_000)
        val bitmap = requireNotNull(automation.takeScreenshot())
        try {
            val dir = File(ctx.getExternalFilesDir(null), "file-actions").apply { mkdirs() }
            File(dir, "$name-$suffix.png").outputStream().use { check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        } finally { bitmap.recycle() }
    }
    private fun pick(result: Intent?, code: Int = Activity.RESULT_OK) = rule.runOnIdle {
        registry.activityResultRegistry.dispatchResult(registry.key, code, result)
    }

    @Test fun createFolderAndTextFromTheNewMenu() {
        show()
        rule.onNodeWithContentDescription("New").performClick()
        tap("New Folder")
        rule.onNode(hasSetTextAction()).performTextReplacement("New folder")
        tap("Create")
        awaitCall("mkdir $root/New folder")
        rule.onNodeWithContentDescription("New").performClick()
        tap("New Text File")
        rule.onNode(hasSetTextAction()).performTextReplacement("notes.txt")
        tap("Create")
        awaitCall("write $root/notes.txt true false")
        rule.onNodeWithTag("file-editor-save").assertExists()
    }

    @Test fun copyAsksBeforeReplacementMoveChoosesARemoteFolderAndDuplicateKeepsBoth() {
        show()
        menu("draft.md"); tap("Duplicate")
        awaitCall("copy [$root/draft.md] $root KeepBoth")
        menu("draft.md"); tap("Copy to…"); tap("Notes"); tap("Copy here")
        awaitCall("copy [$root/draft.md] $root/Notes null")
        assertFalse(machine.calls.any { it.endsWith("Replace") })
        tap("Keep both")
        awaitCall("copy [$root/draft.md] $root/Notes KeepBoth")
        menu("draft.md"); tap("Move…"); tap("Notes"); tap("Move Here")
        awaitCall("move [$root/draft.md] $root/Notes null")
        menu("Notes"); tap("Open Terminal Here")
        awaitCall("terminal $root/Notes")
        menu("draft.md"); tap("Compress")
        awaitCall("zip [$root/draft.md] draft.zip $root")
    }

    @Test fun selectionMovesTheWholeBatchOnlyAfterTheChosenConflictPolicy() {
        machine.moveConflict = true
        show()
        menu("photo.png"); tap("Select"); tap("draft.md")
        rule.onNodeWithContentDescription("Move…").performClick()
        rule.onNodeWithText("Move 2 items").assertExists()
        rule.onNodeWithText("Move Here").assertIsNotEnabled()
        tap("Notes")
        shot("move-destination")
        tap("Move Here")
        val transfer = "move [$root/draft.md, $root/photo.png] $root/Notes"
        awaitCall("$transfer null")
        rule.onNodeWithText("Your choice applies to all name conflicts", substring = true).assertExists()
        shot("move-conflict")
        assertEquals(listOf("$transfer null"), machine.calls.filter { it.startsWith("move ") })
        tap("Cancel")
        rule.onNodeWithContentDescription("Move…").performClick()
        tap("Notes"); tap("Move Here"); tap("Keep Both")
        rule.waitUntil(6_000) { "Moved 2 items" in notices }
        assertEquals(listOf("$transfer null", "$transfer null", "$transfer KeepBoth"), machine.calls.filter { it.startsWith("move ") })
        rule.onNodeWithText("2 selected").assertDoesNotExist()
    }

    @Test fun permissionGridNamesTheChangeAndRequiresTheSecondConfirmation() {
        show()
        menu("draft.md"); tap("Permissions")
        rule.onNodeWithTag("file-permission-2-2").performClick()
        assertEquals(420, machine.granted)
        shot("permissions")
        tap("Review changes")
        rule.onNodeWithText("Everyone else: Read only → Read and run", substring = true).assertIsDisplayed()
        shot("permission-review")
        tap("Cancel")
        assertEquals(420, machine.granted)
        tap("Review changes"); tap("Change")
        awaitCall("chmod $root/draft.md 421")
        menu("link")
        rule.onNodeWithText("Permissions").assertDoesNotExist()
    }

    @Test fun permissionReviewOnlyNamesTheChangedRoleInEachSupportedLanguage() {
        val expected = mapOf(
            "en" to "Everyone else: Read only → Read and run",
            "zh-CN" to "其他人：只读 → 读取和执行",
            "ja" to "その他のユーザー：読み取りのみ → 読み取り・実行",
        )
        for ((tag, change) in expected) {
            val config = Configuration(ctx.resources.configuration).apply { setLocale(Locale.forLanguageTag(tag)) }
            val resources = ctx.createConfigurationContext(config).resources
            assertEquals(change, permissionChanges(resources, 420, 421))
            assertEquals("", permissionChanges(resources, 421, 421))
            assertTrue(permissionWords(resources, 421).contains(resources.getString(R.string.files_action_everyone)))
        }
    }

    @Test fun windowsAndKnownReadOnlyFoldersKeepReadsAndHideUnsafeWrites() {
        show(os = "windows")
        menu("draft.md")
        rule.onNodeWithText("Download").assertExists()
        rule.onNodeWithText("Permissions").assertDoesNotExist()
    }

    @Test fun outsideTheHomeOffersDownloadsAndOldHubsKeepTheirSingleFileRead() {
        machine.supportsWrites = false
        show("/shared")
        rule.onNodeWithContentDescription("New").assertDoesNotExist()
        menu("child")
        rule.onNodeWithText("Download").assertExists()
        for (label in listOf("Move…", "Copy to…", "Compress", "Permissions")) rule.onNodeWithText(label).assertDoesNotExist()
    }

    @Test fun copyMayReadFromOutsideTheHomeIntoANewWritableDestination() {
        show("/shared")
        menu("child"); tap("Copy to…")
        rule.onNode(hasText("Copy here") and hasClickAction()).assertIsNotEnabled()
        rule.onAllNodesWithContentDescription("Up one level").onLast().performClick(); tap("home"); tap("reader"); tap("project")
        tap("New Folder")
        rule.onNode(hasSetTextAction()).performTextReplacement("handoff")
        tap("Create")
        awaitCall("mkdir $root/handoff")
        tap("Copy here")
        awaitCall("copy [/shared/child] $root/handoff null")
        tap("Keep both")
        awaitCall("copy [/shared/child] $root/handoff KeepBoth")
    }

    @Test fun cancellingASlowFolderDownloadStopsTheRequestBeforeTheSavePicker() {
        machine.slowZip = CompletableDeferred()
        show()
        menu("Notes"); tap("Download")
        awaitCall("zip [$root/Notes] Notes.zip null")
        rule.waitUntil(8_000) { rule.onAllNodesWithText("Compressing on the computer…").fetchSemanticsNodes().isNotEmpty() }
        tap("Cancel")
        awaitCall("zip cancelled")
        assertTrue(launched.isEmpty())
        assertTrue(machine.calls.none { it.startsWith("download ") })
    }

    @Test fun folderAndMultiSelectDownloadTemporaryArchivesAndSaveOnlyAfterThePicker() {
        show()
        menu("Notes"); tap("Download")
        awaitCall("zip [$root/Notes] Notes.zip null")
        rule.waitUntil(6_000) { launched.isNotEmpty() }
        assertEquals("application/zip", launched.last().type)
        assertEquals(Intent.ACTION_CREATE_DOCUMENT, launched.last().action)
        assertEquals("Notes.zip", launched.last().getStringExtra(Intent.EXTRA_TITLE))
        pick(null, Activity.RESULT_CANCELED)
        assertFalse(notices.contains("File saved"))
        menu("draft.md"); tap("Select"); tap("Notes")
        rule.onNodeWithText("2 selected", substring = true).assertExists()
        shot("selection")
        rule.onNodeWithContentDescription("Download").performClick()
        awaitCall("zip [$root/Notes, $root/draft.md] Archive.zip null")
        rule.waitUntil(6_000) { launched.size == 2 }
        val uri = newTestDocument(ctx, "picked-note.zip")
        try {
            pick(Intent().setData(uri))
            rule.waitUntil(6_000) { "File saved" in notices }
            ctx.contentResolver.openInputStream(uri)!!.use { assertArrayEquals(machine.bytes, it.readBytes()) }
        } finally { ctx.contentResolver.delete(uri, null, null) }
    }

    @Test fun uploadReadsAllSelectedSafDocumentsAndUsesTheirDisplayNames() {
        show()
        val uris = listOf("first note.txt", "second note.txt").map { newTestDocument(ctx, it) }
        try {
            for ((i, uri) in uris.withIndex()) ctx.contentResolver.openOutputStream(uri)!!.use { it.write("text $i".toByteArray()) }
            rule.onNodeWithContentDescription("New").performClick(); tap("Upload files")
            rule.waitUntil(6_000) { launched.isNotEmpty() }
            assertEquals(Intent.ACTION_OPEN_DOCUMENT, launched.single().action)
            assertTrue(launched.single().getBooleanExtra(Intent.EXTRA_ALLOW_MULTIPLE, false))
            val data = android.content.ClipData.newRawUri("fixtures", uris.first()).apply { addItem(android.content.ClipData.Item(uris.last())) }
            pick(Intent().apply { clipData = data })
            for ((i, uri) in uris.withIndex()) {
                val name = ctx.contentResolver.query(uri, arrayOf(MediaStore.MediaColumns.DISPLAY_NAME), null, null, null)!!.use {
                    assertTrue(it.moveToFirst()); it.getString(0)
                }
                awaitCall("upload $root/$name text $i")
            }
        } finally { uris.forEach { ctx.contentResolver.delete(it, null, null) } }
    }

    @Test fun cancellingThePhotoPickerKeepsTheFolderUntouchedAndAllowsAnotherUpload() {
        show()
        rule.onNodeWithContentDescription("New").performClick(); tap("Upload photos or videos")
        rule.waitUntil(6_000) { launched.size == 1 }
        val photo = launched.single()
        // The AndroidX contract uses the system photo picker when installed and falls back to the document picker.
        assertTrue(photo.action in setOf(MediaStore.ACTION_PICK_IMAGES, Intent.ACTION_OPEN_DOCUMENT,
            ActivityResultContracts.PickVisualMedia.ACTION_SYSTEM_FALLBACK_PICK_IMAGES))
        pick(null, Activity.RESULT_CANCELED)
        assertTrue(machine.calls.isEmpty())
        rule.onNodeWithContentDescription("New").performClick(); tap("Upload files")
        rule.waitUntil(6_000) { launched.size == 2 }
        assertEquals(Intent.ACTION_OPEN_DOCUMENT, launched.last().action)
    }
}

private fun newTestDocument(context: Context, name: String): Uri =
    requireNotNull(context.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, ContentValues().apply {
        put(MediaStore.MediaColumns.DISPLAY_NAME, "persona-fixture-" + System.nanoTime() + "-" + name)
        put(MediaStore.MediaColumns.MIME_TYPE, "application/octet-stream")
    }))
