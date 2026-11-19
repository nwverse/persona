package persona.core.hub

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.*
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.HeldAgentMessage
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AgentMessageHoldFeature
import java.io.IOException

class HttpHeldAgentActionsTest {
    private val unclear = HeldAgentMessage("k/1", "Review screenshots", at = "2026-06-06T12:00:00Z", state = "failed",
        error = "Might not have arrived. Retry checks the chat first.", computer = "Studio Mac")
    private fun receipts(stored: MutableStateFlow<Set<String>> = MutableStateFlow(emptySet())) =
        HeldAgentRetryReceipts(stored) { stored.update(it) }

    @Test fun onlyTheExplicitOperationRunsAndAConflictPreservesTheRow() = runBlocking {
        val calls = mutableListOf<Pair<String, String>>()
        val completed = mutableListOf<Pair<String, Boolean>>()
        var status = 200
        val features = MutableStateFlow(setOf(AgentMessageHoldFeature))
        val owner = CoroutineScope(coroutineContext + SupervisorJob())
        val service = HttpHeldAgentActions(SessionKey("m x", "s/y"), features, receipts(), owner, { path, method, _ ->
            calls += method to path
            Response.Builder().request(Request.Builder().url("https://example.com/").build())
                .protocol(Protocol.HTTP_1_1).code(status).message("fixture").body("{}".toResponseBody()).build()
        }, { id, retry -> completed += id to retry })
        service.retry(unclear)
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
        assertTrue(runCatching { service.retry(unclear) }.isFailure)
        assertEquals(4, calls.size)
        owner.cancel()
    }

    @Test fun retryPermissionsFollowThePairingSessionOperationAndDecisionAcrossReopens() = runBlocking {
        val owner = CoroutineScope(coroutineContext + SupervisorJob())
        val calls = mutableListOf<String>()
        var status = 200
        var failRefresh = false
        var disconnected = false
        val stored = MutableStateFlow(emptySet<String>())
        val otherHub = MutableStateFlow(emptySet<String>())
        val key = SessionKey("one/two", "three")
        fun bind(session: SessionKey = key, storage: MutableStateFlow<Set<String>> = stored) =
            HttpHeldAgentActions(session, MutableStateFlow(setOf(AgentMessageHoldFeature)), receipts(storage), owner,
                { path, method, _ ->
                    calls += "$method $path"
                    if (disconnected) throw IOException("fixture disconnected")
                    Response.Builder().request(Request.Builder().url("https://example.com/").build())
                        .protocol(Protocol.HTTP_1_1).code(status).message("fixture").body("{}".toResponseBody()).build()
                }, { _, _ -> if (failRefresh) error("refresh failed") })
        bind().retry(unclear)
        val preview = unclear.copy(text = "A different preview", images = 1, computer = "Renamed")
        assertFalse(bind().canRetry(preview).first())
        bind().retry(preview)
        assertEquals(1, calls.size)
        for (other in listOf(SessionKey("one", "two/three"), key.copy(machineId = "other"), key.copy(sessionId = "other"))) {
            bind(other).retry(unclear)
        }
        bind().retry(unclear.copy(id = "another"))
        bind(storage = otherHub).retry(unclear)
        assertEquals(6, calls.size)
        val unconfirmed = unclear.copy(error = "Couldn't confirm it arrived. Retry to send it anyway, or delete it.")
        assertTrue(bind().canRetry(unconfirmed).first())
        disconnected = true
        assertTrue(runCatching { bind().retry(unconfirmed) }.exceptionOrNull() is IOException)
        assertTrue(bind().canRetry(unconfirmed).first())
        disconnected = false
        for (error in listOf(403, 409, 500)) {
            status = error
            assertEquals(error, (runCatching { bind().retry(unconfirmed) }.exceptionOrNull() as HubFailure).status)
            assertTrue(bind().canRetry(unconfirmed).first())
        }
        status = 200
        failRefresh = true
        assertTrue(runCatching { bind().retry(unconfirmed) }.isFailure)
        assertFalse(bind().canRetry(unconfirmed).first()) // HTTP succeeded before local reconciliation failed.
        failRefresh = false
        bind().retry(unclear)
        bind().retry(unconfirmed)
        assertEquals(11, calls.size)
        bind().cancel(unclear.id)
        assertTrue(calls.last().startsWith("DELETE "))
        owner.cancel()
    }

    @Test fun closingTheChatWhileRetryIsInFlightCannotDispatchAnotherOrLoseTheResponse() = runBlocking {
        val owner = CoroutineScope(coroutineContext + SupervisorJob())
        val began = CompletableDeferred<Unit>()
        val response = CompletableDeferred<Unit>()
        val settled = CompletableDeferred<Unit>()
        var posts = 0
        val store = receipts()
        val key = SessionKey("desktop", "notes")
        fun bind() = HttpHeldAgentActions(key, MutableStateFlow(setOf(AgentMessageHoldFeature)), store, owner,
            { _, _, _ ->
                posts++
                began.complete(Unit)
                response.await()
                Response.Builder().request(Request.Builder().url("https://example.com/").build())
                    .protocol(Protocol.HTTP_1_1).code(200).message("OK").body("{}".toResponseBody()).build()
            }, { _, _ -> settled.complete(Unit) })
        val firstScreen = launch { bind().retry(unclear) }
        began.await()
        firstScreen.cancelAndJoin()
        bind().retry(unclear)
        response.complete(Unit)
        settled.await()
        bind().retry(unclear)
        assertEquals(1, posts)
        assertFalse(bind().canRetry(unclear).first())
        owner.cancel()
    }

    @Test fun onlyAnAuthoritativeWaitingToFailedCycleRearmsAnIdenticalFailure() = runBlocking {
        val store = receipts()
        val key = SessionKey("desktop", "notes")
        val unconfirmed = unclear.copy(error = "Couldn't confirm it arrived. Retry to send it anyway, or delete it.")
        val waiting = unclear.copy(state = "waiting", error = "Will check the chat when Studio Mac is back")
        var previous = Session(key.machineId, key.sessionId, undelivered = listOf(unclear))
        suspend fun frame(vararg rows: HeldAgentMessage) {
            val next = previous.copy(undelivered = rows.toList())
            store.reconcile(previous, next)
            previous = next
        }
        assertTrue(store.claim(key, unclear))
        frame(unclear)
        assertFalse(store.claim(key, unclear))
        assertTrue(store.claim(key, unconfirmed)) // A different read already presented the manager's later decision.
        frame(waiting)
        assertFalse(store.claim(key, unclear)) // An old cache can still paint the first warning.
        frame(unconfirmed)
        assertFalse(store.claim(key, unconfirmed))
        assertFalse(store.claim(key, unclear))
        frame(waiting)
        frame(unconfirmed)
        assertTrue(store.claim(key, unconfirmed)) // The manager genuinely failed again with the same wire fields.
        assertFalse(store.claim(key, unclear))
        frame()
        assertTrue(store.canRetry(key, unconfirmed).first())
    }
}
