package dev.persona.watch

import dev.persona.watch.data.*
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.ExecutionException
import java.util.concurrent.Future
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

class HubGroupReaderTest {
    private val budget = TimeUnit.SECONDS.toNanos(40)
    private fun sample(now: Long = 10, inboxUnavailable: Boolean = false) = WatchSnapshot(
        listOf(WatchSession("computer", "session", "Sample", "working", canSend = true)),
        emptyList(), emptyList(), listOf(InboxItem("mail", "Sample", "Preview", "mail", true)), now, inboxUnavailable)

    private class Clock(var now: Long = 0) { fun advance(nanos: Long) { now += nanos } }
    private class Request(private val action: (Long) -> WatchSnapshot, done: Boolean = false) : Future<WatchSnapshot> {
        val waits = mutableListOf<Long>()
        var cancellations = 0
        private var finished = done
        private var cancelled = false
        override fun get(timeout: Long, unit: TimeUnit): WatchSnapshot {
            check(!cancelled) { "Cancelled requests must not be collected" }
            waits += unit.toNanos(timeout)
            return action(unit.toNanos(timeout)).also { finished = true }
        }
        override fun get(): WatchSnapshot = error("An unbounded wait is forbidden")
        override fun cancel(mayInterruptIfRunning: Boolean): Boolean {
            assertTrue(mayInterruptIfRunning)
            if (finished) return false
            cancellations++
            cancelled = true
            finished = true
            return true
        }
        override fun isCancelled() = cancelled
        override fun isDone() = finished
    }

    private fun collect(clock: Clock, requests: List<Request>, cached: Map<String, WatchSnapshot> = emptyMap(),
                        deadline: Long = budget): HubGroupRead {
        val groups = requests.indices.map { SavedHub(if (it == 0) "" else "$it", "Sample $it", null) }
        return HubGroupReader.collect(groups, groups.zip(requests).associate { it.first.id to it.second }, cached, deadline) { clock.now }
    }

    @Test fun eachGroupReceivesOnlyTheRemainingSharedBudget() {
        val clock = Clock()
        val first = Request({ clock.advance(TimeUnit.SECONDS.toNanos(12)); sample() })
        val second = Request({ clock.advance(TimeUnit.SECONDS.toNanos(20)); sample() })
        val third = Request({ sample() })
        val result = collect(clock, listOf(first, second, third))
        assertEquals(listOf(budget), first.waits)
        assertEquals(listOf(TimeUnit.SECONDS.toNanos(28)), second.waits)
        assertEquals(listOf(TimeUnit.SECONDS.toNanos(8)), third.waits)
        assertTrue(result.failed.isEmpty())
    }

    @Test fun leadingTimeoutCancelsAllPendingRequestsButKeepsCompletedLaterGroups() {
        val clock = Clock()
        val first = Request({ remaining -> clock.advance(remaining); throw TimeoutException() })
        val pending = Request({ error("Pending group must not receive a fresh wait") })
        val finished = Request({ sample(25) }, done = true)
        val anotherPending = Request({ error("All pending groups must be cancelled together") })
        val result = collect(clock, listOf(first, pending, finished, anotherPending), mapOf("" to sample(), "1" to sample()))
        assertEquals(setOf("", "1", "3"), result.failed)
        assertEquals(listOf(budget), first.waits)
        assertTrue(pending.waits.isEmpty())
        assertTrue(anotherPending.waits.isEmpty())
        assertEquals(listOf(0L), finished.waits)
        assertEquals(25L, result.cache.getValue("2").updatedAt)
        assertEquals(listOf(1, 1, 0, 1), listOf(first, pending, finished, anotherPending).map { it.cancellations })
        assertFalse(result.snapshot.sessions.first().canSend)
        assertTrue(result.snapshot.sessions.last().canSend)
    }

    @Test fun exactDeadlineCancelsWithoutWaitingAndRetainsStaleCache() {
        val clock = Clock(budget)
        val requests = List(4) { Request({ error("No wait at the deadline") }) }
        val result = collect(clock, requests, mapOf("" to sample(17), "1" to sample(19), "removed" to sample(30)))
        assertEquals(setOf("", "1", "2", "3"), result.failed)
        assertTrue(requests.all { it.waits.isEmpty() && it.cancellations == 1 })
        assertEquals(setOf("", "1"), result.cache.keys)
        assertEquals(17L, result.snapshot.updatedAt)
        assertTrue(result.snapshot.inboxUnavailable)
    }

    @Test fun finalNanosecondIsAllowedButDoesNotCreateAnotherBudget() {
        val clock = Clock()
        val first = Request({ clock.advance(budget - 1); sample() })
        val second = Request({ clock.advance(1); sample() })
        val third = Request({ error("Budget has expired") })
        val result = collect(clock, listOf(first, second, third))
        assertEquals(listOf(1L), second.waits)
        assertTrue(third.waits.isEmpty())
        assertEquals(setOf("2"), result.failed)
        assertEquals(1, third.cancellations)
    }

    @Test fun failureBeforeDeadlineDoesNotCancelOtherGroupsOrResetTheirBudget() {
        val clock = Clock()
        val first = Request({ clock.advance(7); throw ExecutionException(IllegalStateException("Unavailable")) })
        val second = Request({ sample() })
        val result = collect(clock, listOf(first, second))
        assertEquals(setOf(""), result.failed)
        assertEquals(listOf(budget - 7), second.waits)
        assertEquals(0, second.cancellations)
    }

