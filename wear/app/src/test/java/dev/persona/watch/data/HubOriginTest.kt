package dev.persona.watch.data

import org.junit.Assert.*
import org.junit.Test

class HubOriginTest {
    @Test fun newEnrollmentNormalizationIsSharedByPairingAndDuplicateIdentity() {
        for ((address, expected) in listOf("HTTPS://HUB.example:443/" to "https://hub.example",
            "HTTP://HUB.local:80/" to "http://hub.local", "HTTP://[FE80::1]:80/" to "http://[fe80::1]",
            "https://HUB.example:8443/base/" to "https://hub.example:8443/base")) {
            assertEquals(expected, HubConfig(address, "sample-token").canonicalized().baseUrl)
            assertEquals(expected, PairingInput.parse(address, "sample-token").baseUrl)
            assertEquals(expected, PrimaryPairing.prepare(null, address, "sample-token").config.baseUrl)
            assertTrue(HubConfig.sameOrigin(address, expected))
        }
        assertFalse(HubConfig.sameOrigin("https://hub.example:8443", "https://hub.example"))
        assertFalse(HubConfig.sameOrigin("https://hub.example/base", "https://hub.example/other"))
    }

    @Test fun validatingALegacyCredentialPreservesItsDraftAndCacheIdentity() {
        val saved = HubConfig("https://HUB.example:443", "sample-token", "A".repeat(43), "sample-device")
        assertEquals(saved, saved.validated())
        assertEquals(saved, PrimaryPairing.prepare(saved, "HTTPS://hub.example/", "").config)
        val inboxKey = "inbox:manager:sample"
        assertEquals(DraftScope.ownedHash(saved.baseUrl, "", inboxKey), DraftScope.ownedHash(saved.validated().baseUrl, "", inboxKey))
        assertEquals("https://hub.example", saved.canonicalized().baseUrl)
        assertTrue(HubConfig.sameOrigin(saved.baseUrl, saved.canonicalized().baseUrl))
    }

    @Test fun normalizationDoesNotStripInvalidComponentsOrRelaxTransportValidation() {
        for (address in listOf("https://user@hub.example/", "https://hub.example/?query=value", "https://hub.example/#fragment",
            "https://hub.example/a/../b", "https://hub.example/%2e%2e", "HTTP://remote.example:80/")) {
            assertThrows(HubException::class.java) { HubConfig(address, "sample-token").validated() }
            assertFalse(HubConfig.sameOrigin(address, "https://hub.example"))
        }
        assertThrows(HubException::class.java) { HubConfig("HTTP://HUB.local:80/", "sample-token", "A".repeat(43)).validated() }
    }

    @Test fun uppercaseHttpPairingOriginUpgradesBeforePinValidation() {
        val config = PairingInput.parse("persona://pair?url=HTTP%3A%2F%2FHUB.local%3A80%2F&token=sample-token&fp=${"A".repeat(43)}&tls=443", "")
        assertEquals("https://hub.local", config.baseUrl)
        assertEquals("A".repeat(43), config.fingerprint)
    }
}
