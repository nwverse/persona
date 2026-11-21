package dev.persona.watch

import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class CredentialCheckScopeTest {
    @get:Rule val temporary = TemporaryFolder()
    private val id = "11111111-1111-1111-1111-111111111111"
    private val otherId = "22222222-2222-2222-2222-222222222222"
    private val alias = "persona.watch.fixture.loss.$id"
    private val folderName = "credential-loss-$id"

    @Test fun exactIsolatedAliasAndCanonicalChildResolveOnlyTheDisposableEnvelope() {
        val root = temporary.newFolder()
        val production = File(root, "connection.enc").apply { writeText("sample production envelope") }
        val directory = File(root, folderName).apply { check(mkdir()) }
        val target = credentialCheckFile(root, File(root, "./$folderName"), alias)
        assertEquals(File(directory.canonicalFile, "connection.enc"), target)
        assertNotEquals(production.canonicalFile, target)
        assertFalse(target.exists()) // Validation must not create or overwrite either envelope.
        assertEquals("sample production envelope", production.readText())
    }

    @Test fun productionAndMalformedAliasesCannotSelectTheIsolatedStore() {
        val root = temporary.newFolder()
        val production = File(root, "connection.enc").apply { writeText("sample production envelope") }
        val directory = File(root, folderName).apply { check(mkdir()) }
        for (invalid in listOf("persona.watch.connection.v1", "connection", "group_$id", "",
            "persona.watch.fixture.loss.${id.dropLast(1)}", "persona.watch.fixture.loss.${id.replace('1', 'z')}",
            "$alias/../connection", "persona.watch.fixture.loss.$otherId")) {
            assertThrows(IllegalArgumentException::class.java) { credentialCheckFile(root, directory, invalid) }
            assertEquals("sample production envelope", production.readText())
            assertTrue(directory.listFiles().orEmpty().isEmpty())
        }
    }

    @Test fun matchingAliasCannotEscapeToRootSiblingNestedOrMismatchedDirectories() {
        val root = temporary.newFolder("private")
        val production = File(root, "connection.enc").apply { writeText("sample production envelope") }
        val sibling = temporary.newFolder(folderName)
        val nested = File(root, "nested/$folderName").apply { check(mkdirs()) }
        val different = File(root, "credential-loss-$otherId").apply { check(mkdir()) }
        for (invalid in listOf(root, sibling, nested, different, File(root, "../$folderName"))) {
            assertThrows(IllegalArgumentException::class.java) { credentialCheckFile(root, invalid, alias) }
            assertEquals("sample production envelope", production.readText())
        }
        assertTrue(sibling.listFiles().orEmpty().isEmpty())
        assertTrue(nested.listFiles().orEmpty().isEmpty())
        assertTrue(different.listFiles().orEmpty().isEmpty())
    }

    @Test fun missingOrNonDirectoryScopeIsRejectedWithoutCreatingFiles() {
        val root = temporary.newFolder()
        val candidate = File(root, folderName)
        assertThrows(IllegalArgumentException::class.java) { credentialCheckFile(root, candidate, alias) }
        assertFalse(candidate.exists())
        candidate.writeText("sample regular file")
        assertThrows(IllegalArgumentException::class.java) { credentialCheckFile(root, candidate, alias) }
        assertEquals("sample regular file", candidate.readText())
    }
}
