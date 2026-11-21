package dev.persona.watch.data

import dev.persona.watch.HubRegistration
import dev.persona.watch.HubRegistrationStore
import dev.persona.watch.PendingHubRegistration
import dev.persona.watch.SavedHub
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

class PrimaryPairingConcurrencyTest {
    private val input = HubConfig("https://new.example", "sample-once")
    private val old = HubConfig("https://old.example", "sample-old")
    private val id = "11111111-1111-1111-1111-111111111111"

    private class Store(var primary: HubConfig) : HubRegistrationStore {
        var rows = emptyList<SavedHub>()
        var journal = emptyList<PendingHubRegistration>()
        private val credentials = mutableMapOf<String, HubConfig>()
        override fun groups() = rows
        override fun primaryUrl() = primary.baseUrl
        override fun pending() = journal
        override fun writePending(pending: List<PendingHubRegistration>) { journal = pending }
        override fun credential(id: String) = credentials[id]
        override fun saveCredential(id: String, config: HubConfig) { credentials[id] = config }
        override fun writeGroups(groups: List<SavedHub>) { rows = groups }
    }

    private fun verified(flow: PrimaryPairing) = flow.authenticate(
        PrimaryPairing.prepare(old, input.baseUrl, input.token), { it.copy(token = "sample-issued") },
        { WatchSnapshot(emptyList(), emptyList(), emptyList(), emptyList(), 12) })

    @Test fun additionalRegistrationWaitsForPrimarySaveAndThenRejectsTheNewPrimaryOrigin() {
        val lock = Any()
        val store = Store(old)
        val flow = PrimaryPairing(lock) { store.rows.map { it.config?.baseUrl } }
        val verified = verified(flow)
        val saveEntered = CountDownLatch(1)
        val allowSave = CountDownLatch(1)
        val addStarted = CountDownLatch(1)
        val executor = Executors.newFixedThreadPool(2)
        try {
            val publish = executor.submit {
                flow.publish(verified) {
                    saveEntered.countDown()
                    check(allowSave.await(5, TimeUnit.SECONDS))
                    store.primary = it
                }
            }
            assertTrue(saveEntered.await(5, TimeUnit.SECONDS))
            val add = executor.submit<Throwable> {
                addStarted.countDown()
                synchronized(lock) {
                    assertThrows(IllegalArgumentException::class.java) {
                        HubRegistration(store) { id }.add("Sample", input) { throw AssertionError("No duplicate exchange") }
                    }
                }
            }
            assertTrue(addStarted.await(5, TimeUnit.SECONDS))
            assertThrows(TimeoutException::class.java) { add.get(100, TimeUnit.MILLISECONDS) }
            allowSave.countDown()
            publish.get(5, TimeUnit.SECONDS)
            assertTrue(add.get(5, TimeUnit.SECONDS) is IllegalArgumentException)
            assertEquals(verified.config, store.primary)
            assertTrue(store.rows.isEmpty())
            assertTrue(store.journal.isEmpty())
        } finally {
            allowSave.countDown()
            executor.shutdownNow()
        }
    }

    @Test fun primaryPublicationWaitsForInFlightAdditionalRegistrationAndRejectsItsPublishedOrigin() {
        val lock = Any()
        val store = Store(old)
        val flow = PrimaryPairing(lock) { store.rows.map { it.config?.baseUrl } }
        val verified = verified(flow)
        val pairingEntered = CountDownLatch(1)
        val allowPairing = CountDownLatch(1)
        val publishStarted = CountDownLatch(1)
        val executor = Executors.newFixedThreadPool(2)
        try {
            val add = executor.submit {
                synchronized(lock) {
                    HubRegistration(store) { id }.add("Sample", input) {
                        pairingEntered.countDown()
                        check(allowPairing.await(5, TimeUnit.SECONDS))
                        it.copy(token = "sample-additional")
                    }
                }
            }
            assertTrue(pairingEntered.await(5, TimeUnit.SECONDS))
            val publish = executor.submit<HubException> {
                publishStarted.countDown()
                assertThrows(HubException::class.java) {
                    flow.publish(verified) { throw AssertionError("No duplicate primary publication") }
                }
            }
            assertTrue(publishStarted.await(5, TimeUnit.SECONDS))
            assertThrows(TimeoutException::class.java) { publish.get(100, TimeUnit.MILLISECONDS) }
            allowPairing.countDown()
            add.get(5, TimeUnit.SECONDS)
            assertEquals("duplicate_connection", publish.get(5, TimeUnit.SECONDS).code)
            assertEquals(old, store.primary)
            assertEquals(input.baseUrl, store.rows.single().config?.baseUrl)
        } finally {
            allowPairing.countDown()
            executor.shutdownNow()
        }
    }
}
