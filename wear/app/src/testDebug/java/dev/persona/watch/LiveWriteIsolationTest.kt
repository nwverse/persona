package dev.persona.watch

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Test

class LiveWriteIsolationTest {
    private val config = HubConfig("https://primary.example.com", "sample-live-watch")
    private val guard = LiveWriteIsolation(config, "sample-fixture-only")

    @Test fun additionalHubIsRejectedBeforeResolvingEvenAnIdenticalSessionId() {
        val primaryIds = listOf("same-session")
        val extraIds = listOf("same-session")
        var resolutions = 0
        val error = assertThrows(IllegalStateException::class.java) {
            guard.verify(config, listOf(SavedHub("extra", "Other", config.copy(baseUrl = "https://extra.example.com"))))
            resolutions++
            check(primaryIds.single() == extraIds.single())
        }
        assertEquals("live_ping_extra_connections_not_allowed", error.message)
        assertEquals(0, resolutions)
    }

    @Test fun addingAnUnavailableConnectionDuringTheRunBlocksTheNextWrite() {
        guard.verify(config, emptyList())
        var writes = 0
        assertThrows(IllegalStateException::class.java) {
            guard.verify(config, listOf(SavedHub("extra", "Other", null)))
            writes++
        }
        assertEquals(0, writes)
    }

    @Test fun removedOrReplacedPrimaryAndFixtureCredentialsAreRejected() {
        for (current in listOf(null, config.copy(token = "sample-replacement"), config.copy(baseUrl = "https://other.example.com"))) {
            assertThrows(IllegalStateException::class.java) { guard.verify(current, emptyList()) }
        }
        assertThrows(IllegalStateException::class.java) {
            LiveWriteIsolation(config, config.token).verify(config, emptyList())
        }
    }

    @Test fun unchangedPrimaryWithoutAdditionalConnectionsIsAllowed() {
        guard.verify(config, emptyList())
    }
}
