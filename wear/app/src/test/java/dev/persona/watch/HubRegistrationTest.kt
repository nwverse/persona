package dev.persona.watch

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Test

class HubRegistrationTest {
    private val input = HubConfig("http://hub.local", "sample-one-time-token")
    private val issued = input.copy(token = "sample-issued-watch-token", deviceId = "sample-device")
    private val id = "11111111-1111-1111-1111-111111111111"

    private class Store : HubRegistrationStore {
        var rows = emptyList<SavedHub>()
        var durableRows = emptyList<SavedHub>()
        var journal = emptyList<PendingHubRegistration>()
        var durableJournal = emptyList<PendingHubRegistration>()
        val credentials = mutableMapOf<String, HubConfig>()
        var primary: String? = null
        var failPublish = false
        var failPrepare = false
        var failCleanup = false
        var failCredential = false
        val unreadableCredentials = mutableSetOf<String>()
        var saves = 0
        var publications = 0
        val abortedIds = mutableSetOf<String>()
        var onPrimaryRead: () -> Unit = {}
        val events = mutableListOf<String>()
        override fun groups() = rows
        override fun primaryUrl(): String? { onPrimaryRead(); return primary }
        override fun aborted(id: String) = id in abortedIds
        override fun pending() = journal
        override fun writePending(pending: List<PendingHubRegistration>) {
            journal = pending // Like SharedPreferences, memory changes even if disk commit fails.
            events += if (pending.isEmpty()) "cleanup" else "prepare"
            check(if (pending.isEmpty()) !failCleanup else !failPrepare)
            durableJournal = pending
        }
        override fun credential(id: String): HubConfig? {
            check(id !in unreadableCredentials || id !in credentials)
            return credentials[id]
        }
        override fun saveCredential(id: String, config: HubConfig) {
            events += "credential"
            check(!failCredential)
            saves++
            credentials[id] = config
        }
        override fun writeGroups(groups: List<SavedHub>) {
            rows = groups
            events += "publish"
            publications++
            check(!failPublish)
            durableRows = groups
        }
        fun restart() { rows = durableRows; journal = durableJournal }
    }

    private fun engine(store: Store) = HubRegistration(store) { id }
    private fun fails(action: () -> Unit) {
        try { action(); fail("Expected injected failure") } catch (_: IllegalStateException) { }
    }

    @Test fun directoryFailureIsRecoveredAfterRestartWithoutTheOneTimeToken() {
        val store = Store().apply { failPublish = true }
        var exchanges = 0
        fails { engine(store).add("Sample Hub", input) { exchanges++; store.events += "exchange"; issued } }
        assertTrue(store.durableRows.isEmpty())
        assertEquals(id, store.durableJournal.single().id)
        assertEquals(issued, store.credentials[id])
        assertEquals(listOf("prepare", "exchange", "credential", "publish"), store.events)
        store.restart()
        store.failPublish = false
        assertTrue(engine(store).recover())
        assertEquals(listOf(SavedHub(id, "Sample Hub", issued)), store.durableRows)
        assertTrue(store.durableJournal.isEmpty())
        assertEquals(1, exchanges)
        assertEquals(1, store.saves)
        assertFalse(engine(store).recover())
    }

    @Test fun retryingAddFinishesTheSameRegistrationWithoutRedeemingAgain() {
        val store = Store().apply { failPublish = true }
        fails { engine(store).add("Sample Hub", input) { issued } }
        store.failPublish = false
        engine(store).add("Sample Hub", input) { throw AssertionError("Consumed token must not be redeemed") }
        assertEquals(1, store.durableRows.size)
        assertEquals(id, store.durableRows.single().id)
        assertEquals(1, store.saves)
        assertEquals(2, store.publications) // Recommit even when the failed write changed the memory view.
    }

    @Test fun repeatedPublicationFailuresKeepTheEncryptedCredentialReachable() {
        val store = Store().apply { failPublish = true }
        fails { engine(store).add("Sample Hub", input) { issued } }
        repeat(3) {
            store.restart()
            fails { engine(store).recover() }
            assertEquals(issued, store.credentials[store.durableJournal.single().id])
        }
        store.failPublish = false
        assertTrue(engine(store).recover())
        assertEquals(1, store.durableRows.size)
        assertEquals(1, store.saves)
    }

    @Test fun cleanupFailureReplaysIdempotentlyAfterRestart() {
        val store = Store().apply { failCleanup = true }
        fails { engine(store).add("Sample Hub", input) { issued } }
        assertEquals(1, store.durableRows.size)
        assertTrue(store.durableJournal.isNotEmpty())
        store.restart()
        store.failCleanup = false
        assertTrue(engine(store).recover())
        assertEquals(1, store.durableRows.size)
        assertEquals(1, store.saves)
        assertTrue(store.durableJournal.isEmpty())
    }

