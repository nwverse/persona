package persona.feature.files

import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.Rect
import android.app.Instrumentation
import android.content.ContentUris
import android.content.Intent
import android.net.Uri
import android.provider.DocumentsContract
import android.provider.MediaStore
import android.view.accessibility.AccessibilityNodeInfo
import android.text.style.CharacterStyle
import android.view.View
import android.view.ViewGroup
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import android.view.inputmethod.InputMethodManager
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.ChatRoute
import java.io.File
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.security.MessageDigest
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicReference
import java.util.UUID

class FileEditorTestActivity : ComponentActivity() {
    val savedDocument = AtomicReference<Uri>()
    @Deprecated("The activity-result registry forwards this platform callback")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (resultCode == RESULT_OK) data?.data?.let(savedDocument::set)
    }
}

private class MachineFailure(override val status: Int, override val code: String) : IOException(code), HubCodedFailure
internal data class EditorWrite(val machine: String, val cwd: String, val path: String, val text: String, val create: Boolean,
    val revision: String?, val sha256: String?, val force: Boolean)

internal class EditorTestMachine : SessionFiles {
    val dir = "/home/reader/project"
    var fileName = "weather.kt"
    val path get() = "$dir/$fileName"
    val initial = "\uFEFFfun forecast() = \"Clear\"\r\n// Clear near noon\r\n"
    var disk = initial
    var revision = "first-read"
    var writable = true
    var partial = false
    var next = 0L
    var reportedSize: Long? = null
    var reads = 0
    var copyCollides = false
    var failWrite = false
    var binary = false
    var mediaType = "text/plain"
    var bytes: ByteArray? = null
    var modernDownload = false
    val downloads = CopyOnWriteArrayList<String>()
    val writes = CopyOnWriteArrayList<EditorWrite>()
    override fun supports(machineId: String, feature: SessionFileFeature) = when (feature) {
        SessionFileFeature.Write -> writable
        SessionFileFeature.Download -> modernDownload
        else -> false
    }
    override suspend fun directory(machineId: String, cwd: String, path: String) =
        SessionDirectory(path, listOf(SessionFileEntry("weather.kt")))
    override suspend fun file(machineId: String, cwd: String, path: String): SessionFileContent {
        reads++
        return SessionFileContent(path, FilesPaths.name(path), size = reportedSize ?: bytes?.size?.toLong() ?: disk.toByteArray().size.toLong(),
            text = disk.takeUnless { binary || it.isEmpty() }, mime = mediaType, revision = revision, truncated = partial, stream = binary,
            next = next)
    }
    override suspend fun download(machineId: String, cwd: String, path: String, into: File): Long {
        downloads += path
        into.writeBytes(bytes ?: disk.toByteArray())
        return into.length()
    }
    override suspend fun writeText(machineId: String, cwd: String, path: String, text: String, create: Boolean,
        revision: String?, sha256: String?, force: Boolean): SessionWrittenFile {
        writes += EditorWrite(machineId, cwd, path, text, create, revision, sha256, force)
        if (failWrite) throw IOException("offline fixture")
        if (create && copyCollides && path.endsWith("weather 2.kt")) throw MachineFailure(409, "exists")
        if (!create && !force && revision != this.revision) throw MachineFailure(409, "file_changed")
        if (!create) { disk = text; this.revision = "saved" }
        return SessionWrittenFile(path, text.toByteArray().size.toLong(), "saved")
    }
    override suspend fun status(machineId: String, cwd: String) = SessionGitStatus(false)
    override suspend fun diff(machineId: String, cwd: String, path: String, staged: Boolean): SessionGitDiff = error("unused")
    override suspend fun log(machineId: String, cwd: String, skip: Int, limit: Int): SessionGitLog = error("unused")
    override suspend fun show(machineId: String, cwd: String, hash: String): SessionGitShow = error("unused")
}

class FilePreviewDeviceTest {
    @get:Rule val rule = createAndroidComposeRule<FileEditorTestActivity>()
    private val machine = EditorTestMachine()
    private val style = InstrumentationRegistry.getArguments().getString("editorStyle")?.let(AppStyle::valueOf) ?: AppStyle.Brief
    private fun label(id: Int) = rule.activity.getString(id)
    private fun tap(id: Int) = rule.onNodeWithText(label(id)).performClick()
    private fun button(id: Int) = rule.onNodeWithContentDescription(label(id))
    private fun awaitWrites(count: Int) = rule.waitUntil(10_000) { machine.writes.size == count }
    private fun field(tag: String) = rule.onNode(hasSetTextAction() and hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true)
    private fun editor(): FileCodeInput {
        fun find(view: View): FileCodeInput? {
            if (view is FileCodeInput) return view
            if (view is ViewGroup) for (index in 0 until view.childCount) find(view.getChildAt(index))?.let { return it }
            return null
        }
        return requireNotNull(find(rule.activity.window.decorView))
    }

