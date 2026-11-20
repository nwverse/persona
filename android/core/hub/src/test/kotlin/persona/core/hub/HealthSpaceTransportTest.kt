package persona.core.hub

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.SessionKey
import persona.core.contracts.api.RequestManager

class HealthSpaceTransportTest {
    @Test fun defaultHealthOwnerIsUsedThroughReportMemoryAndNewChatEvenWithANamedCaller() = runBlocking {
        ProtocolCompatibilityMockServer().use { hub ->
            val api = HttpInboxAssistantTransport("private-pairing", hub.url("/").toString(), "fixture", OkHttpClient())
            suspend fun <T> exchange(method: String, path: List<String>, body: String, space: Boolean, run: suspend () -> T): T {
                hub.enqueue(MockResponse().setBody(body))
                val result = withContext(RequestManager("other-manager")) { run() }
                val request = hub.takeRequest()
                val url = requireNotNull(request.requestUrl)
                assertEquals(method, request.method)
                assertEquals(listOf("api", "v1") + path, url.pathSegments)
                assertEquals(if (space) "health" else null, url.queryParameter("space"))
                // Fixed test origin keeps the empty pin. HubHttpClient strips it and suppresses the active manager header.
                assertEquals("", url.queryParameter(ManagerQuery))
                assertEquals("Bearer fixture", request.getHeader("Authorization"))
                if (method == "POST") assertEquals("""{"space":"health","prompt":"Review sample"}""", request.body.readUtf8())
                if (method == "PUT") assertEquals("""{"content":"Sample update"}""", request.body.readUtf8())
                return result
            }
            val owner = exchange("GET", listOf("assistant"), """{"machine_id":"Sample computer","conversations":[]}""", true) { api.healthSpace() }
            assertEquals("Sample computer", owner.machineId)
            val reports = listOf("machines", "Sample computer", "health", "reports")
            val files = exchange("GET", reports,
                """{"reports":[{"name":"Sample A.pdf","title":"Sample A","path":"/health/reports/Sample A.pdf","record_path":"/health/reports/Sample A.md"},{"name":"記録.md","title":"記録","record_path":"/health/reports/記録.md"}]}""", false) { owner.reports() }
            assertEquals(listOf("Sample A", "記録"), files.map { it.title })
            assertNull(files[1].path)
            exchange("DELETE", reports + files.first().name, "{}", false) { owner.deleteReport(files.first().name) }
            val memory = listOf("assistant", "memory")
            val notes = exchange("GET", memory, """{"files":[{"name":"fixture.md","content":"Sample note"}]}""", true) { owner.memory() }
            assertEquals("Sample note", notes.single().content)
            exchange("PUT", memory + "fixture.md", "{}", true) { owner.saveMemory("fixture.md", "Sample update") }
            exchange("DELETE", memory + "fixture.md", "{}", true) { owner.deleteMemory("fixture.md") }
            val chat = exchange("POST", listOf("assistant", "conversations"),
                """{"session":{"machine_id":"Sample computer","session_id":"health-chat"}}""", true) { owner.startReportChat("Review sample") }
            assertEquals(SessionKey("Sample computer", "health-chat"), chat)
        }
    }

    @Test fun oldNodeOrRefusalKeepsItsStatusAndARetiredPairCannotReadOrChangeRecords() = runBlocking {
        ProtocolCompatibilityMockServer().use { hub ->
            var active = true
            val api = HttpInboxAssistantTransport("fixture", hub.url("/").toString(), "test", OkHttpClient(), isCurrent = { active })
            hub.enqueue(MockResponse().setBody("""{"machine_id":"health-machine"}"""))
            val owner = api.healthSpace()
            hub.takeRequest()
            for ((status, code) in listOf(409 to "node_outdated", 403 to "read_only")) {
                hub.enqueue(MockResponse().setResponseCode(status).setBody("""{"code":"$code","error":"Fixture refusal"}"""))
                val failure = runCatching { owner.reports() }.exceptionOrNull() as HubFailure
                assertEquals(status, failure.status)
                assertEquals(code, failure.code)
                hub.takeRequest()
            }
            active = false
            assertTrue(runCatching { owner.deleteReport("Example.pdf") }.exceptionOrNull() is CancellationException)
            assertTrue(runCatching { owner.memory() }.exceptionOrNull() is CancellationException)
            assertEquals(3, hub.requestCount)
        }
    }

    @Test fun noHealthMachineNeverFallsBackToAChatFromAnotherMachine() = runBlocking {
        ProtocolCompatibilityMockServer().use { hub ->
            val api = HttpInboxAssistantTransport("fixture", hub.url("/").toString(), "test", OkHttpClient())
            hub.enqueue(MockResponse().setBody("""{"conversations":[{"machine_id":"other","session_id":"regular"}]}"""))
            assertTrue(runCatching { api.healthSpace() }.isFailure)
            assertEquals(1, hub.requestCount)
        }
    }
}
