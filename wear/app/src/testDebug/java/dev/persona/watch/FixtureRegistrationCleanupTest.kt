package dev.persona.watch

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.util.concurrent.TimeoutException

class FixtureRegistrationCleanupTest {
    @get:Rule val folder = TemporaryFolder()
    private val id = "11111111-1111-1111-1111-111111111111"
    private val otherId = "22222222-2222-2222-2222-222222222222"
    private val input = HubConfig("http://fixture.local", "sample-one-time-token")
    private val issued = input.copy(token = "sample-issued-token")
    private fun owned(id: String = this.id, config: HubConfig = input) =
        PendingHubRegistration(id, "Same name", config.baseUrl, registrationInputKey(config))

    private inner class Store : HubRegistrationStore, HubRegistrationAbortStore {
        private val directory = folder.newFolder()
        var rows = emptyList<SavedHub>()
        var journal = emptyList<PendingHubRegistration>()
        val configs = mutableMapOf<String, HubConfig>()
        val fence = HubDeletionFence(directory)
        var failPublish = false
        var deleteFailures = 0
        var deletes = 0
        override fun groups() = rows
        override fun primaryUrl(): String? = null
        override fun pending() = journal
        override fun writePending(pending: List<PendingHubRegistration>) { journal = pending }
        override fun credential(id: String) = configs[id]
        override fun saveCredential(id: String, config: HubConfig) {
            configs[id] = config
            envelope(id).writeText("sample encrypted envelope")
        }
        override fun writeGroups(groups: List<SavedHub>) {
            check(!failPublish || groups.none { it.id == id }) { "injected publish failure" }
            rows = groups
        }
        override fun aborted(id: String) = fence.contains(id)
        override fun markRemoved(id: String) = fence.mark(id)
        override fun clearCredential(id: String) {
            deletes++
            check(deleteFailures-- <= 0) { "injected delete failure" }
            check(!envelope(id).exists() || envelope(id).delete())
            configs.remove(id)
        }
        fun envelope(id: String) = File(directory, "group_$id.enc")
    }

    @Test fun failedPublicationIsCleanedByRecordedIdWithoutTouchingOtherPendingOrPublishedData() {
        val store = Store()
        val otherConfig = input.copy(baseUrl = "http://other.local", token = "other-token")
        val otherPending = owned(otherId, otherConfig)
        store.rows = listOf(SavedHub(otherId, "Same name", otherConfig))
        store.journal = listOf(otherPending)
        store.saveCredential(otherId, otherConfig)
        store.failPublish = true
        var recorded: PendingHubRegistration? = null
        assertThrows(IllegalStateException::class.java) {
            HubRegistration(store) { id }.add("Same name", input, record = { recorded = it }) { issued }
        }
        assertTrue(store.envelope(id).exists())
        assertEquals(setOf(id, otherId), store.pending().map { it.id }.toSet())
        val abort = HubRegistrationAbort(store)
        cleanupFixtureRegistrations(listOf(checkNotNull(recorded)), abort::abort)
        assertFalse(store.envelope(id).exists())
        assertEquals(listOf(otherPending), store.pending())
        assertEquals(listOf(otherId), store.groups().map { it.id })
        assertTrue(store.envelope(otherId).exists())
        assertTrue(store.fence.contains(id))
        assertFalse(store.fence.contains(otherId))
        abort.abort(checkNotNull(recorded)) // Repeating cleanup is harmless.
        assertEquals(otherConfig, store.configs[otherId])
    }

    @Test fun failedDeletionIsRetriedWithAThreeAttemptBoundAndRemainsRecoverableOnFailure() {
        val store = Store()
        val entry = owned()
        store.journal = listOf(entry)
        store.rows = listOf(SavedHub(id, entry.name, issued))
        store.saveCredential(id, issued)
        store.deleteFailures = 4
        val abort = HubRegistrationAbort(store)
        assertThrows(IllegalStateException::class.java) { cleanupFixtureRegistrations(listOf(entry), abort::abort) }
        assertEquals(3, store.deletes)
        assertTrue(store.fence.contains(id))
        assertTrue(store.envelope(id).exists())
        assertEquals(listOf(entry), store.pending())
        assertTrue(store.groups().isEmpty())
        assertNull(store.fence.credential(id) { store.credential(id) })
        store.deleteFailures = 1
        cleanupFixtureRegistrations(listOf(entry), abort::abort)
        assertEquals(5, store.deletes)
        assertFalse(store.envelope(id).exists())
        assertTrue(store.pending().isEmpty())
    }

