package persona.feature.files

import android.app.Activity
import android.content.ContentResolver
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Environment
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import persona.core.contracts.api.SessionFileFeature
import persona.core.contracts.api.hubCodedFailure
import persona.core.designsystem.JDialog
import persona.core.designsystem.FeedbackStyle
import persona.core.designsystem.LocalAppFeedback
import java.io.File
import java.io.IOException
import java.util.Base64

private const val MAX_UPLOAD = 2L shl 30 // proto.MaxDirUploadBytes

internal fun fileTransferMime(name: String, announced: String?): String {
    val mime = announced?.substringBefore(';')?.trim()?.lowercase()
    return mime?.takeIf { '/' in it && it != "application/octet-stream" }
        ?: MimeTypeMap.getSingleton().getMimeTypeFromExtension(FilesPaths.extension(name)) ?: "application/octet-stream"
}

private fun downloadCache(context: Context): File = File(context.cacheDir, "file-downloads").apply { mkdirs() }

/** Download or reconstruct an older hub's complete preview. A partial preview can never become a saved document. */
internal suspend fun downloadComplete(controller: FilesController, path: String, into: File): String? {
    if (controller.api.supports(controller.machineId, SessionFileFeature.Download)) {
        try {
            val count = controller.download(path, into)
            if (count != into.length()) throw IOException()
            return null
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (error: Exception) {
            val failure = hubCodedFailure(error)
            if (error !is UnsupportedOperationException && failure?.code != "node_outdated" &&
                !(failure?.status == 404 && failure.code == null)) throw error
        }
    }
    val file = controller.api.file(controller.machineId, controller.cwd, path)
    if (file.truncated || file.next > 0 || file.stream || file.isDir) throw UnsupportedOperationException()
    val bytes = file.data?.let { Base64.getMimeDecoder().decode(it) } ?: editableText(file)?.toByteArray() ?: throw UnsupportedOperationException()
    if (bytes.size.toLong() != file.size) throw UnsupportedOperationException()
    into.writeBytes(bytes)
    return file.mime
}

internal fun saveDocument(resolver: ContentResolver, destination: Uri, source: File) {
    val output = resolver.openOutputStream(destination, "wt") ?: throw IOException()
    output.use { out -> source.inputStream().use { it.copyTo(out) } }
}

/** The gallery row becomes visible only after all bytes were written and the provider acknowledged publication. */
internal fun saveGallery(resolver: ContentResolver, name: String, mime: String, source: File) {
    val video = mime.startsWith("video/")
    val collection = if (video) MediaStore.Video.Media.EXTERNAL_CONTENT_URI else MediaStore.Images.Media.EXTERNAL_CONTENT_URI
    val values = ContentValues().apply {
        put(MediaStore.MediaColumns.DISPLAY_NAME, name)
        put(MediaStore.MediaColumns.MIME_TYPE, mime)
        val base = if (video) Environment.DIRECTORY_MOVIES else Environment.DIRECTORY_PICTURES
        put(MediaStore.MediaColumns.RELATIVE_PATH, "$base/Persona")
        put(MediaStore.MediaColumns.IS_PENDING, 1)
    }
    val uri = resolver.insert(collection, values) ?: throw IOException()
    try {
        saveDocument(resolver, uri, source)
        values.clear()
        values.put(MediaStore.MediaColumns.IS_PENDING, 0)
        if (resolver.update(uri, values, null, null) != 1) throw IOException()
    } catch (error: Exception) {
        runCatching { resolver.delete(uri, null, null) }
        throw error
    }
}

internal data class FileTransferWork(val label: String, val job: Job)

/** Expose slow requests without flashing a progress dialog for fast operations. Cancelling cancels the HTTP call. */
@Composable
internal fun FileTransferProgress(work: FileTransferWork?) {
    var shown by remember(work) { mutableStateOf(false) }
    LaunchedEffect(work) { if (work != null) { delay(1_000); shown = true } }
    if (work != null && shown) JDialog(onDismiss = { work.job.cancel() }, title = work.label) {
        androidx.compose.material3.CircularProgressIndicator()
    }
}

/** Shared by the preview toolbar and browser menus. SAF can return after an activity recreation. */
@Composable
internal fun rememberFileDownload(controller: FilesController): (path: String, name: String, mime: String?) -> Unit {
    val context = LocalContext.current
    val resources = LocalResources.current
    val feedback = LocalAppFeedback.current
    val scope = rememberCoroutineScope()
    var waiting by rememberSaveable(controller.machineId, controller.cwd) { mutableStateOf<String?>(null) }
    var busy by remember(controller) { mutableStateOf(false) }
    var progress by remember(controller) { mutableStateOf<FileTransferWork?>(null) }
    FileTransferProgress(progress)
    fun report(error: Exception) {
        val message = when {
            error is UnsupportedOperationException -> resources.getString(R.string.files_action_download_old)
            hubCodedFailure(error) != null -> editRefusalText(resources, editRefusal(error, null))
            else -> resources.getString(R.string.files_action_save_failed)
        }
        feedback?.notice(message, FeedbackStyle.Error)
    }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        val temp = waiting?.let { File(downloadCache(context), it) }
        waiting = null
        if (temp != null) {
            val uri = result.data?.data
            if (result.resultCode == Activity.RESULT_OK && uri != null) {
                busy = true
                scope.launch {
                    try {
                        withContext(Dispatchers.IO) { saveDocument(context.contentResolver, uri, temp) }
                        feedback?.notice(resources.getString(R.string.files_action_saved), FeedbackStyle.Success)
                    } catch (cancelled: CancellationException) { throw cancelled }
                    catch (error: Exception) { report(error) }
                    finally { temp.delete(); busy = false }
                }
            } else {
                temp.delete()
                if (result.resultCode == Activity.RESULT_OK) report(IOException())
            }
        }
    }
    return { path, name, type ->
        if (busy || waiting != null) {
            feedback?.notice(resources.getString(R.string.files_action_saving))
        } else {
            busy = true
            scope.launch {
                var temp: File? = null
                try {
                    val folder = downloadCache(context)
                    // A document picker abandoned by process death can leave one private staging file.
                    val stale = System.currentTimeMillis() - 7L * 24 * 60 * 60 * 1000
                    folder.listFiles()?.filter { it.lastModified() < stale }?.forEach { it.delete() }
                    val staged = File.createTempFile("download-", ".part", folder).also { temp = it }
                    progress = FileTransferWork(resources.getString(R.string.files_action_downloading), currentCoroutineContext().job)
                    val previewMime = withContext(Dispatchers.IO) { downloadComplete(controller, path, staged) }
                    var mime = fileTransferMime(name, type ?: previewMime)
                    if (mime == "application/octet-stream" && controller.api.supports(controller.machineId, SessionFileFeature.Info)) {
                        try { mime = fileTransferMime(name, controller.api.fileInfo(controller.machineId, controller.cwd, path).mime) }
                        catch (cancelled: CancellationException) { throw cancelled }
                        catch (_: Exception) { /* The download is complete; a provider can still save it as a document. */ }
                    }
                    progress = null
                    if (mime.startsWith("image/") || mime.startsWith("video/")) {
                        withContext(Dispatchers.IO) { saveGallery(context.contentResolver, name, mime, staged) }
                        feedback?.notice(resources.getString(R.string.files_action_gallery_saved), FeedbackStyle.Success)
                    } else {
                        waiting = staged.name
                        picker.launch(Intent(Intent.ACTION_CREATE_DOCUMENT).apply {
                            addCategory(Intent.CATEGORY_OPENABLE)
                            this.type = mime
                            putExtra(Intent.EXTRA_TITLE, name)
                        })
                        temp = null // Picker owns cleanup from here.
                    }
                } catch (cancelled: CancellationException) { throw cancelled }
                catch (error: Exception) { waiting = null; report(error) }
                finally { progress = null; temp?.delete(); busy = false }
            }
        }
    }
}

