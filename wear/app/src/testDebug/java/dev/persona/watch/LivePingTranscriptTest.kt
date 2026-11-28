package dev.persona.watch

import dev.persona.watch.data.WatchMessage
import dev.persona.watch.data.WatchParser
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class LivePingTranscriptTest {
    private val anchor = WatchMessage("acceptance-marker", "user", "Samsung Watch Persona PING_WATCH PONG_WATCH", true)
    private val ready = WatchMessage("ready", "assistant", "WATCH_READY", false)
    private val ping = WatchMessage("ping", "user", "PING_WATCH", true)
    private val pong = WatchMessage("pong", "assistant", "PONG_WATCH", false)
    private fun guard(initial: List<WatchMessage> = listOf(anchor, ready)) =
        LivePingTranscript(initial, "Samsung Watch Persona", "WATCH_READY", "PING_WATCH", "PONG_WATCH")

    @Test fun deserializedCopiesKeepTheOriginalMarkerIdentityAcrossReads() {
        val guard = guard()
        guard.verify(listOf(anchor.copy(), ready.copy()))
        guard.verify(listOf(anchor.copy(), ready.copy(), ping, pong))
    }

    @Test fun aWindowWithoutItsOriginalMarkerFailsEvenIfAnotherMarkerHasIdenticalText() {
        for (window in listOf(listOf(ready, ping, pong), listOf(anchor.copy(id = "new-marker"), ready))) {
            val failure = assertThrows(IllegalStateException::class.java) { guard().verify(window) }
            assertEquals("live_ping_history_anchor_lost", failure.message)
        }
    }

    @Test fun losingTheHistoryAnchorBeforeSendCannotReachTheSendAction() {
        val initial = listOf(anchor, ready)
        val guard = guard(initial)
        val exchange = LivePingExchange(initial, ping.text, pong.text, 0L)
        var sends = 0
        val changed = listOf(ready, pong)
        assertThrows(IllegalStateException::class.java) {
            guard.verify(changed)
            exchange.observe(changed, 1L)
            exchange.sendIfMissing { sends++ }
        }
        assertEquals(0, sends)
        assertTrue(exchange.shouldSend)
    }

    @Test fun theProductionTranscriptWindowCannotHideAnUnrelatedRequestAndPassTheGuard() {
        fun item(message: WatchMessage) = JSONObject().put("id", message.id).put("kind", "text")
            .put("role", message.role).put("text", message.text).put("turn_id", message.id)
        val items = JSONArray().put(item(anchor)).put(item(ready))
        val initial = WatchParser.messages(JSONObject().put("items", items))
        val guard = guard(initial)
        items.put(item(WatchMessage("unrelated", "user", "Sample unrelated request", true)))
        repeat(6) { index -> items.put(item(ready.copy(id = "new-turn-$index"))) }
        val advanced = WatchParser.messages(JSONObject().put("items", items))
        assertEquals(6, advanced.size)
        assertFalse(advanced.any { it.id == anchor.id || it.id == "unrelated" })
        val failure = assertThrows(IllegalStateException::class.java) { guard.verify(advanced) }
        assertEquals("live_ping_history_anchor_lost", failure.message)
    }

    @Test fun unrelatedMessagesCannotHideBehindTheStableMarker() {
        val unrelated = WatchMessage("unrelated", "user", "Sample unrelated request", true)
        val failure = assertThrows(IllegalStateException::class.java) { guard().verify(listOf(anchor, ready, unrelated)) }
        assertEquals("live_ping_unrelated_chat_content", failure.message)
        assertThrows(IllegalStateException::class.java) {
            guard().verify(listOf(anchor.copy(text = anchor.text + " changed"), ready))
        }
    }

    @Test fun ambiguousOrMissingMarkerIdsAndDuplicatePingsFailClosed() {
        for (id in listOf("", "null")) {
            val failure = assertThrows(IllegalStateException::class.java) { guard(listOf(anchor.copy(id = id), ready)) }
            assertEquals("live_ping_initial_marker_id_required", failure.message)
        }
        assertThrows(IllegalStateException::class.java) { guard(listOf(ready)) }
        assertThrows(IllegalStateException::class.java) { guard().verify(listOf(anchor, anchor.copy(), ready)) }
        assertThrows(IllegalStateException::class.java) { guard().verify(listOf(anchor, ready, ping, ping.copy(id = "other-ping"))) }
        assertThrows(IllegalStateException::class.java) { guard().verify(listOf(anchor)) }
    }

    @Test fun aPreSendPingReceiptStillPreventsDuplicateSending() {
        val initial = listOf(anchor, ready)
        val guard = guard(initial)
        val exchange = LivePingExchange(initial, ping.text, pong.text, 0L)
        val changed = listOf(anchor.copy(), ready.copy(), ping)
        guard.verify(changed)
        exchange.observe(changed, 1L)
        assertFalse(exchange.sendIfMissing { fail("An existing ping must not be sent again") })
    }
}
