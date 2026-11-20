package persona.feature.files

import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.*
import java.io.IOException

class FileEditorDocumentTest {
    private val file = SessionFileContent("/home/me/cases/a.kt", "a.kt", size = 4, text = "text", revision = "r1")

    @Test fun onlyCompleteTextUpToOneMebibyteCanOpen() {
        assertNull(fileEditProblem(file.copy(size = FILE_EDIT_BYTES.toLong())))
        assertEquals(R.string.files_editor_too_large, fileEditProblem(file.copy(size = FILE_EDIT_BYTES + 1L)))
        assertEquals(R.string.files_editor_partial, fileEditProblem(file.copy(truncated = true)))
        assertEquals(R.string.files_editor_partial, fileEditProblem(file.copy(next = 2_147_483_655L)))
        assertEquals(R.string.files_editor_not_text, fileEditProblem(file.copy(text = null, data = "YWJj")))
        assertEquals(R.string.files_editor_not_text, fileEditProblem(file.copy(isDir = true)))
    }

    @Test fun emptyTextOmittedByTheNodeCanOpenDirectlyFromNewTextFile() {
        val empty = file.copy(size = 0, text = null, mime = "text/plain; charset=utf-8")
        assertNull(fileEditProblem(empty))
        assertEquals("", editableText(empty))
        assertEquals("", editableText(empty.copy(mime = "application/json")))
        assertNull(editableText(empty.copy(mime = "image/png", stream = true)))
        assertNull(editableText(empty.copy(mime = "application/octet-stream")))
    }

    @Test fun bomAndConsistentLineEndingsRoundTripThroughTheEditor() {
        val opened = OpenedFileText("\uFEFFOne\r\nTwo\r\n")
        assertEquals("One\nTwo\n", opened.body)
        assertEquals("\uFEFFOne\r\nTwo!\r\n", opened.encoded("One\nTwo!\n"))
        val mixed = OpenedFileText("One\r\nTwo\nThree\r")
        assertEquals("One\r\nTwo\nThree\r", mixed.encoded(mixed.body))
        assertEquals("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", OpenedFileText("").sha256)
    }

    private class Failure(override val status: Int, override val code: String) : IOException(), HubCodedFailure

    @Test fun oldHubReceivesTheDigestOfItsOriginalBytesWithoutARevision() = runTest {
        var saved = false
        val api = object : SessionFiles by EditMachine() {
            override suspend fun writeText(machineId: String, cwd: String, path: String, text: String, create: Boolean,
                revision: String?, sha256: String?, force: Boolean): SessionWrittenFile {
                assertEquals(listOf("pair/host", "/session", file.path), listOf(machineId, cwd, path))
                assertNull(revision)
                assertFalse(force)
                assertFalse(create)
                assertEquals("changed", text)
                assertEquals(OpenedFileText(file.text!!).sha256, sha256)
                saved = true
                return SessionWrittenFile(path, text.length.toLong(), "r2")
            }
        }
        FileEditorDocument(file.copy(revision = null), api, "pair/host", "/session").save("changed")
        assertTrue(saved)
    }

    @Test fun aWindowsCopyStaysBesideTheOpenFileAndStopsOnARealFailure() = runTest {
        val tried = mutableListOf<String>()
        val api = object : SessionFiles by EditMachine() {
            override suspend fun writeText(machineId: String, cwd: String, path: String, text: String, create: Boolean,
                revision: String?, sha256: String?, force: Boolean): SessionWrittenFile {
                assertTrue(create)
                assertFalse(force)
                tried += path
                throw if (tried.size == 1) IOException("wrapped", Failure(409, "exists")) else Failure(403, "denied")
            }
        }
        val doc = FileEditorDocument(file.copy(path = "C:\\users\\ann\\other\\.notes", name = ".notes"), api, "m", "C:\\session")
        try { doc.saveCopy("mine"); fail("403 should reach the editor") } catch (e: Failure) { assertEquals(403, e.status) }
        assertEquals(listOf("C:\\users\\ann\\other\\.notes 2", "C:\\users\\ann\\other\\.notes 3"), tried)
    }

    @Test fun savingACopyNeverOverwritesAnExistingSibling() = runTest {
        var attempts = 0
        val api = object : SessionFiles by EditMachine() {
            override suspend fun writeText(machineId: String, cwd: String, path: String, text: String, create: Boolean,
                revision: String?, sha256: String?, force: Boolean): SessionWrittenFile {
                assertTrue(create)
                assertNull(revision)
                assertNull(sha256)
                assertFalse(force)
                attempts++
                throw Failure(409, "exists")
            }
        }
        assertNull(FileEditorDocument(file, api, "m", "/").saveCopy("mine"))
        assertEquals(98, attempts)
    }
}
