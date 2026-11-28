package dev.persona.watch.pet

import dev.persona.watch.HubGroupRead
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.WatchSession
import dev.persona.watch.data.WatchSnapshot
import org.junit.Assert.*
import org.junit.Test

class PetLifecycleTest {
    private val now = 10_000_000L
    private val primary = HubConfig("http://hub.local", "sample-token")
    private val saved = PetStoredStatus(PetStatus(PetState.WORKING, 1), now)
    private val manifest = PetManifest(emptyMap(), emptyMap(), "{}")
    private fun snapshot(date: Long = now) = WatchSnapshot(
        listOf(WatchSession("sample", "session", "Sample", "working")), emptyList(), emptyList(), emptyList(), date)

    private fun read() = HubGroupRead(snapshot(), mapOf("" to snapshot()), emptySet())

    @Test fun replacingPrimaryClearsAllConnectionOwnedPageDataAndRejectsOldWork() {
        val page = PetPageState()
        page.resume(primary, saved, now)
        val old = page.generation
        assertTrue(page.acceptRefresh(old, manifest, read(), 1, now))
        val replacement = primary.copy(baseUrl = "http://other.local", token = "other-sample-token")
        val replacementStatus = PetStoredStatus(PetStatus(PetState.IDLE), now + 1)
        assertTrue(page.resume(replacement, replacementStatus, now + 1))
        assertEquals(replacement, page.connection)
        assertNull(page.manifest)
        assertTrue(page.groups.isEmpty())
        assertEquals(replacementStatus, page.stored)
        assertFalse(page.acceptManifest(old, manifest))
        assertFalse(page.acceptRefresh(old, manifest, read(), 1, now + 1))
        assertEquals(replacementStatus, page.stored)
        assertNull(page.manifest)
    }

    @Test fun removingPrimaryClearsStatusManifestAndGroupsAndRejectsCallbacks() {
        val page = PetPageState()
        page.resume(primary, saved, now)
        val old = page.generation
        page.acceptRefresh(old, manifest, read(), 1, now)
        assertTrue(page.resume(null, saved, now + 1))
        assertNull(page.connection)
        assertNull(page.manifest)
        assertTrue(page.groups.isEmpty())
        assertEquals(PetStoredStatus(PetStatus(PetState.OFFLINE), 0), page.stored)
        assertFalse(page.acceptManifest(old, manifest))
        assertFalse(page.accepts(page.generation))
    }

    @Test fun changingOnlyTheCredentialStillInvalidatesOldCallbacks() {
        val page = PetPageState()
        page.resume(primary, saved, now)
        val old = page.generation
        assertTrue(page.resume(primary.copy(token = "replacement-sample-token"), saved, now))
        assertFalse(page.accepts(old))
    }

    @Test fun statusCacheSeparatesCredentialsEvenWhenVerifiedArtworkCanBeShared() {
        val replaced = primary.copy(token = "replacement-sample-token")
        assertEquals(PetPack.scope(primary), PetPack.scope(replaced))
        assertNotEquals(petStatusScope(primary), petStatusScope(replaced))
        assertNotEquals(petStatusScope(primary), petStatusScope(primary.copy(deviceId = "sample-device")))
        assertNotEquals(petStatusScope(primary), petStatusScope(primary.copy(fingerprint = "A".repeat(43))))
        assertNotEquals(petStatusScope(primary), petStatusScope(primary.copy(baseUrl = "http://other.local")))
        assertFalse(petStatusScope(primary).contains(primary.token))
    }

    @Test fun pausedWorkCannotPublishIntoResumedPageAndResumeAlwaysRevalidates() {
        val page = PetPageState()
        page.resume(primary, saved, now)
        val old = page.generation
        page.invalidate()
        assertFalse(page.resume(primary, saved, now + PetStoredStatus.STALE_MS + 1))
        assertEquals(PetState.OFFLINE, page.stored.status.state)
        assertFalse(page.acceptRefresh(old, manifest, read(), 1, now))
        assertEquals(PetState.OFFLINE, page.stored.status.state)
    }

    @Test fun failedAndNullSnapshotPollsKeepLastSuccessfulTimestamp() {
        val offline = PetFreshness.next(saved, snapshot(0), unreachable = true, now = now + 10, hour = 12)
        assertEquals(now, offline.updatedAt)
        assertEquals(PetState.OFFLINE, offline.status.state)
        val withoutSnapshot = PetFreshness.next(saved, null, unreachable = true, now = now + 10, hour = 12)
        assertEquals(offline, withoutSnapshot)
        val zeroDate = PetFreshness.next(saved, snapshot(0), unreachable = false, now = now + 10, hour = 12)
        assertEquals(now, zeroDate.updatedAt)
        assertEquals(PetState.WORKING, zeroDate.status.state)
        val cached = PetFreshness.next(saved.copy(updatedAt = 0), snapshot(), true, now = now + 10, hour = 12)
        assertEquals(now, cached.updatedAt)
        assertEquals(PetState.OFFLINE, cached.status.state)
    }

