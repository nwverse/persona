package dev.persona.watch.data

import dev.persona.watch.ActivityWork
import org.junit.Assert.*
import org.junit.Test
import java.net.URLEncoder

class PrimaryPairingTest {
    private val old = HubConfig("https://old.example", "sample-old", "A".repeat(43), "sample-device")
    private fun snapshot() = WatchSnapshot(emptyList(), emptyList(), emptyList(), emptyList(), 12)
    private fun PrimaryPairing.connect(request: PrimaryPairingRequest, pair: (HubConfig) -> HubConfig,
                                       refresh: (HubConfig) -> WatchSnapshot, save: (HubConfig) -> Unit): Pair<HubConfig, WatchSnapshot> {
        val verified = authenticate(request, pair, refresh)
        publish(verified, save)
        return verified.config to verified.snapshot
    }
    @Test fun invalidatedPairingCompletionCannotReplaceCredentialsButSameFormCanRetry() {
        val flow = PrimaryPairing()
        val work = ActivityWork()
        val ticket = work.beginMutation()!!
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-new")
        var saved = old
        val verified = flow.authenticate(request, { it.copy(token = "sample-issued") }, { snapshot() })
        work.connectionsChanged()
        if (work.finishMutation(ticket)) flow.publish(verified) { saved = it }
        assertEquals(old, saved)
        assertFalse(work.busy)
        val next = work.beginMutation()!!
        val retry = flow.authenticate(request, { throw AssertionError("Must retain issued credential") }, { snapshot() })
        if (work.finishMutation(next)) flow.publish(retry) { saved = it }
        assertEquals("sample-issued", saved.token)
    }
    @Test fun failedPublicationRetainsIssuedCredentialForRetry() {
        val flow = PrimaryPairing()
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-new")
        val verified = flow.authenticate(request, { it.copy(token = "sample-issued") }, { snapshot() })
        assertThrows(IllegalStateException::class.java) { flow.publish(verified) { throw IllegalStateException("Disk unavailable") } }
        val retry = flow.authenticate(request, { throw AssertionError("Must not redeem again") }, { snapshot() })
        var saved = old
        flow.publish(retry) { saved = it }
        assertEquals(verified.config, saved)
    }
    @Test fun changedOriginCannotReuseAnOldTokenOrDeviceId() {
        assertThrows(HubException::class.java) { PrimaryPairing.prepare(old, "https://new.example", "") }
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-new")
        assertFalse(request.reuseSaved)
        assertEquals("sample-new", request.config.token)
        assertNull(request.config.deviceId)
        assertNull(request.config.fingerprint)
    }
    @Test fun equivalentNormalizedAddressCanReuseSavedCredential() {
        val request = PrimaryPairing.prepare(old, "https://OLD.example:443/", "")
        assertTrue(request.reuseSaved)
        assertEquals(old, request.config)
    }
    @Test fun copiedLinkMustCarryItsOwnNonemptyTokenForANewOrigin() {
        val url = URLEncoder.encode("https://new.example", "UTF-8")
        for (suffix in listOf("", "&token=")) {
            assertThrows(HubException::class.java) { PrimaryPairing.prepare(old, "persona://pair?url=$url$suffix", "") }
        }
        val request = PrimaryPairing.prepare(old, "persona://pair?url=$url&token=sample-new", "")
        assertFalse(request.reuseSaved)
        assertEquals("sample-new", request.config.token)
        assertNull(request.config.deviceId)
    }
    @Test fun failedInitialAuthenticationPreservesOldConnectionAndRetryReusesIssuedCredential() {
        val flow = PrimaryPairing()
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-pairing")
        var saved = old
        var exchanges = 0
        val issued = request.config.copy(token = "sample-issued", deviceId = "sample-issued-device")
        assertThrows(HubException::class.java) {
            flow.connect(request, { exchanges++; issued }, { throw HubException("hub_unavailable") }, { saved = it })
        }
        assertEquals(old, saved)
        val result = flow.connect(request, { throw AssertionError("Do not redeem again") }, { snapshot() }, { saved = it })
        assertEquals(issued, result.first)
        assertEquals(issued, saved)
        assertEquals(1, exchanges)
    }
    @Test fun differentInputCannotReuseThePendingCredential() {
        val flow = PrimaryPairing()
        val first = PrimaryPairing.prepare(old, "https://first.example", "sample-first")
        assertThrows(HubException::class.java) { flow.connect(first, { it }, { throw HubException("unreachable") }, {}) }
        val next = PrimaryPairing.prepare(old, "https://second.example", "sample-second")
        var exchanged = false
        flow.connect(next, { exchanged = true; it.copy(token = "sample-issued") }, { snapshot() }, { assertEquals("https://second.example", it.baseUrl) })
        assertTrue(exchanged)
    }
    @Test fun failedPairingNeverPublishesOrReadsTheNewHub() {
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-new")
        assertThrows(HubException::class.java) {
            PrimaryPairing().connect(request, { throw HubException("pairing_expired") },
                { throw AssertionError("No refresh") }, { throw AssertionError("No replacement") })
        }
    }

