package persona.feature.files

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.*
import persona.core.hub.HubError
import persona.core.hub.HubFailure

class FileBrowserOperationsTest {
    private val root = "/home/fixture/Project"
    private fun target(name: String, dir: Boolean = false) = FileBrowserTarget("$root/$name", SessionFileEntry(name, dir))

    @Test fun archivesKeepDirectoryAndHiddenFileNamesAndDistinguishCompressedFiles() {
        val file = listOf(target("test notes.txt"))
        assertEquals("test notes.zip", archiveName(file, "Archive.zip", false))
        assertEquals("test notes.txt.zip", archiveName(file, "Archive.zip", true))
        assertEquals("draft.v1.zip", archiveName(listOf(target("draft.v1", true)), "Archive.zip", false))
        assertEquals(".notes.zip", archiveName(listOf(target(".notes")), "Archive.zip", false))
        assertEquals("Archive.zip", archiveName(file + target("More"), "Archive.zip", true))
    }

    @Test fun onlyAnAcknowledgedCollisionCanReplaceAndSkippingDoesNotSend() = runTest {
        val taken = HubFailure(HubError.Server, status = 409, code = "exists", body = """{"name":"remote.txt"}""")
        val attempts = mutableListOf<SessionFileConflict?>()
        var shown = ""
        suspend fun copy(choice: SessionFileConflict?): String {
            attempts += choice
            if (choice == null) throw taken
            return "copied"
        }
        assertEquals("copied", onFileConflict("local.txt", { shown = it; SessionFileConflict.Replace }, ::copy))
        assertEquals("remote.txt", shown)
        assertEquals(listOf(null, SessionFileConflict.Replace), attempts)
        attempts.clear()
        assertNull(onFileConflict("local.txt", { null }, ::copy))
        assertEquals(listOf(null), attempts)
    }

    @Test fun permissionErrorsAndCancellationAreNeverRetriedAsConflicts() = runTest {
        val errors = listOf(HubFailure(HubError.Server, status = 409, code = "read_only"), CancellationException("left folder"))
        for (failure in errors) {
            try {
                onFileConflict<Unit>("file", { error("unexpected prompt") }) { throw failure }
                fail("expected original failure")
            } catch (error: Exception) { assertSame(failure, error) }
        }
    }
}