    @Test fun failureToPrepareJournalDoesNotConsumeTheToken() {
        val store = Store().apply { failPrepare = true }
        fails { engine(store).add("Sample Hub", input) { throw AssertionError("Must prepare before exchange") } }
        assertTrue(store.credentials.isEmpty())
        assertTrue(store.durableJournal.isEmpty())
        store.failPrepare = false
        var exchanges = 0
        engine(store).add("Sample Hub", input) { exchanges++; issued }
        assertEquals(1, exchanges)
        assertEquals(1, store.durableRows.size)
    }

    @Test fun missingIssuedCredentialNeverTriggersAutomaticRedemptionOrFakeRecovery() {
        val store = Store().apply { failCredential = true }
        fails { engine(store).add("Sample Hub", input) { issued } }
        store.restart()
        assertFalse(engine(store).recover())
        assertTrue(store.durableRows.isEmpty())
        assertTrue(store.durableJournal.isEmpty())
        // A crash/write failure before issued credentials become durable cannot be locally recovered.
    }

    @Test fun validatesCapacityAndDuplicatesBeforeTokenExchange() {
        val store = Store()
        store.primary = input.baseUrl
        assertThrows(IllegalArgumentException::class.java) {
            engine(store).add("Sample Hub", input) { throw AssertionError("Duplicate must not exchange") }
        }
        store.primary = null
        store.rows = (1..3).map { SavedHub("sample-$it", "Sample", input.copy(baseUrl = "http://hub$it.local")) }
        assertThrows(IllegalArgumentException::class.java) {
            engine(store).add("Sample Hub", input) { throw AssertionError("Full directory must not exchange") }
        }
        assertTrue(store.events.isEmpty())
    }

    @Test fun recoveryPreservesUnrelatedConnections() {
        val store = Store()
        val other = SavedHub("other", "Other", input.copy(baseUrl = "http://other.local"))
        store.rows = listOf(other)
        store.durableRows = store.rows
        store.failPublish = true
        fails { engine(store).add("Sample Hub", input) { issued } }
        store.restart()
        store.failPublish = false
        engine(store).recover()
        assertEquals(listOf(other, SavedHub(id, "Sample Hub", issued)), store.durableRows)
    }

    @Test fun unreadableSavedCredentialKeepsItsJournalAndPreventsExchangeForTheSameHub() {
        val store = Store().apply { failPublish = true }
        fails { engine(store).add("Sample Hub", input) { issued } }
        store.restart()
        store.failPublish = false
        store.unreadableCredentials += id
        fails { engine(store).recover() }
        fails {
            engine(store).add("Sample Hub", input) {
                throw AssertionError("Unreadable saved credential must not be abandoned")
            }
        }
        assertEquals(id, store.durableJournal.single().id)
        assertEquals(1, store.saves)
        store.unreadableCredentials.clear()
        assertTrue(engine(store).recover())
        assertEquals(listOf(SavedHub(id, "Sample Hub", issued)), store.durableRows)
    }

    @Test fun failedCredentialSaveDoesNotBlockANewHubOrRedeemTheOriginalToken() {
        val store = Store().apply { failCredential = true }
        var oldExchanges = 0
        fails { engine(store).add("Sample Hub", input) { oldExchanges++; issued } }
        store.restart()
        store.failCredential = false
        val next = input.copy(baseUrl = "http://other.local", token = "other-pairing-token")
        val nextIssued = next.copy(token = "other-issued-token")
        HubRegistration(store) { "22222222-2222-2222-2222-222222222222" }.add("Other", next) {
            assertEquals(next, it)
            nextIssued
        }
        assertEquals(1, oldExchanges)
        assertEquals(nextIssued, store.durableRows.single().config)
        assertTrue(store.durableJournal.isEmpty())
        assertFalse(id in store.credentials)
    }

    @Test fun pendingIssuedHubDoesNotGateDifferentNewHubAndRecoversIndependently() {
        val store = Store().apply { failPublish = true }
        fails { engine(store).add("Sample Hub", input) { issued } }
        store.restart()
        store.failPublish = false
        // The old Hub cannot publish while it duplicates the primary; the different Hub remains valid.
        store.primary = input.baseUrl
        val next = input.copy(baseUrl = "http://other.local", token = "other-pairing-token")
        HubRegistration(store) { "22222222-2222-2222-2222-222222222222" }.add("Other", next) { it.copy(token = "other-issued-token") }
        assertEquals(listOf("http://other.local"), store.durableRows.map { it.config?.baseUrl })
        assertEquals(id, store.durableJournal.single().id)
        store.primary = null
        assertTrue(engine(store).recover())
        assertEquals(setOf(input.baseUrl, next.baseUrl), store.durableRows.map { it.config?.baseUrl }.toSet())
        assertEquals(2, store.saves)
        assertTrue(store.durableJournal.isEmpty())
    }

