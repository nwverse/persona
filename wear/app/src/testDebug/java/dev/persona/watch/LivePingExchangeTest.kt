package dev.persona.watch

import dev.persona.watch.data.WatchMessage
import org.junit.Assert.*
import org.junit.Test

class LivePingExchangeTest {
    private val ping = WatchMessage("ping", "user", "PING_WATCH", true)
    private val pong = WatchMessage("pong", "assistant", "PONG_WATCH", false)
    private class Clock(var time: Long = 100L) {
        val waits = mutableListOf<Long>()
        fun now() = time
        fun waitFor(delay: Long) { waits += delay; time += delay }
    }
    private fun exchange(initial: List<WatchMessage> = emptyList(), now: Long = 100L) =
        LivePingExchange(initial, ping.text, pong.text, now)

    @Test fun acceptedPingIsReusedWhileItsReplyIsStillPending() {
        val clock = Clock()
        val exchange = exchange(listOf(ping))
        var sends = 0
        var reads = 0
        assertFalse(exchange.shouldSend)
        assertFalse(exchange.sendIfMissing { sends++ })
        val result = exchange.awaitReply(110_100L, clock::now, {}, {
            reads++
            if (reads == 4) listOf(ping, pong) else listOf(ping)
        }, clock::waitFor)
        assertEquals(listOf(ping, pong), result)
        assertEquals(4, reads)
        assertEquals(0, sends)
        assertEquals(100L, checkNotNull(exchange.acknowledgedAt))
        assertEquals(3_100L, checkNotNull(exchange.completedAt))
        assertEquals(listOf(1_000L, 1_000L, 1_000L), clock.waits)
    }

    @Test fun acceptedPingWithoutReplyWaitsTheEntireReplyWindow() {
        val clock = Clock()
        val exchange = exchange(listOf(ping))
        val budgets = mutableListOf<Long>()
        val failure = assertThrows(IllegalStateException::class.java) {
            exchange.awaitReply(110_100L, clock::now, {}, { remaining ->
                budgets += remaining
                listOf(ping)
            }, clock::waitFor)
        }
        assertEquals("live_ping_agent_response_timeout", failure.message)
        assertEquals(45_100L, clock.time)
        assertEquals(45, budgets.size)
        assertEquals(45_000L, budgets.first())
        assertEquals(1_000L, budgets.last())
        assertFalse(exchange.shouldSend)
    }

    @Test fun aPreviouslyObservedPingIsRememberedWhenLaterSnapshotsOmitIt() {
        val clock = Clock()
        val exchange = exchange(listOf(ping))
        var reads = 0
        val failure = assertThrows(IllegalStateException::class.java) {
            exchange.awaitReply(110_100L, clock::now, {}, {
                reads++
                emptyList()
            }, clock::waitFor)
        }
        assertEquals("live_ping_agent_response_timeout", failure.message)
        assertEquals(45, reads)
        assertEquals(45_100L, clock.time)
        assertEquals(100L, checkNotNull(exchange.acknowledgedAt))
        assertFalse(exchange.sendIfMissing { fail("A known ping must not be sent again") })
    }

    @Test fun anExpiredTotalDeadlineDoesNotReadOrWaitAndRetainsThePingDistinction() {
        val clock = Clock()
        for ((initial, expected) in listOf(
            listOf(ping) to "live_ping_agent_response_timeout",
            emptyList<WatchMessage>() to "live_ping_ping_not_observed",
        )) {
            val exchange = exchange(initial)
            val failure = assertThrows(IllegalStateException::class.java) {
                exchange.awaitReply(clock.now(), clock::now, {}, {
                    fail("An expired deadline must not start another read")
                    emptyList()
                }, clock::waitFor)
            }
            assertEquals(expected, failure.message)
            if (initial.isNotEmpty()) {
                assertFalse(exchange.sendIfMissing { fail("A known ping must not be sent again") })
            }
        }
        assertEquals(100L, clock.time)
        assertTrue(clock.waits.isEmpty())
    }

