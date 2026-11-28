package dev.persona.watch.data

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class HubClientTest {
    private val config = HubConfig("http://127.0.0.1:18660", "fixture-pair-token")
    private val session = WatchSession("computer/a", "thread with space", "Test", "working", canSend = true, canInterrupt = true)

    @Test fun pairingMintsWatchRoleAndDoesNotCarryFullToken() {
        val client = HubClient(config, transport = HubTransport { c, method, path, body, _ ->
            assertEquals("fixture-pair-token", c.token)
            assertEquals("POST", method)
            assertEquals("/api/v1/auth/tokens", path)
            assertEquals("watch", JSONObject(body!!).getString("kind"))
            HubResponse(200, """{"role":"answer","id":"dev_fixture","token":"fixture-watch-token"}""")
        })
        val paired = client.pair()
        assertEquals("fixture-watch-token", paired.token)
        assertEquals("dev_fixture", paired.deviceId)
        assertFalse(paired.toString().contains("fixture"))
    }

    @Test fun refusesPairingResponseWithFullRole() {
        val client = HubClient(config, transport = HubTransport { _, _, _, _, _ ->
            HubResponse(200, """{"role":"full","id":"dev_fixture","token":"fixture-full-token"}""")
        })
        assertEquals("invalid_watch_credential", assertThrows(HubException::class.java) { client.pair() }.code)
    }

    @Test fun messagesEncodeSegmentsAndKeepRetryIdentity() {
        val sent = mutableListOf<String>()
        val opId = "373fcf63-52a2-4614-b89b-01ca2338ba56"
        val client = HubClient(config, transport = HubTransport { _, method, path, body, headers ->
            assertEquals("POST", method)
            assertEquals("/api/v1/sessions/computer%2Fa/thread%20with%20space/messages", path)
            assertEquals(opId, headers["X-Persona-Op-Id"])
            assertEquals("zh-Hans", headers["Accept-Language"])
            sent += JSONObject(body!!).getString("client_msg_id")
            assertEquals("Hello", JSONObject(body).getString("text"))
            HubResponse(200, """{"ok":true}""")
        })
        client.send(session, "Hello", opId)
        client.send(session, "Hello", opId)
        assertEquals(listOf(opId, opId), sent)
    }

    @Test fun failuresNeverExposeServerBodyOrSecret() {
        val client = HubClient(config, transport = HubTransport { _, _, _, _, _ ->
            HubResponse(401, """{"error":"secret fixture-pair-token https://secret.invalid/"}""")
        })
        val failure = assertThrows(HubException::class.java) { client.refresh() }
        assertEquals("pairing_expired", failure.message)
        assertEquals(401, failure.status)
        assertFalse(failure.toString().contains("fixture"))
    }

    @Test fun redirectIsRejectedInsteadOfFollowingCredential() {
        val client = HubClient(config, transport = HubTransport { _, _, _, _, _ -> HubResponse(302, "") })
        assertEquals("redirect_refused", assertThrows(HubException::class.java) { client.refresh() }.code)
    }

    @Test fun expiredInteractionIsHandledAsGone() {
        val ix = WatchInteraction("i", "m", "s", "permission", "", "", canAnswer = true)
        val client = HubClient(config, transport = HubTransport { _, _, path, _, headers ->
            assertEquals("/api/v1/interactions/i/resolve", path)
            assertNotNull(headers["X-Persona-Op-Id"])
            HubResponse(410, """{"status":"expired"}""")
        })
        assertTrue(client.resolve(ix, WatchResolution.permission(ix, true), "operation-001").gone)
    }

    @Test fun readOnlySendNeverReachesNetwork() {
        val client = HubClient(config, transport = HubTransport { _, _, _, _, _ -> fail("Unexpected request"); HubResponse(200, "{}") })
        assertEquals("read_only", assertThrows(HubException::class.java) {
            client.send(session.copy(canSend = false), "Hello", "operation-001")
        }.code)
    }

    @Test fun addressesRequireLanOrHttpsAndRejectCredentialUrls() {
        assertEquals("http://127.0.0.1:18660", HubConfig("http://127.0.0.1:18660/", "token").validated().baseUrl)
        assertEquals("https://hub.example.com/prefix", HubConfig("https://hub.example.com/prefix/", "token").validated().baseUrl)
        listOf("http://8.8.8.8", "http://example.com", "https://user:password@example.com", "https://example.com?token=x",
            "https://example.com/#fragment", "https://example.com/%2fother", "file:///tmp/hub").forEach { address ->
            assertThrows(address, HubException::class.java) { HubConfig(address, "token").validated() }
        }
        assertThrows(HubException::class.java) { HubConfig("http://127.0.0.1", "token\r\nX-Test: bad").validated() }
    }

    @Test fun snapshotPreservesOptionalInboxFailureSignal() {
        val client = HubClient(config, transport = HubTransport { _, _, path, _, _ -> when {
            path.endsWith("/sessions") -> HubResponse(200, """{"sessions":[]}""")
            path.endsWith("/interactions") -> HubResponse(200, """{"interactions":[]}""")
            path.endsWith("/machines") -> HubResponse(200, """{"machines":[]}""")
            else -> HubResponse(503, "{}")
        } })
        assertTrue(client.refresh().inboxUnavailable)
    }

    @Test fun foldedWorkIsFetchedOnlyOnDemandAndPagesProgress() {
        val paths = mutableListOf<String>()
        val expected = listOf(
            "/api/v1/sessions/computer%2Fa/thread%20with%20space/items?turns=2&limit=80&lazy_images=1&fold_work=1",
            "/api/v1/sessions/computer%2Fa/thread%20with%20space/items?from_seq=2&through_seq=3&limit=1000&lazy_images=1",
            "/api/v1/sessions/computer%2Fa/thread%20with%20space/items?from_seq=3&through_seq=3&limit=1000&lazy_images=1",
        )
        val client = HubClient(config, transport = HubTransport { _, method, path, body, _ ->
            assertEquals("GET", method)
            assertNull(body)
            assertTrue("Unexpected extra folded-work request: $path", paths.size < expected.size)
            assertEquals(expected[paths.size], path)
            paths += path
            when (path) {
                expected[0] -> HubResponse(200, """{"items":[{"id":"fold:f","seq":2,"kind":"fold","role":"assistant","fold":{"items":2,"through_seq":3}},{"id":"a","kind":"text","role":"assistant","text":"Answer"}]}""")
                expected[1] -> HubResponse(200, """{"items":[{"id":"tool","seq":2,"kind":"tool_use","tool_name":"Read","text":"Read a file"}],"more":true}""")
                expected[2] -> HubResponse(200, """{"items":[{"id":"result","seq":3,"kind":"tool_result","text":"Found the result"}],"more":false}""")
                else -> throw AssertionError("Unexpected folded-work route: $path")
            }
        })
        val message = client.messages(session).single()
        assertEquals(expected.take(1), paths)
        assertEquals(listOf("Read\nRead a file", "Found the result"), client.work(session, message))
        assertEquals(expected, paths)
    }

    @Test fun networkFailureDoesNotAutomaticallyRepeatMutation() {
        var calls = 0
        val client = HubClient(config, transport = HubTransport { _, _, _, _, _ -> calls++; throw HubException("timeout") })
        assertThrows(HubException::class.java) { client.send(session, "Hello", "operation-001") }
        assertEquals(1, calls)
    }
}