    @Test fun unreadableOldHubDoesNotBlockNewPairingAndOtherRecovery() {
        val store = Store().apply { failPublish = true }
        fails { engine(store).add("Sample Hub", input) { issued } }
        store.restart()
        store.failPublish = false
        store.unreadableCredentials += id
        val next = input.copy(baseUrl = "http://other.local", token = "other-pairing-token")
        HubRegistration(store) { "22222222-2222-2222-2222-222222222222" }.add("Other", next) { it.copy(token = "other-issued-token") }
        assertEquals(id, store.durableJournal.single().id)
        assertEquals(next.baseUrl, store.durableRows.single().config?.baseUrl)
        store.unreadableCredentials.clear()
        assertTrue(engine(store).recover())
        assertEquals(2, store.durableRows.size)
    }

    @Test fun oneUnreadableJournalDoesNotPreventAnotherPendingHubFromRecovering() {
        val store = Store()
        val nextId = "22222222-2222-2222-2222-222222222222"
        val nextIssued = issued.copy(baseUrl = "http://other.local", token = "other-issued-token")
        store.journal = listOf(PendingHubRegistration(id, "Sample", input.baseUrl), PendingHubRegistration(nextId, "Other", nextIssued.baseUrl))
        store.durableJournal = store.journal
        store.credentials[id] = issued
        store.credentials[nextId] = nextIssued
        store.unreadableCredentials += id
        fails { engine(store).recover() }
        assertEquals(listOf(nextId), store.durableRows.map { it.id })
        assertEquals(listOf(id), store.durableJournal.map { it.id })
        assertEquals(issued, store.credentials[id])
    }

    @Test fun legacySingleEntryJournalMigratesWithoutPublishingCredentials() {
        val legacy = """{"id":"$id","name":"Sample","baseUrl":"${input.baseUrl}"}"""
        val pending = PendingHubJournal.decode(legacy)
        assertEquals(listOf(PendingHubRegistration(id, "Sample", input.baseUrl)), pending)
        val serialized = PendingHubJournal.encode(pending)
        assertEquals(pending, PendingHubJournal.decode(serialized))
        assertFalse(serialized.contains(input.token))
        assertFalse(serialized.contains(issued.token))
        assertTrue(PendingHubJournal.decode(null).isEmpty())
    }

    @Test fun publicationConflictsKeepIssuedCredentialsDiscoverableUntilConflictIsRemoved() {
        for (conflict in listOf("primary", "additional", "capacity")) {
            val store = Store().apply { failPublish = true }
            fails { engine(store).add("Sample Hub", input) { issued } }
            store.restart()
            store.failPublish = false
            when (conflict) {
                "primary" -> store.primary = input.baseUrl
                "additional" -> store.rows = listOf(SavedHub("other", "Other", issued))
                "capacity" -> store.rows = (1..3).map { SavedHub("other-$it", "Other", input.copy(baseUrl = "http://other$it.local")) }
            }
            repeat(2) {
                assertThrows(IllegalArgumentException::class.java) { engine(store).recover() }
                assertEquals(listOf(PendingHubRegistration(id, "Sample Hub", input.baseUrl, registrationInputKey(input))), store.durableJournal)
                assertEquals(issued, store.credentials[id])
            }
            store.primary = null
            store.rows = emptyList()
            assertTrue(engine(store).recover())
            assertEquals(listOf(SavedHub(id, "Sample Hub", issued)), store.durableRows)
            assertTrue(store.durableJournal.isEmpty())
            assertEquals(1, store.saves)
        }
    }

    @Test fun equivalentPrimaryAndAdditionalOriginsAreRejectedBeforeExchange() {
        for ((existing, entered) in listOf("https://hub.example" to "HTTPS://HUB.example:443/",
            "HTTP://HUB.local:80/" to "http://hub.local", "https://hub.example/base/" to "https://HUB.example:443/base")) {
            for (primary in listOf(true, false)) {
                val store = Store()
                if (primary) store.primary = existing
                else store.rows = listOf(SavedHub("other", "Other", input.copy(baseUrl = existing)))
                assertThrows(IllegalArgumentException::class.java) {
                    engine(store).add("Duplicate", input.copy(baseUrl = entered)) { throw AssertionError("Must not exchange duplicate") }
                }
                assertTrue(store.events.isEmpty())
            }
        }
    }