    private fun render(dark: Boolean = false, readOnly: Boolean = false) {
        rule.setContent {
            PersonaTheme(darkTheme = dark, style = style, reduceMotion = true) {
                SessionFilesScreen(ChatRoute("fixture", "session"), machine.dir, machine, {}, {},
                    initialPath = machine.path, machineHome = if (readOnly) "/another/home" else "/home/reader")
            }
        }
        rule.waitUntil(10_000) { rule.onAllNodesWithContentDescription(label(R.string.files_editor_download)).fetchSemanticsNodes().isNotEmpty() }
    }

    private fun menu() { button(R.string.files_preview_options).performClick() }
    private fun edit() {
        menu()
        tap(R.string.files_editor_edit)
        rule.onNodeWithTag("file-editor-save").assertIsNotEnabled()
    }

    private fun append(extra: String) = rule.runOnIdle {
        val view = editor()
        view.setSelection(view.length())
        view.text.append(extra)
    }

    private fun changed() {
        edit()
        append("// Mine\n")
        machine.disk = "fun newer() = true\n"
        machine.revision = "changed-elsewhere"
        button(R.string.files_editor_save).performClick()
        rule.onNodeWithText(label(R.string.files_editor_copy)).assertIsDisplayed()
        assertEquals(1, machine.writes.size)
        assertFalse(machine.writes.single().force)
    }

