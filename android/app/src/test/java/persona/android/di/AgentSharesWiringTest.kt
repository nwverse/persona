package persona.android.di

import java.lang.reflect.Proxy
import java.net.InetAddress
import java.util.concurrent.ConcurrentLinkedQueue
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.hub.*
import persona.feature.chat.ChatAppBindings

/** Exercise the application's DI factory and its real grouped chat facade through actual HTTP requests. */
class AgentSharesWiringTest {
    private class Server : AutoCloseable {
        val requests = ConcurrentLinkedQueue<RecordedRequest>()
        val metadata = """{"share_id":"sealed","revision":1,"content_version":1,"selection":{"mode":"all","count":1},"include_future":false,"show_tools":false,"created_at":"2026-11-10T00:00:00Z","updated_at":"2026-11-10T00:00:00Z","expires_at":"2026-11-13T00:00:00Z","status":"active","publication_status":"frozen"}"""
        val server = MockWebServer().apply {
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    if (!request.requestUrl!!.encodedPath.startsWith("/api/v1/")) return MockResponse().setResponseCode(404)
                    return when (request.requestUrl!!.encodedPath) {
                    "/api/v1/health" -> MockResponse().setBody("""{"ok":true,"hub_id":"synthetic-hub"}""")
                    "/api/v1/ws" -> MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                        override fun onOpen(webSocket: WebSocket, response: Response) {
                            webSocket.send("""{"type":"hello","hub_id":"synthetic-hub","features":[],"endpoints":[]}""")
                        }
                    })
                    else -> {
                        if (!request.requestUrl!!.encodedPath.startsWith("/api/v1/")) return MockResponse().setResponseCode(404)
                        requests += request
                        val body = when {
                            request.path!!.contains("share-catalog") -> """{"can_share":false,"reason":"ownership_unbound","messages":[]}"""
                            request.method == "POST" && request.requestUrl!!.encodedPath == "/api/v1/agent-shares" -> """{"share":$metadata,"public_url":"http://share.example.test:8080/share/${"a".repeat(64)}"}"""
                            request.method == "DELETE" -> """{"share_id":"sealed","revision":2,"status":"revoked"}"""
                            request.path!!.contains("/messages") -> """{"revision":1,"content_version":1,"messages":[]}"""
                            request.path!!.contains("share-preview") -> """{"preview_id":"preview","expires_at":"2099-01-01T00:00:00Z","policy_version":1,"selected_count":1,"omitted_count":0,"messages":[],"requires_insecure_transport_ack":true}"""
                            request.requestUrl!!.encodedPath == "/api/v1/agent-shares" -> """{"shares":[$metadata]}"""
                            else -> """{"share":$metadata,"can_widen":false}"""
                        }
                        MockResponse().setBody(body)
                    }
                    }
                }
            }
            start(InetAddress.getLoopbackAddress(), 0)
        }
        fun connection() = OkHttpHubConnection(HubConnectionConfig("synthetic-hub", null, "synthetic-device",
            listOf(Endpoint(server.url("/").toString(), "lan")), "test"), HubNetwork("synthetic-network"))
        override fun close() = server.close()
    }

    @Test fun applicationFactoryAndChatFacadeRouteEveryOperationToOriginalGroupWithRawSessionIds() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            withTimeout(8000) { connection.state.first { it == ConnectionState.Online } }
            val scope = CoroutineScope(SupervisorJob() + Dispatchers.Unconfined)
            var current = true
            try {
                // This is the same factory installed by createChatWiring, not an independent test AgentShares implementation.
                val provider = ChatWiringModule.agentShares(connection) { current }
                val composer = object : ChatComposerService {
                    override val deliveries = MutableStateFlow<Map<String, ComposerDelivery>>(emptyMap())
                    override suspend fun upload(session: SessionKey, name: String, file: java.io.File) = error("unused")
                    override suspend fun interrupt(session: SessionKey) = error("unused")
                    override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean) = error("unused")
                }
                val sender = object : MainConversationSender {
                    override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
                    override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = error("unused")
                }
                val source = ChatAppBindings(HubScope("synthetic-hub", "synthetic-generation"), scope,
                    unused(), connection, unused(), composer, unused(), unused(), sender, agentShares = provider)
                assertSame(source, source.inGroup(GroupNames(null)))
                val merged = source.inGroup(GroupNames("g0a1b2c3d"))
                val session = SessionKey("machine~g0a1b2c3d", "session")
                val api = requireNotNull(merged.agentShares).forSession(session, "explicit-manager")
                assertFalse(api.catalog().canShare)
                api.preview(SharePreviewRequest("create", "snapshot", "v1", ShareSelection.All, false, false, 3))
                api.previewPage("preview", "next")
                val created = api.create(ShareCreateRequest("00000000-0000-4000-8000-000000000001", session.machineId,
                    session.sessionId, "snapshot", "v1", ShareSelection.All,
                    acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview", insecureTransport = true)))
                assertTrue(created.publicUrl.startsWith("http://"))
                val sent = kotlinx.serialization.json.Json.parseToJsonElement(server.requests.last().body.readUtf8())
                assertFalse(sent.toString().contains("~g0a1b2c3d"))
                api.list(); api.get("sealed"); api.messages("sealed")
                api.update("sealed", ShareUpdateRequest(1, showTools = false)); api.revoke("sealed", 1)
                assertEquals(9, server.requests.size)
                server.requests.forEach { request ->
                    assertEquals("explicit-manager", request.getHeader(ManagerHeader))
                    assertEquals("Bearer synthetic-device", request.getHeader("Authorization"))
                    assertEquals(ClientLanguage.tag, request.getHeader("Accept-Language"))
                    assertFalse(request.path!!.contains("~g0a1b2c3d"))
                    assertNull(request.getHeader("X-Persona-Operation-Id"))
                }
                val before = server.requests.size
                val wrong = runCatching { requireNotNull(merged.agentShares).forSession(SessionKey("machine", "session"), "explicit-manager") }.exceptionOrNull()
                assertEquals("session_conflict", (wrong as AgentShareFailure).code)
                current = false
                val late = runCatching { api.list() }.exceptionOrNull()
                assertEquals("authority_changed", (late as AgentShareFailure).code)
                assertEquals(before, server.requests.size)
            } finally { scope.cancel() }
        } }
    }

    /** Unrelated app boundaries fail if touched; sharing itself uses the production HTTP adapter. */
    private inline fun <reified T> unused(): T = Proxy.newProxyInstance(T::class.java.classLoader, arrayOf(T::class.java)) { proxy, method, args ->
        when (method.name) {
            "toString" -> "Unused boundary"
            "hashCode" -> System.identityHashCode(proxy)
            "equals" -> proxy === args?.firstOrNull()
            else -> error("Unexpected boundary access")
        }
    } as T
}
