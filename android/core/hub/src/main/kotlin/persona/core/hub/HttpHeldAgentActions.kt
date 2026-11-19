package persona.core.hub

import kotlinx.coroutines.flow.StateFlow
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentMessageHoldFeature
import persona.core.contracts.api.HeldAgentActions

/** Bound to one pairing generation and its plain session key, even in a multi-hub group. */
class HttpHeldAgentActions(
    private val session: SessionKey,
    override val features: StateFlow<Set<String>>,
    private val request: suspend (String, String, RequestBody?) -> Response,
    private val completed: suspend (String, Boolean) -> Unit,
) : HeldAgentActions {
    override suspend fun retry(id: String) = act(id, retry = true)
    override suspend fun cancel(id: String) = act(id, retry = false)

    private suspend fun act(id: String, retry: Boolean) {
        require(id.isNotBlank() && AgentMessageHoldFeature in features.value)
        val path = HttpUrl.Builder().scheme("https").host("example.com").addPathSegments("api/v1/sessions")
            .addPathSegment(session.machineId).addPathSegment(session.sessionId).addPathSegment("held").addPathSegment(id)
            .apply { if (retry) addPathSegment("retry") }.build().encodedPath
        try {
            request(path, if (retry) "POST" else "DELETE",
                if (retry) "{}".toRequestBody("application/json".toMediaType()) else null).use {
                if (!it.isSuccessful) throw HubFailure.from(it)
            }
        } catch (failure: HubFailure) {
            if (failure.status != 404) throw failure
            completed(id, false)
            return
        }
        completed(id, retry)
    }
}