    @Test fun mismatchedInputOrOriginCannotDeleteAnExistingRegistration() {
        for (changed in listOf(owned().copy(inputKey = "b".repeat(64)), owned().copy(baseUrl = "http://other.local"))) {
            val store = Store()
            store.journal = listOf(owned())
            store.saveCredential(id, issued)
            assertThrows(IllegalArgumentException::class.java) { HubRegistrationAbort(store).abort(changed) }
            assertFalse(store.fence.contains(id))
            assertTrue(store.envelope(id).exists())
            assertEquals(0, store.deletes)
        }
    }

    @Test fun lateRecordedOrInFlightPairCannotPublishAfterAnAbortFence() {
        for (duringExchange in listOf(false, true)) {
            val store = Store()
            var recorded: PendingHubRegistration? = null
            var exchanges = 0
            assertThrows(IllegalStateException::class.java) {
                HubRegistration(store) { id }.add("Same name", input, record = {
                    recorded = it
                    if (!duringExchange) store.fence.mark(it.id)
                }) {
                    exchanges++
                    store.fence.mark(checkNotNull(recorded).id)
                    issued
                }
            }
            assertEquals(if (duringExchange) 1 else 0, exchanges)
            cleanupFixtureRegistrations(listOf(checkNotNull(recorded)), HubRegistrationAbort(store)::abort)
            assertTrue(store.pending().isEmpty())
            assertTrue(store.groups().isEmpty())
            assertFalse(store.envelope(id).exists())
        }
    }

    @Test fun oneFailedAbortDoesNotSkipOtherOwnedRegistrations() {
        val attempts = mutableMapOf<String, Int>()
        assertThrows(IllegalStateException::class.java) {
            cleanupFixtureRegistrations(listOf(owned(), owned(otherId))) { entry ->
                attempts[entry.id] = (attempts[entry.id] ?: 0) + 1
                check(entry.id != id) { "injected permanent failure" }
            }
        }
        assertEquals(mapOf(id to 3, otherId to 1), attempts)
    }

    @Test fun alreadyOwnedRegistrationIsFencedAndAbortedBeforeAnUnsuccessfulWait() {
        val store = Store()
        val entry = owned()
        store.journal = listOf(entry)
        store.rows = listOf(SavedHub(id, entry.name, issued))
        store.saveCredential(id, issued)
        val order = mutableListOf<String>()
        val failure = assertThrows(IllegalStateException::class.java) {
            drainFixtureRegistrations(
                owned = { listOf(entry) },
                fence = { order += "fence"; store.fence.mark(it.id) },
                abort = { value, _ -> order += "abort"; HubRegistrationAbort(store).abort(value) },
                awaitRecord = {
                    order += "await"
                    assertTrue(store.fence.contains(id))
                    assertTrue(store.rows.isEmpty() && store.journal.isEmpty())
                    assertFalse(store.envelope(id).exists())
                    false
                },
            )
        }
        assertEquals("multihub_registration_not_observed", failure.message)
        assertEquals(listOf("fence", "abort", "await"), order)
    }

    @Test fun entriesRecordedDuringFailedOrThrowingWaitAreStillAborted() {
        for (throws in listOf(false, true)) {
            val store = Store()
            val entries = mutableListOf<PendingHubRegistration>()
            val failure = assertThrows(IllegalStateException::class.java) {
                drainFixtureRegistrations(
                    owned = { entries.toList() },
                    fence = { store.fence.mark(it.id) },
                    abort = { entry, _ -> HubRegistrationAbort(store).abort(entry) },
                    awaitRecord = {
                        val entry = owned()
                        entries += entry
                        store.journal = listOf(entry)
                        store.rows = listOf(SavedHub(id, entry.name, issued))
                        store.saveCredential(id, issued)
                        check(!throws) { "injected record failure" }
                        false
                    },
                )
            }
            assertEquals(if (throws) "injected record failure" else "multihub_registration_not_observed", failure.message)
            assertTrue(store.fence.contains(id))
            assertTrue(store.rows.isEmpty() && store.journal.isEmpty())
            assertFalse(store.envelope(id).exists())
        }
    }

