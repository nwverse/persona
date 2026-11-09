package persona.core.hub

import persona.core.contracts.Endpoint
import persona.core.contracts.api.ConnectionState
import java.net.InetAddress
import java.security.MessageDigest
import java.util.Base64
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
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

/** One connection for a group of hubs (docs/android/android.md "Multiple hubs"): route across the hubs, each with its own credential. */
class HubGroupConnectionTest {
    private val homeToken = "home-token"

    private fun group(
        home: Endpoint, homeId: String, vararg members: HubMember,
        recheckMillis: Long = OkHttpHubConnection.RECHECK_MILLIS, fingerprint: String? = null,
        revoked: MutableList<Pair<String, Boolean>> = CopyOnWriteArrayList(),
    ) = OkHttpHubConnection(
        HubConnectionConfig(homeId, fingerprint, homeToken, listOf(home), "test", members = members.toList()),
        HubNetwork("offline", available = false), recheckMillis = recheckMillis,
        onMemberRevoked = { hubId, own -> revoked += hubId to own },
    )

    private fun member(hub: LoopbackHub, token: String?, center: Boolean = false) =
        HubMember(hub.hubId, null, token, listOf(hub.endpoint("lan")), center = center)

    private suspend fun OkHttpHubConnection.awaitRoute(url: String, millis: Long = 8000) =
        withTimeout(millis) { route.first { it != null && endpointKey(it.endpoint.url) == endpointKey(url) } }!!

    private suspend fun OkHttpHubConnection.awaitOnline() = withTimeout(8000) { state.first { it == ConnectionState.Online } }

    private fun LoopbackHub.socketAuthorizations() = authorizations.filter { it.first == "/api/v1/ws" }.map { it.second }