    @Test fun interruptedWaitCancelsAllPendingRequestsAndRestoresInterrupt() {
        val clock = Clock()
        val first = Request({ throw InterruptedException() })
        val second = Request({ error("No later wait after interruption") })
        try {
            val result = collect(clock, listOf(first, second))
            assertTrue(Thread.currentThread().isInterrupted)
            assertEquals(setOf("", "1"), result.failed)
            assertEquals(1, first.cancellations)
            assertEquals(1, second.cancellations)
            assertTrue(second.waits.isEmpty())
        } finally { Thread.interrupted() }
    }

    @Test fun freshSnapshotWithUnavailableInboxRetainsOnlyItsOwnCachedInbox() {
        val old = sample(8)
        val fresh = sample(30, inboxUnavailable = true).copy(inbox = emptyList())
        val result = collect(Clock(), listOf(Request({ fresh })), mapOf("" to old, "removed" to sample()))
        assertEquals(old.inbox, result.cache.getValue("").inbox)
        assertEquals(30L, result.cache.getValue("").updatedAt)
        assertTrue(result.snapshot.inboxUnavailable)
        assertEquals(setOf(""), result.cache.keys)
    }

    @Test fun monotonicClockWraparoundKeepsTheRemainingDurationPositive() {
        val clock = Clock(Long.MAX_VALUE - 5)
        val deadline = clock.now + budget
        val first = Request({ clock.advance(10); sample() })
        val second = Request({ sample() })
        assertTrue(collect(clock, listOf(first, second), deadline = deadline).failed.isEmpty())
        assertEquals(listOf(budget), first.waits)
        assertEquals(listOf(budget - 10), second.waits)
    }

    @Test fun aSuccessfulSiblingCannotRenewDisplayedCachedData() {
        for (failedIndex in listOf(0, 1)) {
            val requests = List(2) { index -> Request({
                if (index == failedIndex) throw ExecutionException(IllegalStateException("Unavailable"))
                sample(900)
            }) }
            val failedId = if (failedIndex == 0) "" else "1"
            val result = collect(Clock(), requests, mapOf(failedId to sample(120)))
            assertEquals(120L, result.snapshot.updatedAt)
            assertEquals(2, result.snapshot.sessions.size)
            assertEquals(120L, result.cache.getValue(failedId).updatedAt)
            assertFalse(result.snapshot.sessions.single { it.groupId == failedId }.canSend)
            assertTrue(result.snapshot.sessions.single { it.groupId != failedId }.canSend)
        }
    }

    @Test fun successfulEmptySiblingStillCannotRenewTheOnlyDisplayedCachedTasks() {
        val empty = sample(900).copy(sessions = emptyList(), inbox = emptyList())
        val result = collect(Clock(), listOf(Request({ throw TimeoutException() }), Request({ empty }, done = true)),
            mapOf("" to sample(120)))
        assertEquals(1, result.snapshot.sessions.size)
        assertEquals(120L, result.snapshot.updatedAt)
        assertEquals(900L, result.cache.getValue("1").updatedAt)
    }

    @Test fun allSuccessfulReadsUseTheirActualObservationTimesAndReplaceOlderCache() {
        val result = collect(Clock(), listOf(Request({ sample(800) }), Request({ sample(900) })),
            mapOf("" to sample(20), "1" to sample(30), "removed" to sample(1)))
        assertTrue(result.failed.isEmpty())
        assertEquals(800L, result.snapshot.updatedAt)
        assertEquals(setOf("", "1"), result.cache.keys)
        assertEquals(900L, result.cache.getValue("1").updatedAt)
    }

    @Test fun failureWithoutCachedContentDoesNotInventOrAgeAnObservation() {
        val emptyFailure = collect(Clock(), listOf(Request({ throw TimeoutException() })))
        assertEquals(0L, emptyFailure.snapshot.updatedAt)
        assertTrue(emptyFailure.cache.isEmpty())
        val partial = collect(Clock(), listOf(Request({ throw TimeoutException() }), Request({ sample(900) }, done = true)))
        assertEquals(900L, partial.snapshot.updatedAt)
        assertEquals(1, partial.snapshot.sessions.size)
    }

    @Test fun healthyPrimaryAndFailedSecondaryKeepCachedApprovalsCompletelyReadOnly() {
        val request = WatchInteraction("same", "computer", "session", "permission", "Approval", "Review",
            canAnswer = true, canCancel = true)
        val old = sample(100).copy(interactions = listOf(request))
        val fresh = old.copy(updatedAt = 200)
        val result = collect(Clock(), listOf(Request({ fresh }), Request({ throw TimeoutException() })),
            mapOf("1" to old))
        val displayed = result.displayAfter(old, 2)!!
        assertEquals(setOf("1"), result.failed)
        val healthy = displayed.interactions.single { it.groupId == "" }
        val failed = displayed.interactions.single { it.groupId == "1" }
        assertTrue(healthy.canAnswer); assertTrue(healthy.canCancel)
        assertFalse(failed.canAnswer); assertFalse(failed.canCancel)
        assertEquals(request.title, failed.title)
        assertEquals(100L, displayed.updatedAt)
        assertFalse(WatchResolution.canResolve(failed, result.failed))
        assertTrue(WatchResolution.canResolve(healthy, result.failed))
        // All permissions, including Deny and dismiss/Handle elsewhere, fail the network client's validator.
        for (resolution in listOf(WatchResolution.permission(request, true), WatchResolution.permission(request, false),
                WatchResolution.dismiss(request))) {
            assertEquals("read_only", assertThrows(HubException::class.java) {
                WatchResolution.validate(failed, resolution)
            }.code)
            WatchResolution.validate(healthy, resolution)
        }
    }
}
