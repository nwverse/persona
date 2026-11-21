package dev.persona.watch.pet

import dev.persona.watch.HubGroupRead
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.HubGroupSnapshot
import dev.persona.watch.data.HubGroups
import dev.persona.watch.data.WatchInteraction
import dev.persona.watch.data.WatchSession
import dev.persona.watch.data.WatchSnapshot
import org.junit.Assert.*
import org.junit.Test

class PetGroupFreshnessTest {
    private val now = 10_000_000L
    private val primary = HubConfig("http://hub.local", "sample-token")
    private val empty = PetStoredStatus(PetStatus(PetState.OFFLINE), 0)
    private val manifest = PetManifest(emptyMap(), emptyMap(), "{}")
    private fun snapshot(date: Long, waiting: Boolean = false) = WatchSnapshot(
        listOf(WatchSession("machine", "same-session", "Sample", if (waiting) "needs_you" else "working")),
        if (waiting) listOf(WatchInteraction("request", "machine", "same-session", "permission", "Sample", "Sample")) else emptyList(),
        emptyList(), emptyList(), date)
    private fun read(cache: Map<String, WatchSnapshot>, failed: Set<String>) =
        // Deliberately newer aggregate timestamp: it must not renew an individual stale entry.
        HubGroupRead(snapshot(now), cache, failed)

    @Test fun oneFailedGroupDoesNotTurnAHealthyWorkingGroupOffline() {
        for (failed in listOf("", "additional")) {
            val healthy = if (failed.isEmpty()) "additional" else ""
            val result = read(mapOf(healthy to snapshot(now)), setOf(failed))
            assertEquals(PetReachability(false, true), PetReachability.from(result, 2))
            assertEquals(PetStatus(PetState.WORKING, 1), PetFreshness.groups(empty, result, 2, now, 12).status)
        }
    }

    @Test fun allFailedAndMissingOuterResultAreOffline() {
        val result = read(mapOf("" to snapshot(now)), setOf("", "additional"))
        assertEquals(PetReachability(true, true), PetReachability.from(result, 2))
        assertEquals(PetState.OFFLINE, PetFreshness.groups(empty, result, 2, now, 12).status.state)
        assertEquals(PetReachability(true, false), PetReachability.from(null, 2))
        assertEquals(PetState.OFFLINE, PetFreshness.groups(empty, null, 2, now, 12).status.state)
    }

    @Test fun anUnavailablePrimaryInboxDoesNotHideUnreadTasksBehindAHealthyIdleAdditionalHub() {
        val unread = snapshot(now - 100).copy(
            sessions = listOf(WatchSession("machine", "completed", "Sample completed task", "unread")),
            inboxUnavailable = true)
        val idle = snapshot(now).copy(sessions = listOf(WatchSession("machine", "idle", "Sample idle task", "idle")))
        val cache = mapOf("" to unread, "additional" to idle)
        val merged = HubGroups.merge(listOf(HubGroupSnapshot("", "Primary", unread, true),
            HubGroupSnapshot("additional", "Additional", idle, true)), now)
        assertTrue(merged.inboxUnavailable)
        val result = HubGroupRead(merged, cache, emptySet())
        assertEquals(PetReachability(false, false), PetReachability.from(result, 2))
        val expected = PetStoredStatus(PetStatus(PetState.DONE, 1), unread.updatedAt)
        assertEquals(expected, PetFreshness.groups(empty, result, 2, now, 12))
        val page = PetPageState()
        page.resume(primary, empty, now)
        assertTrue(page.acceptRefresh(page.generation, manifest, result, 2, now))
        assertEquals(expected, page.stored)
        assertEquals(expected, page.publication())
    }

    @Test fun anActualHubFailureRemainsOfflineInsteadOfClaimingAllRetainedWorkIsDone() {
        val unread = snapshot(now - 100).copy(
            sessions = listOf(WatchSession("machine", "completed", "Sample completed task", "unread")),
            inboxUnavailable = true)
        val idle = snapshot(now).copy(sessions = emptyList())
        for (failed in listOf(setOf(""), setOf("additional"), setOf("", "additional"))) {
            val result = read(mapOf("" to unread, "additional" to idle), failed)
            assertTrue(PetReachability.from(result, 2).partlyFailed)
            assertEquals(PetState.OFFLINE, PetFreshness.groups(empty, result, 2, now, 12).status.state)
        }
    }

