package dev.persona.watch

import dev.persona.watch.data.WatchMessage

/** A known ping is a receipt, not a request to send again while its agent reply is pending. */
internal class LivePingExchange(initial: List<WatchMessage>, private val ping: String, private val pong: String, now: Long) {
    private var latest = initial
    private var sendAttempted = false
    var acknowledgedAt: Long? = null
        private set
    var completedAt: Long? = null
        private set
    val shouldSend get() = acknowledgedAt == null && !sendAttempted

    init { observe(initial, now) }

    fun observe(messages: List<WatchMessage>, now: Long) {
        val count = messages.count { it.own && it.text == ping }
        check(count <= 1) { "live_ping_duplicate_ping" }
        if (count == 1) {
            if (acknowledgedAt == null) acknowledgedAt = now
        }
        completedAt = now.takeIf { count == 1 && messages.any { !it.own && it.role == "assistant" && it.text.trim() == pong } }
        latest = messages
    }

    fun sendIfMissing(send: () -> Unit): Boolean {
        if (!shouldSend) return false
        // A false/uncertain UI action throws, but never permits a second attempt.
        sendAttempted = true
        send()
        return true
    }

    fun awaitReply(totalDeadline: Long, now: () -> Long, verify: () -> Unit,
                   read: (remainingMs: Long) -> List<WatchMessage>, wait: (Long) -> Unit): List<WatchMessage> {
        val end = minOf(totalDeadline, now() + REPLY_WINDOW_MS)
        verify()
        while (now() < end) {
            verify()
            val remaining = end - now()
            if (remaining <= 0) break
            observe(read(remaining), now())
            if (completedAt != null) return latest
            val delay = minOf(POLL_MS, (end - now()).coerceAtLeast(0))
            if (delay > 0) wait(delay)
        }
        error(if (acknowledgedAt != null) "live_ping_agent_response_timeout" else "live_ping_ping_not_observed")
    }

    companion object {
        const val REPLY_WINDOW_MS = 45_000L
        private const val POLL_MS = 1_000L
    }
}