    @Test fun failedPollThenRetainedStaleSnapshotCannotReviveWorkingStatus() {
        val late = now + PetStoredStatus.STALE_MS
        val offline = PetFreshness.next(saved, null, true, now = late, hour = 12)
        val retained = PetFreshness.next(offline, snapshot(), false, now = late + 1, hour = 12)
        assertEquals(now, retained.updatedAt)
        assertEquals(PetState.OFFLINE, retained.status.state)
        val fresh = PetFreshness.next(retained, snapshot(late + 2), false, now = late + 2, hour = 12)
        assertEquals(PetState.WORKING, fresh.status.state)
        assertEquals(late + 2, fresh.updatedAt)
    }

    @Test fun foregroundExpiryHasOneFixedDeadlineAndSwitchesWithoutAnotherPoll() {
        val page = PetPageState()
        page.resume(primary, saved, now)
        assertEquals(PetStoredStatus.STALE_MS, page.expiryDelay(now))
        assertEquals(1L, page.expiryDelay(now + PetStoredStatus.STALE_MS - 1))
        assertFalse(page.revalidate(now + PetStoredStatus.STALE_MS - 1))
        assertTrue(page.revalidate(now + PetStoredStatus.STALE_MS))
        assertEquals(PetState.OFFLINE, page.stored.status.state)
        assertNull(page.expiryDelay(now + PetStoredStatus.STALE_MS))
        assertFalse(page.revalidate(now + PetStoredStatus.STALE_MS + 1))
    }

    @Test fun everyAcceptedRefreshUsesVisibleStatusBeforePublication() {
        val page = PetPageState()
        page.resume(primary, saved, now)
        assertTrue(page.acceptRefresh(page.generation, manifest, read(), 1, now + PetStoredStatus.STALE_MS))
        assertEquals(PetState.OFFLINE, page.stored.status.state)
        assertEquals(now, page.stored.updatedAt)
    }

    @Test fun reducedMotionAndNoninteractivePlaybackDecodeOnlyAStaticFaceEvenOnTap() {
        for ((motion, interactive) in listOf(false to true, false to false, true to false)) {
            val plan = PetPlayback.plan(PetState.IDLE, "tap", motion, interactive)
            val calls = mutableListOf<String>()
            val result = plan.load(still = { calls += it; "static-$it" },
                frames = { throw AssertionError("Reduced motion must not decode $it") })
            assertFalse(plan.animated)
            assertEquals("idle", plan.clip)
            assertEquals(listOf("idle"), calls)
            assertEquals("static-idle", result.still)
            assertTrue(result.frames.isEmpty())
        }
    }

    @Test fun motionEnabledTapCanUseItsOneShotClip() {
        val plan = PetPlayback.plan(PetState.IDLE, "tap", motion = true, interactive = true)
        val calls = mutableListOf<String>()
        val result = plan.load(still = { "static-$it" }, frames = { calls += it; listOf("frame") })
        assertTrue(plan.animated)
        assertEquals(listOf("tap"), calls)
        assertEquals(listOf("frame"), result.frames)
    }

    @Test fun oomAndOtherThrowablesReleaseEveryAlreadyAllocatedFrameThenRethrow() {
        for (failure in listOf(OutOfMemoryError("sample allocation"), AssertionError("sample decoder"), IllegalStateException("sample failure"))) {
            val released = mutableListOf<String>()
            try {
                collectPetFrames<String>(3, recycle = { released += it }) { output ->
                    output += "frame-1"; output += "frame-2"
                    throw failure
                }
                fail("Expected decode failure")
            } catch (caught: Throwable) { assertSame(failure, caught) }
            assertEquals(listOf("frame-1", "frame-2"), released)
        }
    }

    @Test fun completedFramesTransferOwnershipAndCleanupFailureDoesNotHideOriginalError() {
        val released = mutableListOf<String>()
        val frames = collectPetFrames<String>(1, recycle = { released += it }) { it += "frame" }
        assertEquals(listOf("frame"), frames)
        assertTrue(released.isEmpty())
        val failure = OutOfMemoryError("sample allocation")
        try {
            collectPetFrames<String>(2, recycle = { released += it; if (it == "first") error("sample cleanup") }) {
                it += "first"; it += "second"; throw failure
            }
            fail("Expected allocation failure")
        } catch (caught: Throwable) { assertSame(failure, caught) }
        assertEquals(listOf("first", "second"), released)
        assertEquals(1, failure.suppressed.size)
    }
}
