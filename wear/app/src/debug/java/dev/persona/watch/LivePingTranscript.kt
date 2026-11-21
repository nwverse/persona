package dev.persona.watch

import dev.persona.watch.data.WatchMessage

/** The bounded transcript must still contain the original acceptance-chat marker before any write. */
internal class LivePingTranscript(initial: List<WatchMessage>, private val marker: String,
                                  private val ready: String, private val ping: String, private val pong: String) {
    private val anchor = initial.singleOrNull(::isMarker) ?: error("live_ping_initial_marker_required")

    init {
        check(anchor.id.isNotBlank() && anchor.id != "null") { "live_ping_initial_marker_id_required" }
        verify(initial)
    }

    fun verify(messages: List<WatchMessage>) {
        val current = messages.singleOrNull { it.id == anchor.id }
        check(current != null && current.own && current.text == anchor.text && isMarker(current)) {
            "live_ping_history_anchor_lost"
        }
        check(messages.count(::isMarker) == 1) { "live_ping_initial_marker_required" }
        check(messages.any { !it.own && it.role == "assistant" && it.text.trim() in setOf(ready, pong) }) {
            "live_ping_ready_marker_required"
        }
        check(messages.filter { it.own }.all { it.id == anchor.id || it.text == ping }) {
            "live_ping_unrelated_chat_content"
        }
        check(messages.count { it.own && it.text == ping } <= 1) { "live_ping_duplicate_ping" }
    }

    private fun isMarker(message: WatchMessage) =
        message.own && message.text.contains(marker) && message.text.contains(ping) && message.text.contains(pong)
}