    private fun capture(name: String) {
        rule.waitForIdle()
        val ui = InstrumentationRegistry.getInstrumentation().uiAutomation
        ui.waitForIdle(250, 2_000)
        val image = ui.takeScreenshot()
        val folder = File(rule.activity.getExternalFilesDir(null), "file-editor").apply { mkdirs() }
        val styleSuffix = if (style == AppStyle.Brief) "" else "-${style.name.lowercase()}"
        val fontSuffix = if (rule.activity.resources.configuration.fontScale > 1.5f) "-large" else ""
        try { File(folder, "$name$styleSuffix$fontSuffix.png").outputStream().use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) } }
        finally { image.recycle() }
    }

    private fun assertFirstLineVisible() {
        val (bounds, ink) = rule.runOnIdle {
            val view = editor()
            val location = IntArray(2)
            view.getLocationOnScreen(location)
            val left = location[0] + view.totalPaddingLeft
            val top = location[1] + view.totalPaddingTop
            Rect(left + view.layout.getPrimaryHorizontal(4).toInt(), top,
                minOf(location[0] + view.width, left + view.layout.getLineMax(0).toInt()), top + view.layout.getLineBottom(0)) to view.currentTextColor
        }
        val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
        automation.waitForIdle(250, 2_000)
        val bitmap = automation.takeScreenshot()
        try {
            var darkText = 0
            for (y in bounds.top until bounds.bottom) for (x in bounds.left until bounds.right) {
                val draw = bitmap.getPixel(x, y)
                if (kotlin.math.abs(Color.red(draw) - Color.red(ink)) + kotlin.math.abs(Color.green(draw) - Color.green(ink)) +
                    kotlin.math.abs(Color.blue(draw) - Color.blue(ink)) < 100) darkText++
            }
            assertTrue("First code line has visible ink in $bounds: $darkText pixels", darkText >= 20)
        } finally { bitmap.recycle() }
    }

    @Test fun downloadIsAvailableInTheFileToolbar() {
        render()
        button(R.string.files_editor_download).assertIsDisplayed()
        rule.onNodeWithText(label(R.string.files_insert_path)).assertDoesNotExist()
        capture("preview")
        menu()
        rule.onNodeWithText(label(R.string.files_insert_path)).assertIsDisplayed()
        rule.onNodeWithText(label(R.string.files_share)).assertIsDisplayed()
        tap(R.string.files_editor_show)
        button(R.string.files_preview_options).assertDoesNotExist()
        rule.onNodeWithText("weather.kt").assertIsDisplayed()
    }

    @Test fun lineNumbersSyntaxSearchReplaceUndoAndVersionedSave() {
        render()
        edit()
        val statusBottom = rule.onNodeWithText(label(R.string.files_editor_editing)).fetchSemanticsNode().boundsInWindow.bottom
        rule.runOnIdle {
            assertEquals(3, editor().lineCount)
            val position = IntArray(2)
            editor().getLocationInWindow(position)
            assertTrue("The editor starts below its status at this text size", statusBottom <= position[1] + 1)
            assertTrue(editor().text.getSpans(0, editor().length(), CharacterStyle::class.java).isNotEmpty())
        }
        capture("editor-light")
        assertFirstLineVisible()
        button(R.string.files_editor_find_replace).performClick()
        field("editor-query").performTextInput("Clear")
        field("editor-replacement").performTextInput("Cloudy")
        // Close the system keyboard so every search action is available on compact devices as well.
        rule.runOnIdle { rule.activity.getSystemService(InputMethodManager::class.java)
            .hideSoftInputFromWindow(rule.activity.window.decorView.windowToken, 0) }
        tap(R.string.files_editor_next)
        rule.runOnIdle { assertEquals("Clear", editor().text.subSequence(editor().selectionStart, editor().selectionEnd).toString()) }
        tap(R.string.files_editor_replace_all)
        rule.runOnIdle { assertEquals(2, Regex("Cloudy").findAll(editor().text).count()) }
        button(R.string.files_undo).performClick()
        rule.onNodeWithTag("file-editor-save").assertIsNotEnabled()
        tap(R.string.files_editor_next)
        rule.onAllNodesWithText(label(R.string.files_editor_replace)).filter(hasClickAction()).onLast().performClick()
        capture("find-replace")
        button(R.string.files_editor_save).performClick()
        awaitWrites(1)
        rule.waitUntil(10_000) { machine.reads >= 2 }
        val out = machine.writes.single()
        assertEquals(listOf("fixture", machine.dir, machine.path), listOf(out.machine, out.cwd, out.path))
        assertEquals("first-read", out.revision)
        assertEquals(MessageDigest.getInstance("SHA-256").digest(machine.initial.toByteArray()).joinToString("") { "%02x".format(it) }, out.sha256)
        assertFalse(out.force)
        assertFalse(out.create)
        assertTrue(out.text.startsWith("\uFEFF"))
        assertEquals(2, Regex("\r\n").findAll(out.text).count())
        assertEquals(1, Regex("Cloudy").findAll(out.text).count())
        assertTrue(machine.reads >= 2)
    }

    @Test fun keepDraftThenConfirmOverwriteAgain() {
        render(dark = true)
        changed()
        capture("conflict-dark")
        tap(R.string.files_editor_keep)
        assertEquals(1, machine.writes.size)
        rule.runOnIdle { assertTrue(editor().text.endsWith("// Mine\n")) }
        button(R.string.files_editor_save).performClick()
        tap(R.string.files_editor_overwrite_anyway)
        capture("overwrite-dark")
        tap(R.string.files_editor_cancel)
        assertEquals(2, machine.writes.size)
        button(R.string.files_editor_save).performClick()
        tap(R.string.files_editor_overwrite_anyway)
        assertTrue(machine.writes.none { it.force })
        tap(R.string.files_editor_overwrite)
        awaitWrites(4)
        assertEquals(listOf(false, false, false, true), machine.writes.map { it.force })
        assertTrue(machine.disk.endsWith("// Mine\r\n"))
    }

    @Test fun saveAsCopyKeepsNewerVersionAndTriesAnAvailableName() {
        render()
        changed()
        machine.copyCollides = true
        tap(R.string.files_editor_copy)
        awaitWrites(3)
        rule.waitUntil(10_000) { machine.reads >= 2 }
        assertEquals(listOf(machine.path, machine.dir + "/weather 2.kt", machine.dir + "/weather 3.kt"), machine.writes.map { it.path })
        assertTrue(machine.writes.drop(1).all { it.create && !it.force && it.revision == null && it.sha256 == null })
        assertEquals("fun newer() = true\n", machine.disk)
        assertTrue(machine.reads >= 2)
    }

    @Test fun discardMineOpensNewServerVersion() {
        render()
        changed()
        tap(R.string.files_editor_discard_mine)
        rule.onNodeWithText("fun newer() = true").assertIsDisplayed()
        assertEquals(1, machine.writes.size)
    }

    @Test fun ioFailurePreservesDraftAndBackAsksBeforeLosingIt() {
        render()
        edit()
        machine.failWrite = true
        append("edited")
        button(R.string.files_editor_save).performClick()
        rule.waitUntil(10_000) { rule.onAllNodesWithText("Couldn’t save", substring = true).fetchSemanticsNodes().isNotEmpty() }
        rule.runOnIdle { assertTrue(editor().text.endsWith("edited")) }
        rule.runOnIdle { rule.activity.onBackPressedDispatcher.onBackPressed() }
        tap(R.string.files_editor_keep)
        assertEquals(1, machine.writes.size)
        machine.failWrite = false
        button(R.string.files_editor_save).performClick()
        awaitWrites(2)
        assertEquals(listOf(false, false), machine.writes.map { it.force })
    }

    @Test fun legacyNodeKeepsPreviewAndReadActions() {
        machine.writable = false
        render()
        menu()
        rule.onNodeWithText(label(R.string.files_editor_edit)).assertDoesNotExist()
        rule.onNodeWithText(label(R.string.files_share)).assertIsDisplayed()
        assertTrue(machine.writes.isEmpty())
    }

    @Test fun outsideHomeKeepsDownloadAndDisablesWrites() {
        render(readOnly = true)
        menu()
        rule.onNodeWithText(label(R.string.files_editor_edit)).assertIsNotEnabled()
        assertTrue(machine.writes.isEmpty())
    }

    @Test fun aNextPagePointerAloneDisablesEditingAndExplainsWhy() {
        machine.next = 12_345
        render()
        menu()
        rule.onNodeWithText(label(R.string.files_editor_edit)).assertIsNotEnabled()
        rule.onNodeWithText(label(R.string.files_editor_partial)).assertIsDisplayed()
    }

    @Test fun fileAboveTheOpeningLimitRetainsDownloadAndShowsItsEditLimit() {
        machine.reportedSize = FILE_EDIT_BYTES + 1L
        render()
        menu()
        rule.onNodeWithText(label(R.string.files_editor_edit)).assertIsNotEnabled()
        rule.onNodeWithText(label(R.string.files_editor_too_large)).assertIsDisplayed()
    }

    @Test fun aFullMebibyteOpensAndCanGrowOnSave() {
        machine.disk = ("// " + "x".repeat(1020) + "\n").repeat(1024)
        assertEquals(FILE_EDIT_BYTES, machine.disk.toByteArray().size)
        render()
        edit()
        rule.runOnIdle { assertEquals(FILE_EDIT_BYTES, editor().length()) }
        append("// extra line\n")
        button(R.string.files_editor_save).performClick()
        awaitWrites(1)
        assertTrue(machine.writes.single().text.endsWith("// extra line\n"))
        assertEquals(FILE_EDIT_BYTES + 14, machine.writes.single().text.toByteArray().size)
    }

    @Test fun emptyFileWithOmittedTextGetsItsFirstVersionedWrite() {
        machine.disk = ""
        render()
        edit()
        append("first line\n")
        button(R.string.files_editor_save).performClick()
        awaitWrites(1)
        assertEquals("first line\n", machine.writes.single().text)
        assertEquals(OpenedFileText("").sha256, machine.writes.single().sha256)
    }

    @Test fun downloadingAPictureFromPreviewSavesTheWholeFileToTheGallery() {
        val image = Bitmap.createBitmap(3, 2, Bitmap.Config.ARGB_8888)
        image.eraseColor(android.graphics.Color.BLUE)
        val bytes = ByteArrayOutputStream().use { output -> image.compress(Bitmap.CompressFormat.PNG, 100, output); output.toByteArray() }
        image.recycle()
        assertGalleryDownload("png", "image/png", MediaStore.Images.Media.EXTERNAL_CONTENT_URI, bytes)
    }

    @Test fun downloadingAVideoFromPreviewSavesTheWholeFileInMovies() {
        // One synthetic silent 16 × 16 MPEG-4 frame, generated from FFmpeg's color source.
        val bytes = InstrumentationRegistry.getInstrumentation().context.assets.open("editor-frame.mp4").use { it.readBytes() }
        assertGalleryDownload("mp4", "video/mp4", MediaStore.Video.Media.EXTERNAL_CONTENT_URI, bytes)
    }

    private fun assertGalleryDownload(extension: String, mime: String, collection: Uri, bytes: ByteArray) {
        machine.fileName = "persona-preview-" + UUID.randomUUID() + "." + extension
        machine.binary = true
        machine.modernDownload = true
        machine.mediaType = mime
        machine.bytes = bytes
        render(readOnly = true)
        // A streamed picture may already have been fetched for its preview. Download issues a separate complete read.
        if (mime == "image/png") rule.waitUntil(10_000) { machine.downloads.isNotEmpty() }
        val before = machine.downloads.size
        val resolver = rule.activity.contentResolver
        var id: Long? = null
        try {
            button(R.string.files_editor_download).performClick()
            rule.waitUntil(15_000) {
                id = resolver.query(collection, arrayOf(MediaStore.MediaColumns._ID), MediaStore.MediaColumns.DISPLAY_NAME + " = ?",
                    arrayOf(machine.fileName), null)?.use { if (it.moveToFirst()) it.getLong(0) else null }
                id != null
            }
            assertTrue(machine.downloads.size > before)
            assertArrayEquals(bytes, resolver.openInputStream(ContentUris.withAppendedId(collection, requireNotNull(id)))!!.use { it.readBytes() })
        } finally { id?.let { resolver.delete(ContentUris.withAppendedId(collection, it), null, null) } }
    }

    @Test fun truncatedTextSharesOnlyAfterTheCompleteDownloadAndUsesTheSystemChooser() {
        machine.partial = true
        machine.disk = "Preview excerpt"
        machine.bytes = "Beginning, middle and the end of the complete file.".toByteArray()
        machine.modernDownload = true
        render()
        menu()
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val receipt = AtomicReference<Intent>()
        val monitor = object : Instrumentation.ActivityMonitor() {
            override fun onStartActivity(intent: Intent): Instrumentation.ActivityResult? {
                if (intent.action == Intent.ACTION_CHOOSER) receipt.set(intent)
                return null
            }
        }
        instrumentation.addMonitor(monitor)
        try {
            tap(R.string.files_share)
            rule.waitUntil(10_000) { receipt.get() != null }
            val inner = androidx.core.content.IntentCompat.getParcelableExtra(receipt.get(), Intent.EXTRA_INTENT, Intent::class.java)!!
            val uri = inner.clipData!!.getItemAt(0).uri
            assertTrue(inner.flags and Intent.FLAG_GRANT_READ_URI_PERMISSION != 0)
            assertNull(inner.getStringExtra(Intent.EXTRA_TEXT))
            assertArrayEquals(machine.bytes, rule.activity.contentResolver.openInputStream(uri)!!.use { it.readBytes() })
            rule.waitUntil(10_000) { instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString()?.contains("intentresolver") == true }
            capture("system-share")
        } finally {
            instrumentation.removeMonitor(monitor)
            instrumentation.uiAutomation.performGlobalAction(android.accessibilityservice.AccessibilityService.GLOBAL_ACTION_BACK)
        }
    }

    @Test fun previewDownloadsTextThroughTheSystemSaveDialog() {
        machine.fileName = "persona-preview-" + UUID.randomUUID() + ".txt"
        machine.modernDownload = true
        render()
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val receipt = AtomicReference<Intent>()
        val monitor = object : Instrumentation.ActivityMonitor() {
            override fun onStartActivity(intent: Intent): Instrumentation.ActivityResult? {
                if (intent.action == Intent.ACTION_CREATE_DOCUMENT) receipt.set(intent)
                return null
            }
        }
        instrumentation.addMonitor(monitor)
        try {
            button(R.string.files_editor_download).performClick()
            rule.waitUntil(10_000) { receipt.get() != null }
            assertEquals("text/plain", receipt.get().type)
            assertEquals(machine.fileName, receipt.get().getStringExtra(Intent.EXTRA_TITLE))
            val ui = instrumentation.uiAutomation
            rule.waitUntil(10_000) { ui.rootInActiveWindow?.packageName?.toString()?.contains("documentsui") == true }
            capture("system-save")
            val save = ui.rootInActiveWindow.findAccessibilityNodeInfosByText("Save")
                .first { it.text?.toString()?.equals("Save", ignoreCase = true) == true && it.isClickable }
            assertTrue(save.performAction(AccessibilityNodeInfo.ACTION_CLICK))
            rule.waitUntil(10_000) { rule.activity.savedDocument.get() != null }
            val uri = rule.activity.savedDocument.get()
            rule.waitUntil(10_000) {
                rule.activity.contentResolver.openInputStream(uri)?.use { it.readBytes().contentEquals(machine.disk.toByteArray()) } == true
            }
            assertEquals(listOf(machine.path), machine.downloads)
        } finally {
            instrumentation.removeMonitor(monitor)
            val uri = rule.activity.savedDocument.get()
            if (uri != null) DocumentsContract.deleteDocument(rule.activity.contentResolver, uri)
            else instrumentation.uiAutomation.performGlobalAction(android.accessibilityservice.AccessibilityService.GLOBAL_ACTION_BACK)
        }
    }
}
