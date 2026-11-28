package dev.persona.watch

import dev.persona.watch.data.*
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.CompletableFuture

class HubSnapshotRecoveryTest {
    private fun sample(title: String = "Paired", date: Long = 120) = WatchSnapshot(
        listOf(WatchSession("computer", "session", title, "working", canSend = true, canInterrupt = true, canApprove = true)),
        listOf(WatchInteraction("request", "computer", "session", "screen", title, "Prompt", canAnswer = true, canCancel = true)),
        listOf(WatchMachine("computer", "Sample", true)),
        listOf(InboxItem("item", title, "Preview", "mail", true)), date)

    private fun failedRead(cache: Map<String, WatchSnapshot>, ids: List<String> = listOf("")): HubGroupRead {
        val groups = ids.map { SavedHub(it, "Sample", null) }
        val requests = ids.associateWith {
            CompletableFuture<WatchSnapshot>().apply { completeExceptionally(IllegalStateException("Unavailable")) }
        }
        return HubGroupReader.collect(groups, requests, cache, 40_000_000_000L) { 0L }
    }

    private fun assertReadOnly(expected: WatchSnapshot, actual: WatchSnapshot) {
        assertEquals(expected.updatedAt, actual.updatedAt)
        assertEquals(expected.sessions.map { it.title }, actual.sessions.map { it.title })
        assertTrue(actual.sessions.all { it.state == "offline" && !it.canSend && !it.canInterrupt && !it.canApprove })
        assertEquals(expected.interactions.map { it.key }, actual.interactions.map { it.key })
        assertTrue(actual.interactions.all { !it.canAnswer && !it.canCancel })
        assertEquals(expected.machines.map { it.id }, actual.machines.map { it.id })
        assertTrue(actual.machines.none { it.online })
        assertEquals(expected.inbox, actual.inbox)
        assertTrue(actual.inboxUnavailable)
        assertEquals(InboxSelection.State.UNAVAILABLE, InboxSelection.review(expected.inbox.single(), actual, true).state)
    }

    @Test fun pairingSeedsThePrimaryCacheBeforeTheFirstFailedPoll() {
        val fresh = sample()
        val initial = HubGroupRead.pairedPrimary(fresh)
        assertSame(fresh, initial.snapshot)
        assertEquals(mapOf("" to fresh), initial.cache)
        assertTrue(initial.failed.isEmpty())
        val failed = failedRead(initial.cache)
        assertEquals(fresh, failed.cache.getValue(""))
        assertEquals(setOf(""), failed.failed)
        assertReadOnly(fresh, checkNotNull(failed.displayAfter(initial.snapshot, 1)))
    }

    @Test fun totalFailureWithoutCacheRetainsThePreviouslyVisibleSnapshotReadOnly() {
        val previous = sample()
        val failed = failedRead(emptyMap())
        assertTrue(failed.snapshot.sessions.isEmpty())
        assertReadOnly(previous, checkNotNull(failed.displayAfter(previous, 1)))
    }

    @Test fun allFailedCacheCanRepopulateAViewWithoutAnEarlierAggregate() {
        val cached = sample()
        val failed = failedRead(mapOf("" to cached))
        assertReadOnly(cached, checkNotNull(failed.displayAfter(null, 1)))
        assertNull(failedRead(emptyMap()).displayAfter(null, 1))
    }

    @Test fun totalFailurePreservesEachVisibleGroupIdentityAndDisablesEveryMutation() {
        val primary = sample()
        val additional = sample("Additional", 130)
        val previous = HubGroups.merge(listOf(HubGroupSnapshot("", "Primary", primary, true),
            HubGroupSnapshot("additional", "Additional", additional, true)), 120)
        val failed = failedRead(emptyMap(), listOf("", "additional"))
        val visible = checkNotNull(failed.displayAfter(previous, 2))
        assertReadOnly(previous, visible)
        assertEquals(setOf("", "additional"), visible.sessions.map { it.groupId }.toSet())
        assertEquals(2, visible.sessions.map { it.key }.distinct().size)
    }

    @Test fun aReplacementPairingDropsOldConnectionCachesEvenWhenIdsCollide() {
        val old = HubGroupRead.pairedPrimary(sample("Old account"))
        val fresh = sample("New account", 500)
        val replacement = HubGroupRead.pairedPrimary(fresh)
        assertEquals(setOf(""), replacement.cache.keys)
        assertNotEquals(old.cache, replacement.cache)
        val failed = failedRead(replacement.cache)
        val visible = checkNotNull(failed.displayAfter(replacement.snapshot, 1))
        assertReadOnly(fresh, visible)
        assertNull(failed.displayAfter(visible, 0)) // Removing all connections cannot retain old content.
    }

    @Test fun aSuccessfulRefreshReplacesReadOnlyContentAndRestoresOnlyFreshCapabilities() {
        val initial = sample()
        val stale = checkNotNull(failedRead(emptyMap()).displayAfter(initial, 1))
        val fresh = sample("Recovered", 900)
        val success = HubGroupRead(fresh, mapOf("" to fresh), emptySet())
        assertSame(fresh, success.displayAfter(stale, 1))
        assertTrue(checkNotNull(success.displayAfter(stale, 1)).sessions.single().canSend)
    }

    @Test fun anUnavailableInboxKeepsItsPriorContentWithoutRestoringActions() {
        val previous = sample()
        val fresh = sample("New session", 900).copy(inbox = emptyList(), inboxUnavailable = true)
        val read = HubGroupRead(fresh, mapOf("" to fresh), emptySet())
        val visible = checkNotNull(read.displayAfter(previous, 1))
        assertEquals(previous.inbox, visible.inbox)
        assertEquals(fresh.sessions, visible.sessions)
        assertTrue(visible.inboxUnavailable)
        assertEquals(InboxSelection.State.UNAVAILABLE, InboxSelection.review(previous.inbox.single(), visible, true).state)
    }
}
