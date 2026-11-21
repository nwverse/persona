package dev.persona.watch.data

import org.junit.Assert.*
import org.junit.Test

class PinnedOriginTest {
    private val pinnedHttp = HubConfig("http://127.0.0.1:1", "sample-watch-token", "A".repeat(43))

    @Test fun aFinalConfigCannotSilentlyIgnoreAPinOnPlaintext() {
        assertEquals("https_required", assertThrows(HubException::class.java) { pinnedHttp.validated() }.code)
        assertEquals(pinnedHttp.fingerprint, pinnedHttp.copy(baseUrl = "https://hub.example").validated().fingerprint)
        assertNull(pinnedHttp.copy(fingerprint = null).validated().fingerprint)
    }

    @Test fun hubClientRejectsPinnedHttpBeforeAnInjectedTransportReceivesTheCredential() {
        var calls = 0
        val transport = HubTransport { _, _, _, _, _ -> calls++; HubResponse(200, "{}") }
        assertEquals("https_required", assertThrows(HubException::class.java) { HubClient(pinnedHttp, transport = transport).refresh() }.code)
        assertEquals(0, calls)
    }

    @Test fun theDirectJsonTransportAlsoFailsBeforeOpeningAConnection() {
        assertEquals("https_required", assertThrows(HubException::class.java) {
            UrlConnectionTransport().request(pinnedHttp, "GET", "/api/v1/health", null, emptyMap())
        }.code)
    }

    @Test fun binaryAssetsRejectPinnedHttpBeforeOpeningAConnection() {
        assertEquals("https_required", assertThrows(HubException::class.java) {
            AssetTransport().get(pinnedHttp, "/api/v1/pets/manifest")
        }.code)
    }
}
