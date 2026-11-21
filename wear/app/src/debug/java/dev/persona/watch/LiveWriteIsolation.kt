package dev.persona.watch

import dev.persona.watch.data.HubConfig

/** A live write probe supports exactly one primary Hub; session IDs alone are not cross-Hub identities. */
internal class LiveWriteIsolation(private val expected: HubConfig, private val fixtureToken: String) {
    fun verify(current: HubConfig?, additional: List<SavedHub>) {
        check(expected.token != fixtureToken && current == expected) { "live_ping_credential_changed" }
        check(additional.isEmpty()) { "live_ping_extra_connections_not_allowed" }
    }
}
