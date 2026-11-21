package dev.persona.watch

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class HubDeletionFenceTest {
    @get:Rule val folder = TemporaryFolder()
    private val id = "11111111-1111-1111-1111-111111111111"
    private val other = "22222222-2222-2222-2222-222222222222"
    private val config = HubConfig("http://hub.local", "sample-issued-token")

    @Test fun anotherProcessWithAnOrphanEnvelopeCannotReuseARemovedConnection() {
        val app = HubDeletionFence(folder.root)
        val provider = HubDeletionFence(folder.root)
        val envelope = File(folder.root, "group_$id.enc").apply { writeText("sample encrypted envelope") }
        var loads = 0
        val load = { loads++; config }
        assertEquals(config, provider.credential(id, load))
        app.mark(id) // Directory/credential cleanup may fail after this durable write.
        assertTrue(envelope.exists())
        assertNull(provider.credential(id, load))
        assertNull(HubDeletionFence(folder.root).credential(id, load))
        assertEquals(1, loads)
        assertEquals(config, provider.credential(other, load))
        assertEquals(setOf(id), provider.ids())
    }

    @Test fun removalDuringCredentialReadRejectsTheJustReadEnvelope() {
        val app = HubDeletionFence(folder.root)
        val provider = HubDeletionFence(folder.root)
        assertNull(provider.credential(id) { app.mark(id); config })
    }

    @Test fun invalidIdsNeverReachCredentialStorageOrEscapeTheDirectory() {
        val fence = HubDeletionFence(folder.root)
        for (invalid in listOf("", "primary", "../connection", "../" + "a".repeat(36))) {
            assertNull(fence.credential(invalid) { throw AssertionError("Invalid ID reached storage") })
            assertThrows(IllegalArgumentException::class.java) { fence.mark(invalid) }
        }
        assertTrue(folder.root.listFiles().orEmpty().isEmpty())
    }
}