    @Test fun additionalOriginIsRejectedBeforeTokenExchangeOrRefresh() {
        val aliases = listOf("https://hub.example", "HTTPS://HUB.example:443/", "https://Hub.Example/")
        for (saved in aliases) {
            for (address in aliases) {
                val flow = PrimaryPairing { listOf(null, "https://unrelated.example", saved) }
                val request = PrimaryPairing.prepare(old, address, "sample-once")
                val failure = assertThrows(HubException::class.java) {
                    flow.connect(request, { throw AssertionError("Must not consume the pairing token") },
                        { throw AssertionError("Must not read a duplicate Hub") }, { throw AssertionError("Must not replace the primary") })
                }
                assertEquals("duplicate_connection", failure.code)
            }
        }
    }

    @Test fun additionalOriginIsAlsoRejectedForPairingLinksAndSavedPrimaryReuse() {
        val flow = PrimaryPairing { listOf("HTTPS://OLD.example:443/") }
        val encoded = URLEncoder.encode(old.baseUrl, "UTF-8")
        val requests = listOf(PrimaryPairing.prepare(old, "persona://pair?url=$encoded&token=sample-once", ""),
            PrimaryPairing.prepare(old, old.baseUrl, ""))
        for (request in requests) {
            val failure = assertThrows(HubException::class.java) {
                flow.connect(request, { throw AssertionError("No exchange") },
                    { throw AssertionError("No refresh") }, { throw AssertionError("No publication") })
            }
            assertEquals("duplicate_connection", failure.code)
        }
    }

    @Test fun additionalOriginAddedAfterPreparationIsReadBeforeAuthentication() {
        var additional = emptyList<String?>()
        val flow = PrimaryPairing { additional }
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-once")
        additional = listOf("HTTPS://NEW.example:443/")
        val failure = assertThrows(HubException::class.java) {
            flow.authenticate(request, { throw AssertionError("Do not consume a token after the directory changes") },
                { throw AssertionError("No refresh") })
        }
        assertEquals("duplicate_connection", failure.code)
    }

    @Test fun publicationRereadsAdditionalOriginsAndKeepsIssuedCredentialForRetry() {
        var additional = emptyList<String?>()
        var reads = 0
        val flow = PrimaryPairing { reads++; additional }
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-once")
        var saved = old
        var exchanges = 0
        val verified = flow.authenticate(request, { exchanges++; it.copy(token = "sample-issued", deviceId = "sample-device-new") }, { snapshot() })
        additional = listOf("HTTPS://NEW.example:443/")
        val failure = assertThrows(HubException::class.java) { flow.publish(verified) { saved = it } }
        assertEquals("duplicate_connection", failure.code)
        assertEquals(old, saved)
        assertEquals(2, reads)
        // A retry while the conflict remains must not refresh or redeem again.
        assertThrows(HubException::class.java) {
            flow.authenticate(request, { throw AssertionError("No repeat exchange") }, { throw AssertionError("No duplicate refresh") })
        }
        additional = emptyList()
        val retry = flow.authenticate(request, { throw AssertionError("Reuse the issued credential") }, { snapshot() })
        flow.publish(retry) { saved = it }
        assertEquals(verified.config, saved)
        assertEquals(1, exchanges)
        assertEquals(5, reads)
    }

    @Test fun unrelatedAdditionalOriginsAndUnreadableEntriesDoNotBlockPrimaryPairing() {
        val flow = PrimaryPairing { listOf(null, "https://new.example:444", "https://other.example") }
        val request = PrimaryPairing.prepare(old, "https://new.example", "sample-once")
        var saved = old
        var exchanges = 0
        val result = flow.connect(request, { exchanges++; it.copy(token = "sample-issued") }, { snapshot() }, { saved = it })
        assertEquals(result.first, saved)
        assertEquals(1, exchanges)
    }
}