internal class UploadTooLarge : IOException()
internal class UploadDocumentUnreadable : IOException()

/** A SAF provider can omit the length. Copy once and reuse the same private bytes for a chosen conflict retry. */
internal suspend fun <T> withUploadDocument(context: Context, document: Uri, send: suspend (String, File) -> T): T =
    withContext(Dispatchers.IO) {
        val resolver = context.contentResolver
        val columns = arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)
        val (name, length) = resolver.query(document, columns, null, null, null)?.use { cursor ->
            if (!cursor.moveToFirst()) return@use null
            val ni = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
            val si = cursor.getColumnIndex(OpenableColumns.SIZE)
            val name = if (ni >= 0 && !cursor.isNull(ni)) cursor.getString(ni) else null
            name?.let { it to if (si >= 0 && !cursor.isNull(si)) cursor.getLong(si) else null }
        } ?: throw UploadDocumentUnreadable()
        if (nameProblem(name) != null || '\\' in name) throw UploadDocumentUnreadable()
        if (length != null && length > MAX_UPLOAD) throw UploadTooLarge()
        val temp = File.createTempFile("file-upload-", ".part", context.cacheDir)
        try {
            val stream = resolver.openInputStream(document) ?: throw UploadDocumentUnreadable()
            stream.use { input -> temp.outputStream().use { output ->
                val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
                var total = 0L
                while (true) {
                    currentCoroutineContext().ensureActive()
                    val n = input.read(buffer)
                    if (n < 0) break
                    total += n
                    if (total > MAX_UPLOAD) throw UploadTooLarge()
                    output.write(buffer, 0, n)
                }
            } }
            if (length != null && length >= 0 && temp.length() != length) throw UploadDocumentUnreadable()
            send(name, temp)
        } finally { temp.delete() }
    }
