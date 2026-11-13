package persona.core.hub

import java.io.IOException
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.*

class HttpSessionListsTest {
    private fun transport(server: MockWebServer): HubChatTransport {
        val client = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
        return HubChatTransport({ path, method, body, headers ->
            client.newCall(Request.Builder().url(server.url(path)).method(method, body).apply {
                headers.forEach { (key, value) -> header(key, value) }
            }.build()).execute()
        })
    }

    private val page = """{"sessions":[{"machine_id":"demo","session_id":"chat","waiting":true,"background_count":2,"running_subagents":1,"running_workflows":1}],"remove":["[\"demo\",\"gone/chat\"]"],"has_more":true,"next_cursor":"next+/=","revision":"checkpoint","total":8000}"""
    private val counts = """{"total":8000,"machine":{"demo":8000},"project":{"/demo/app":8000},"group":{"space":8000},"state":{"working":8000},"revision":"checkpoint"}"""
    private val filter = SessionListFilter("working", "demo /+&", "/demo/a & b", "space/+", "runtime&one", "agent+one", "words & 日本語")

    @Test fun pageEncodesEveryFilterAndOpaqueTokenAndDecodesSummary() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setBody(page))
            val result = transport(server).sessionPage(filter, "cursor/+ = &", "revision/+ = &")
            val request = server.takeRequest()
            val url = request.requestUrl!!
            assertEquals("GET", request.method)
            assertEquals("/api/v1/sessions", url.encodedPath)
            assertEquals("1", url.queryParameter("summary"))
            assertEquals("50", url.queryParameter("limit"))
            assertEquals(filter.state, url.queryParameter("state"))
            assertEquals(filter.machineId, url.queryParameter("machine_id"))
            assertEquals(filter.project, url.queryParameter("project"))
            assertEquals(filter.group, url.queryParameter("group"))
            assertEquals(filter.source, url.queryParameter("source"))
            assertEquals(filter.provider, url.queryParameter("provider"))
            assertEquals(filter.query, url.queryParameter("q"))
            assertEquals("cursor/+ = &", url.queryParameter("cursor"))
            assertEquals("revision/+ = &", url.queryParameter("since"))
            assertEquals(8000, result.total)
            assertEquals("checkpoint", result.revision)
            assertTrue(result.hasMore)
            assertEquals("next+/=", result.nextCursor)
            assertEquals(listOf("[\"demo\",\"gone/chat\"]"), result.remove)
            val session = result.sessions.single()
            assertEquals(true, session.waiting)
            assertEquals(2, session.backgroundCount)
            assertEquals(1, session.runningSubagents)
            assertEquals(1, session.runningWorkflowsCount)
            assertNull(session.background)
        }
    }

    @Test fun completeMembershipReadsCompleteRowsInLargePages() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setBody(page))
            transport(server).sessionPage(SessionListFilter(requiresCompleteMembership = true), since = "checkpoint")
            val url = server.takeRequest().requestUrl!!
            assertEquals("0", url.queryParameter("summary"))
            assertEquals("200", url.queryParameter("limit"))
            assertEquals("checkpoint", url.queryParameter("since"))
            assertNull(url.queryParameter("state"))
            assertNull(url.queryParameter("q"))
        }
    }

    @Test fun countsRetainAllSuppliedFiltersWithoutFetchingRows() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setBody(counts))
            val result = transport(server).sessionCounts(filter)
            val url = server.takeRequest().requestUrl!!
            assertEquals("1", url.queryParameter("counts"))
            assertEquals("1", url.queryParameter("summary"))
            assertEquals("50", url.queryParameter("limit"))
            assertEquals(filter.machineId, url.queryParameter("machine_id"))
            assertEquals(filter.state, url.queryParameter("state"))
            assertEquals(filter.query, url.queryParameter("q"))
            assertNull(url.queryParameter("cursor"))
            assertNull(url.queryParameter("since"))
            assertEquals(8000, result.total)
            assertEquals(mapOf("demo" to 8000), result.machine)
            assertEquals(mapOf("/demo/app" to 8000), result.project)
            assertEquals(mapOf("space" to 8000), result.group)
            assertEquals(mapOf("working" to 8000), result.state)
        }
    }

    @Test fun ignoredQueryAndMissingEnvelopeAreUnsupported(): Unit = runBlocking {
        MockWebServer().use { server ->
            val api = transport(server)
            for (body in listOf("""{"sessions":[]}""", """{"sessions":[],"revision":"r"}""", "[]")) {
                server.enqueue(MockResponse().setBody(body))
                assertThrows(SessionListsUnsupported::class.java) { runBlocking { api.sessionPage() } }
            }
            server.enqueue(MockResponse().setBody("""{"sessions":[]}"""))
            assertThrows(SessionListsUnsupported::class.java) { runBlocking { api.sessionCounts() } }
        }
    }

    @Test fun onlyCapabilityStatusesAllowFallbackAndOnlyCodedExpiryResets() = runBlocking {
        MockWebServer().use { server ->
            val api = transport(server)
            for (status in listOf(404, 405, 501)) {
                server.enqueue(MockResponse().setResponseCode(status))
                assertThrows(SessionListsUnsupported::class.java) { runBlocking { api.sessionPage() } }
                server.enqueue(MockResponse().setResponseCode(status))
                assertThrows(SessionListsUnsupported::class.java) { runBlocking { api.sessionCounts() } }
            }
            for (code in listOf("cursor_expired", "revision_expired")) {
                server.enqueue(MockResponse().setResponseCode(409).setBody("""{"code":"$code","error":"expired"}"""))
                assertThrows(SessionListExpired::class.java) { runBlocking { api.sessionPage(cursor = "cursor", since = "since") } }
                server.enqueue(MockResponse().setResponseCode(409).setBody("""{"code":"$code","error":"expired"}"""))
                assertThrows(SessionListExpired::class.java) { runBlocking { api.sessionCounts() } }
            }
            for (status in listOf(400, 401, 403, 409, 429, 500, 502, 503)) {
                server.enqueue(MockResponse().setResponseCode(status).setBody("""{"code":"invalid_query","error":"refused"}"""))
                val failure = assertThrows(HubFailure::class.java) { runBlocking { api.sessionPage() } }
                assertEquals(status, failure.status)
            }
        }
    }

    @Test fun invalidBoundedPayloadIsProtocolFailureAndNetworkFailureDoesNotFallback() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setBody("""{"sessions":"wrong","has_more":false,"total":0,"revision":"r"}"""))
            val failure = assertThrows(HubFailure::class.java) { runBlocking { transport(server).sessionPage() } }
            assertEquals(HubError.Protocol, failure.kind)
        }
        val original = IOException("connection lost")
        val api = HubChatTransport({ _, _, _, _ -> throw original })
        assertSame(original, assertThrows(IOException::class.java) { runBlocking { api.sessionPage() } })
    }
}