    @Test fun newRegistrationStoresTheCanonicalOriginInJournalCredentialAndDirectory() {
        val store = Store()
        val entered = input.copy(baseUrl = "HTTPS://HUB.example:443/")
        engine(store).add("Sample", entered) { candidate ->
            assertEquals("https://hub.example", candidate.baseUrl)
            assertEquals(candidate.baseUrl, store.durableJournal.single().baseUrl)
            candidate.copy(baseUrl = entered.baseUrl, token = issued.token, deviceId = issued.deviceId)
        }
        assertEquals("https://hub.example", store.credentials[id]?.baseUrl)
        assertEquals("https://hub.example", store.durableRows.single().config?.baseUrl)
    }

    @Test fun legacyNoncanonicalPendingOriginRecoversIndependentlyWithoutAnExchange() {
        val store = Store()
        val old = issued.copy(baseUrl = "https://HUB.example:443")
        store.credentials[id] = old
        store.journal = listOf(PendingHubRegistration(id, "Sample", old.baseUrl))
        store.durableJournal = store.journal
        assertTrue(engine(store).recover())
        assertEquals(old, store.durableRows.single().config)
        assertTrue(store.durableJournal.isEmpty())
        assertEquals(0, store.saves)
    }

    @Test fun newTokenNeverReusesOldPendingCredentialAndKeepsItDiscoverable() {
        for (legacy in listOf(false, true)) {
            val store = Store().apply { failPublish = true }
            fails { engine(store).add("Sample", input) { issued } }
            store.restart()
            store.failPublish = false
            if (legacy) store.journal = store.journal.map { it.copy(inputKey = null) }
            store.unreadableCredentials += id // The old account may also be unavailable.
            val next = input.copy(token = "new-one-time-token")
            val nextId = "22222222-2222-2222-2222-222222222222"
            var exchanges = 0
            HubRegistration(store) { nextId }.add("Sample", next) {
                assertEquals(next, it); exchanges++; it.copy(token = "new-issued-token")
            }
            assertEquals(1, exchanges)
            assertEquals(nextId, store.durableRows.single().id)
            assertEquals("new-issued-token", store.durableRows.single().config?.token)
            assertEquals(id, store.durableJournal.single().id)
            assertEquals(issued, store.credentials[id])
        }
    }

    @Test fun exactInputDigestIncludesIdentityButCanonicalOriginAliasesResume() {
        val config = input.copy(baseUrl = "HTTPS://HUB.example:443/", fingerprint = "a".repeat(43), deviceId = "sample-device")
        val key = registrationInputKey(config)
        assertEquals(key, registrationInputKey(config.copy(baseUrl = "https://hub.example")))
        for (changed in listOf(config.copy(token = "next-token"), config.copy(fingerprint = "b".repeat(43)),
            config.copy(deviceId = "other-device"), config.copy(baseUrl = "https://other.example"))) {
            assertNotEquals(key, registrationInputKey(changed))
        }
        val serialized = PendingHubJournal.encode(listOf(PendingHubRegistration(id, "Sample", config.baseUrl, key)))
        assertEquals(key, PendingHubJournal.decode(serialized).single().inputKey)
        assertFalse(serialized.contains(config.token))
        assertFalse(serialized.contains(config.deviceId!!))
        val store = Store().apply { failPublish = true }
        val candidate = input.copy(baseUrl = "HTTP://HUB.local:80/")
        fails { engine(store).add("Sample", candidate) { issued } }
        store.restart(); store.failPublish = false
        engine(store).add("Sample", input) { throw AssertionError("Same pairing input must resume") }
        assertEquals(issued, store.durableRows.single().config)
        assertEquals(1, store.saves)
    }

    @Test fun abortBeforeExchangeOrAfterExchangeNeverPublishesAndLeavesJournalDiscoverable() {
        val before = Store()
        fails { engine(before).add("Sample", input, record = { before.abortedIds += it.id }) {
            throw AssertionError("Aborted queued registration must not exchange")
        } }
        assertTrue(before.credentials.isEmpty())
        assertTrue(before.durableJournal.isEmpty())
        val during = Store()
        fails { engine(during).add("Sample", input) { during.abortedIds += id; issued } }
        assertEquals(id, during.durableJournal.single().id)
        assertTrue(during.credentials.isEmpty())
        assertTrue(during.durableRows.isEmpty())
    }

