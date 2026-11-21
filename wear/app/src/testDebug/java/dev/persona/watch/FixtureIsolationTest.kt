package dev.persona.watch

import dev.persona.watch.data.HubConfig
import org.junit.Assert.*
import org.junit.Test

class FixtureIsolationTest {
    private val token = "sample-fixture-only"
    private val fixture = HubConfig("http://localhost", token)

    @Test fun inboxAndRecoveryFailAtTheSharedEntryBeforeAnyActionWithAnAdditionalHub() {
        for (scenario in listOf("inbox", "recovery", "question", "nonvoice")) {
            var actions = 0
            val guard = FixtureIsolation(fixture, token)
            val error = assertThrows(IllegalStateException::class.java) {
                guard.verify(fixture, listOf(SavedHub("extra", "Sample extra", null)))
                actions++
            }
            assertEquals(scenario, "fixture_extra_connections_not_allowed", error.message)
            assertEquals(0, actions)
        }
    }

    @Test fun aConnectionAddedDuringTheRunFailsBeforeTheNextWrite() {
        val guard = FixtureIsolation(fixture, token)
        guard.verify(fixture, emptyList())
        var actions = 0
        assertThrows(IllegalStateException::class.java) {
            guard.verify(fixture, listOf(SavedHub("extra", "Sample extra", fixture.copy(baseUrl = "http://other.local"))))
            actions++
        }
        assertEquals(0, actions)
    }

    @Test fun replacingOrRemovingThePrimaryInvalidatesTheGuard() {
        val guard = FixtureIsolation(fixture, token)
        for (current in listOf(null, fixture.copy(baseUrl = "http://other.local"), fixture.copy(token = "different"))) {
            assertEquals("fixture_credential_changed", assertThrows(IllegalStateException::class.java) {
                guard.verify(current, emptyList())
            }.message)
        }
    }

    @Test fun onlyTheUnchangedFixtureWithoutAdditionalConnectionsPasses() {
        FixtureIsolation(fixture, token).verify(fixture, emptyList())
        assertThrows(IllegalStateException::class.java) { FixtureIsolation(fixture, "another-fixture-token").verify(fixture, emptyList()) }
    }
}