    @Test fun absentPingHasOneSendAttemptAndASeparateDeadlineBoundedFailure() {
        val clock = Clock()
        val exchange = exchange()
        var sends = 0
        val budgets = mutableListOf<Long>()
        assertTrue(exchange.sendIfMissing { sends++ })
        assertFalse(exchange.sendIfMissing { sends++ })
        val failure = assertThrows(IllegalStateException::class.java) {
            exchange.awaitReply(2_600L, clock::now, {}, { remaining ->
                budgets += remaining
                emptyList()
            }, clock::waitFor)
        }
        assertEquals("live_ping_ping_not_observed", failure.message)
        assertEquals(1, sends)
        assertEquals(listOf(2_500L, 1_500L, 500L), budgets)
        assertEquals(listOf(1_000L, 1_000L, 500L), clock.waits)
        assertEquals(2_600L, clock.time)
        assertNull(exchange.acknowledgedAt)
        assertNull(exchange.completedAt)
    }

    @Test fun newlySentPingAndReplyRecordTheirSeparateObservationTimes() {
        val clock = Clock()
        val exchange = exchange()
        exchange.sendIfMissing {}
        var reads = 0
        exchange.awaitReply(110_100L, clock::now, {}, {
            when (++reads) {
                1 -> emptyList()
                2 -> listOf(ping)
                else -> listOf(ping, pong)
            }
        }, clock::waitFor)
        assertEquals(1_100L, checkNotNull(exchange.acknowledgedAt))
        assertEquals(2_100L, checkNotNull(exchange.completedAt))
        assertFalse(exchange.shouldSend)
    }

    @Test fun pingFoundByThePreClickReadPreventsAnotherSend() {
        val clock = Clock()
        val exchange = exchange()
        exchange.observe(listOf(ping), clock.now())
        assertFalse(exchange.sendIfMissing { fail("A pre-click receipt must prevent a duplicate send") })
        var reads = 0
        exchange.awaitReply(110_100L, clock::now, {}, {
            if (++reads == 2) listOf(ping, pong) else listOf(ping)
        }, clock::waitFor)
        assertEquals(2, reads)
    }

    @Test fun anUnconfirmedSendIsNeverRetried() {
        val exchange = exchange()
        val clickFailure = IllegalStateException("live_ping_send_click_unconfirmed")
        val failure = assertThrows(IllegalStateException::class.java) {
            exchange.sendIfMissing { throw clickFailure }
        }
        assertSame(clickFailure, failure)
        assertFalse(exchange.sendIfMissing { fail("An uncertain click must not be retried") })
    }

    @Test fun readAndIsolationFailuresRemainSeparateFromReplyTimeouts() {
        val clock = Clock()
        val exchange = exchange(listOf(ping))
        val readFailure = AssertionError("live_ping_reply_read_failed")
        assertSame(readFailure, assertThrows(AssertionError::class.java) {
            exchange.awaitReply(110_100L, clock::now, {}, { throw readFailure }, clock::waitFor)
        })
        val guardFailure = IllegalStateException("live_ping_extra_connections_not_allowed")
        assertSame(guardFailure, assertThrows(IllegalStateException::class.java) {
            exchange.awaitReply(110_100L, clock::now, { throw guardFailure }, {
                fail("Isolation must be verified before reading")
                emptyList()
            }, clock::waitFor)
        })
        assertTrue(clock.waits.isEmpty())
    }

    @Test fun anAlreadyCompletedExchangeStillChecksTheCurrentTranscript() {
        val clock = Clock()
        val exchange = exchange(listOf(ping, pong))
        var reads = 0
        exchange.awaitReply(110_100L, clock::now, {}, {
            if (++reads == 1) listOf(ping) else listOf(ping, pong)
        }, clock::waitFor)
        assertEquals(2, reads)
        assertEquals(1_100L, checkNotNull(exchange.completedAt))
    }

    @Test fun pongWithoutAPingDoesNotCompleteAndDuplicatePingsAreRejected() {
        val clock = Clock()
        val exchange = exchange(listOf(pong))
        val failure = assertThrows(IllegalStateException::class.java) {
            exchange.awaitReply(600L, clock::now, {}, { listOf(pong) }, clock::waitFor)
        }
        assertEquals("live_ping_ping_not_observed", failure.message)
        assertNull(exchange.completedAt)
        val duplicate = assertThrows(IllegalStateException::class.java) {
            exchange.observe(listOf(ping, ping.copy(id = "duplicate")), clock.now())
        }
        assertEquals("live_ping_duplicate_ping", duplicate.message)
    }
}
