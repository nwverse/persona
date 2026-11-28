package dev.persona.watch.pet

import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubException
import dev.persona.watch.data.WatchInteraction
import dev.persona.watch.data.WatchSession
import dev.persona.watch.data.WatchSnapshot
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import java.io.File

class PetModelsTest {
    private fun snapshot(sessions: List<WatchSession> = emptyList(), cards: List<WatchInteraction> = emptyList()) =
        WatchSnapshot(sessions, cards, emptyList(), emptyList(), 1000)
    private fun session(id: String, state: String, group: String = "") = WatchSession("m", id, "Sample", state, groupId = group)
    private fun card(id: String, session: String, group: String = "") =
        WatchInteraction(id, "m", session, "question", "Sample", "Sample", groupId = group)

    @Test fun offlineOverridesOldPendingTasksButPartialFailurePreservesKnownWork() {
        val waiting = snapshot(listOf(session("a", "working")), listOf(card("q", "b")))
        assertEquals(PetStatus(PetState.OFFLINE), PetStatus.from(waiting, true, 12))
        assertEquals(PetStatus(PetState.NEEDS_YOU, 1), PetStatus.from(waiting, false, 12, true))
        assertEquals(PetStatus(PetState.WORKING, 1), PetStatus.from(snapshot(listOf(session("a", "working"))), false, 12, true))
        assertEquals(PetStatus(PetState.OFFLINE), PetStatus.from(snapshot(listOf(session("a", "unread"))), false, 12, true))
    }

    @Test fun waitingCountsSessionsOnceAndKeepsDifferentConnectionsDistinct() {
        val result = PetStatus.from(snapshot(listOf(session("a", "needs_you"), session("a", "needs_you", "other")),
            listOf(card("1", "a"), card("2", "a"), card("3", "a", "other"))), false, 12)
        assertEquals(PetStatus(PetState.NEEDS_YOU, 2), result)
    }

    @Test fun mutedArchivedAndQuietStateDoNotMakeThePetBusy() {
        val muted = card("q", "x").copy(muted = true)
        val delegated = card("r", "y").copy(delegating = true)
        val result = PetStatus.from(snapshot(listOf(session("a", "working").copy(quiet = true),
            session("b", "unread").copy(archived = true)), listOf(muted, delegated)), false, 12)
        assertEquals(PetState.IDLE, result.state)
    }

    @Test fun workingPrecedesUnreadAndNightUsesTheSameBoundariesAsAppleWatch() {
        assertEquals(PetStatus(PetState.WORKING, 1), PetStatus.from(snapshot(listOf(session("a", "unread"), session("b", "working"))), false, 12))
        assertEquals(PetStatus(PetState.DONE, 1), PetStatus.from(snapshot(listOf(session("a", "unread"))), false, 23))
        for (hour in listOf(0, 6, 23)) assertEquals(PetState.SLEEP, PetStatus.from(snapshot(), false, hour).state)
        for (hour in listOf(7, 12, 22)) assertEquals(PetState.IDLE, PetStatus.from(snapshot(), false, hour).state)
    }

    @Test fun staleOrFutureWidgetTimestampNeverLooksIdle() {
        val now = 10_000_000L
        val fresh = PetStoredStatus(PetStatus(PetState.IDLE), now)
        assertEquals(PetState.IDLE, fresh.visible(now + PetStoredStatus.STALE_MS - 1).state)
        assertEquals(PetState.OFFLINE, fresh.visible(now + PetStoredStatus.STALE_MS).state)
        assertEquals(PetState.OFFLINE, fresh.visible(now + PetStoredStatus.STALE_MS + 1).state)
        assertEquals(PetState.OFFLINE, fresh.visible(now - 60_001).state)
        assertEquals(PetState.OFFLINE, fresh.copy(updatedAt = 0).visible(now).state)
    }

    @Test fun providerRefreshDoesNotExtendTheOriginalExpiry() {
        val now = 10_000_000L
        val stored = PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 3), now - PetStoredStatus.STALE_MS + 5_000)
        assertEquals(now + 5_000, stored.freshUntil(now))
        assertEquals(now + 5_000, stored.freshUntil(now + 4_999))
        assertEquals(PetStatus(PetState.NEEDS_YOU, 3), stored.visible(now + 4_999))
        assertNull(stored.freshUntil(now + 5_000))
        assertEquals(PetStatus(PetState.OFFLINE), stored.visible(now + 5_000))
        assertEquals(now + 4_000 + PetStoredStatus.STALE_MS, stored.copy(updatedAt = now + 4_000).freshUntil(now + 4_000))
    }

    @Test fun futureAndInvalidTimestampsCannotScheduleAnIdleTimeline() {
        val now = 10_000_000L
        val stored = PetStoredStatus(PetStatus(PetState.IDLE), now + 60_000)
        assertEquals(PetState.IDLE, stored.visible(now).state)
        assertEquals(now + 60_000 + PetStoredStatus.STALE_MS, stored.freshUntil(now))
        for (timestamp in listOf(-1L, 0L, now + 60_001, Long.MAX_VALUE)) {
            val invalid = stored.copy(updatedAt = timestamp)
            assertNull(invalid.freshUntil(now))
            assertEquals(PetState.OFFLINE, invalid.visible(now).state)
        }
        assertNull(stored.freshUntil(-1))
    }

    @Test fun actualUpstreamPackageIsCompleteAndHashVerified() {
        val directory = upstreamDirectory()
        val manifest = PetManifest.parse(File(directory, "pet.json").readText())
        assertEquals(7, manifest.clips.size)
        assertEquals(6, manifest.faces.size)
        manifest.assets.forEach { asset ->
            val bytes = File(directory.parentFile, asset.file).readBytes()
            assertEquals(asset.file, asset.hash, PetPack.hash(bytes))
        }
    }

    @Test fun incompleteOrPathEscapingManifestIsRejected() {
        fun source() = JSONObject(File(upstreamDirectory(), "pet.json").readText())
        val missing = source().apply { getJSONObject("faces").remove("offline") }
        val traversal = source().apply { getJSONObject("clips").getJSONObject("tap").put("file", "pet/../private.png") }
        val oversized = source().apply { getJSONObject("clips").getJSONObject("tap").put("frames", 121) }
        val badStill = source().apply { getJSONObject("clips").getJSONObject("tap").put("still", 100) }
        val badHash = source().apply { getJSONObject("faces").getJSONObject("idle").put("sha256", "../../secret") }
        for (invalid in listOf(missing, traversal, oversized, badStill, badHash))
            assertThrows(HubException::class.java) { PetManifest.parse(invalid.toString()) }
    }

    @Test fun cacheIsScopedToOriginAndPinnedPublicKey() {
        val a = HubConfig("http://127.0.0.1:10001", "sample")
        assertNotEquals(PetPack.scope(a), PetPack.scope(a.copy(baseUrl = "http://127.0.0.1:10002")))
        assertNotEquals(PetPack.scope(a), PetPack.scope(a.copy(fingerprint = "a".repeat(43))))
        assertEquals(PetPack.scope(a), PetPack.scope(a.copy(token = "another-sample")))
    }

    companion object {
        fun upstreamDirectory(): File = generateSequence(File(System.getProperty("user.dir"))) { it.parentFile }
            .map { File(it, "server/internal/hub/personas/nwverse/pet") }.first { it.isDirectory }
    }
}
