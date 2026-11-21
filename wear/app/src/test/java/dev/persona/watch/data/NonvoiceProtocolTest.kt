package dev.persona.watch.data

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class NonvoiceProtocolTest {
    private val config = HubConfig("http://127.0.0.1:18660", "fixture-watch-token")
    private val inbox = InboxItem("mail/a b", "Sample", "Preview", "mail", true, manager = "group/a b")
    private val screen = WatchInteraction("screen", "fixture", "session", "screen", "", "", canAnswer = true)

    @Test fun pendingAndUnknownInboxRepliesRetainTheOriginalOperationForRetry() {
        for (state in listOf("pending", "unknown")) {
            val operations = mutableListOf<String?>()
            var attempts = 0
            val client = HubClient(config, transport = HubTransport { _, _, path, body, headers ->
                assertEquals("/api/v1/inbox/mail%2Fa%20b/reply?manager=group%2Fa%20b", path)
                assertEquals("Sample reply", JSONObject(body!!).getString("text"))
                operations += headers["X-Persona-Op-Id"]
                if (attempts++ == 0) HubResponse(202, JSONObject().put("state", state).toString())
                else HubResponse(200, """{"state":"sent"}""")
            })
            val failure = assertThrows(HubException::class.java) { client.reply(inbox, "Sample reply", "same-operation") }
            assertEquals("delivery_unconfirmed", failure.code)
            client.reply(inbox, "Sample reply", "same-operation")
            assertEquals(listOf("same-operation", "same-operation"), operations)
        }
    }

    @Test fun draftUsesOwningManagerAndDoesNotSendOrMarkDone() {
        val paths = mutableListOf<String>()
        val client = HubClient(config, transport = HubTransport { _, method, path, body, _ ->
            paths += path
            assertEquals("POST", method)
            assertEquals("/api/v1/inbox/mail%2Fa%20b/draft?manager=group%2Fa%20b", path)
            assertEquals(setOf("instruction"), JSONObject(body!!).keys().asSequence().toSet())
            assertEquals("", JSONObject(body).getString("instruction"))
            HubResponse(200, """{"text":"A suggestion awaiting review"}""")
        })
        assertEquals("A suggestion awaiting review", client.draftReply(inbox))
        assertEquals(1, paths.size)
        assertFalse(paths.any { it.contains("/reply") || it.contains("/done") })
    }

    @Test fun cannotGenerateDraftForAnItemWithoutReplyCapability() {
        var calls = 0
        val client = HubClient(config, transport = HubTransport { _, _, _, _, _ -> calls++; HubResponse(200, "{}") })
        assertEquals("read_only", assertThrows(HubException::class.java) { client.draftReply(inbox.copy(canReply = false)) }.code)
        assertEquals(0, calls)
    }

    @Test fun unusableDraftResponsesAreRejected() {
        listOf("{}", """{"text":"   "}""", JSONObject().put("text", "x".repeat(16001)).toString()).forEach { response ->
            val client = HubClient(config, transport = HubTransport { _, _, _, _, _ -> HubResponse(200, response) })
            assertEquals("invalid_response", assertThrows(HubException::class.java) { client.draftReply(inbox) }.code)
        }
    }

    @Test fun uncertainDraftGenerationDoesNotRetryOrSend() {
        var calls = 0
        val client = HubClient(config, transport = HubTransport { _, _, path, _, _ ->
            calls++
            assertTrue(path.substringBefore('?').endsWith("/draft"))
            throw HubException("timeout")
        })
        assertEquals("timeout", assertThrows(HubException::class.java) { client.draftReply(inbox) }.code)
        assertEquals(1, calls)
    }

    @Test fun serverSuggestionAndAccountNameArePreservedWithoutInventingReplyCapability() {
        val items = WatchParser.inbox(JSONObject("""{"items":[
            {"id":"reply","title":"Review","source":"mail","status":"action","account_name":"Sample mailbox","manager":"m","agent":{"draft":"Prepared reply"}},
            {"id":"readonly","source":"calendar","status":"action","agent":{"draft":"Not a sending capability"}}
        ]}"""))
        assertEquals("Prepared reply", items[0].suggestedReply)
        assertEquals("Sample mailbox", items[0].accountName)
        assertEquals("m", items[0].manager)
        assertTrue(items[0].canReply)
        assertFalse(items[1].canReply)
    }

    @Test fun terminalNavigationAllowsOnlyOneEscapeOrEnterAndRespectsReadOnly() {
        for (key in listOf("esc", "enter")) WatchResolution.validate(screen, JSONObject().put("keys", JSONArray().put(key)))
        for (body in listOf("""{"keys":[]}""", """{"keys":["ctrl-c"]}""", """{"keys":["esc","enter"]}""", """{"keys":["enter"],"text":"unexpected"}""")) {
            assertThrows(HubException::class.java) { WatchResolution.validate(screen, JSONObject(body)) }
        }
        for (blocked in listOf(screen.copy(canAnswer = false), screen.copy(muted = true), screen.copy(delegating = true))) {
            assertEquals("read_only", assertThrows(HubException::class.java) {
                WatchResolution.validate(blocked, JSONObject("""{"keys":["enter"]}"""))
            }.code)
        }
    }

    @Test fun handoffDoesNotEncodeApprovalOrBypassUnknownRequests() {
        val permission = screen.copy(kind = "permission", requiresInput = true)
        val body = WatchResolution.dismiss(permission)
        assertEquals(setOf("dismiss"), body.keys().asSequence().toSet())
        assertTrue(body.getBoolean("dismiss"))
        WatchResolution.validate(permission, body)
        assertThrows(HubException::class.java) { WatchResolution.dismiss(permission.copy(canAnswer = false)) }
        assertThrows(HubException::class.java) { WatchResolution.dismiss(permission.copy(kind = "future")) }
    }
}