    @Test fun whenItsHubIsDownTheGroupConnectsThroughAnotherWithThatHubsOwnCredential() = runBlocking<Unit> {
        LoopbackHub(healthy = false, hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { other ->
                group(home.endpoint("lan"), home.hubId, member(other, "b-token")).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitRoute(other.url)
                    connection.awaitOnline()
                    assertEquals("b-token", (connection.token).zhAny())
                    connection.request("/api/v1/ping").close()
                    assertEquals(listOf("Bearer b-token"), (other.socketAuthorizations()).zhAny())
                    assertEquals("Bearer b-token", (other.authorizations.last { it.first == "/api/v1/ping" }.second).zhAny())
                    // The home hub's health was asked first (it failed); it never saw a credential.
                    assertTrue(home.probes.get() > 0)
                    assertTrue(home.authorizations.all { it.second == null })
                    assertEquals(listOf(home.url, other.url), connection.allCandidates().map { endpointKey(it.url) })
                    assertEquals(listOf(home.url), connection.candidates().map { endpointKey(it.url) })
                }
            }
        }
    }

    @Test fun aHubPairedOnlyThroughTheMeshBorrowsTheGroupCredential() = runBlocking<Unit> {
        LoopbackHub(healthy = false, hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { other ->
                group(home.endpoint("lan"), home.hubId, member(other, null)).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitOnline()
                    assertEquals(homeToken, connection.token)
                    assertEquals(listOf("Bearer $homeToken"), (other.socketAuthorizations()).zhAny())
                }
            }
        }
    }

    @Test fun theRecheckMovesBackToTheHubListedFirstOnceItAnswers() = runBlocking<Unit> {
        LoopbackHub(healthy = false, hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { other ->
                group(home.endpoint("lan"), home.hubId, member(other, "b-token"), recheckMillis = 300).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitRoute(other.url)
                    home.healthy = true
                    connection.awaitRoute(home.url)
                    eventually { home.opened.get() > 0 }
                    connection.awaitOnline()
                    assertEquals(homeToken, connection.token)
                    assertEquals(listOf("Bearer $homeToken"), (home.socketAuthorizations()).zhAny())
                }
            }
        }
    }

    @Test fun whenBothCenterAndMemberAreAvailableConnectionPrefersCenter() = runBlocking<Unit> {
        LoopbackHub(hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { centerHub ->
                group(home.endpoint("lan"), home.hubId, member(centerHub, "b-token", center = true)).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitRoute(centerHub.url)
                    connection.awaitOnline()
                    assertEquals("b-token", (connection.token).zhAny())
                    assertFalse(connection.onMember())
                }
            }
        }
    }

    @Test fun whenConnectedToAMemberTheRecheckMovesBackToCenterOnceItAnswers() = runBlocking<Unit> {
        LoopbackHub(hubId = "hub-a").use { home ->
            LoopbackHub(healthy = false, hubId = "hub-b").use { centerHub ->
                group(home.endpoint("lan"), home.hubId, member(centerHub, "b-token", center = true), recheckMillis = 300).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitRoute(home.url)
                    connection.awaitOnline()
                    assertEquals(homeToken, connection.token)
                    assertTrue(connection.onMember())

                    centerHub.healthy = true
                    connection.awaitRoute(centerHub.url)
                    eventually { centerHub.opened.get() > 0 }
                    connection.awaitOnline()
                    assertEquals("b-token", (connection.token).zhAny())
                    assertFalse(connection.onMember())
                }
            }
        }
    }

    @Test fun whenCenterAnswersSlightlyLaterWithinGraceItIsPickedOverFastMember() = runBlocking<Unit> {
        LoopbackHub(hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { centerHub ->
                centerHub.answerMillis = 100
                group(home.endpoint("lan"), home.hubId, member(centerHub, "b-token", center = true)).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitRoute(centerHub.url)
                    connection.awaitOnline()
                    assertEquals("b-token", (connection.token).zhAny())
                    assertFalse(connection.onMember())
                }
            }
        }
    }

    @Test fun aHubRefusingItsCredentialIsDroppedAndTheGroupCarriesOn() = runBlocking<Unit> {
        val revoked = CopyOnWriteArrayList<Pair<String, Boolean>>()
        val states = ConcurrentLinkedQueue<ConnectionState>()
        LoopbackHub(healthy = false, hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { other ->
                group(home.endpoint("lan"), home.hubId, member(other, "b-token"), revoked = revoked).use { connection ->
                    val watcher = launchCollect(connection, states)
                    try {
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitOnline()
                    // A request refused by the hub in use drops that hub only.
                    other.refused = setOf("b-token")
                    val failure = runCatching { connection.request("/api/v1/ping") }.exceptionOrNull()
                    assertEquals(HubError.PairingExpired, (failure as HubFailure).kind)
                    eventually { revoked.isNotEmpty() }
                    assertEquals(listOf("hub-b" to true), (revoked.toList()).zhAny())
                    val sockets = other.sockets.get()
                    home.healthy = true
                    connection.awaitRoute(home.url)
                    connection.awaitOnline()
                    assertEquals(sockets, other.sockets.get())
                    assertFalse(ConnectionState.Revoked in states)
                    } finally { watcher.cancel() }
                }
            }
        }
    }

    @Test fun theConnectionIsRevokedOnlyWhenNoHubOfTheGroupIsLeft() = runBlocking<Unit> {
        val revoked = CopyOnWriteArrayList<Pair<String, Boolean>>()
        LoopbackHub(hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { other ->
                LoopbackHub(hubId = "hub-c").use { third ->
                    home.refused = setOf(homeToken)
                    other.refused = setOf("b-token")
                    third.refused = setOf(homeToken)
                    group(home.endpoint("lan"), home.hubId, member(other, "b-token"), member(third, null), revoked = revoked).use { connection ->
                        connection.updateNetwork(HubNetwork("network"))
                        withTimeout(10_000) { connection.state.first { it == ConnectionState.Revoked } }
                        // The last refusal publishes Revoked first and reports its hub just after, outside the lock;
                        // wait for that report instead of assuming it already arrived.
                        eventually { revoked.size >= 3 }
                        assertEquals(listOf("hub-a" to true, "hub-b" to true, "hub-c" to false), (revoked.toList()).zhAny())
                        assertEquals(HubError.PairingExpired, connection.failure.value?.kind)
                        // Revoked is final: nothing is tried again.
                        val sockets = listOf(home, other, third).sumOf { it.sockets.get() }
                        connection.reconnect()
                        delay(300)
                        assertEquals(sockets, listOf(home, other, third).sumOf { it.sockets.get() })
                    }
                }
            }
        }
    }

    @Test fun aDroppedHubComesBackOnlyWhenTheGroupGivesItAgainChanged() = runBlocking<Unit> {
        val revoked = CopyOnWriteArrayList<Pair<String, Boolean>>()
        LoopbackHub(healthy = false, hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-b").use { other ->
                other.refused = setOf("old-token")
                val stale = member(other, "old-token")
                group(home.endpoint("lan"), home.hubId, stale, revoked = revoked).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    eventually { revoked.isNotEmpty() }
                    connection.updateMembers(listOf(stale))
                    delay(500)
                    assertNull(connection.route.value)
                    connection.updateMembers(listOf(member(other, "new-token")))
                    connection.awaitRoute(other.url)
                    connection.awaitOnline()
                    assertEquals("new-token", (connection.token).zhAny())
                }
            }
        }
    }

    @Test fun anAddressAnsweringForAnotherHubIsNeverUsed() = runBlocking<Unit> {
        LoopbackHub(healthy = false, hubId = "hub-a").use { home ->
            LoopbackHub(hubId = "hub-c").use { stranger ->
                val listedAsB = HubMember("hub-b", null, "b-token", listOf(stranger.endpoint("lan")))
                group(home.endpoint("lan"), home.hubId, listedAsB).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    fun reported() = connection.diagnostics.value.candidates.firstOrNull { endpointKey(it.url) == stranger.url }?.result
                    eventually { reported().let { it != null && it.zh() != "未实测" } }
                    assertEquals("不是配对的那台 hub", (reported()).zhAny())
                    assertEquals(0, stranger.sockets.get())
                    // An identity mismatch stops the attempts as it does for a single hub, until the next reconnect.
                    home.healthy = true
                    connection.reconnect()
                    connection.awaitRoute(home.url)
                    assertEquals(0, stranger.sockets.get())
                }
            }
        }
    }

    @Test fun anotherHubsCandidatesNeverReplaceThisHubsOwn() = runBlocking<Unit> {
        val extra = """{"url":"https://hub-b.example.com:18662","kind":"name"}"""
        LoopbackHub(healthy = false, hubId = "hub-a").use { home ->
            lateinit var other: LoopbackHub
            other = LoopbackHub(hubId = "hub-b", endpoints = { "[${other.wire("lan")},$extra]" })
            other.use {
                group(home.endpoint("lan"), home.hubId, member(other, "b-token")).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitOnline()
                    eventually { connection.memberEndpoints.value["hub-b"] != null }
                    assertEquals(listOf(other.url, "https://hub-b.example.com:18662"),
                        connection.memberEndpoints.value.getValue("hub-b").map { endpointKey(it.url) })
                    assertEquals(listOf(home.url), connection.endpoints.value.map { endpointKey(it.url) })
                    assertTrue(connection.advertised.value.isEmpty())
                    assertTrue("https://hub-b.example.com:18662" in connection.allCandidates().map { endpointKey(it.url) })
                    // An endpoints event from that hub updates only its list too.
                    other.advertise("[${other.wire("lan")}]")
                    eventually { connection.memberEndpoints.value.getValue("hub-b").size == 1 }
                    assertEquals(listOf(home.url), connection.endpoints.value.map { endpointKey(it.url) })
                }
            }
        }
    }

    @Test fun anotherHubsPinCannotStandInForThisHub() = runBlocking<Unit> {
        val homeCertificate = HeldCertificate.Builder().commonName("example.com").build()
        val otherCertificate = HeldCertificate.Builder().commonName("example.com").build()
        // Presents the home hub's certificate (trusted: it is a pin of the group) but claims to be hub-b.
        tlsHub(homeCertificate, "hub-b").use { impostor ->
            tlsHub(otherCertificate, "hub-b").use { real ->
                val other = HubMember("hub-b", fingerprint(otherCertificate), "b-token", listOf(endpoint(impostor), endpoint(real)))
                val down = Endpoint("http://${InetAddress.getLoopbackAddress().hostAddress}:${closedPort()}/", "lan")
                group(down, "hub-a", other, fingerprint = fingerprint(homeCertificate)).use { connection ->
                    connection.updateNetwork(HubNetwork("network"))
                    connection.awaitRoute(endpoint(real).url)
                    connection.awaitOnline()
                    assertEquals("证书指纹不符",
                        (connection.diagnostics.value.candidates.single { endpointKey(it.url) == endpointKey(endpoint(impostor).url) }.result).zhAny())
                    assertEquals(fingerprint(otherCertificate), connection.diagnostics.value.fingerprint)
                    assertEquals(0, impostor.requests.count { it.requestUrl!!.encodedPath == "/api/v1/ws" })
                    // Without the group, the other hub's pin is not trusted at all.
                    HubHttpClient("hub-b", fingerprint(homeCertificate), "test").use { single ->
                        assertEquals(HubError.CertificateMismatch,
                            runCatching { single.health(endpoint(real)) }.exceptionOrNull()?.let { HubFailure.from(it).kind })
                    }
                }
            }
        }
    }

    private fun CoroutineScope.launchCollect(connection: OkHttpHubConnection, into: MutableCollection<ConnectionState>) =
        launch(Dispatchers.IO) { connection.state.collect { into += it } }

    private class TlsHub(val server: MockWebServer) : AutoCloseable {
        val requests = ConcurrentLinkedQueue<RecordedRequest>()
        override fun close() = server.close()
    }

    private fun tlsHub(certificate: HeldCertificate, hubId: String): TlsHub {
        val server = MockWebServer()
        val hub = TlsHub(server)
        server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                hub.requests += request
                return when (request.requestUrl!!.encodedPath) {
                    "/api/v1/health" -> MockResponse().setBody("""{"ok":true,"hub_id":"$hubId"}""")
                    "/api/v1/ws" -> MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                        override fun onOpen(webSocket: WebSocket, response: Response) {
                            webSocket.send("""{"type":"hello","hub_id":"$hubId","features":[]}""")
                        }
                    })
                    else -> MockResponse().setResponseCode(404)
                }
            }
        }
        server.start(InetAddress.getLoopbackAddress(), 0)
        return hub
    }

    private fun endpoint(hub: TlsHub) = Endpoint(
        hub.server.url("/").newBuilder().host(InetAddress.getLoopbackAddress().hostAddress!!).build().toString(), "lan",
    )

    private fun fingerprint(certificate: HeldCertificate) = Base64.getUrlEncoder().withoutPadding().encodeToString(
        MessageDigest.getInstance("SHA-256").digest(certificate.certificate.publicKey.encoded),
    )
}