    @Test fun entriesArrivingDuringThePostWaitAbortAreResnapshottedAndDrained() {
        val entries = mutableListOf<PendingHubRegistration>()
        val fenced = mutableSetOf<String>()
        val aborted = mutableListOf<String>()
        var clock = 0L
        drainFixtureRegistrations(
            owned = { entries.toList() },
            fence = { fenced += it.id },
            abort = { entry, _ ->
                assertTrue(entry.id in fenced)
                aborted += entry.id
                if (entry.id == id) entries += owned(otherId)
            },
            awaitRecord = { entries += owned(); true },
            now = { clock }, wait = { clock += it }, timeoutMs = 100,
        )
        assertEquals(listOf(id, otherId), aborted)
        assertEquals(setOf(id, otherId), fenced)
        assertEquals(50L, clock)
    }

    @Test fun initialAbortFailuresAreRetriedAfterTheWaitUntilNoOwnedResidueRemains() {
        val store = Store()
        val entry = owned()
        store.journal = listOf(entry)
        store.saveCredential(id, issued)
        store.deleteFailures = 7
        var clock = 0L
        drainFixtureRegistrations(
            owned = { listOf(entry) },
            fence = { store.fence.mark(it.id) },
            abort = { value, _ -> HubRegistrationAbort(store).abort(value) },
            awaitRecord = { assertEquals(3, store.deletes); true },
            now = { clock }, wait = { clock += it }, timeoutMs = 100,
        )
        assertEquals(8, store.deletes)
        assertEquals(50L, clock)
        assertTrue(store.fence.contains(id))
        assertTrue(store.journal.isEmpty())
        assertFalse(store.envelope(id).exists())
    }

    @Test fun abortTimeoutFencesEveryKnownEntryAndReportsResidueAtTheSharedDeadline() {
        val entries = listOf(owned(), owned(otherId))
        val fenced = mutableSetOf<String>()
        val budgets = mutableListOf<Long>()
        var clock = 0L
        var awaited = false
        val failure = assertThrows(IllegalStateException::class.java) {
            drainFixtureRegistrations(
                owned = { entries },
                fence = { fenced += it.id },
                abort = { _, budget ->
                    assertEquals(setOf(id, otherId), fenced)
                    budgets += budget
                    clock += budget
                    throw TimeoutException("injected abort timeout")
                },
                awaitRecord = { budget -> awaited = true; assertEquals(0L, budget); false },
                now = { clock }, wait = { clock += it }, timeoutMs = 100,
            )
        }
        assertTrue(awaited)
        assertEquals("multihub_cleanup_incomplete_2", failure.message)
        assertEquals(setOf(id, otherId), fenced)
        assertEquals(listOf(100L), budgets)
        assertEquals(100L, clock)
        assertTrue(failure.suppressed.any { it is TimeoutException })
        assertTrue(failure.suppressed.any { it.message == "multihub_registration_not_observed" })
    }

    @Test fun registrationWaitTimeoutLeavesOnlyTheRemainingCleanupBudget() {
        val entries = mutableListOf<PendingHubRegistration>()
        var clock = 0L
        var aborted = false
        val failure = assertThrows(IllegalStateException::class.java) {
            drainFixtureRegistrations(
                owned = { entries.toList() },
                fence = {},
                abort = { _, budget -> aborted = true; assertEquals(10_000L, budget) },
                awaitRecord = { budget ->
                    assertEquals(5_000L, budget)
                    clock += budget
                    entries += owned()
                    false
                },
                now = { clock }, wait = { clock += it },
            )
        }
        assertTrue(aborted)
        assertEquals("multihub_registration_not_observed", failure.message)
        assertEquals(5_000L, clock)
    }
}
