package persona.core.hub

import java.io.ByteArrayOutputStream
import java.net.InetAddress
import java.security.MessageDigest
import java.util.concurrent.ConcurrentLinkedQueue
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Endpoint
import persona.core.contracts.api.AppUpdate
import persona.core.contracts.api.ConnectionState

class HubAppDownloadTest {
    private val apk = "verified update bytes"
    private val update = AppUpdate(2, "0.2", "/api/v1/app/android/apk", digest(apk))

    private class Hub(val id: String = "update-hub", val prefix: String = "", certificate: HeldCertificate? = null) : AutoCloseable {
        @Volatile var healthy = true
        @Volatile var apkResponse: () -> MockResponse = { MockResponse().setBody("verified update bytes") }
        val requests = ConcurrentLinkedQueue<RecordedRequest>()
        val server = MockWebServer().apply {
            if (certificate != null) useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    requests += request
                    val path = request.path!!.substringBefore('?').removePrefix(prefix)
                    return when (path) {
                        "/api/v1/health" -> if (healthy) MockResponse().setBody("""{"ok":true,"hub_id":"$id"}""")
                            else MockResponse().setResponseCode(503)
                        "/api/v1/ws" -> MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                            override fun onOpen(webSocket: WebSocket, response: Response) {
                                webSocket.send("""{"type":"hello","hub_id":"$id","features":[]}""")
                            }
                        })
                        "/api/v1/app/android/apk" -> apkResponse()
                        else -> MockResponse().setResponseCode(404)
                    }
                }
            }
            start(InetAddress.getLoopbackAddress(), 0)
        }
        val address get() = server.url(prefix.ifEmpty { "/" }).toString()
        val endpoint get() = Endpoint(address, "lan")
        val downloads get() = requests.filter { it.path!!.substringBefore('?').endsWith("/android/apk") }
        override fun close() = server.close()
    }

    @Test fun aStandaloneClientChecksLocalHttpIdentityWithoutATokenThenDownloadsUnderTheProxyPrefix() = runBlocking {
        Hub(prefix = "/persona").use { hub ->
            HubPairingClient(hub.id, null, listOf(hub.address), "test").use { client ->
                val output = ByteArrayOutputStream()
                client.download("paired-token", update.copy(downloadUrl = update.downloadUrl + "?version=2"), output)
                assertEquals(apk, output.toString())
                assertEquals(listOf("/persona/api/v1/health", "/persona/api/v1/app/android/apk?version=2"), hub.requests.map { it.path })
                assertNull(hub.requests.first().getHeader("Authorization"))
                assertEquals("Bearer paired-token", hub.downloads.single().getHeader("Authorization"))
            }
        }
    }

    @Test fun publicPlaintextAndAWrongLocalHubAreRejectedBeforeAnApkReceivesCredentials() = runBlocking {
        HubPairingClient("update-hub", null, listOf("http://192.0.2.1/"), "test").use { client ->
            val failure = assertThrows(HubFailure::class.java) { runBlocking { client.download("must-not-leak", update, ByteArrayOutputStream()) } }
            assertEquals(HubError.TlsRequired, failure.kind)
        }
        Hub(id = "another-hub").use { hub ->
            HubPairingClient("update-hub", null, listOf(hub.address), "test").use { client ->
                val failure = assertThrows(HubFailure::class.java) { runBlocking { client.download("must-not-leak", update, ByteArrayOutputStream()) } }
                assertEquals(HubError.HubMismatch, failure.kind)
                assertTrue(hub.downloads.isEmpty())
                assertTrue(hub.requests.all { it.getHeader("Authorization") == null })
            }
        }
    }

    @Test fun localHttpStillRejectsAnotherOriginProxyEscapeCredentialsAndFragments() = runBlocking {
        Hub(prefix = "/persona").use { hub ->
            Hub().use { foreign ->
                HubPairingClient(hub.id, null, listOf(hub.address), "test").use { client ->
                    for (url in listOf(foreign.address + "api/v1/app/android/apk",
                        hub.server.url("/outside.apk").toString(), "/../outside.apk", "/%2e%2e/outside.apk",
                        hub.server.url("/persona/api/v1/app/android/apk").newBuilder().username("other").build().toString(),
                        "/api/v1/app/android/apk#fragment")) {
                        assertThrows("Must reject $url", IllegalArgumentException::class.java) {
                            runBlocking { client.download("must-not-leak", update.copy(downloadUrl = url), ByteArrayOutputStream()) }
                        }
                    }
                    assertTrue(hub.downloads.isEmpty())
                    assertTrue(hub.requests.all { it.getHeader("Authorization") == null })
                    assertEquals(0, foreign.server.requestCount)
                }
            }
        }
    }

    @Test fun localHttpRejectsRedirectsAndTheWrongDigest() = runBlocking {
        Hub().use { hub ->
            Hub().use { foreign ->
                HubPairingClient(hub.id, null, listOf(hub.address), "test").use { client ->
                    hub.apkResponse = { MockResponse().setResponseCode(302).setHeader("Location", foreign.address + "apk") }
                    val failure = assertThrows(HubFailure::class.java) { runBlocking { client.download("paired-token", update, ByteArrayOutputStream()) } }
                    assertEquals(302, failure.status)
                    assertEquals(0, foreign.server.requestCount)
                    hub.apkResponse = { MockResponse().setBody("modified apk") }
                    val digestFailure = assertThrows(HubFailure::class.java) { runBlocking { client.download("paired-token", update, ByteArrayOutputStream()) } }
                    assertEquals("apk_checksum_mismatch", digestFailure.code)
                    assertFalse(digestFailure.retryable)
                    assertTrue(hub.downloads.all { it.getHeader("Authorization") == "Bearer paired-token" })
                }
            }
        }
    }

    @Test fun aPinnedHttpsDownloadStillRejectsADifferentKeyBeforeSendingTheToken() = runBlocking {
        val actual = HeldCertificate.Builder().commonName("actual").build()
        val paired = HeldCertificate.Builder().commonName("paired").build()
        Hub(certificate = actual).use { hub ->
            HubPairingClient(hub.id, spkiFingerprint(paired.certificate), listOf(hub.address), "test").use { client ->
                val failure = assertThrows(HubFailure::class.java) { runBlocking { client.download("must-not-leak", update, ByteArrayOutputStream()) } }
                assertEquals(HubError.CertificateMismatch, failure.kind)
                assertEquals(0, hub.server.requestCount)
            }
        }
    }

    @Test fun boundLocalDownloadsUseTheLiveCredentialAndRejectAnOldOriginAfterRouteChanges() = runBlocking {
        Hub(prefix = "/first").use { first ->
            Hub(prefix = "/second").use { second ->
                OkHttpHubConnection(HubConnectionConfig(first.id, null, "live-token", listOf(first.endpoint), "test"),
                    HubNetwork("online")).use { connection ->
                    withTimeout(5_000) { connection.state.first { it == ConnectionState.Online } }
                    HubPairingClient(connection).use { client ->
                        client.download("stale-token", update, ByteArrayOutputStream())
                        assertEquals("Bearer live-token", first.downloads.single().getHeader("Authorization"))
                        val original = connection.activeRoute()
                        connection.updateEndpoints(listOf(second.endpoint))
                        withTimeout(5_000) { connection.route.first { it?.endpoint == second.endpoint } }
                        val mismatch = assertThrows(HubFailure::class.java) {
                            runBlocking { connection.request(update.downloadUrl, expectedRoute = original).close() }
                        }
                        assertEquals(HubError.NoNetwork, mismatch.kind)
                        assertThrows(IllegalArgumentException::class.java) {
                            runBlocking { client.download("stale-token", update.copy(downloadUrl = first.server.url("/first/api/v1/app/android/apk").toString()), ByteArrayOutputStream()) }
                        }
                        assertTrue(second.downloads.isEmpty())
                        val output = ByteArrayOutputStream()
                        client.download("stale-token", update, output)
                        assertEquals(apk, output.toString())
                        assertEquals("/second/api/v1/app/android/apk", second.downloads.single().path)
                        assertEquals("Bearer live-token", second.downloads.single().getHeader("Authorization"))
                    }
                }
            }
        }
    }

    @Test fun aMeshOwnerPublicationRaceNeverFallsBackToSendingTheHomeCredentialToAMember() = runBlocking {
        Hub(id = "home").use { home ->
            home.healthy = false
            Hub(id = "member").use { member ->
                val other = HubMember(member.id, null, "member-token", listOf(member.endpoint))
                OkHttpHubConnection(HubConnectionConfig(home.id, null, "home-secret", listOf(home.endpoint), "test", members = listOf(other)),
                    HubNetwork("online")).use { connection ->
                    withTimeout(5_000) { connection.state.first { it == ConnectionState.Online } }
                    HubPairingClient(connection).use { client ->
                        client.download("stale-token", update, ByteArrayOutputStream())
                        assertEquals("Bearer member-token", member.downloads.single().getHeader("Authorization"))
                    }
                    val selected = connection.activeRoute()
                    val field = OkHttpHubConnection::class.java.getDeclaredField("routeOwner").apply { isAccessible = true }
                    val previous = field.get(connection) as Pair<*, *>
                    // connectLoop publishes the new immutable route/owner pair before publishing its route StateFlow.
                    field.set(connection, HubRoute(home.endpoint, selected.health) to previous.second)
                    try {
                        val failure = assertThrows(HubFailure::class.java) {
                            runBlocking { connection.request(update.downloadUrl, expectedRoute = selected).close() }
                        }
                        assertEquals(HubError.NoNetwork, failure.kind)
                        assertEquals(1, member.downloads.size)
                        assertTrue(member.requests.none { it.getHeader("Authorization") == "Bearer home-secret" })
                    } finally { field.set(connection, previous) }
                }
            }
        }
    }

    private fun digest(text: String) = MessageDigest.getInstance("SHA-256").digest(text.toByteArray()).joinToString("") { "%02x".format(it) }
}
