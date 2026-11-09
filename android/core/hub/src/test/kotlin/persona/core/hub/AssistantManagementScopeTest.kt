package persona.core.hub

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import org.junit.After
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.RequestManager
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

class AssistantManagementScopeTest {
    @After fun reset() = AssistantManagerChoice.reset()

    @Test fun pinnedManagerAndDefaultSurviveSelectionChangeBeforeInterceptorsRun() = runBlocking {
        for (manager in listOf("work", null)) {
            MockWebServer().use { server ->
                HubHttpClient("hub-a", null, "test").use { hub ->
                    val entered = CompletableDeferred<Unit>()
                    val release = CountDownLatch(1)
                    val builder = hub.client.newBuilder()
                    builder.interceptors().add(0) { chain ->
                        entered.complete(Unit)
                        check(release.await(5, TimeUnit.SECONDS)) { "Timed out waiting for selection change" }
                        chain.proceed(chain.request())
                    }
                    val api = HttpAssistantManagement(server.url("/").toString(), "test-token", builder.build())
                        .forManager(manager)
                    AssistantManagerChoice.choose("hub-a", manager, "assistant")
                    server.enqueue(MockResponse().setBody("{}"))
                    val request = async { api.warm() }
                    try {
                        withTimeout(5_000) { entered.await() }
                        AssistantManagerChoice.pick("other-manager")
                    } finally {
                        release.countDown()
                    }
                    request.await()
                    server.takeRequest().let { wire ->
                        assertEquals(manager, wire.requestUrl?.queryParameter(ManagerQuery))
                        assertEquals(manager, wire.getHeader(ManagerHeader))
                        assertEquals("Bearer test-token", wire.getHeader("Authorization"))
                    }
                }
            }
        }
    }

    @Test fun coroutineScopePinsRequestsAndExplicitInstanceTakesPrecedence() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAssistantManagement(server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("{}"))
            withContext(RequestManager("work")) { api.warm() }
            assertEquals("work", server.takeRequest().requestUrl?.queryParameter(ManagerQuery))

            server.enqueue(MockResponse().setBody("{}"))
            withContext(RequestManager("other")) { api.forManager("work").warm() }
            assertEquals("work", server.takeRequest().requestUrl?.queryParameter(ManagerQuery))

            server.enqueue(MockResponse().setBody("{}"))
            api.warm()
            assertEquals("/api/v1/assistant/warm", server.takeRequest().path)
        }
    }

    @Test fun managerCloneKeepsPairingGuardAndCanBeRebound() = runBlocking {
        MockWebServer().use { server ->
            var current = true
            val api = HttpAssistantManagement(server.url("/").toString(), "test-token", OkHttpClient()) { current }
                .forManager("work")
            server.enqueue(MockResponse().setBody("{}"))
            api.forManager("other").warm()
            assertEquals("other", server.takeRequest().requestUrl?.queryParameter(ManagerQuery))
            current = false
            assertTrue(runCatching { api.warm() }.exceptionOrNull() is CancellationException)
            assertEquals(1, server.requestCount)
        }
    }
}
