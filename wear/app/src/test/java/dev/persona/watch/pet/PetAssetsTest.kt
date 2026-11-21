package dev.persona.watch.pet

import dev.persona.watch.HubGroupRead
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.WatchSession
import dev.persona.watch.data.WatchSnapshot
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException
import java.util.Base64

class PetAssetsTest {
    @get:Rule val temporary = TemporaryFolder()
    private val config = HubConfig("https://hub.example", "sample-token")
    private val now = 10_000_000L
    private val saved = PetStoredStatus(PetStatus(PetState.WORKING, 1), now - 100)
    private val png = Base64.getDecoder().decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6qO8AAAAASUVORK5CYII=")

    private fun seed(pack: PetPack): PetManifest {
        val clips = JSONObject(); val faces = JSONObject()
        fun asset(name: String) = JSONObject().put("file", "pet/$name.png").put("sha256", PetPack.hash(png))
        for (name in PetState.entries.map { it.wire }) faces.put(name, asset(name))
        for (name in PetState.entries.map { it.wire } + "tap")
            clips.put(name, asset(name).put("fps", 1).put("frames", 1).put("still", 0))
        val json = JSONObject().put("format", 1).put("clips", clips).put("faces", faces).toString()
        val manifest = PetManifest.parse(json)
        pack.directory.mkdirs()
        pack.file(manifest.assets.first()).writeBytes(png)
        File(pack.directory, "pet.json").writeText(json)
        return checkNotNull(pack.cached())
    }

    private fun fresh(date: Long = now): HubGroupRead {
        val snapshot = WatchSnapshot(listOf(WatchSession("sample", "session", "Sample", "needs_you")),
            emptyList(), emptyList(), emptyList(), date)
        return HubGroupRead(snapshot, mapOf("" to snapshot), emptySet())
    }

    @Test fun failedAssetRefreshRetainsVerifiedFilesAndCanPublishCurrentTaskState() {
        val pack = PetPack(temporary.newFolder(), config)
        val original = seed(pack)
        val failure = IOException("Sample asset failure")
        val assets = readPetAssets(true, cached = { pack.cached() }, fetch = { throw failure })
        assertEquals(original, assets.manifest)
        assertSame(failure, assets.failure)
        val page = PetPageState()
        page.resume(config, saved, now)
        page.acceptRefresh(page.generation, assets.manifest, fresh(), 1, now)
        assertEquals(PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 1), now), page.publication())
    }

    @Test fun deletedOrCorruptPackCannotPublishFreshTasksButNextPollCanRecoverAssets() {
        for (damage in listOf("manifest", "asset", "corrupt")) {
            val pack = PetPack(temporary.newFolder(), config)
            val original = seed(pack)
            val page = PetPageState()
            page.resume(config, saved, now)
            page.acceptManifest(page.generation, original)
            when (damage) {
                "manifest" -> assertTrue(File(pack.directory, "pet.json").delete())
                "asset" -> assertTrue(pack.file(original.assets.first()).delete())
                else -> pack.file(original.assets.first()).writeBytes(png.copyOf().apply { this[0] = 0 })
            }
            var attempts = 0
            val missing = readPetAssets(false, cached = { pack.cached() }, fetch = { attempts++; throw IOException("Sample outage") })
            assertNull(missing.manifest)
            page.acceptRefresh(page.generation, missing.manifest, fresh(), 1, now)
            assertNull(page.manifest)
            assertEquals(saved, page.stored)
            assertNull(page.publication())
            page.revalidate(now + 1)
            assertEquals(saved, page.stored) // A timer wake cannot adopt the withheld successful Hub read.
            val recovered = readPetAssets(false, cached = { pack.cached() }, fetch = { attempts++; seed(pack) })
            assertEquals(2, attempts)
            assertNull(recovered.failure)
            page.acceptRefresh(page.generation, recovered.manifest, fresh(now + 2), 1, now + 2)
            assertEquals(PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 1), now + 2), page.publication())
        }
    }

    @Test fun fallbackRechecksFilesAfterTheFailedFetchInsteadOfTrustingItsEarlierManifest() {
        val pack = PetPack(temporary.newFolder(), config)
        val original = seed(pack)
        val assets = readPetAssets(true, cached = { pack.cached() }, fetch = {
            assertTrue(pack.file(original.assets.first()).delete())
            throw IOException("Sample eviction during fetch")
        })
        assertNull(assets.manifest)
        assertNotNull(assets.failure)
        val page = PetPageState()
        val empty = PetStoredStatus(PetStatus(PetState.OFFLINE), 0)
        page.resume(config, empty, now)
        page.acceptRefresh(page.generation, assets.manifest, fresh(), 1, now)
        assertEquals(empty, page.stored)
        assertNull(page.publication())
    }

    @Test fun usableCachedAssetsAvoidFetchAndMissingAssetsDoNotExtendPriorExpiry() {
        val pack = PetPack(temporary.newFolder(), config)
        val original = seed(pack)
        assertEquals(original, readPetAssets(false, cached = { pack.cached() },
            fetch = { throw AssertionError("A usable cache needs no fetch") }).manifest)
        val page = PetPageState()
        page.resume(config, saved, now)
        val late = saved.updatedAt + PetStoredStatus.STALE_MS
        page.acceptRefresh(page.generation, null, fresh(late), 1, late)
        assertEquals(PetState.OFFLINE, page.stored.status.state)
        assertEquals(saved.updatedAt, page.stored.updatedAt)
        assertNull(page.publication())
    }
}
