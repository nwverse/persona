package dev.persona.watch.data

import org.junit.Assert.*
import org.junit.Test
import java.net.URLEncoder

class PairingInputTest {
    private val pin = "A".repeat(43)
    private fun link(url: String = "http://hub.local:18660", suffix: String = "") =
        "persona://pair?url=${URLEncoder.encode(url, "UTF-8")}&token=sample-token&fp=$pin&tls=18662$suffix"
    @Test fun upgradesOnlyTheExplicitLanOriginAndKeepsItsPin() {
        val config = PairingInput.parse(link(suffix = "&alt=https%3A%2F%2Fignored.example"), "")
        assertEquals("https://hub.local:18662", config.baseUrl)
        assertEquals(pin, config.fingerprint)
        assertEquals("sample-token", config.token)
    }
    @Test fun plainAddressStillUsesTheSuppliedPairingToken() {
        assertEquals("http://hub.local", PairingInput.parse("http://hub.local", "sample-token").baseUrl)
    }
    @Test fun rejectsConflictingCredentialsDuplicateQueriesAndHiddenAuthority() {
        for ((value, token) in listOf(link() to "different", link(suffix = "&token=other") to "",
            link("http://user@hub.local") to "", link("http://hub.local/?next=other") to "", link("http://hub.local/#fragment") to "")) {
            try { PairingInput.parse(value, token); fail("Invalid link accepted") } catch (_: HubException) { }
        }
    }
    @Test fun neverDowngradesAPinnedLinkWithoutATlsPort() {
        try { PairingInput.parse(link().replace("&tls=18662", ""), ""); fail("Pinned link was sent over plaintext") } catch (_: HubException) { }
    }
    @Test fun pinnedRemoteHttpsNeedsNoUpgradePort() {
        val config = PairingInput.parse(link("https://hub.example.com:8443/prefix").replace("&tls=18662", ""), "")
        assertEquals("https://hub.example.com:8443/prefix", config.baseUrl)
        assertEquals(pin, config.fingerprint)
        assertEquals("sample-token", config.token)
    }
    @Test fun anHttpsOriginIsNotRewrittenByAnHttpUpgradeHint() {
        val config = PairingInput.parse(link("https://hub.example.com:8443"), "")
        assertEquals("https://hub.example.com:8443", config.baseUrl)
        assertEquals(pin, config.fingerprint)
    }
}
