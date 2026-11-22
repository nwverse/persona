package persona.core.hub

import java.net.InetAddress
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.first
import kotlinx.serialization.json.*
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.*
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Endpoint
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*

class HttpAgentSharesTest {
    private val session = SessionKey("machine/one", "session?one")
    private val metadata = """{"share_id":"sealed","revision":2,"content_version":3,"selection":{"mode":"all","count":1},"include_future":false,"show_tools":false,"created_at":"2026-11-10T00:00:00Z","updated_at":"2026-11-10T00:00:00Z","expires_at":"2026-11-13T00:00:00Z","status":"active","publication_status":"frozen","pause_reason":null}"""
    private val catalog = """{"can_share":true,"can_include_future":true,"snapshot_id":"snapshot","snapshot_version":"v1","expires_at":"2099-01-01T00:00:00Z","complete":true,"total_messages":101,"omitted_messages":1,"omissions":["unknown_provenance"],"messages":[{"message_id":"old","role":"user","text":"Synthetic history","selectable":true}],"next_cursor":"older"}"""
    private val preview = """{"preview_id":"preview","expires_at":"2099-01-01T00:00:00Z","policy_version":1,"selected_count":1,"omitted_count":1,"messages":[{"message_id":"local","role":"assistant","text":"Synthetic result","execution":[{"kind":"tool","tool_id":"local-tool","name":"SyntheticTool","arguments":"{}","results":[{"text":"Synthetic output"}]}]}],"next_cursor":null}"""

