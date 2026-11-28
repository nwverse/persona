package dev.persona.watch

import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class MutationDeliveryTest {
    @Test fun continueSettlesAndReloadsOnlyAfterDispatchToTheUiThread() {
        val ui = Thread.currentThread()
        val queued = ConcurrentLinkedQueue<() -> Unit>()
        val events = mutableListOf<String>()
        val work = ActivityWork()
        val ticket = work.beginMutation()!!
        val worker = Executors.newSingleThreadExecutor()
        try {
            worker.submit {
                assertNotSame(ui, Thread.currentThread())
                postMutationResult(Result.success(Unit), post = { queued.add(it) }, settled = {
                    assertSame(ui, Thread.currentThread()); events += "clear captured operation"
                }) { result ->
                    assertSame(ui, Thread.currentThread())
                    if (work.finishMutation(ticket) && result.isSuccess) events += "reload transcript"
                }
            }.get(5, TimeUnit.SECONDS)
            assertTrue(events.isEmpty())
            assertTrue(work.busy)
            queued.remove().invoke()
            assertEquals(listOf("clear captured operation", "reload transcript"), events)
            assertFalse(work.busy)
            assertTrue(queued.isEmpty())
        } finally { worker.shutdownNow() }
    }

    @Test fun failedSendKeepsItsOperationAndDoesNotReloadAsSuccess() {
        val queued = ConcurrentLinkedQueue<() -> Unit>()
        val failure = IllegalStateException("sample failure")
        var completed = false
        postMutationResult(Result.failure<Unit>(failure), post = { queued.add(it) },
            settled = { throw AssertionError("Failed delivery cannot clear operation") }) {
            assertSame(failure, it.exceptionOrNull()); completed = true
        }
        assertFalse(completed)
        queued.remove().invoke()
        assertTrue(completed)
    }

    @Test fun changedConnectionSettlesCapturedOperationWithoutReloadingTheNewHub() {
        val queued = ConcurrentLinkedQueue<() -> Unit>()
        val work = ActivityWork()
        val ticket = work.beginMutation()!!
        var settled = false
        var reloaded = false
        postMutationResult(Result.success(Unit), post = { queued.add(it) }, settled = { settled = true }) {
            if (work.finishMutation(ticket) && it.isSuccess) reloaded = true
        }
        work.connectionsChanged()
        queued.remove().invoke()
        assertTrue(settled)
        assertFalse(reloaded)
    }
}
