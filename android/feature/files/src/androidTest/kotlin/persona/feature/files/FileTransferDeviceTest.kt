package persona.feature.files

import android.content.ContentProvider
import android.content.ContentResolver
import android.content.ContentValues
import android.content.ContentUris
import android.content.Context
import android.content.pm.ProviderInfo
import android.database.Cursor
import android.graphics.Bitmap
import android.net.Uri
import android.os.ParcelFileDescriptor
import android.provider.MediaStore
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.*
import java.io.File
import java.io.FileNotFoundException
import java.io.IOException

class FileTransferDeviceTest {
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext

    @Test fun galleryPublishesTheCompleteImageAndVideoIntoTheirRespectiveCollections() {
        val file = File.createTempFile("synthetic-pixel-", ".png", context.cacheDir)
        try {
            val bitmap = Bitmap.createBitmap(2, 2, Bitmap.Config.ARGB_8888)
            try { file.outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) } } finally { bitmap.recycle() }
            assertPublished(file, "pixel.png", "image/png", MediaStore.Images.Media.EXTERNAL_CONTENT_URI, "Pictures/Persona")
            InstrumentationRegistry.getInstrumentation().context.assets.open("editor-frame.mp4").use { input ->
                file.outputStream().use { input.copyTo(it) }
            }
            assertPublished(file, "clip.mp4", "video/mp4", MediaStore.Video.Media.EXTERNAL_CONTENT_URI, "Movies/Persona")
            assertEquals("video/mp4", fileTransferMime("clip.mp4", null))
            assertEquals("application/octet-stream", fileTransferMime("no-extension", null))
        } finally { file.delete() }
    }

    private fun assertPublished(file: File, suffix: String, mime: String, collection: Uri, folder: String) {
        val resolver = context.contentResolver
        val name = "persona-" + System.nanoTime() + "-" + suffix
        val filter = MediaStore.MediaColumns.DISPLAY_NAME + " = ?"
        try {
            saveGallery(resolver, name, mime, file)
            val columns = arrayOf(MediaStore.MediaColumns._ID, MediaStore.MediaColumns.IS_PENDING, MediaStore.MediaColumns.RELATIVE_PATH)
            resolver.query(collection, columns, filter, arrayOf(name), null)!!.use { cursor ->
                assertTrue(cursor.moveToFirst())
                assertEquals(0, cursor.getInt(1))
                assertTrue(cursor.getString(2).startsWith(folder))
                val uri = ContentUris.withAppendedId(collection, cursor.getLong(0))
                resolver.openInputStream(uri)!!.use { assertArrayEquals(file.readBytes(), it.readBytes()) }
            }
        } finally { resolver.delete(collection, filter, arrayOf(name)) }
    }

    @Test fun refusingTheGalleryPublicationRemovesOnlyItsPendingRowAndReturnsFailure() {
        val file = File.createTempFile("pending-gallery-", ".mp4", context.cacheDir)
        val outputFile = File.createTempFile("pending-row-", ".tmp", context.cacheDir)
        val rows = mutableListOf<String>()
        val provider = object : ContentProvider() {
            override fun onCreate() = true
            override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
            override fun getType(uri: Uri) = "video/mp4"
            override fun insert(uri: Uri, values: ContentValues?): Uri {
                assertEquals(MediaStore.Video.Media.EXTERNAL_CONTENT_URI, uri)
                assertEquals(1, values!!.getAsInteger(MediaStore.MediaColumns.IS_PENDING))
                rows += "pending"
                return ContentUris.withAppendedId(uri, 123)
            }
            override fun openFile(uri: Uri, mode: String) = ParcelFileDescriptor.open(outputFile, ParcelFileDescriptor.parseMode(mode))
            override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int {
                assertEquals(0, values!!.getAsInteger(MediaStore.MediaColumns.IS_PENDING))
                rows += "refused"
                return 0
            }
            override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int {
                assertEquals(123L, ContentUris.parseId(uri))
                rows += "deleted"
                return 1
            }
        }
        provider.attachInfo(context, ProviderInfo().apply { authority = MediaStore.AUTHORITY })
        try {
            file.writeText("tiny fixture")
            try {
                saveGallery(ContentResolver.wrap(provider), "fixture.mp4", "video/mp4", file)
                fail("expected rejected gallery publication")
            } catch (_: IOException) { }
            assertEquals(listOf("pending", "refused", "deleted"), rows)
            assertEquals(file.readText(), outputFile.readText())
        } finally { file.delete(); outputFile.delete() }
    }

    @Test fun completeLegacyTextIncludingAnEmptyFileCanBeSavedButAPartialPageCannot() = runBlocking {
        var file = SessionFileContent("a", "a", size = 0, mime = "text/plain; charset=utf-8")
        val computer = object : SessionFiles by EditorTestMachine() {
            override fun supports(machineId: String, feature: SessionFileFeature) = false
            override suspend fun file(machineId: String, cwd: String, path: String) = file
        }
        val controller = FilesController(computer, "lab", "/", CoroutineScope(Dispatchers.Main))
        val temp = File.createTempFile("legacy-read-", ".part", context.cacheDir)
        try {
            downloadComplete(controller, "a", temp)
            assertEquals(0, temp.length())
            file = file.copy(text = "Ω", size = 2)
            downloadComplete(controller, "a", temp)
            assertEquals("Ω", temp.readText())
            for (partial in listOf(file.copy(next = 2), file.copy(truncated = true), file.copy(size = 9))) {
                file = partial
                try { downloadComplete(controller, "a", temp); fail("expected incomplete download") }
                catch (_: UnsupportedOperationException) { }
            }
            try { saveDocument(context.contentResolver, Uri.parse("content://persona.synthetic.missing/document/404"), temp); fail("expected provider refusal") }
            catch (_: FileNotFoundException) { }
        } finally { temp.delete() }
    }
}
