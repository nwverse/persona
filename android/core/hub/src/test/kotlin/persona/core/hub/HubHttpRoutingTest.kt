package persona.core.hub

import persona.core.contracts.Endpoint
import persona.core.contracts.HubScope
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import java.io.ByteArrayOutputStream
import java.net.InetAddress
import java.security.MessageDigest
import java.util.Base64
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.*
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.first
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

class HubHttpRoutingTest {
    private val session = SessionKey("machine", "session/encoded")
    private val pairing = HubScope("test-hub", "generation")
    private val update = AppUpdate(1, "test", "/api/v1/app/android/apk", sha256("apk".toByteArray()))

    @Test fun compatibilityAuthenticationFailureIsTerminal() = runBlocking {
        OkHttpHubConnection(HubConnectionConfig("test-hub", null, "test-token",
            listOf(Endpoint("https://example.com", "name")), "test"),
            HubNetwork("offline", available = false)).use { connection ->
            connection.revoke()
            connection.reconnect()
            connection.updateNetwork(HubNetwork("changed"))
            assertEquals(ConnectionState.Revoked, connection.state.value)
            assertNull(connection.route.value)
            try {
                connection.request("/api/v1/interactions")
                fail("Revoked requests must fail locally")
            } catch (error: HubFailure) {
                assertEquals(HubError.PairingExpired, error.kind)
                assertFalse(error.retryable)
            }
        }
    }

    @Test fun allHttpTransportsFollowEndpointAndNetworkChanges() = runBlocking {
        val certificate = HeldCertificate.Builder().commonName("example.com").build()
        val firstRequests = Channel<RecordedRequest>(Channel.UNLIMITED)
        val secondRequests = Channel<RecordedRequest>(Channel.UNLIMITED)
        server(certificate, firstRequests).use { first ->
            server(certificate, secondRequests).use { second ->
                val firstEndpoint = endpoint(first)
                val secondEndpoint = endpoint(second)
                val jobs = CoroutineScope(SupervisorJob() + Dispatchers.Default)
                lateinit var sender: HttpMessageTransport
                connection(certificate, listOf(firstEndpoint)) { sender.onReady() }.use { connection ->
                    try {
                        sender = HttpMessageTransport(pairing, connection, jobs)
                        val chat = HubChatTransport(connection)
                        val inbox = HttpInboxAssistantTransport("scope", connection)
                        HubPairingClient(connection).use { settings ->
                            connection.updateNetwork(HubNetwork("first"))

                            suspend fun verify(target: Endpoint, requests: Channel<RecordedRequest>) {
                                withTimeout(5000) { connection.route.first { it?.endpoint == target } }
                                withTimeout(5000) { sender.readyToSend.first { it } }
                                chat.items(session, ItemsQuery(afterSeq = 2))
                                assertArrayEquals("media".toByteArray(), chat.image(session, "item"))
                                inbox.inbox()
                                inbox.reply("item/encoded", null, "reply", "client-id")
                                inbox.messages("feed/encoded", around = 3, before = null, after = null)
                                inbox.assistant()
                                assertArrayEquals("media".toByteArray(), inbox.media("/api/v1/lark/media/image?size=120"))
                                settings.json("/api/v1/devices/test/sessions", "stale-token")
                                assertEquals(update, settings.checkUpdate("stale-token"))
                                val apk = ByteArrayOutputStream()
                                settings.download("stale-token", update, apk)
                                assertEquals("apk", (apk.toString()).zhAny())
                                assertEquals(MessageTransportResult.Accepted, sender.send(message()))
                                val received = withTimeout(5000) { List(11) { requests.receive() } }
                                assertTrue(received.all { it.getHeader("Authorization") == "Bearer test-token" })
                                assertEquals("/api/v1/sessions/machine/session%2Fencoded/items?lazy_images=1&after_seq=2", (received[0].path).zhAny())
                                assertEquals("/api/v1/inbox/item%2Fencoded/reply", (received[3].path).zhAny())
                                assertEquals("/api/v1/lark/conversations/feed%2Fencoded/messages?around=3", (received[4].path).zhAny())
                                assertEquals("/api/v1/lark/media/image?size=120", (received[6].path).zhAny())
                                assertEquals("stable-id", (received.last().getHeader("X-Persona-Op-Id")).zhAny())
                            }

                            verify(firstEndpoint, firstRequests)
                            connection.updateEndpoints(listOf(secondEndpoint))
                            verify(secondEndpoint, secondRequests)
                            connection.updateEndpoints(listOf(firstEndpoint, secondEndpoint))
                            connection.updateNetwork(HubNetwork("second"))
                            verify(firstEndpoint, firstRequests)
                            assertTrue(secondRequests.tryReceive().isFailure)
                        }
                        assertEquals(ConnectionState.Online, connection.state.value)
                    } finally { jobs.cancel() }
                }
            }
        }
    }