    @Test fun abortDuringPublicationValidationPreventsTheFinalDirectoryWrite() {
        val store = Store().apply { failPublish = true }
        fails { engine(store).add("Sample", input) { issued } }
        store.restart(); store.failPublish = false
        store.onPrimaryRead = { store.abortedIds += id }
        fails { engine(store).recover() }
        assertTrue(store.durableRows.isEmpty())
        assertEquals(id, store.durableJournal.single().id)
        assertEquals(issued, store.credentials[id])
    }

    private fun fullDirectoryAfterThirdRegistrationCleanupFailure(): Store {
        val store = Store()
        store.rows = listOf(
            SavedHub("22222222-2222-2222-2222-222222222222", "Second Hub", input.copy(baseUrl = "http://second.local", token = "second-issued-token")),
            SavedHub("33333333-3333-3333-3333-333333333333", "Third Hub", input.copy(baseUrl = "http://third.local", token = "third-issued-token")),
        )
        store.durableRows = store.rows
        store.rows.forEach { store.credentials[it.id] = checkNotNull(it.config) }
        store.primary = "http://primary.local"
        store.failCleanup = true
        fails { engine(store).add("Sample Hub", input) { store.events += "exchange"; issued } }
        assertEquals(3, store.durableRows.size)
        assertEquals(id, store.durableJournal.single().id)
        store.restart() // Failed preference cleanup changed memory but left the durable journal intact.
        store.failCleanup = false
        return store
    }

    @Test fun journalCleanupFailureAtThreeRowsRecoversRepeatedlyWithoutClaimingAFourthSlot() {
        val store = fullDirectoryAfterThirdRegistrationCleanupFailure()
        val originalRows = store.durableRows
        val originalCredentials = store.credentials.toMap()
        repeat(2) {
            store.failCleanup = true
            fails { engine(store).recover() }
            assertEquals(originalRows, store.durableRows)
            assertEquals(id, store.durableJournal.single().id)
            assertEquals(originalCredentials, store.credentials)
            store.restart()
        }
        store.failCleanup = false
        assertTrue(engine(store).recover())
        assertFalse(engine(store).recover())
        assertEquals(originalRows, store.durableRows)
        assertEquals(3, store.durableRows.map { it.id }.distinct().size)
        assertTrue(store.durableJournal.isEmpty())
        assertEquals(originalCredentials, store.credentials)
        assertEquals(1, store.saves)
        assertEquals(1, store.events.count { it == "exchange" })
    }

    @Test fun matchingPendingAddAtThreeRowsReusesItsSlotAndIssuedCredential() {
        val store = fullDirectoryAfterThirdRegistrationCleanupFailure()
        val originalRows = store.durableRows
        val originalCredentials = store.credentials.toMap()
        engine(store).add("Sample Hub", input.copy(baseUrl = "HTTP://HUB.local:80/")) {
            throw AssertionError("A pending third registration must not exchange its token again")
        }
        assertEquals(originalRows, store.durableRows)
        assertEquals(3, store.durableRows.map { it.id }.distinct().size)
        assertEquals(originalCredentials, store.credentials)
        assertTrue(store.durableJournal.isEmpty())
        assertFalse(engine(store).recover())
        assertEquals(1, store.saves)
        assertEquals(1, store.events.count { it == "exchange" })
    }

    @Test fun replayingAnOwnedSlotAtCapacityStillRejectsOtherOrPrimaryDuplicateOrigins() {
        for (replay in listOf("recover", "add")) for (duplicate in listOf("primary", "additional")) {
            val store = fullDirectoryAfterThirdRegistrationCleanupFailure()
            if (duplicate == "primary") store.primary = "HTTP://HUB.local:80/"
            else {
                store.rows = store.rows.map { row ->
                    if (row.id == id) row else if (row.id.startsWith("2222"))
                        row.copy(config = issued.copy(baseUrl = "HTTP://HUB.local:80/")) else row
                }
                store.rows.forEach { store.credentials[it.id] = checkNotNull(it.config) }
                store.durableRows = store.rows
            }
            val originalRows = store.durableRows
            val originalPending = store.durableJournal
            val originalCredentials = store.credentials.toMap()
            val originalPublications = store.publications
            assertThrows(IllegalArgumentException::class.java) {
                if (replay == "recover") engine(store).recover()
                else engine(store).add("Sample Hub", input) { throw AssertionError("Duplicate must not exchange") }
            }
            assertEquals(originalRows, store.durableRows)
            assertEquals(originalPending, store.durableJournal)
            assertEquals(originalCredentials, store.credentials)
            assertEquals(originalPublications, store.publications)
            assertEquals(1, store.saves)
        }
    }
}
