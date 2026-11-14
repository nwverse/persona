package persona.core.hub

import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Session
import persona.core.contracts.api.SessionListFilter
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer

class HttpHierarchyTest {
    private val page = """{"sessions":[],"has_more":false,"total":0,"revision":"r"}"""
    private val counts = """{"total":0,"machine":{},"project":{},"group":{},"state":{},"revision":"r"}"""

    @Test fun hierarchyIsIndependentlyGatedAndRepeatedWithEveryRootPageCountAndDelta() = runBlocking {
        MockWebServer().use { server ->
            val client = OkHttpClient()
            var hierarchy = false
            val api = HubChatTransport({ path, method, body, _ ->
                client.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
            }, hierarchy = { hierarchy })
            server.enqueue(MockResponse().setBody(page))
            api.sessionPage()
            assertNull(server.takeRequest().requestUrl!!.queryParameter("roots"))
            hierarchy = true
            for (count in listOf(false, true)) {
                server.enqueue(MockResponse().setBody(if (count) counts else page))
                if (count) api.sessionCounts(SessionListFilter(machineId = "m"))
                else api.sessionPage(SessionListFilter(machineId = "m"), "opaque/+", "since/+")
                val url = server.takeRequest().requestUrl!!
                assertEquals("1", url.queryParameter("roots"))
                assertEquals("m", url.queryParameter("machine_id"))
                if (!count) {
                    assertEquals("opaque/+", url.queryParameter("cursor"))
                    assertEquals("since/+", url.queryParameter("since"))
                }
            }
            server.enqueue(MockResponse().setBody(page))
            api.sessionPage(SessionListFilter(machineId = "m", parentSessionId = "parent/+"))
            val childUrl = server.takeRequest().requestUrl!!
            assertNull(childUrl.queryParameter("roots"))
            assertEquals("parent/+", childUrl.queryParameter("parent_session_id"))
            server.enqueue(MockResponse().setBody(page))
            api.sessionPage(SessionListFilter(requiresCompleteMembership = true))
            assertNull(server.takeRequest().requestUrl!!.queryParameter("roots"))
        }
    }

    @Test fun parentChatReadsEveryChildPageWithItsOwnStateAndTheSameParentFilter() = runBlocking {
        MockWebServer().use { server ->
            val client = OkHttpClient()
            val api = HttpChatActions { path, method, body ->
                client.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
            }
            server.enqueue(MockResponse().setBody("""{"sessions":[{"session_id":"child","folded_under":"p/+","state":"needs_you"}],"has_more":true,"next_cursor":"next/+","total":2,"revision":"r"}"""))
            server.enqueue(MockResponse().setBody("""{"sessions":[{"session_id":"grandchild","folded_under":"p/+","state":"working"}],"has_more":false,"total":2,"revision":"r"}"""))
            val rows = api.childRows(Session("m", "p/+"))
            assertEquals(listOf("child", "grandchild"), rows.map { it.sessionId })
            assertEquals(listOf("needs_you", "working"), rows.map { it.state })
            assertTrue(rows.all { it.machineId == "m" })
            repeat(2) { i ->
                val url = server.takeRequest().requestUrl!!
                assertEquals("p/+", url.queryParameter("parent_session_id"))
                assertEquals("m", url.queryParameter("machine_id"))
                assertNull(url.queryParameter("roots"))
                assertEquals(if (i == 0) null else "next/+", url.queryParameter("cursor"))
            }
        }
    }
}