    // The cards' API says what the hub it talks to said in its hello: a hub without assistant_views takes no answer.
    @Test fun theCardsApiFollowsTheHubsHello() = runBlocking {
        for (features in listOf("""["client_ops"]""", """["client_ops","assistant_views"]""")) {
            val certificate = HeldCertificate.Builder().commonName("example.com").build()
            val server = MockWebServer().apply {
                useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
                dispatcher = object : Dispatcher() {
                    override fun dispatch(request: RecordedRequest): MockResponse = when (request.requestUrl!!.encodedPath) {
                        "/api/v1/health" -> MockResponse().setBody("""{"ok":true,"hub_id":"test-hub"}""")
                        "/api/v1/ws" -> MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                            override fun onOpen(webSocket: WebSocket, response: Response) {
                                webSocket.send("""{"type":"hello","hub_id":"test-hub","features":$features}""")
                            }
                        })
                        else -> MockResponse().setBody("{}")
                    }
                }
                start(InetAddress.getLoopbackAddress(), 0)
            }
            server.use {
                connection(certificate, listOf(endpoint(server))) {}.use { connection ->
                    val api = HttpTasksApi(connection)
                    assertFalse("Before a hello: unknown, no answers", "assistant_views" in api.features!!.value)
                    connection.updateNetwork(HubNetwork("online"))
                    withTimeout(5000) { connection.state.first { it == ConnectionState.Online } }
                    assertEquals(features, "assistant_views" in features, "assistant_views" in api.features!!.value)
                }
            }
        }
    }

    // A card refused for this device (403 forbidden: a read-only role, cards switched off) is that request's refusal:
    // the pairing stays connected (#2022). The same request with a revoked token (401) still ends it.
    @Test fun aRefusedCardDoesNotRevokeThePairing() = runBlocking {
        val certificate = HeldCertificate.Builder().commonName("example.com").build()
        val revoked = AtomicBoolean()
        val server = MockWebServer().apply {
            useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    if (request.path == "/api/v1/health") return MockResponse().setBody("""{"ok":true,"hub_id":"test-hub"}""")
                    if (request.requestUrl!!.encodedPath == "/api/v1/ws") return MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                        override fun onOpen(webSocket: WebSocket, response: Response) {
                            webSocket.send("""{"type":"hello","hub_id":"test-hub","features":["client_ops"]}""")
                        }
                    })
                    return if (revoked.get()) MockResponse().setResponseCode(401).setBody("""{"error":"device revoked","code":"invalid_token"}""")
                    else MockResponse().setResponseCode(403)
                        .setBody("""{"error":"read-only role cannot reply from a card","code":"forbidden","retryable":false,"delivered":"none"}""")
                }
            }
            start(InetAddress.getLoopbackAddress(), 0)
        }
        server.use {
            connection(certificate, listOf(endpoint(server))) {}.use { connection ->
                connection.updateNetwork(HubNetwork("online"))
                withTimeout(5000) { connection.state.first { it == ConnectionState.Online } }
                val api = HttpTasksApi(connection)
                val input = persona.core.contracts.generated.ExtensionViewActionInput("a", null, null, "m", expectedVersion = 1)
                val refused = runCatching { api.viewAction("c", "view_1", input) }.exceptionOrNull() as HubFailure
                assertEquals(403, refused.status)
                assertEquals(HubError.Server, refused.kind)
                assertEquals(ConnectionState.Online, connection.state.value)
                assertNotNull(connection.route.value)
                revoked.set(true)
                val expired = runCatching { api.viewAction("c", "view_1", input) }.exceptionOrNull() as HubFailure
                assertEquals(HubError.PairingExpired, expired.kind)
                assertEquals(ConnectionState.Revoked, connection.state.value)
            }
        }
    }

    @Test fun everyHttpPathRevokesImmediatelyAndNeverRetries() = runBlocking {
        val certificate = HeldCertificate.Builder().commonName("example.com").build()
        val rejected = AtomicBoolean()
        server(certificate, Channel(Channel.UNLIMITED), rejected).use { server ->
            for (operation in listOf("items", "image", "inbox", "reply", "assistant", "media", "settings", "download", "message")) {
                rejected.set(false)
                val jobs = CoroutineScope(SupervisorJob() + Dispatchers.Default)
                val expired = AtomicBoolean()
                lateinit var sender: HttpMessageTransport
                connection(certificate, listOf(endpoint(server))) { sender.onReady() }.use { connection ->
                    try {
                        sender = HttpMessageTransport(pairing, connection, jobs) { expired.set(true) }
                        connection.updateNetwork(HubNetwork("online"))
                        withTimeout(5000) { sender.readyToSend.first { it } }
                        val chat = HubChatTransport(connection)
                        val inbox = HttpInboxAssistantTransport("scope", connection)
                        HubPairingClient(connection).use { settings ->
                            rejected.set(true)
                            try {
                                when (operation) {
                                    "items" -> chat.items(session, ItemsQuery())
                                    "image" -> chat.image(session, "item")
                                    "inbox" -> inbox.inbox()
                                    "reply" -> inbox.reply("item", null, "reply", "client-id")
                                    "assistant" -> inbox.assistant()
                                    "media" -> inbox.media("/api/v1/lark/media/image")
                                    "settings" -> settings.json("/api/v1/devices/test/sessions", "stale-token")
                                    "download" -> settings.download("stale-token", update, ByteArrayOutputStream())
                                    "message" -> {
                                        val result = sender.send(message()) as MessageTransportResult.HubError
                                        assertEquals("invalid_token", (result.code).zhAny())
                                        assertFalse(result.retryable)
                                        assertTrue(expired.get())
                                    }
                                }
                                if (operation != "message") fail("Expected HTTP 401 for $operation")
                            } catch (error: HubFailure) {
                                assertEquals(HubError.PairingExpired, error.kind)
                                assertEquals(401, error.status)
                                assertFalse(error.retryable)
                            }
                            assertEquals(operation, ConnectionState.Revoked, connection.state.value)
                            assertNull(connection.route.value)
                            withTimeout(5000) { sender.readyToSend.first { !it } }
                            val count = server.requestCount
                            connection.reconnect()
                            connection.updateNetwork(HubNetwork("changed"))
                            connection.updateEndpoints(listOf(endpoint(server).copy(kind = "name")))
                            try {
                                inbox.media("/api/v1/lark/media/image")
                                fail("Revoked requests must fail locally")
                            } catch (error: HubFailure) {
                                assertEquals(HubError.PairingExpired, error.kind)
                                assertFalse(error.retryable)
                            }
                            assertEquals(MessageTransportResult.NotSent, sender.send(message()))
                            delay(1200)
                            assertEquals("Unexpected retry after $operation", count, server.requestCount)
                            assertEquals(ConnectionState.Revoked, connection.state.value)
                        }
                    } finally { jobs.cancel() }
                }
            }
        }
    }

    private fun message() = MessageRequest(pairing, session, "hello", "stable-id", null)

    /**
     * The manager a request is made for (docs/assistant/managers.md "Clients"): named on the request itself (`?manager=`), so it
     * holds wherever the request goes later; the default's ("") names none, over what a list noted for the path too.
     */
    @Test fun aRequestNamesTheManagerItWasMadeFor() = runBlocking {
        val certificate = HeldCertificate.Builder().commonName("example.com").build()
        val requests = Channel<RecordedRequest>(Channel.UNLIMITED)
        server(certificate, requests).use { hub ->
            connection(certificate, listOf(endpoint(hub))) {}.use { connection ->
                try {
                    connection.updateNetwork(HubNetwork("lan"))
                    withTimeout(5000) { connection.route.first { it != null } }
                    val inbox = HttpInboxAssistantTransport("scope", connection)
                    AssistantManagerChoice.choose("test-hub", null, "assistant")
                    AssistantManagerChoice.noteItem(listOf("inbox", "x"), "m-1")
                    suspend fun sent(manager: String?): Pair<String, String?> {
                        if (manager == null) inbox.setDone("x", true)
                        else withContext(RequestManager(manager)) { inbox.setDone("x", true) }
                        val r = withTimeout(5000) { requests.receive() }
                        return r.path!! to r.getHeader(ManagerHeader)
                    }
                    assertEquals("/api/v1/inbox/x/done" to "m-1", sent(null)) // the list's stamp, as before
                    assertEquals("/api/v1/inbox/x/done?manager=m-3" to null, sent("m-3"))
                    assertEquals("/api/v1/inbox/x/done" to null, sent("")) // the default's: none at all
                } finally {
                    AssistantManagerChoice.reset(); AssistantManagerChoice.forgetItems()
                }
            }
        }
    }

    @Test fun connectorRequestsCarryTheClickedManagerToTheWire() = runBlocking {
        val certificate = HeldCertificate.Builder().commonName("example.com").build()
        val requests = Channel<RecordedRequest>(Channel.UNLIMITED)
        server(certificate, requests).use { hub ->
            connection(certificate, listOf(endpoint(hub))) {}.use { connection ->
                connection.updateNetwork(HubNetwork("lan"))
                withTimeout(5000) { connection.route.first { it != null } }
                val api = HttpConnectorsManagement(connection)
                for (owner in listOf("work", "assistant", "")) {
                    withContext(RequestManager(owner)) {
                        api.connectors(all = true)
                        api.connectorLogin("github", "", emptyMap())
                        api.connectorLogout("github")
                        api.setConnectorEnabled("github", false)
                    }
                    val paths = listOf("/api/v1/connectors?all=1", "/api/v1/connectors/github/login",
                        "/api/v1/connectors/github/logout", "/api/v1/connectors/github/settings")
                    for (path in paths) {
                        val request = withTimeout(5000) { requests.receive() }
                        val suffix = if (owner.isEmpty()) "" else (if ('?' in path) "&" else "?") + "manager=$owner"
                        assertEquals(path + suffix, request.path)
                    }
                }
            }
        }
    }

    private fun connection(certificate: HeldCertificate, endpoints: List<Endpoint>, onReady: suspend () -> Unit) =
        OkHttpHubConnection(HubConnectionConfig("test-hub", fingerprint(certificate), "test-token", endpoints, "test"),
            HubNetwork("offline", available = false), onReady = { onReady() })

    private fun server(certificate: HeldCertificate, requests: Channel<RecordedRequest>, rejected: AtomicBoolean = AtomicBoolean()) =
        MockWebServer().apply {
            useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    if (request.path == "/api/v1/health") return MockResponse().setBody("""{"ok":true,"hub_id":"test-hub"}""")
                    if (request.requestUrl!!.encodedPath == "/api/v1/ws") return MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                        override fun onOpen(webSocket: WebSocket, response: Response) {
                            webSocket.send("""{"type":"hello","hub_id":"test-hub","features":["client_ops"]}""")
                        }
                    })
                    requests.trySend(request)
                    if (rejected.get()) return MockResponse().setResponseCode(401)
                        .setBody("""{"error":"device revoked","code":"invalid_token","retryable":true}""")
                    return MockResponse().setBody(when {
                        request.path == "/api/v1/app/android/apk" -> "apk"
                        request.path == "/api/v1/app/android/latest" ->
                            """{"version_code":1,"version_name":"test","download_url":"${update.downloadUrl}","sha256":"${update.sha256}"}"""
                        request.path!!.substringBefore('?').endsWith("/connectors/github/settings") ->
                            """{"id":"github","name":"GitHub","enabled":false}"""
                        request.path!!.substringBefore('?').endsWith("/image") -> "media"
                        else -> "{}"
                    })
                }
            }
            start(InetAddress.getLoopbackAddress(), 0)
        }

    private fun endpoint(server: MockWebServer) = Endpoint(server.url("/").newBuilder()
        .host(InetAddress.getLoopbackAddress().hostAddress!!).build().toString(), "lan")

    private fun fingerprint(certificate: HeldCertificate) = Base64.getUrlEncoder().withoutPadding().encodeToString(
        MessageDigest.getInstance("SHA-256").digest(certificate.certificate.publicKey.encoded))

    private fun sha256(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
}