    private class Server : AutoCloseable {
        val requests = ConcurrentLinkedQueue<RecordedRequest>()
        var result = "{}"
        var status = 200
        var socketPolicy: SocketPolicy? = null
        var block: (() -> Unit)? = null
        val server = MockWebServer().apply {
            dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    return when (request.requestUrl!!.encodedPath) {
                        "/api/v1/health" -> MockResponse().setBody("""{"ok":true,"hub_id":"synthetic-hub"}""")
                        "/api/v1/ws" -> MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
                            override fun onOpen(webSocket: WebSocket, response: Response) {
                                webSocket.send("""{"type":"hello","hub_id":"synthetic-hub","features":[],"endpoints":[]}""")
                            }
                        })
                        else -> {
                            if (!request.requestUrl!!.encodedPath.startsWith("/api/v1/")) return MockResponse().setResponseCode(404)
                            requests += request; block?.invoke(); MockResponse().setResponseCode(status).setBody(result)
                                .apply { this@Server.socketPolicy?.let(::setSocketPolicy) }
                        }
                    }
                }
            }
            start(InetAddress.getLoopbackAddress(), 0)
        }
        fun connection() = OkHttpHubConnection(HubConnectionConfig("synthetic-hub", null, "synthetic-credential",
            listOf(Endpoint(server.url("/").toString(), "lan")), "test"), HubNetwork("network"))
        suspend fun ready(connection: OkHttpHubConnection) = withTimeout(8000) { connection.state.first { it == ConnectionState.Online } }
        fun last() = requests.last()
        override fun close() = server.close()
    }

    @Test fun fullFrozenContractUsesRealActiveRouteBearerAndExplicitManagerWithoutReceipts() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection)
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            server.result = catalog
            val page = api.catalog()
            assertEquals(101, page.totalMessages)
            assertEquals("unknown_provenance", page.omissions.single())
            assertEquals("/api/v1/sessions/machine%2Fone/session%3Fone/share-catalog?limit=100", server.last().path)
            api.catalog("snapshot", "older+opaque")
            assertEquals("older+opaque", server.last().requestUrl!!.queryParameter("cursor"))
            assertEquals("snapshot", server.last().requestUrl!!.queryParameter("snapshot_id"))
            server.result = preview
            val proposed = SharePreviewRequest("create", "snapshot", "v1", ShareSelection.From("old"), false, false, 3)
            val prepared = api.preview(proposed)
            assertTrue(prepared.messages.single().execution!!.single() is ShareExecution.Tool)
            val previewBody = Json.parseToJsonElement(server.last().body.readUtf8()).jsonObject
            assertEquals(buildJsonObject { put("mode", "from"); put("from_message_id", "old") }, previewBody["selection"])
            assertFalse(previewBody.containsKey("share_id"))
            api.previewPage("preview", "second")
            assertEquals("/api/v1/sessions/machine%2Fone/session%3Fone/share-preview/preview", server.last().requestUrl!!.encodedPath)
            server.result = """{"share":$metadata,"public_url":"https://share.example.test/share/${"a".repeat(64)}"}"""
            val created = api.create(ShareCreateRequest("00000000-0000-4000-8000-000000000001", session.machineId, session.sessionId,
                "snapshot", "v1", ShareSelection.All, acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview")))
            assertFalse(created.toString().contains("https"))
            val body = Json.parseToJsonElement(server.last().body.readUtf8()).jsonObject
            assertEquals(false, body["include_future"]!!.jsonPrimitive.boolean)
            assertEquals(false, body["show_tools"]!!.jsonPrimitive.boolean)
            assertEquals(3, body["expires_in_days"]!!.jsonPrimitive.int)
            assertEquals(buildJsonObject { put("mode", "all") }, body["selection"])
            assertEquals("preview", body["acknowledgement"]!!.jsonObject["preview_id"]!!.jsonPrimitive.content)
            server.result = """{"shares":[$metadata]}"""; assertEquals("sealed", api.list().shares.single().shareId)
            assertEquals(session.machineId, server.last().requestUrl!!.queryParameter("machine"))
            assertEquals(session.sessionId, server.last().requestUrl!!.queryParameter("session_id"))
            server.result = """{"share":$metadata,"can_widen":false,"reason":"authority_unavailable"}"""; assertFalse(api.get("sealed").canWiden)
            server.result = """{"revision":2,"content_version":3,"messages":[],"next_cursor":"next"}"""; assertEquals("next", api.messages("sealed").nextCursor)
            server.result = """{"share":$metadata}"""
            api.update("sealed", ShareUpdateRequest(2, removeMessageIds = listOf("editor-id"), showTools = false, includeFuture = false))
            val patch = Json.parseToJsonElement(server.last().body.readUtf8()).jsonObject
            assertEquals("PATCH", server.last().method)
            assertFalse(patch.containsKey("selection")); assertFalse(patch.containsKey("acknowledgement"))
            assertEquals(listOf("editor-id"), patch["remove_message_ids"]!!.jsonArray.map { it.jsonPrimitive.content })
            server.result = """{"share_id":"sealed","revision":3,"status":"revoked"}"""; api.revoke("sealed", 2)
            assertEquals("DELETE", server.last().method)
            assertEquals("""{"expected_revision":2}""", server.last().body.readUtf8())
            server.requests.forEach { request ->
                assertEquals("Bearer synthetic-credential", request.getHeader("Authorization"))
                assertEquals("manager-one", request.getHeader(ManagerHeader))
                assertEquals("no-store", request.getHeader("Cache-Control"))
                assertNull(request.getHeader("X-Persona-Operation-Id"))
                assertNull(request.getHeader("Cookie"))
                assertNull(request.requestUrl!!.queryParameter("token"))
            }
        } }
    }

    @Test fun httpTransportAcknowledgementAndIndividualUpdateAreSerializedWithoutNullSelectorFields() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection)
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            server.result = preview.replace("\"next_cursor\":null", "\"next_cursor\":null,\"requires_insecure_transport_ack\":true")
            val selected = ShareSelection.Individual(listOf("older-a", "older-b"))
            assertTrue(api.preview(SharePreviewRequest("create", "snapshot", "v1", selected, false, false, 3)).requiresInsecureTransportAck)
            server.result = """{"share":$metadata,"public_url":"http://share.example.test:8080/share/${"a".repeat(64)}"}"""
            api.create(ShareCreateRequest("id", session.machineId, session.sessionId, "snapshot", "v1", selected,
                acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview", insecureTransport = true)))
            val create = Json.parseToJsonElement(server.last().body.readUtf8()).jsonObject
            assertTrue(create["acknowledgement"]!!.jsonObject["insecure_transport"]!!.jsonPrimitive.boolean)
            assertEquals("individual", create["selection"]!!.jsonObject["mode"]!!.jsonPrimitive.content)
            server.result = """{"share":$metadata}"""
            api.update("sealed", ShareUpdateRequest(2, 3, showTools = true, includeFuture = true,
                expiresAt = "2026-12-01T00:00:00Z", acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview", insecureTransport = true)))
            val update = Json.parseToJsonElement(server.last().body.readUtf8()).jsonObject
            assertEquals(3L, update["expected_content_version"]!!.jsonPrimitive.long)
            assertFalse(update.containsKey("selection")); assertFalse(update.containsKey("snapshot_id"))
            assertTrue(update["acknowledgement"]!!.jsonObject["insecure_transport"]!!.jsonPrimitive.boolean)
        } }
    }

    @Test fun sharingUsesCurrentAppLanguageAndRetainsServerTransportWarningOnEveryPreviewPage() = runBlocking {
        val original = persona.core.contracts.ClientLanguage.current
        try {
            Server().use { server -> server.connection().use { connection ->
                server.ready(connection)
                val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
                listOf(persona.core.contracts.AppLanguage.En, persona.core.contracts.AppLanguage.ZhHans,
                    persona.core.contracts.AppLanguage.Ja).forEach { language ->
                    persona.core.contracts.ClientLanguage.current = language
                    val warning = "Synthetic HTTP transport warning (${persona.core.contracts.ClientLanguage.tag})"
                    server.result = preview.replace("\"next_cursor\":null",
                        "\"next_cursor\":null,\"requires_insecure_transport_ack\":true,\"transport_warning\":\"$warning\"")
                    val prepared = api.preview(SharePreviewRequest("create", "snapshot", "v1", ShareSelection.All, false, false, 3))
                    assertEquals(persona.core.contracts.ClientLanguage.tag, server.last().getHeader("Accept-Language"))
                    assertEquals(warning, prepared.transportWarning); assertTrue(prepared.requiresInsecureTransportAck)
                    val continued = api.previewPage(prepared.previewId, "next")
                    assertEquals(persona.core.contracts.ClientLanguage.tag, server.last().getHeader("Accept-Language"))
                    assertEquals(warning, continued.transportWarning); assertTrue(continued.requiresInsecureTransportAck)
                }
            } }
        } finally { persona.core.contracts.ClientLanguage.current = original }
    }

    @Test fun aNewSheetCannotCaptureOldCredentialsAfterTheActiveMemberCredentialWasReplaced() = runBlocking {
        Server().use { server ->
            val member = HubMember("synthetic-hub", null, "member-credential", listOf(Endpoint(server.server.url("/").toString(), "lan")))
            OkHttpHubConnection(HubConnectionConfig("synthetic-unavailable-home", null, "home-credential", emptyList(), "test", members = listOf(member)),
                HubNetwork("network")).use { connection ->
                server.ready(connection)
                val provider = HttpAgentShares(connection) { true }
                val api = provider.forSession(session, "manager-one")
                connection.updateMembers(listOf(member.copy(token = "replacement-credential")))
                assertFailure("authority_changed") { api.list() }
                assertFailure("authority_changed") { provider.forSession(session, "manager-one") }
                assertTrue(server.requests.isEmpty())
            }
        }
    }

    @Test fun changingGroupCredentialInvalidatesPreviouslyCapturedSharingFence() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection)
            val member = HubMember("synthetic-member", null, "first-credential", emptyList())
            connection.updateMembers(listOf(member))
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            connection.updateMembers(listOf(member.copy(token = "rotated-credential")))
            assertFailure("authority_changed") { api.list() }
            assertTrue(server.requests.isEmpty())
        } }
    }

    @Test fun ambientRequestAndPagePinsCannotOverrideExplicitSessionManager() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection); server.result = catalog
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            assertFailure("manager_selector_conflict") { withContext(RequestManager("wrong")) { api.catalog() } }
            AssistantManagerChoice.atHub("synthetic-hub")
            val pins = ManagerPins.of(listOf(listOf("sessions")), "wrong", "synthetic-hub")!!
            assertFailure("manager_selector_conflict") { withContext(pins) { api.catalog() } }
            assertTrue(server.requests.isEmpty())
            withContext(RequestManager("manager-one")) { api.catalog() }
            assertEquals("manager-one", server.last().requestUrl!!.queryParameter(ManagerQuery))
            AssistantManagerChoice.reset()
        } }
    }

    @Test fun pairingGenerationAndCredentialRotationFenceBeforeSendingAndRejectABA() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection); server.result = catalog
            var current = true
            val api = HttpAgentShares(connection) { current }.forSession(session, "manager-one")
            current = false
            assertFailure("authority_changed") { api.catalog() }
            current = true
            connection.updateMembers(emptyList(), "center-b")
            connection.updateMembers(emptyList(), "center-a")
            assertFailure("authority_changed") { api.catalog() }
            assertTrue(server.requests.isEmpty())
        } }
    }

    @Test fun delayedSuccessAfterAuthorityChangeIsDiscardedIncludingOneTimeCreate() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection)
            val entered = CountDownLatch(1); val release = CountDownLatch(1)
            server.result = """{"share":$metadata,"public_url":"https://share.example.test/share/${"a".repeat(64)}"}"""
            server.block = { entered.countDown(); check(release.await(8, TimeUnit.SECONDS)) }
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            val request = async(Dispatchers.IO) {
                runCatching { api.create(ShareCreateRequest("id", session.machineId, session.sessionId, "snapshot", "v1", ShareSelection.All,
                    acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview"))) }
            }
            check(entered.await(8, TimeUnit.SECONDS)); connection.updateMembers(emptyList(), "new-center"); release.countDown()
            assertEquals("authority_changed", (request.await().exceptionOrNull() as AgentShareFailure).code)
            assertEquals(1, server.requests.size)
        } }
    }

    @Test fun duplicateCreateAndLostResponseExposeOnlyStructuralFailureAndNeverRetry() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection); server.status = 409
            server.result = """{"code":"creation_already_committed","error":"Synthetic refusal","share":$metadata}"""
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            assertFailure("creation_already_committed") { api.create(ShareCreateRequest("id", session.machineId, session.sessionId, "snapshot", "v1", ShareSelection.All,
                acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview"))) }
            assertEquals(1, server.requests.size)
            assertFailure("session_conflict") { api.create(ShareCreateRequest("id", "another", session.sessionId, "snapshot", "v1", ShareSelection.All,
                acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview"))) }
            assertEquals(1, server.requests.size)
        } }
    }

    @Test fun disconnectedCreateResponseIsNotAutomaticallyRetriedAndErrorBodiesNeverEscape() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection)
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            val creation = ShareCreateRequest("id", session.machineId, session.sessionId, "snapshot", "v1", ShareSelection.All,
                acknowledgement = ShareAcknowledgement(policyVersion = 1, previewId = "preview"))
            server.result = """{"share":$metadata,"public_url":"https://share.example.test/share/${"a".repeat(64)}"}"""
            server.socketPolicy = SocketPolicy.DISCONNECT_AFTER_REQUEST
            assertFailure("request_failed") { api.create(creation) }
            assertEquals(1, server.requests.size)
            server.socketPolicy = null; server.status = 409
            server.result = """{"code":"revision_conflict","error":"Bearer synthetic-secret http://share.example.test/share/${"a".repeat(64)}"}"""
            val failure = runCatching { api.update("sealed", ShareUpdateRequest(2)) }.exceptionOrNull() as AgentShareFailure
            assertEquals("revision_conflict", failure.code)
            assertFalse(failure.toString().contains("synthetic-secret")); assertFalse(failure.toString().contains("http"))
            assertNull(failure.cause); assertEquals(2, server.requests.size)
        } }
    }

    @Test fun oversizedResponsesAreBoundedAndRouteReplacementDoesNotFallBack() = runBlocking {
        Server().use { server -> server.connection().use { connection ->
            server.ready(connection)
            val api = HttpAgentShares(connection) { true }.forSession(session, "manager-one")
            server.result = "x".repeat(512 * 1024 + 1)
            assertFailure("response_limit") { api.catalog() }
            connection.updateNetwork(HubNetwork("offline", available = false))
            eventually { connection.route.value == null }
            assertFailure("authority_changed") { api.catalog() }
            assertEquals(1, server.requests.size)
        } }
    }

    private suspend fun assertFailure(code: String, block: suspend () -> Unit) {
        val failure = runCatching { block() }.exceptionOrNull() as? AgentShareFailure
        assertNotNull("Expected structural failure", failure)
        assertEquals(code, failure!!.code); assertNull(failure.cause)
    }
}
