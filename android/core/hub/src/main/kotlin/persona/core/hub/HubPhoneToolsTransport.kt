package persona.core.hub

import persona.core.contracts.JsonCodec
import persona.core.contracts.api.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.ExperimentalSerializationApi
import okhttp3.Response
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.serialization.json.*

/** Feed accept from the socket text hook before ServerEvent.decode, which ignores tool_call. */
class HubPhoneToolsTransport internal constructor(
    private val connection: HubConnection,
    private val get: suspend (String) -> Response,
) : PhoneToolsTransport, AutoCloseable {
    constructor(connection: OkHttpHubConnection) : this(connection, { path -> connection.request(path, buffered = false, allowForbidden = true) })

    private val queue = Channel<PhoneToolCall>(16)
    override val calls = queue.receiveAsFlow()

    @OptIn(ExperimentalSerializationApi::class)
    override suspend fun pendingWriteCount(): Int? = try {
        get("/api/v1/phone/write-queue?count_only=true").use { response ->
            if (response.code == 403) return@use null
            withContext(Dispatchers.IO) {
                response.body?.byteStream()?.let { JsonCodec.decodeFromStream<PhoneWriteQueueSummary>(it) }
                    ?.pendingCount?.takeIf { it >= 0 }
            }
        }
    } catch (error: HubFailure) {
        if (error.status == 404) null else throw error
    }

    fun accept(message: JsonObject): Boolean {
        if ((message["type"] as? JsonPrimitive)?.contentOrNull != "tool_call") return false
        val id = (message["call_id"] as? JsonPrimitive)?.takeIf { it.isString }?.content
            ?.takeIf { it.isNotBlank() } ?: return true
        val name = (message["name"] as? JsonPrimitive)?.takeIf { it.isString }?.content
        // A tool without parameters may come with no arguments at all (null or missing): that is an empty object.
        val args = when (val raw = message["args"]) {
            null, JsonNull -> JsonObject(emptyMap())
            else -> raw as? JsonObject
        }
        if (name.isNullOrBlank() || args == null) {
            result(id, PhoneToolResult.text("参数错误 / Invalid argument: the arguments are not a JSON object.", true)) // i18n-ignore: a tool result for the model
        } else if (!queue.trySend(PhoneToolCall(id, name, args)).isSuccess) {
            result(id, PhoneToolResult.text("手机工具正在忙，请稍后重试。", true)) // i18n-ignore: a tool result for the model
        }
        return true
    }

    override fun register(deviceId: String, deviceName: String, simulator: Boolean, tools: List<PhoneToolSpec>): Boolean {
        require(deviceId.isNotBlank())
        return connection.send(buildJsonObject {
            put("type", "register_phone"); put("device_id", deviceId); put("platform", "android")
            put("device_name", deviceName); put("simulator", simulator)
            put("tools", JsonArray(tools.map { it.toWire() }))
        })
    }

    override fun result(callId: String, result: PhoneToolResult) = connection.send(buildJsonObject {
        put("type", "tool_result"); put("call_id", callId)
        put("content", result.content); put("is_error", result.isError)
    })

    override fun discardPending() { while (queue.tryReceive().isSuccess) { /* Discard calls from a previous socket. */ } }

    override fun close() { queue.close() }
}