    @Test fun unreadTasksWithAnUnavailableInboxExpireAtTheirOwnExclusiveDeadline() {
        val old = now - PetStoredStatus.STALE_MS + 100
        val unread = snapshot(old).copy(
            sessions = listOf(WatchSession("machine", "completed", "Sample completed task", "unread")),
            inboxUnavailable = true)
        val idle = snapshot(now).copy(sessions = emptyList())
        val result = read(mapOf("" to unread, "additional" to idle), emptySet())
        val retained = PetFreshness.groups(empty, result, 2, now, 12)
        assertEquals(PetStoredStatus(PetStatus(PetState.DONE, 1), old), retained)
        assertEquals(PetState.DONE, retained.visible(now + 99).state)
        assertEquals(PetState.OFFLINE, retained.visible(now + 100).state)
        assertEquals(retained, PetFreshness.groups(retained, result, 2, now + 99, 12))
        val expired = PetFreshness.groups(retained, result, 2, now + 100, 12)
        assertEquals(PetStoredStatus(PetStatus(PetState.IDLE), now), expired)
        assertEquals(PetState.OFFLINE, PetFreshness.groups(expired, result, 2, now + PetStoredStatus.STALE_MS, 12).status.state)
    }

    @Test fun expiredFailedPrimaryCannotBorrowAdditionalHubsFreshTimestamp() {
        val result = read(mapOf("" to snapshot(now - PetStoredStatus.STALE_MS, true), "additional" to snapshot(now)), setOf(""))
        val previous = PetStoredStatus(PetStatus(PetState.NEEDS_YOU, 1), now - 1)
        val next = PetFreshness.groups(previous, result, 2, now, 12)
        assertEquals(PetStatus(PetState.WORKING, 1), next.status)
        assertEquals(now, next.updatedAt)
        val page = PetPageState()
        page.resume(primary, previous, now)
        assertTrue(page.acceptRefresh(page.generation, manifest, result, 2, now))
        assertEquals(next, page.stored)
    }

    @Test fun retainedPrimaryExpiresOnTheForegroundTimerWithoutAnotherPoll() {
        val old = now - PetStoredStatus.STALE_MS + 100
        val result = read(mapOf("" to snapshot(old, true), "additional" to snapshot(now)), setOf(""))
        val page = PetPageState()
        page.resume(primary, empty, now)
        page.acceptRefresh(page.generation, manifest, result, 2, now)
        assertEquals(PetStatus(PetState.NEEDS_YOU, 1), page.stored.status)
        assertEquals(old, page.stored.updatedAt)
        assertEquals(100L, page.expiryDelay(now))
        val published = page.stored
        assertEquals(PetState.OFFLINE, published.visible(now + 100).state)
        assertTrue(page.revalidate(now + 100))
        assertEquals(PetStatus(PetState.WORKING, 1), page.stored.status)
        assertEquals(now, page.stored.updatedAt)
        assertEquals(PetStoredStatus.STALE_MS - 100, page.expiryDelay(now + 100))
        page.revalidate(now + PetStoredStatus.STALE_MS)
        assertEquals(PetState.OFFLINE, page.stored.status.state)
        assertNull(page.expiryDelay(now + PetStoredStatus.STALE_MS))
    }

    @Test fun freshSnapshotReplacesAFuturePreviousTimestampAfterClockRollback() {
        val future = PetStoredStatus(PetStatus(PetState.WORKING, 9), now + 120_000)
        assertEquals(PetState.OFFLINE, future.visible(now).state)
        val next = PetFreshness.next(future, snapshot(now - 1), false, now = now, hour = 12)
        assertEquals(now - 1, next.updatedAt)
        assertEquals(PetStatus(PetState.WORKING, 1), next.status)
        val failure = PetFreshness.next(future, null, true, now = now, hour = 12)
        assertEquals(now, failure.updatedAt)
        assertEquals(PetState.OFFLINE, failure.status.state)
    }

    @Test fun futureGroupCacheIsExcludedAndANewCurrentClockFetchRecovers() {
        val future = read(mapOf("" to snapshot(now + 120_000, true), "additional" to snapshot(now)), setOf(""))
        val next = PetFreshness.groups(empty, future, 2, now, 12)
        assertEquals(PetStatus(PetState.WORKING, 1), next.status)
        assertEquals(now, next.updatedAt)
        val recovered = read(mapOf("" to snapshot(now + 1, true), "additional" to snapshot(now + 1)), emptySet())
        assertEquals(PetStatus(PetState.NEEDS_YOU, 1), PetFreshness.groups(next, recovered, 2, now + 1, 12).status)
    }
}
