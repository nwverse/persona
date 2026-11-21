package dev.persona.watch

import dev.persona.watch.data.HubConfig

/** Shared by every fixture UI scenario, and rechecked before each navigation/write/wait step. */
internal class FixtureIsolation(private val expected: HubConfig, private val fixtureToken: String) {
    fun verify(current: HubConfig?, additional: List<SavedHub>) {
        check(expected.token == fixtureToken && current == expected) { "fixture_credential_changed" }
        check(additional.isEmpty()) { "fixture_extra_connections_not_allowed" }
    }
}
