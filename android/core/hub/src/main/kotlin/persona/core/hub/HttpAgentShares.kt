package persona.core.hub

import kotlinx.coroutines.CancellationException
import kotlinx.serialization.KSerializer
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*

/** Deliberately opaque; neither credentials nor origins can enter a generated toString. */
class HubSharingFence internal constructor(internal val route: HubRoute, private val verify: () -> Unit) {
    fun requireCurrent() = verify()
}

/** Real active-connection adapter; each sheet gets one immutable authority, never a mutable default manager. */
class HttpAgentShares(
    private val connection: OkHttpHubConnection,
    private val isCurrent: () -> Boolean,
) : AgentSharesProvider {
    override fun forSession(session: SessionKey, manager: String): AgentShares {
        if (!isCurrent()) throw AgentShareFailure("authority_changed")
        require(manager.isNotBlank())
        return BoundShares(connection, connection.captureSharingFence(), session, manager, isCurrent)
    }
}

private val sharingJson = AgentShareJson

/** Preserve only bounded structural errors, never generic bodies, receipts or capability-bearing messages. */
internal fun sharingFailure(response: okhttp3.Response): HubFailure {
    val code = runCatching {
        val source = response.body?.source() ?: return@runCatching null
        source.request(64L * 1024 + 1)
        if (source.buffer.size > 64 * 1024) return@runCatching null
        val value = sharingJson.parseToJsonElement(source.buffer.readUtf8()) as? kotlinx.serialization.json.JsonObject
        (value?.get("code") as? kotlinx.serialization.json.JsonPrimitive)?.content
            ?.takeIf { it.matches(Regex("[a-z][a-z0-9_]{0,63}")) }
    }.getOrNull()
    val kind = when {
        response.code == 401 || response.code == 403 && code == null -> HubError.PairingExpired
        response.code == 429 -> HubError.RateLimited
        else -> HubError.Server
    }
    return HubFailure(kind, status = response.code, code = code, retryable = false)
}

private class BoundShares(
    private val connection: OkHttpHubConnection,
    private val fence: HubSharingFence,
    private val session: SessionKey,
    private val manager: String,
    private val isCurrent: () -> Boolean,
) : AgentShares {
    override fun requireCurrent() {
        if (!isCurrent()) throw AgentShareFailure("authority_changed")
        fence.requireCurrent()
    }
    private fun sessionPath(vararg suffix: String): String = path("sessions", session.machineId, session.sessionId, *suffix)
    private fun path(vararg segments: String): String = HttpUrl.Builder().scheme("https").host("example.test")
        .addPathSegments("api/v1").apply { segments.forEach(::addPathSegment) }.build().encodedPath
    private fun page(path: String, cursor: String?, snapshot: String? = null): String = HttpUrl.Builder()
        .scheme("https").host("example.test").encodedPath(path).addQueryParameter("limit", "100")
        .apply { cursor?.let { addQueryParameter("cursor", it) }; snapshot?.let { addQueryParameter("snapshot_id", it) } }
        .build().let { it.encodedPath + "?" + it.encodedQuery }

    private suspend fun <T> request(path: String, result: KSerializer<T>, method: String = "GET", body: String? = null): T {
        requireCurrent()
        if (body != null && body.toByteArray().size > 64 * 1024) throw AgentShareFailure("request_limit", 400)
        try {
            connection.request(path, method, body?.toRequestBody("application/json".toMediaType()),
                headers = mapOf(ManagerHeader to manager, "Cache-Control" to "no-store"),
                buffered = false, expectedRoute = fence.route, sharingFence = fence).use { response ->
                val source = response.body?.source() ?: throw AgentShareFailure("invalid_response")
                // Bound before decoding; never buffer an unbounded share or capability-bearing response.
                source.request(512L * 1024 + 1)
                val bytes = source.buffer.readByteArray()
                if (bytes.size > 512 * 1024) throw AgentShareFailure("response_limit")
                requireCurrent()
                val value = sharingJson.decodeFromString(result, bytes.toString(Charsets.UTF_8))
                requireCurrent()
                return value
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (safe: AgentShareFailure) { throw safe }
        catch (error: HubFailure) { throw AgentShareFailure(error.code ?: "request_failed", error.status) }
        catch (_: Exception) { throw AgentShareFailure("request_failed") }
    }
    override suspend fun catalog(snapshotId: String?, cursor: String?) = request(
        page(sessionPath("share-catalog"), cursor, snapshotId), ShareCatalog.serializer())
    override suspend fun preview(request: SharePreviewRequest) = request(sessionPath("share-preview"), SharePreview.serializer(),
        "POST", sharingJson.encodeToString(SharePreviewRequest.serializer(), request))
    override suspend fun previewPage(previewId: String, cursor: String) = request(
        page(sessionPath("share-preview", previewId), cursor), SharePreview.serializer())
    override suspend fun create(request: ShareCreateRequest): ShareCreated {
        if (request.machine != session.machineId || request.sessionId != session.sessionId) throw AgentShareFailure("session_conflict", 400)
        return request(path("agent-shares"), ShareCreated.serializer(), "POST", sharingJson.encodeToString(ShareCreateRequest.serializer(), request))
    }
    override suspend fun list(): ShareList {
        val url = HttpUrl.Builder().scheme("https").host("example.test").encodedPath(path("agent-shares"))
            .addQueryParameter("machine", session.machineId).addQueryParameter("session_id", session.sessionId).build()
        return request(url.encodedPath + "?" + url.encodedQuery, ShareList.serializer())
    }
    override suspend fun get(shareId: String) = request(path("agent-shares", shareId), ShareDetail.serializer())
    override suspend fun messages(shareId: String, cursor: String?) = request(page(path("agent-shares", shareId, "messages"), cursor), ShareMessages.serializer())
    override suspend fun update(shareId: String, request: ShareUpdateRequest) = request(path("agent-shares", shareId), ShareResult.serializer(),
        "PATCH", sharingJson.encodeToString(ShareUpdateRequest.serializer(), request))
    override suspend fun revoke(shareId: String, expectedRevision: Long) = request(path("agent-shares", shareId), ShareRevoked.serializer(),
        "DELETE", sharingJson.encodeToString(ShareRevokeRequest.serializer(), ShareRevokeRequest(expectedRevision)))
}
