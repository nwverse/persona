package persona.core.hub

import persona.core.contracts.JsonCodec
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.*
import org.junit.Test

class HttpAgentOperationsTest {
    @Test fun pathsBodiesAndFailuresMatchHubContractWithoutRetryingWrites() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAgentOperations("scope", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("{}"))
            api.renameSession(SessionKey("machine/id", "session#id"), "新名称", null)
            val rename = server.takeRequest()
            assertEquals("POST", (rename.method).zhAny())
            assertEquals("/api/v1/sessions/machine%2Fid/session%23id/rename", (rename.path).zhAny())
            assertEquals("Bearer test-token", (rename.getHeader("Authorization")).zhAny())
            assertEquals(buildJsonObject { put("title", "新名称") }, JsonCodec.parseToJsonElement(rename.body.readUtf8()))
            server.enqueue(MockResponse().setBody("{}"))
            api.renameSpace("machine", "workspace/id", "新空间")
            assertEquals("/api/v1/machines/machine/spaces/workspace%2Fid/rename", (server.takeRequest().path).zhAny())
            server.enqueue(MockResponse().setBody("{}"))
            api.saveMutes(AgentOperationPreferences(spaces = listOf("machine/新空间"), allowed = setOf("machine/session")))
            val mute = server.takeRequest()
            assertEquals("PUT", (mute.method).zhAny())
            assertEquals("/api/v1/mutes", (mute.path).zhAny())
            assertEquals("machine/session", (JsonCodec.parseToJsonElement(mute.body.readUtf8()).jsonObject.getValue("allowed").jsonArray.single().jsonPrimitive.content).zhAny())
            server.enqueue(MockResponse().setResponseCode(400).setBody("{}"))
            try { api.closeSession(SessionKey("machine", "session")); fail("Close must fail") }
            catch (e: AgentOperationFailure) { assertFalse(e.uncertain) }
            assertEquals("/api/v1/sessions/machine/session/close", (server.takeRequest().path).zhAny())
            server.enqueue(MockResponse().setResponseCode(503).setBody("{}"))
            try { api.closeSession(SessionKey("machine", "session")); fail("Close must be uncertain") }
            catch (e: AgentOperationFailure) { assertTrue(e.uncertain) }
            server.takeRequest()
            assertEquals(5, server.requestCount)
        }
    }

    @Test fun attachPostsToTheSessionAndRefusalsAreNotUncertain() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAgentOperations("scope", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("{}"))
            api.attachSession(SessionKey("server", "c0de-thread"))
            val attach = server.takeRequest()
            assertEquals("POST", (attach.method).zhAny())
            assertEquals("/api/v1/sessions/server/c0de-thread/attach", (attach.path).zhAny())
            server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"这个会话不能实时接入","code":"codex_read_only","delivered":"none"}"""))
            try { api.attachSession(SessionKey("server", "history")); fail("Attach must fail") }
            catch (e: AgentOperationFailure) { assertFalse(e.uncertain) }
        }
    }

    @Test fun deleteSendsTheSessionsDeleteAndRefusalsAreNotUncertain() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAgentOperations("scope", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"ok":true}"""))
            api.deleteSession(SessionKey("server", "s1"))
            val delete = server.takeRequest()
            assertEquals("DELETE", (delete.method).zhAny())
            assertEquals("/api/v1/sessions/server/s1", (delete.path).zhAny())
            server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"这个 Agent 还在运行，先关闭再删除"}"""))
            try { api.deleteSession(SessionKey("server", "s1")); fail("Delete must fail") }
            catch (e: AgentOperationFailure) { assertFalse(e.uncertain) }
        }
    }

    @Test fun forkSessionPostsEmptyAtItemIdAndReturnsTheBranchedSessionId() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAgentOperations("scope", server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"session_id":"branched-42"}"""))
            assertEquals("branched-42", api.forkSession(SessionKey("server", "source-1")))
            val fork = server.takeRequest()
            assertEquals("POST", (fork.method).zhAny())
            assertEquals("/api/v1/sessions/server/source-1/fork", (fork.path).zhAny())
            assertEquals(buildJsonObject { put("at_item_id", "") }, JsonCodec.parseToJsonElement(fork.body.readUtf8()))
            server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"这一轮还没做完，等它停下再分支","code":"fork_turn_running"}"""))
            try { api.forkSession(SessionKey("server", "source-1")); fail("Fork must fail while turn runs") }
            catch (e: AgentOperationFailure) { assertFalse(e.uncertain) }
        }
    }
}
