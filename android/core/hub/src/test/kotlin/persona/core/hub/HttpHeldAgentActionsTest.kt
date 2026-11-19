package persona.core.hub

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentMessageHoldFeature

class HttpHeldAgentActionsTest {
    @Test fun onlyTheExplicitOperationRunsAndAConflictPreservesTheRow() = runBlocking {
        val calls = mutableListOf<Pair<String, String>>()
        val completed = mutableListOf<Pair<String, Boolean>>()
        var status = 200
        val features = MutableStateFlow(setOf(AgentMessageHoldFeature))
        val service = HttpHeldAgentActions(SessionKey("m x", "s/y"), features, { path, method, _ ->
            calls += method to path
            Response.Builder().request(Request.Builder().url("https://example.com/").build())
                .protocol(Protocol.HTTP_1_1).code(status).message("fixture").body("{}".toResponseBody()).build()
        }, { id, retry -> completed += id to retry })
        service.retry("k/1")
        assertEquals("POST" to "/api/v1/sessions/m%20x/s%2Fy/held/k%2F1/retry", calls.single())
        for (error in listOf(403, 409)) {
            status = error
            assertEquals(error, (runCatching { service.cancel("k/1") }.exceptionOrNull() as HubFailure).status)
            assertEquals(listOf("k/1" to true), completed)
        }
        status = 404
        service.cancel("k/1")
        assertEquals("DELETE" to "/api/v1/sessions/m%20x/s%2Fy/held/k%2F1", calls.last())
        assertEquals(listOf("k/1" to true, "k/1" to false), completed)
        features.value = emptySet()
        assertTrue(runCatching { service.retry("k/1") }.isFailure)
        assertEquals(4, calls.size)
    }
}
