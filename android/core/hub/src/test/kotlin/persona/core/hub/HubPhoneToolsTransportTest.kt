package persona.core.hub

import persona.core.contracts.ServerEvent
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.PhoneToolCall
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.RecordedRequest
import java.util.concurrent.LinkedBlockingQueue
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

class HubPhoneToolsTransportTest {
    private class Socket : HubConnection {
        val sent = mutableListOf<JsonObject>()
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean { sent += message; return true }
    }

    @Test fun pairedConnectionReadsOnlyTheCountAndARefusedOptionalReadKeepsTheSocket() = runBlocking {
        val requests = LinkedBlockingQueue<RecordedRequest>()
        val replies = LinkedBlockingQueue<MockResponse>()
        LoopbackHub(api = { request ->
            if (request.requestUrl!!.encodedPath == "/api/v1/phone/write-queue") {
                requests.add(request)
                replies.remove()
            } else MockResponse().setResponseCode(404)
        }).use { hub ->
            OkHttpHubConnection(HubConnectionConfig(hub.hubId, null, "test-token", listOf(hub.endpoint("lan")), "test"),
                HubNetwork("offline", available = false)).use { socket ->
                socket.updateNetwork(HubNetwork("test"))
                withTimeout(5_000) { socket.currentHello.first { it != null } }
                HubPhoneToolsTransport(socket).use { transport ->
                    suspend fun read(status: Int, json: String): Int? {
                        replies.add(MockResponse().setResponseCode(status).setBody(json))
                        val count = transport.pendingWriteCount()
                        val request = requests.remove()
                        assertEquals("GET", request.method)
                        assertEquals("/api/v1/phone/write-queue?count_only=true", request.path)
                        assertEquals("Bearer test-token", request.getHeader("Authorization"))
                        return count
                    }
                    assertEquals(0, read(200, """{"pending_count":0}"""))
                    assertEquals(3, read(200, """{"items":[{"args":{"unused":[1,"example"]}}],"pending_count":3}"""))
                    assertNull(read(200, """{"items":[]}"""))
                    assertNull(read(200, """{"pending_count":-1}"""))
                    assertNull(read(404, "{}"))
                    assertNull(read(403, """{"code":"permission_denied"}"""))
                    assertNull(read(403, "{}"))
                    assertEquals(1, read(200, """{"pending_count":1}"""))
                    assertEquals(ConnectionState.Online, socket.state.value)
                    assertEquals(1, hub.opened.get())
                }
            }
        }
    }

    @Test fun callsWithoutArgumentsAreEmptyObjectsAndOtherShapesAreRefused() = runBlocking {
        val socket = Socket()
        val transport = HubPhoneToolsTransport(socket) { error("Unexpected queue read") }
        fun call(id: String, args: JsonElement?) = buildJsonObject {
            put("type", "tool_call"); put("call_id", id); put("name", "phone_device_info"); args?.let { put("args", it) }
        }
        assertTrue(transport.accept(call("missing", null)))
        assertEquals(PhoneToolCall("missing", "phone_device_info", JsonObject(emptyMap())), transport.calls.first())
        assertTrue(transport.accept(call("null", JsonNull)))
        assertEquals(PhoneToolCall("null", "phone_device_info", JsonObject(emptyMap())), transport.calls.first())
        assertTrue(transport.accept(call("array", JsonArray(emptyList()))))
        val refusal = socket.sent.single()
        assertEquals("array", (refusal.getValue("call_id").jsonPrimitive.content).zhAny())
        assertTrue(refusal.getValue("is_error").jsonPrimitive.boolean)
        assertFalse(transport.accept(buildJsonObject { put("type", "sessions") }))
    }
}
